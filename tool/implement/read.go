package implement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

type ReadTool struct {}

func (t *ReadTool) ID() string {
	return "read"
}

func (t *ReadTool) Description() string {
	return "Read a file or directory from the local filesystem. Returns content with line numbers."
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

func (t *ReadTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		FilePath string `json:"filePath"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
		Compact  bool   `json:"compact"`
	}
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
		
		b, _ := json.Marshal(list)
		return &tool.ToolResult{
			Title:  fmt.Sprintf("Directory: %s", path),
			Output: string(b),
		}, nil
	}

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if isBinary(contentBytes) {
		contentType := http.DetectContentType(contentBytes)
		if strings.HasPrefix(contentType, "image/") {
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

	if params.Compact {
		lang := GetLanguage(filepath.Ext(path))
		if lang != nil {
			parser := sitter.NewParser()
			parser.SetLanguage(lang)
			tree, err := parser.ParseCtx(context.Background(), nil, contentBytes)
			if err == nil && tree != nil {
				defer tree.Close()
				removeLine := make(map[int]bool)
				var walk func(node *sitter.Node)
				walk = func(node *sitter.Node) {
					t := node.Type()
					lt := strings.ToLower(t)
					isDef := strings.Contains(lt, "function") || strings.Contains(lt, "method") || strings.Contains(lt, "class")
					if isDef {
						bodyNode := node.ChildByFieldName("body")
						if bodyNode == nil {
							bodyNode = node.ChildByFieldName("consequence")
						}
						if bodyNode != nil {
							start := int(bodyNode.StartPoint().Row)
							end := int(bodyNode.EndPoint().Row)
							if end-start > 2 {
								lines[start+1] = "  // ... implementation hidden"
								for i := start + 2; i < end; i++ {
									removeLine[i] = true
								}
							}
						}
					}
					for i := 0; i < int(node.ChildCount()); i++ {
						walk(node.Child(i))
					}
				}
				walk(tree.RootNode())

				var newLines []string
				for i, l := range lines {
					if !removeLine[i] {
						newLines = append(newLines, l)
					}
				}
				lines = newLines
			}
		}
	}

	if params.Offset > 0 {
		if params.Offset-1 < len(lines) {
			lines = lines[params.Offset-1:]
		} else {
			lines = []string{}
		}
	}

	truncated := false
	limit := params.Limit
	if limit == 0 {
		limit = 800
	}

	if len(lines) > limit {
		lines = lines[:limit]
		truncated = true
	}

	offset := params.Offset
	if offset == 0 {
		offset = 1
	}

	var numbered []string
	for i, l := range lines {
		numbered = append(numbered, fmt.Sprintf("%d: %s", i+offset, l))
	}

	outText := strings.Join(numbered, "\n")
	if truncated {
		outText += "\n\n... [File truncated for length. Use 'offset' and 'limit' arguments to read further.]"
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("File: %s (%d lines)", path, len(lines)),
		Output: outText,
	}, nil
}

func isBinary(data []byte) bool {
	// Check content-type sniff first (PNG, JPEG, GIF, etc.)
	contentType := http.DetectContentType(data)
	if strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") ||
		contentType == "application/zip" ||
		contentType == "application/gzip" ||
		contentType == "application/x-gzip" ||
		contentType == "application/pdf" ||
		strings.HasPrefix(contentType, "application/octet-stream") {
		return true
	}

	// Check for null bytes as a strong binary indicator
	if len(data) > 0 && data[0] == 0 {
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
