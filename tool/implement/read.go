package implement

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
	"sort"
	"strconv"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
)

type ReadTool struct{}

func (t *ReadTool) ID() string {
	return "read"
}

func (t *ReadTool) Description() string {
	return "Read a file or directory from the local filesystem. Returns content with line numbers, which are extremely useful when combined with edit tools (startLine/endLine) to avoid whitespace mismatches."
}

func (t *ReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath": map[string]interface{}{"type": "string", "description": "Absolute path to the file or directory"},
			"offset":   map[string]interface{}{"type": "integer", "description": "Line number to start from (1-indexed)"},
			"limit":    map[string]interface{}{"type": "integer", "description": "Maximum number of lines to read"},
			"compact":  map[string]interface{}{"type": "boolean", "description": "If true, strips out function bodies using AST to save tokens (shows file skeleton only)."},
		},
		"required": []string{"filePath"},
	}
}

// perLanguageDefKinds maps file extensions to the tree-sitter node kinds
// that represent definition-level constructs (functions, methods, classes).
var perLanguageDefKinds = map[string][]string{
	".go":    {"function_declaration", "method_declaration", "type_declaration"},
	".py":    {"function_definition", "class_definition"},
	".rs":    {"function_item", "impl_item", "struct_item", "trait_item"},
	".js":    {"function_declaration", "class_declaration", "method_definition", "arrow_function"},
	".ts":    {"function_declaration", "class_declaration", "method_definition", "arrow_function"},
	".jsx":   {"function_declaration", "class_declaration", "method_definition", "arrow_function"},
	".tsx":   {"function_declaration", "class_declaration", "method_definition", "arrow_function"},
	".c":     {"function_definition"},
	".cpp":   {"function_definition", "class_specifier"},
	".h":     {"function_definition"},
	".hpp":   {"function_definition", "class_specifier"},
	".java":  {"method_declaration", "class_declaration"},
	".rb":    {"method", "class"},
	".swift": {"function_declaration", "class_declaration"},
	".kt":    {"function_declaration", "class_declaration"},
	".lua":   {"function_declaration"},
}

var genericDefKinds = []string{
	"function_definition", "function_declaration", "function_item",
	"method_definition", "method_declaration", "method",
	"class_definition", "class_declaration", "class_specifier",
	"type_declaration", "struct_item", "trait_item", "impl_item",
}

func getDefKinds(ext string) []string {
	if kinds, ok := perLanguageDefKinds[ext]; ok {
		return kinds
	}
	return genericDefKinds
}

// Parser pool — avoids per-read allocation of tree-sitter parsers.
var parserPool = sync.Pool{
	New: func() any { return sitter.NewParser() },
}

// AST cache — keyed by (path + mtime + size), stores compacted lines.
type astCacheEntry struct {
	lines []string
	idx   int // insertion order for eviction
}

var (
	astCache   = map[string]astCacheEntry{}
	astCacheMu sync.Mutex
	astCacheIdx int // monotonically increasing insertion counter for FIFO eviction
)

type readParams struct {
	FilePath string `json:"filePath"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	Compact  bool   `json:"compact"`
}

func (t *ReadTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params readParams
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}

	path, err := util.JailPath(ctx.Workspace, params.FilePath)
	if err != nil {
		return &tool.ToolResult{
			Error:  "access_denied",
			Output: fmt.Sprintf("Failed to read file %s: %v", params.FilePath, err),
		}, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("File not found: %s", path)}, nil
	}

	if info.IsDir() {
		return t.readDir(path)
	}

	const maxFileSize = 5 * 1024 * 1024 // 5MB
	if info.Size() > maxFileSize {
		return &tool.ToolResult{
			Error: "file_too_large",
			Output: fmt.Sprintf("File is too large (%d bytes). Maximum: %d bytes. Use 'shell' tool with head/tail or a targeted read with offset/limit.", info.Size(), maxFileSize),
		}, nil
	}

	// For large offsets, use scanner-based skip instead of loading entire file
	if params.Offset > 500 && !params.Compact {
		return t.readWithScanner(path, &params)
	}

	return t.readFull(ctx.Context, path, info, &params)
}

func (t *ReadTool) readDir(path string) (*tool.ToolResult, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	type DirEntry struct {
		Name      string `json:"name"`
		IsDir     bool   `json:"is_dir"`
		SizeBytes int64  `json:"size_bytes"`
	}
	var list []DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "__pycache__") {
			continue
		}
		info, err := e.Info()
		size := int64(0)
		if err == nil && !e.IsDir() {
			size = info.Size()
		}
		list = append(list, DirEntry{
			Name:      name,
			IsDir:     e.IsDir(),
			SizeBytes: size,
		})
	}

	if list == nil {
		list = []DirEntry{}
	}

	// Sort: directories first, then alphabetical
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})

	if len(list) > 150 {
		originalLength := len(list)
		list = list[:150]
		list = append(list, DirEntry{
			Name:      fmt.Sprintf("[Truncated: showing 150 of %d entries. Use a more specific path to view nested files.]", originalLength),
			IsDir:     false,
			SizeBytes: 0,
		})
	}

	b, _ := json.Marshal(list)
	return &tool.ToolResult{
		Title:  fmt.Sprintf("Directory: %s", path),
		Output: string(b),
	}, nil
}

// readWithScanner uses bufio.Scanner to skip to the offset without loading the whole file.
// Only used when no compact mode and offset > 500.
func (t *ReadTool) readWithScanner(path string, params *readParams) (*tool.ToolResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // 4MB buffer for minified files

	// Skip to offset
	lineNum := 1
	for lineNum < params.Offset && scanner.Scan() {
		lineNum++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	limit := params.Limit
	if limit == 0 {
		limit = 800
	}

	var numbered strings.Builder
	truncated := false
	count := 0
	offset := params.Offset

	for scanner.Scan() && count < limit {
		if count > 0 {
			numbered.WriteByte('\n')
		}
		line := scanner.Text()
		if len(line) > 2000 {
			line = line[:2000] + "..."
		}
		numbered.WriteString(strconv.Itoa(offset + count))
		numbered.WriteString(": ")
		numbered.WriteString(line)
		count++
	}
	if scanner.Scan() {
		truncated = true
	}

	outText := numbered.String()
	if truncated {
		outText += "\n\n... [File truncated. Use 'offset' and 'limit' to read further.]"
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("File: %s (%d+ lines)", path, count),
		Output: outText,
	}, nil
}

// readFull reads the full file into memory, then applies compact, offset, and limit.
func (t *ReadTool) readFull(parentCtx context.Context, path string, info os.FileInfo, params *readParams) (*tool.ToolResult, error) {
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if isBinary(contentBytes) {
		contentType := http.DetectContentType(contentBytes)
		if strings.HasPrefix(contentType, "image/") {
			// Cap image base64 at ~2MB to avoid excessive memory
			maxImageBytes := 2 * 1024 * 1024
			if len(contentBytes) > maxImageBytes {
				contentBytes = contentBytes[:maxImageBytes]
			}
			base64Data := base64.StdEncoding.EncodeToString(contentBytes)
			dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, base64Data)

			return &tool.ToolResult{
				Title:  fmt.Sprintf("Image File: %s", path),
				Output: fmt.Sprintf("Successfully read image file: %s (%d bytes).", path, len(contentBytes)),
				Attachments: []map[string]interface{}{
					{"url": dataURL},
				},
			}, nil
		}
		return &tool.ToolResult{
			Error:  "binary_file",
			Title:  fmt.Sprintf("File: %s", path),
			Output: fmt.Sprintf("Cannot display binary file: %s. Detected as binary (file size: %d bytes). Use 'shell' tool with appropriate commands to inspect this file instead.", path, len(contentBytes)),
		}, nil
	}

	content := string(contentBytes)
	lines := strings.Split(content, "\n")
	fullLines := len(lines)

	// Pipeline: compact → offset → limit → number

	// Step 1: AST compact (on full lines, line numbers match original file)
	if params.Compact {
		lines = compactAST(parentCtx, path, info, contentBytes, lines)
	}

	// Step 2: offset
	if params.Offset > 1 {
		if params.Offset-1 < len(lines) {
			lines = lines[params.Offset-1:]
		} else {
			lines = nil
		}
	}

	// Step 3: limit
	limit := params.Limit
	if limit == 0 {
		limit = 800
	}
	truncated := false
	if len(lines) > limit {
		lines = lines[:limit]
		truncated = true
	}

	// Step 4: number lines
	offset := params.Offset
	if offset == 0 {
		offset = 1
	}

	var outText strings.Builder
	outText.Grow(len(lines) * 40) // pre-allocate ~40 bytes per line
	for i, l := range lines {
		if i > 0 {
			outText.WriteByte('\n')
		}
		outText.WriteString(strconv.Itoa(i + offset))
		outText.WriteString(": ")
		outText.WriteString(l)
	}

	if truncated && (params.Offset+len(lines)) < fullLines {
		outText.WriteString("\n\n... [File truncated for length. Use 'offset' and 'limit' arguments to read further.]")
	}

	shownLines := len(lines)
	if shownLines == 0 && fullLines > 0 {
		shownLines = fullLines
	}
	return &tool.ToolResult{
		Title:  fmt.Sprintf("File: %s (%d lines)", path, shownLines),
		Output: outText.String(),
	}, nil
}

// compactAST walks the tree-sitter AST using cursor traversal and replaces
// function/method/class body lines with empty placeholders, preserving
// original line numbers for reliable edit targeting.
func compactAST(ctx context.Context, path string, info os.FileInfo, contentBytes []byte, lines []string) []string {
	// Try AST cache first
	cacheKey := path + "@" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "@" + strconv.FormatInt(info.Size(), 10)
	astCacheMu.Lock()
	if entry, ok := astCache[cacheKey]; ok {
		astCacheMu.Unlock()
		return entry.lines
	}
	astCacheMu.Unlock()

	lang := GetLanguage(filepath.Ext(path))
	if lang == nil {
		return lines
	}

	parser := parserPool.Get().(*sitter.Parser)
	defer parserPool.Put(parser)
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(ctx, nil, contentBytes)
	if err != nil || tree == nil {
		return lines
	}
	defer tree.Close()

	defKinds := getDefKinds(filepath.Ext(path))
	kindSet := make(map[string]bool, len(defKinds))
	for _, k := range defKinds {
		kindSet[k] = true
	}

	// Collect body ranges to hide (line indices, 0-based)
	type hideRange struct{ start, end int }
	var toHide []hideRange

	// Cursor traversal — depth-first, no recursion
	cursor := sitter.NewTreeCursor(tree.RootNode())
	defer cursor.Close()

	for {
		node := cursor.CurrentNode()
		if kindSet[node.Type()] {
			bodyNode := node.ChildByFieldName("body")
			if bodyNode == nil {
				bodyNode = node.ChildByFieldName("consequence")
			}
			if bodyNode != nil {
				start := int(bodyNode.StartPoint().Row)
				end := int(bodyNode.EndPoint().Row)
				if end-start > 2 {
					toHide = append(toHide, hideRange{start + 1, end})
				}
			}
		}

		// Depth-first traversal: try first child, then next sibling, then up
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

	if len(toHide) == 0 {
		return lines
	}

	// Build result: same length as original, replacing hidden lines with empty strings.
	// This preserves original line numbers for accurate edit targeting.
	result := make([]string, len(lines))
	copy(result, lines)

	hideIdx := 0
	for i := range lines {
		for hideIdx < len(toHide) && toHide[hideIdx].end <= i {
			hideIdx++
		}
		if hideIdx < len(toHide) {
			r := toHide[hideIdx]
			if i == r.start {
				result[i] = "  // ... implementation hidden"
			} else if i > r.start && i < r.end {
				result[i] = ""
			}
		}
	}

	// Cache the result
	resultCopy := make([]string, len(result))
	copy(resultCopy, result)
	astCacheMu.Lock()
	astCacheIdx++
	cacheEntry := astCacheEntry{lines: resultCopy, idx: astCacheIdx}
	astCache[cacheKey] = cacheEntry
	if len(astCache) > 50 {
		// FIFO eviction: remove entry with smallest idx
		var oldestKey string
		oldestIdx := astCacheIdx + 1
		for k, e := range astCache {
			if e.idx < oldestIdx {
				oldestIdx = e.idx
				oldestKey = k
			}
		}
		delete(astCache, oldestKey)
	}
	astCacheMu.Unlock()

	return result
}

func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	contentType := http.DetectContentType(data)
	if strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") ||
		contentType == "application/zip" ||
		contentType == "application/gzip" ||
		contentType == "application/x-gzip" ||
		contentType == "application/pdf" {
		return true
	}
	// Removed: application/octet-stream — too many false positives for unknown text formats

	// Check for null bytes as the reliable binary indicator
	if data[0] == 0 {
		return true
	}
	nullCount := 0
	maxCheck := len(data)
	if maxCheck > 8192 {
		maxCheck = 8192
	}
	for _, b := range data[:maxCheck] {
		if b == 0 {
			nullCount++
		}
	}
	return nullCount > 0
}

// Ensure io.EOF is referenced (bufio.Scanner uses it internally).
var _ = io.EOF
