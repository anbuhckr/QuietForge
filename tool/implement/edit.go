package implement

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"quietforge/storage"
	"quietforge/tool"
	"quietforge/util"
)

const maxEditFileSize = 10 * 1024 * 1024 // 10MB cap

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

	hasOld := params.OldString != nil
	hasStart := params.StartLine != nil
	hasEnd := params.EndLine != nil

	if hasOld && (hasStart || hasEnd) {
		return &tool.ToolResult{
			Error:  "invalid_args",
			Output: "Specify either oldString or startLine/endLine, not both.",
		}, nil
	}

	if hasStart != hasEnd {
		return &tool.ToolResult{
			Error:  "invalid_args",
			Output: "Both startLine and endLine must be provided together.",
		}, nil
	}

	pathStr, err := util.JailPath(ctx.Workspace, params.FilePath)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	info, err := os.Stat(pathStr)
	if err != nil {
		if os.IsNotExist(err) {
			return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("File not found: %s", params.FilePath)}, nil
		}
		return &tool.ToolResult{Error: "read_error", Output: err.Error()}, nil
	}
	if info.Size() > maxEditFileSize {
		return &tool.ToolResult{Error: "file_too_large", Output: fmt.Sprintf("File is too large (%d bytes). Maximum: %d bytes.", info.Size(), maxEditFileSize)}, nil
	}

	contentBytes, err := os.ReadFile(pathStr)
	if err != nil {
		return &tool.ToolResult{Error: "read_error", Output: err.Error()}, nil
	}

	content := string(contentBytes)
	hasCRLF := strings.Contains(content, "\r\n")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	newString := strings.ReplaceAll(params.NewString, "\r\n", "\n")

	var newContent string
	count := 0

	if hasStart {
		lines := splitLinesKeepEnds(content)
		startLine := *params.StartLine
		endLine := *params.EndLine

		if startLine < 1 || endLine < startLine || startLine > len(lines) || endLine > len(lines) {
			return &tool.ToolResult{Error: "invalid_args", Output: fmt.Sprintf("Invalid line range %d-%d for file with %d lines.", startLine, endLine, len(lines))}, nil
		}

		prefix := strings.Join(lines[:startLine-1], "")
		suffix := strings.Join(lines[endLine:], "")
		newLines := splitLinesKeepEnds(newString)

		if len(newLines) > 0 && !strings.HasSuffix(newLines[len(newLines)-1], "\n") && len(suffix) > 0 {
			newLines[len(newLines)-1] += "\n"
		}

		newContent = prefix + strings.Join(newLines, "") + suffix
		if newContent == content {
			count = 0
		} else {
			count = 1
		}
	} else if hasOld {
		if *params.OldString == "" {
			return &tool.ToolResult{Error: "invalid_args", Output: "oldString cannot be empty."}, nil
		}
		old := strings.ReplaceAll(*params.OldString, "\r\n", "\n")
		if params.ReplaceAll {
			if !strings.Contains(content, old) {
				return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("oldString not found in %s", params.FilePath)}, nil
			}
			newContent = strings.ReplaceAll(content, old, newString)
			if newContent == content {
				count = 0
			} else {
				count = strings.Count(content, old)
			}
		} else {
			count = strings.Count(content, old)
			if count > 1 {
				return &tool.ToolResult{Error: "multiple_matches", Output: fmt.Sprintf("Found %d matches. Use replaceAll or provide more context.", count)}, nil
			}
			if count == 0 {
				return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("oldString not found in %s", params.FilePath)}, nil
			}
			newContent = strings.Replace(content, old, newString, 1)
			if newContent == content {
				count = 0
			} else {
				count = 1
			}
		}
	} else {
		return &tool.ToolResult{Error: "invalid_args", Output: "Must provide either oldString OR (startLine and endLine)"}, nil
	}

	if count == 0 {
		return &tool.ToolResult{Output: "No changes made. The replacement is identical to the original content."}, nil
	}

	if hasCRLF {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}

	perm := info.Mode().Perm()
	if err := atomicWriteFile(pathStr, []byte(newContent), perm); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: err.Error()}, nil
	}

	if repo, ok := ctx.Extra["repo"].(*storage.Repository); ok && repo != nil {
		tool.GlobalLspManager.NotifyFileChanged(ctx.Workspace, pathStr, newContent, repo)
	}

	relPath := pathStr
	if r, err := filepath.Rel(ctx.Workspace, pathStr); err == nil {
		relPath = r
	}

	s := ""
	if count != 1 {
		s = "s"
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Edited %s (%d replacement%s)", relPath, count, s),
		Output: fmt.Sprintf("Applied %d change(s) to %s", count, relPath),
	}, nil
}

// splitLinesKeepEnds splits a string into lines, preserving trailing newline
// characters and empty lines (including a trailing empty line from a final \n).
func splitLinesKeepEnds(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	// strings.SplitAfter on "a\nb\n" produces ["a\n", "b\n"] — correct
	// strings.SplitAfter on "a\nb" produces ["a\n", "b"] — correct
	// strings.SplitAfter on "" produces [""] — correct for empty input
	return lines
}
