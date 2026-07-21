package implement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
	wspkg "quietforge/workspace"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
)

const (
	astSearchMaxFiles   = 500
	astSearchMaxResults = 100
	snippetLines        = 10
	maxASTFileSize      = 5 * 1024 * 1024 // 5MB
)

var errMaxFiles = errors.New("max files reached")

type AstSearchTool struct{}

func (t *AstSearchTool) ID() string {
	return "ast_search"
}

func (t *AstSearchTool) Description() string {
	return "CRITICAL: ALWAYS use this tool INSTEAD of grep whenever you need to find where a function, method, class, or struct is defined. Semantic code search using Tree-sitter."
}

func (t *AstSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"symbolName": map[string]interface{}{"type": "string", "description": "The exact name of the function, class, or method to find."},
			"filePath":   map[string]interface{}{"type": "string", "description": "(Optional) Absolute path or relative path to a specific file or directory to narrow the search."},
		},
		"required": []string{"symbolName"},
	}
}

// langInfo bundles everything we know about a supported language:
// the tree-sitter Language and which node types define named entities.
type langInfo struct {
	Language *sitter.Language
	DefKinds map[string]struct{}
}

// languageRegistry is the single source of truth for all supported extensions.
// Both GetLanguage() and parseFileAST() read from this map.
var languageRegistry = map[string]langInfo{
	".py":   {wspkg.GetLanguage(".py"), map[string]struct{}{"function_definition": {}, "class_definition": {}}},
	".js":   {wspkg.GetLanguage(".js"), map[string]struct{}{"function_declaration": {}, "class_declaration": {}, "method_definition": {}, "lexical_declaration": {}, "variable_declaration": {}, "variable_declarator": {}}},
	".jsx":  {wspkg.GetLanguage(".jsx"), map[string]struct{}{"function_declaration": {}, "class_declaration": {}, "method_definition": {}, "lexical_declaration": {}, "variable_declaration": {}, "variable_declarator": {}}},
	".ts":   {wspkg.GetLanguage(".ts"), map[string]struct{}{"function_declaration": {}, "class_declaration": {}, "method_definition": {}, "lexical_declaration": {}, "variable_declaration": {}, "variable_declarator": {}}},
	".tsx":  {wspkg.GetLanguage(".tsx"), map[string]struct{}{"function_declaration": {}, "class_declaration": {}, "method_definition": {}, "lexical_declaration": {}, "variable_declaration": {}, "variable_declarator": {}}},
	".go":   {wspkg.GetLanguage(".go"), map[string]struct{}{"function_declaration": {}, "method_declaration": {}, "type_spec": {}, "var_spec": {}, "const_spec": {}}},
	".rs":   {wspkg.GetLanguage(".rs"), map[string]struct{}{"function_item": {}, "struct_item": {}, "impl_item": {}, "trait_item": {}}},
	".java": {wspkg.GetLanguage(".java"), map[string]struct{}{"method_declaration": {}, "class_declaration": {}, "interface_declaration": {}}},
	".c":    {wspkg.GetLanguage(".c"), map[string]struct{}{"function_definition": {}, "declaration": {}}},
	".h":    {wspkg.GetLanguage(".h"), map[string]struct{}{"function_definition": {}, "declaration": {}}},
	".cpp":  {wspkg.GetLanguage(".cpp"), map[string]struct{}{"function_definition": {}, "class_specifier": {}, "declaration": {}}},
	".hpp":  {wspkg.GetLanguage(".hpp"), map[string]struct{}{"function_definition": {}, "class_specifier": {}, "declaration": {}}},
}

// supportedExtsSet is built once from languageRegistry at init time.
var supportedExtsSet map[string]struct{}

func init() {
	supportedExtsSet = make(map[string]struct{}, len(languageRegistry))
	for ext := range languageRegistry {
		supportedExtsSet[ext] = struct{}{}
	}
}

// perLanguageParsers is a sync.Pool keyed by file extension.
// Each pool yields pre-configured parsers for a single language.
// Thread-safe — multiple goroutines can Parse concurrently.
var perLanguageParsers = map[string]*sync.Pool{}
var parserRegistryMu sync.Mutex

func getParserPool(ext string, lang *sitter.Language) *sync.Pool {
	parserRegistryMu.Lock()
	defer parserRegistryMu.Unlock()
	if pool, ok := perLanguageParsers[ext]; ok {
		return pool
	}
	pool := &sync.Pool{
		New: func() any {
			p := sitter.NewParser()
			p.SetLanguage(lang)
			return p
		},
	}
	perLanguageParsers[ext] = pool
	return pool
}

type astSearchParams struct {
	SymbolName string `json:"symbolName"`
	FilePath   string `json:"filePath"`
}

type astMatch struct {
	FilePath  string
	StartLine int
	EndLine   int
	Snippet   string
}

type astMatchJson struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
}

func (t *AstSearchTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params astSearchParams
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	symbol := params.SymbolName
	searchPathStr, err := util.JailPath(ctx.Workspace, params.FilePath)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	info, err := os.Stat(searchPathStr)
	if os.IsNotExist(err) {
		return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("Path not found: %s", searchPathStr)}, nil
	}
	if err != nil {
		return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("Cannot access path: %s", searchPathStr)}, nil
	}

	supExts := supportedExtsSet
	skipDirs := map[string]struct{}{
		"node_modules": {}, ".git": {}, ".venv": {}, "__pycache__": {},
		".agent": {}, "dist": {}, "build": {}, "target": {}, "vendor": {},
	}

	// Collect files to search (capped at astSearchMaxFiles)
	var filesToSearch []string

	if !info.IsDir() {
		ext := filepath.Ext(searchPathStr)
		_, ok := supExts[ext]
		if ok {
			filesToSearch = append(filesToSearch, searchPathStr)
		}
	} else {
		walkErr := filepath.WalkDir(searchPathStr, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			// Check cancellation inside walk
			select {
			case <-ctx.Context.Done():
				return ctx.Context.Err()
			default:
			}
			if d.IsDir() {
				_, isSkip := skipDirs[d.Name()]
				if isSkip || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			_, ok := supExts[filepath.Ext(path)]
			if ok {
				if len(filesToSearch) >= astSearchMaxFiles {
					return errMaxFiles
				}
				filesToSearch = append(filesToSearch, path)
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, errMaxFiles) {
			// Unexpected walk error — log for debugging but continue with partial results
			if !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) {
				log.Printf("ast_search: walk error in %s: %v", searchPathStr, walkErr)
			}
		}
	}

	// Search files (cap results at astSearchMaxResults)
	var matches []astMatch
	for _, fpath := range filesToSearch {
		select {
		case <-ctx.Context.Done():
			goto doneSearch
		default:
		}

		if len(matches) >= astSearchMaxResults {
			break
		}

		m := parseFileAST(ctx.Context, fpath, symbol)
		if m != nil {
			remaining := astSearchMaxResults - len(matches)
			if len(m) > remaining {
				m = m[:remaining]
			}
			matches = append(matches, m...)
		}
	}
doneSearch:

	if len(matches) == 0 {
		return &tool.ToolResult{
			Output: fmt.Sprintf("Symbol '%s' not found in any AST.", symbol),
		}, nil
	}

	jsonMatches := make([]astMatchJson, 0, len(matches))
	for _, m := range matches {
		relPath := m.FilePath
		if ctx.Workspace != "" {
			if r, err := filepath.Rel(ctx.Workspace, m.FilePath); err == nil {
				relPath = r
			}
		}
		jsonMatches = append(jsonMatches, astMatchJson{
			File:      relPath,
			StartLine: m.StartLine,
			EndLine:   m.EndLine,
			Snippet:   m.Snippet,
		})
	}

	b, err := json.Marshal(jsonMatches)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal AST results: %w", err)
	}

	truncated := ""
	if len(filesToSearch) >= astSearchMaxFiles {
		truncated = fmt.Sprintf(" (capped at %d files)", astSearchMaxFiles)
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("AST Search: %s%s", symbol, truncated),
		Output: string(b),
	}, nil
}

func GetLanguage(ext string) *sitter.Language {
	if info, ok := languageRegistry[ext]; ok {
		return info.Language
	}
	return nil
}

func parseFileAST(ctx context.Context, path, symbol string) []astMatch {
	// Skip files over 5MB
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxASTFileSize {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	lines := strings.Split(content, "\n")

	ext := filepath.Ext(path)
	langInfo, hasLang := languageRegistry[ext]
	if !hasLang {
		return nil
	}
	lang := langInfo.Language
	kindSet := langInfo.DefKinds

	pool := getParserPool(ext, lang)
	parser := pool.Get().(*sitter.Parser)
	defer pool.Put(parser)

	tree, err := parser.ParseCtx(ctx, nil, data)
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	var matches []astMatch

	// Cursor traversal — depth-first, no recursion
	cursor := sitter.NewTreeCursor(root)
	defer cursor.Close()

	for {
		// Check cancellation and max results
		if len(matches) >= astSearchMaxResults {
			break
		}
		select {
		case <-ctx.Done():
			return matches
		default:
		}

		node := cursor.CurrentNode()
		if _, isDef := kindSet[node.Type()]; isDef {
			nameNode := node.ChildByFieldName("name")
			if nameNode == nil {
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					t := child.Type()
					if t == "identifier" || t == "property_identifier" || t == "variable_declarator" {
						nameNode = child
						break
					}
				}
			}
			// For variable_declarator nodes, the name is the "name" field child
			if nameNode == nil && node.Type() == "variable_declarator" {
				nameNode = node.ChildByFieldName("name")
			}

			if nameNode != nil {
				nodeName := nameNode.Content(data)
				if nodeName == symbol {
					startLine := int(node.StartPoint().Row) + 1
					endLine := int(node.EndPoint().Row) + 1
					snippetEnd := startLine + snippetLines - 1
					if snippetEnd > endLine {
						snippetEnd = endLine
					}
					snippet := strings.Join(lines[startLine-1:snippetEnd], "\n")
					if endLine > snippetEnd {
						snippet += "\n..."
					}
					matches = append(matches, astMatch{
						FilePath:  path,
						StartLine: startLine,
						EndLine:   endLine,
						Snippet:   snippet,
					})
				}
			}
		}

		if cursor.GoToFirstChild() {
			continue
		}
		for !cursor.GoToNextSibling() {
			if !cursor.GoToParent() {
				goto done
			}
		}
	}
done:

	return matches
}

var _ tool.Tool = (*AstSearchTool)(nil)
