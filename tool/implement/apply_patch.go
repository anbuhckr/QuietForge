package implement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
	"strings"
	"time"
)

type ApplyPatchTool struct{}

func (t *ApplyPatchTool) ID() string {
	return "apply_unified_patch"
}

func (t *ApplyPatchTool) Description() string {
	return "Apply a unified diff patch to the codebase."
}

func (t *ApplyPatchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"patch": map[string]interface{}{"type": "string", "description": "The raw unified diff patch content"},
		},
		"required": []string{"patch"},
	}
}

func (t *ApplyPatchTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	workspace := ctx.Workspace
	if workspace == "" {
		workspace, _ = os.Getwd()
	}

	if err := validateUnifiedPatchPaths(workspace, params.Patch); err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	patchFile := filepath.Join(workspace, ".quietforge_temp.patch")
	if err := os.WriteFile(patchFile, []byte(params.Patch), 0644); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to write patch file: %v", err)}, nil
	}
	defer os.Remove(patchFile)

	execCtx, execCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer execCancel()

	var gitOut, gitErr bytes.Buffer
	gitCmd := exec.CommandContext(execCtx, "git", "apply", ".quietforge_temp.patch")
	gitCmd.Dir = workspace
	gitCmd.Stdout = &gitOut
	gitCmd.Stderr = &gitErr
	if gitCmd.Run() == nil {
		return &tool.ToolResult{Output: "Patch applied successfully via git apply."}, nil
	}

	var patchOut, patchErr bytes.Buffer
	patchCmd := exec.CommandContext(execCtx, "patch", "-p1", "-i", ".quietforge_temp.patch")
	patchCmd.Dir = workspace
	patchCmd.Stdout = &patchOut
	patchCmd.Stderr = &patchErr
	if patchCmd.Run() == nil {
		return &tool.ToolResult{Output: "Patch applied successfully via patch utility."}, nil
	}

	return &tool.ToolResult{
		Error:  "patch_failed",
		Output: fmt.Sprintf("Failed to apply patch.\nGit error: %s\nPatch error: %s", gitErr.String(), patchErr.String()),
	}, nil
}

func validateUnifiedPatchPaths(workspace, patch string) error {
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		var paths []string

		switch {
		case strings.HasPrefix(line, "diff --git "):
			paths = append(paths, parseDiffGitPaths(strings.TrimSpace(strings.TrimPrefix(line, "diff --git ")))...)
		case strings.HasPrefix(line, "--- "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
		case strings.HasPrefix(line, "+++ "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		case strings.HasPrefix(line, "rename from "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "rename from ")))
		case strings.HasPrefix(line, "rename to "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "rename to ")))
		case strings.HasPrefix(line, "copy from "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "copy from ")))
		case strings.HasPrefix(line, "copy to "):
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "copy to ")))
		}

		for _, p := range paths {
			if err := validateUnifiedPatchPath(workspace, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseDiffGitPaths(raw string) []string {
	fields := strings.Fields(raw)
	if len(fields) < 2 {
		return nil
	}
	return []string{fields[0], fields[1]}
}

func validateUnifiedPatchPath(workspace, rawPath string) error {
	path := cleanUnifiedPatchPath(rawPath)
	if path == "" || path == "/dev/null" {
		return nil
	}
	if _, err := util.JailPath(workspace, path); err != nil {
		return fmt.Errorf("patch path escapes workspace: %s", path)
	}
	return nil
}

func cleanUnifiedPatchPath(path string) string {
	path = strings.TrimSpace(path)
	if i := strings.IndexByte(path, '\t'); i >= 0 {
		path = path[:i]
	} else if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		if i := strings.IndexByte(path, ' '); i >= 0 {
			path = path[:i]
		}
	}
	path = strings.Trim(path, `"`)
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return path
}

type SearchReplaceTool struct{}

func (t *SearchReplaceTool) ID() string {
	return "apply_patch"
}

func (t *SearchReplaceTool) Description() string {
	return "Apply SEARCH/REPLACE blocks to modify files. Each block must have exact matching SEARCH text."
}

func (t *SearchReplaceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"patches": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"filePath": map[string]interface{}{"type": "string"},
						"search":   map[string]interface{}{"type": "string"},
						"replace":  map[string]interface{}{"type": "string"},
					},
					"required": []string{"filePath", "search", "replace"},
				},
			},
		},
		"required": []string{"patches"},
	}
}

func (t *SearchReplaceTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Patches []struct {
			FilePath string `json:"filePath"`
			Search   string `json:"search"`
			Replace  string `json:"replace"`
		} `json:"patches"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	var results []string
	for i, p := range params.Patches {
		pathStr, err := util.JailPath(ctx.Workspace, p.FilePath)
		if err != nil {
			results = append(results, fmt.Sprintf("Block %d: Access denied: %v", i, err))
			continue
		}

		contentBytes, err := os.ReadFile(pathStr)
		if err != nil {
			results = append(results, fmt.Sprintf("Block %d: File not found: %s", i, p.FilePath))
			continue
		}

		content := string(contentBytes)
		hasCRLF := strings.Contains(content, "\r\n")
		normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")
		normalizedSearch := strings.ReplaceAll(p.Search, "\r\n", "\n")
		normalizedReplace := strings.ReplaceAll(p.Replace, "\r\n", "\n")

		if !strings.Contains(normalizedContent, normalizedSearch) {
			results = append(results, fmt.Sprintf("Block %d: SEARCH text not found in %s", i, p.FilePath))
			continue
		}

		newContent := strings.Replace(normalizedContent, normalizedSearch, normalizedReplace, 1)
		if hasCRLF {
			newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
		}

		if err := os.WriteFile(pathStr, []byte(newContent), 0644); err != nil {
			results = append(results, fmt.Sprintf("Block %d: Failed to write %s: %v", i, p.FilePath, err))
			continue
		}

		results = append(results, fmt.Sprintf("Block %d: Successfully patched %s", i, p.FilePath))
	}

	output := strings.Join(results, "\n")
	if output == "" {
		output = "No patches applied."
	}
	return &tool.ToolResult{
		Title:  fmt.Sprintf("Applied %d patch(es)", len(params.Patches)),
		Output: output,
	}, nil
}
