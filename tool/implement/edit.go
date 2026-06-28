package implement

import (
	"quietforge/tool"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type EditTool struct{}

func (t *EditTool) ID() string {
	return "edit"
}

func (t *EditTool) Description() string {
	return "Edit an existing file using either exact string replacement (oldString) OR exact line-range replacement (startLine, endLine). Line-range is highly recommended to avoid whitespace mismatch errors."
}

func (t *EditTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath":   map[string]interface{}{"type": "string", "description": "Absolute path to the file to edit"},
			"newString":  map[string]interface{}{"type": "string", "description": "The replacement text"},
			"oldString":  map[string]interface{}{"type": "string", "description": "(Optional) The exact text to replace"},
			"replaceAll": map[string]interface{}{"type": "boolean", "description": "(Optional) Replace all occurrences if true"},
			"startLine":  map[string]interface{}{"type": "integer", "description": "(Optional) Start line number for replacement (1-indexed)"},
			"endLine":    map[string]interface{}{"type": "integer", "description": "(Optional) End line number for replacement (1-indexed)"},
		},
		"required": []string{"filePath", "newString"},
	}
}

func (t *EditTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		FilePath   string  `json:"filePath"`
		NewString  string  `json:"newString"`
		OldString  *string `json:"oldString"`
		ReplaceAll bool    `json:"replaceAll"`
		StartLine  *int    `json:"startLine"`
		EndLine    *int    `json:"endLine"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	pathStr := params.FilePath
	if !filepath.IsAbs(pathStr) && ctx.Workspace != "" {
		pathStr = filepath.Join(ctx.Workspace, pathStr)
	}

	contentBytes, err := os.ReadFile(pathStr)
	if err != nil {
		if os.IsNotExist(err) {
			return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("File not found: %s", pathStr)}, nil
		}
		return &tool.ToolResult{Error: "read_error", Output: err.Error()}, nil
	}

	content := string(contentBytes)
	hasCRLF := strings.Contains(content, "\r\n")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	newString := strings.ReplaceAll(params.NewString, "\r\n", "\n")
	newContent := ""
	count := 0

	if params.StartLine != nil && params.EndLine != nil {
		lines := splitLinesKeepEnds(content)
		startLine := *params.StartLine
		endLine := *params.EndLine

		if startLine < 1 || endLine < startLine || startLine > len(lines) {
			return &tool.ToolResult{Error: "invalid_args", Output: fmt.Sprintf("Invalid line range %d-%d for file with %d lines.", startLine, endLine, len(lines))}, nil
		}

		prefix := strings.Join(lines[:startLine-1], "")
		suffix := strings.Join(lines[endLine:], "")
		newLines := splitLinesKeepEnds(newString)

		if len(newLines) > 0 && !strings.HasSuffix(newLines[len(newLines)-1], "\n") && len(suffix) > 0 {
			newLines[len(newLines)-1] += "\n"
		}

		newContent = prefix + strings.Join(newLines, "") + suffix
		count = 1
	} else if params.OldString != nil {
		old := strings.ReplaceAll(*params.OldString, "\r\n", "\n")
		if params.ReplaceAll {
			if !strings.Contains(content, old) {
				return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("oldString not found in %s", params.FilePath)}, nil
			}
			count = strings.Count(content, old)
			newContent = strings.ReplaceAll(content, old, newString)
		} else {
			count = strings.Count(content, old)
			if count > 1 {
				return &tool.ToolResult{Error: "multiple_matches", Output: fmt.Sprintf("Found %d matches. Use replaceAll or provide more context.", count)}, nil
			}
			if count == 0 {
				return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("oldString not found in %s", params.FilePath)}, nil
			}
			newContent = strings.Replace(content, old, newString, 1)
			count = 1
		}
	} else {
		return &tool.ToolResult{Error: "invalid_args", Output: "Must provide either oldString OR (startLine and endLine)"}, nil
	}

	if hasCRLF {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}
	if err := os.WriteFile(pathStr, []byte(newContent), 0644); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: err.Error()}, nil
	}

	s := ""
	if count > 1 {
		s = "s"
	}
	return &tool.ToolResult{
		Title:  fmt.Sprintf("Edited %s (%d replacement%s)", pathStr, count, s),
		Output: fmt.Sprintf("Applied %d change(s) to %s", count, pathStr),
	}, nil
}

func splitLinesKeepEnds(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

