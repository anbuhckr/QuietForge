package implement

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"quietforge/tool"
	"quietforge/util"
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

	patchFile := filepath.Join(workspace, ".quietforge_temp.patch")
	if err := os.WriteFile(patchFile, []byte(params.Patch), 0644); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to write patch file: %v", err)}, nil
	}
	defer os.Remove(patchFile)

	var gitOut, gitErr bytes.Buffer
	gitCmd := exec.Command("git", "apply", ".quietforge_temp.patch")
	gitCmd.Dir = workspace
	gitCmd.Stdout = &gitOut
	gitCmd.Stderr = &gitErr
	if gitCmd.Run() == nil {
		return &tool.ToolResult{Output: "Patch applied successfully via git apply."}, nil
	}

	var patchOut, patchErr bytes.Buffer
	patchCmd := exec.Command("patch", "-p1", "-i", ".quietforge_temp.patch")
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
		if !strings.Contains(content, p.Search) {
			results = append(results, fmt.Sprintf("Block %d: SEARCH text not found in %s", i, p.FilePath))
			continue
		}

		newContent := strings.Replace(content, p.Search, p.Replace, 1)
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
