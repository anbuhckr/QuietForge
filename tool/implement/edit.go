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
	return "Edit an existing file using a search and replace block (oldString/newString). The tool uses a Fuzzy Matcher, so you do NOT need to worry about exact indentation or whitespace in your oldString, just provide the raw code logic to replace. Alternatively, you can use exact line-range replacement (startLine, endLine)."
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

	if hasStart && hasEnd {
		hasOld = false // Prioritize line-range replacement and ignore oldString to help small models
	} else if hasOld {
		// If oldString is provided but line range is incomplete, just use oldString
		hasStart = false
		hasEnd = false
	} else if hasStart != hasEnd {
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
				return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("oldString not found in %s. Exact match failed and replaceAll does not support fuzzy matching.", params.FilePath)}, nil
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
				return &tool.ToolResult{Error: "multiple_matches", Output: fmt.Sprintf("Found %d matches for exact oldString. Provide more context to make it unique.", count)}, nil
			}
			if count == 0 {
				var fuzzyCount int
				newContent, fuzzyCount = doFuzzyReplace(content, old, newString)
				if fuzzyCount == 1 {
					count = 1
				} else if fuzzyCount > 1 {
					return &tool.ToolResult{Error: "multiple_matches", Output: fmt.Sprintf("Fuzzy matcher found %d matches for your oldString. Please provide a few more lines of surrounding code in your oldString to make it unique.", fuzzyCount)}, nil
				} else {
					return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("oldString not found in %s. Both exact match and fuzzy match failed to find a matching block of code. Please read the file again to ensure you are targeting the correct code.", params.FilePath)}, nil
				}
			} else {
				newContent = strings.Replace(content, old, newString, 1)
				if newContent == content {
					count = 0
				} else {
					count = 1
				}
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
	return lines
}

func doFuzzyReplace(content, oldString, newString string) (string, int) {
	oldLines := strings.Split(oldString, "\n")
	var target []string
	for _, l := range oldLines {
		t := strings.TrimSpace(l)
		if t != "" {
			target = append(target, t)
		}
	}
	if len(target) == 0 {
		return content, 0
	}

	contentLines := splitLinesKeepEnds(content)
	var matches [][]int
	
	for i := 0; i < len(contentLines); i++ {
		t := strings.TrimSpace(contentLines[i])
		if t == "" { continue }
		
		if t == target[0] {
			matchLen := 1
			j := i + 1
			for matchLen < len(target) && j < len(contentLines) {
				tj := strings.TrimSpace(contentLines[j])
				if tj == "" {
					j++
					continue
				}
				if tj == target[matchLen] {
					matchLen++
					j++
				} else {
					break
				}
			}
			if matchLen == len(target) {
				matches = append(matches, []int{i, j - 1})
			}
		}
	}
	
	if len(matches) == 1 {
		startIdx := matches[0][0]
		endIdx := matches[0][1]
		
		baseIndent := getIndentation(contentLines[startIdx])
		
		newLines := splitLinesKeepEnds(newString)
		var commonIndent *string
		for _, l := range newLines {
			if strings.TrimSpace(l) == "" { continue }
			ind := getIndentation(l)
			if commonIndent == nil {
				commonIndent = &ind
			} else {
				if len(ind) < len(*commonIndent) {
					*commonIndent = ind
				}
			}
		}
		
		cInd := ""
		if commonIndent != nil {
			cInd = *commonIndent
		}
		
		var adjustedNewLines []string
		for _, l := range newLines {
			if strings.TrimSpace(l) == "" {
				adjustedNewLines = append(adjustedNewLines, l)
			} else {
				stripped := l
				if strings.HasPrefix(l, cInd) {
					stripped = l[len(cInd):]
				}
				adjustedNewLines = append(adjustedNewLines, baseIndent + stripped)
			}
		}
		
		prefix := strings.Join(contentLines[:startIdx], "")
		suffix := strings.Join(contentLines[endIdx+1:], "")
		
		newContent := prefix + strings.Join(adjustedNewLines, "") + suffix
		return newContent, 1
	}
	
	return content, len(matches)
}

func getIndentation(s string) string {
	for i, c := range s {
		if c != ' ' && c != '\t' {
			return s[:i]
		}
	}
	return strings.TrimRight(s, "\n\r")
}
