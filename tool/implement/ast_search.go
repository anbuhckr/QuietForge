package implement

import (
	"quietforge/tool"
	"quietforge/util"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type AstSearchTool struct{}

func (t *AstSearchTool) ID() string {
	return "ast_search"
}

func (t *AstSearchTool) Description() string {
	return "Semantic code search using Tree-sitter. Finds classes, functions, methods, and variables accurately across multiple languages. Excellent for resolving ReferenceError."
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

type astMatch struct {
	FilePath  string
	StartLine int
	EndLine   int
	Snippet   string
}

func (t *AstSearchTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		SymbolName string `json:"symbolName"`
		FilePath   string `json:"filePath"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	symbol := params.SymbolName
	searchPathStr, err := util.JailPath(ctx.Workspace, params.FilePath)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	if _, err := os.Stat(searchPathStr); os.IsNotExist(err) {
		return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("Path not found: %s", searchPathStr)}, nil
	}

	var filesToSearch []string
	info, err := os.Stat(searchPathStr)
	if err != nil {
		return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("Path not found: %s", searchPathStr)}, nil
	}
	if !info.IsDir() {
		filesToSearch = append(filesToSearch, searchPathStr)
	} else {
		supportedExts := map[string]bool{
			".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
			".go": true, ".rs": true, ".java": true, ".c": true, ".cpp": true,
			".h": true, ".hpp": true,
		}
		filepath.WalkDir(searchPathStr, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := d.Name()
				if base == "node_modules" || base == ".git" || base == ".venv" || base == "__pycache__" || base == ".agent" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if supportedExts[ext] {
				filesToSearch = append(filesToSearch, path)
			}
			return nil
		})
	}

	var matches []astMatch
	for _, fpath := range filesToSearch {
		m := parseFileAST(fpath, symbol)
		if m != nil {
			matches = append(matches, m...)
		}
	}

	if len(matches) == 0 {
		return &tool.ToolResult{
			Output: fmt.Sprintf("Symbol '%s' not found in any AST.", symbol),
		}, nil
	}

	type astMatchJson struct {
		File      string `json:"file"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		Snippet   string `json:"snippet"`
	}
	var jsonMatches []astMatchJson
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

	if jsonMatches == nil {
		jsonMatches = []astMatchJson{}
	}

	b, _ := json.Marshal(jsonMatches)

	return &tool.ToolResult{
		Title:  fmt.Sprintf("AST Search: %s", symbol),
		Output: string(b),
	}, nil
}

func GetLanguage(ext string) *sitter.Language {
	switch ext {
	case ".py":
		return python.GetLanguage()
	case ".js", ".jsx":
		return javascript.GetLanguage()
	case ".ts":
		return typescript.GetLanguage()
	case ".tsx":
		return tsx.GetLanguage()
	case ".go":
		return golang.GetLanguage()
	case ".rs":
		return rust.GetLanguage()
	case ".java":
		return java.GetLanguage()
	case ".c", ".h":
		return c.GetLanguage()
	case ".cpp", ".hpp":
		return cpp.GetLanguage()
	}
	return nil
}

func parseFileAST(path, symbol string) []astMatch {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	lines := strings.Split(content, "\n")

	lang := GetLanguage(filepath.Ext(path))
	if lang == nil {
		return nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(lang)
	tree, err := parser.ParseCtx(context.Background(), nil, data)
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	var matches []astMatch

	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		t := node.Type()
		lt := strings.ToLower(t)

		// Look for definition/declaration nodes
		isDef := strings.Contains(lt, "function") ||
			strings.Contains(lt, "method") ||
			strings.Contains(lt, "class") ||
			strings.Contains(lt, "struct") ||
			strings.Contains(lt, "interface") ||
			strings.Contains(lt, "declaration") ||
			strings.Contains(lt, "definition") ||
			strings.Contains(lt, "declarator")

		if isDef {
			// Try to find the name field
			nameNode := node.ChildByFieldName("name")
			if nameNode == nil {
				// Fallback: look for first identifier child
				for i := 0; i < int(node.ChildCount()); i++ {
					child := node.Child(i)
					if child.Type() == "identifier" || child.Type() == "property_identifier" {
						nameNode = child
						break
					}
				}
			}

			if nameNode != nil {
				nodeName := nameNode.Content(data)
				if nodeName == symbol {
					startLine := int(node.StartPoint().Row) + 1
					endLine := int(node.EndPoint().Row) + 1
					snippetEnd := startLine + 9
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

		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)
	return matches
}

var _ tool.Tool = (*AstSearchTool)(nil)
