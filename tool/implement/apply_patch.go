package implement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"quietforge/tool"
	"quietforge/util"
	"strings"
	"time"
)

const (
	patchTimeout    = 30 * time.Second
	maxPatchOutput  = 2000 // chars of stderr on failure
	maxSRFileSize   = 5 * 1024 * 1024 // 5MB cap for search/replace files
)

type ApplyPatchTool struct{}

func (t *ApplyPatchTool) ID() string {
	return "apply_unified_patch"
}

func (t *ApplyPatchTool) Description() string {
	return "Apply a unified diff patch to the codebase. Ensure the diff format is strictly correct."
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

	if strings.TrimSpace(params.Patch) == "" {
		return &tool.ToolResult{Error: "invalid_args", Output: "Patch content is empty."}, nil
	}

	workspace := ctx.Workspace
	if workspace == "" {
		workspace, _ = os.Getwd()
	}

	if err := validateUnifiedPatchPaths(workspace, params.Patch); err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	tmpFile, err := os.CreateTemp(workspace, ".quietforge_temp_*.patch")
	if err != nil {
		return &tool.ToolResult{
			Error:  "write_error",
			Output: fmt.Sprintf("Failed to create temporary patch file: %v", err),
		}, nil
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(params.Patch); err != nil {
		_ = tmpFile.Close()
		return &tool.ToolResult{
			Error:  "write_error",
			Output: fmt.Sprintf("Failed to write patch file: %v", err),
		}, nil
	}

	if err := tmpFile.Close(); err != nil {
		return &tool.ToolResult{
			Error:  "write_error",
			Output: fmt.Sprintf("Failed to finalize patch file: %v", err),
		}, nil
	}

	patchFile := tmpFile.Name()

	execCtx, execCancel := context.WithTimeout(ctx.Context, patchTimeout)
	defer execCancel()

	// Try git apply first
	var gitErr bytes.Buffer
	gitCmd := exec.CommandContext(execCtx, "git", "apply", "--recount", "--whitespace=fix", patchFile)
	gitCmd.Dir = workspace
	gitCmd.Stderr = &gitErr
	if gitCmd.Run() == nil {
		return &tool.ToolResult{Output: "Patch applied successfully via git apply."}, nil
	}

	// Fall back to patch utility
	var patchErr bytes.Buffer
	patchCmd := exec.CommandContext(execCtx, "patch", "-p1", "--fuzz=3", "-i", patchFile)
	patchCmd.Dir = workspace
	patchCmd.Stderr = &patchErr
	if patchCmd.Run() == nil {
		return &tool.ToolResult{Output: "Patch applied successfully via patch utility."}, nil
	}

	// Truncate error output
	gitErrStr := gitErr.String()
	if len(gitErrStr) > maxPatchOutput {
		gitErrStr = gitErrStr[:maxPatchOutput] + "... [truncated]"
	}
	patchErrStr := patchErr.String()
	if len(patchErrStr) > maxPatchOutput {
		patchErrStr = patchErrStr[:maxPatchOutput] + "... [truncated]"
	}

	return &tool.ToolResult{
		Error:  "patch_failed",
		Output: fmt.Sprintf("Failed to apply patch.\nGit error: %s\nPatch error: %s", gitErrStr, patchErrStr),
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

// ---------------------------------------------------------------------------
// SearchReplaceTool — SEARCH/REPLACE blocks
// ---------------------------------------------------------------------------

type SearchReplaceTool struct{}

func (t *SearchReplaceTool) ID() string {
	return "apply_patch"
}

func (t *SearchReplaceTool) Description() string {
	return "Apply SEARCH/REPLACE blocks to modify files. Each block must have EXACT character matching SEARCH text, including indentation and newlines."
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

	// Group blocks by file, then apply all blocks per file atomically.
	// Build: file → ordered list of {search, replace, blockIndex}
	type blockOp struct {
		search  string
		replace string
		idx     int
	}
	fileOps := make(map[string][]blockOp)
	for i, p := range params.Patches {
		pathStr, err := util.JailPath(ctx.Workspace, p.FilePath)
		if err != nil {
			return &tool.ToolResult{Error: "access_denied", Output: fmt.Sprintf("Block %d: Access denied: %v", i, err)}, nil
		}
		fileOps[pathStr] = append(fileOps[pathStr], blockOp{p.Search, p.Replace, i})
	}

	var results []string
	patchesApplied := 0

	for pathStr, ops := range fileOps {
		select {
		case <-ctx.Context.Done():
			results = append(results, "Cancelled.")
			goto doneSearch
		default:
		}

		info, err := os.Stat(pathStr)
		if err != nil {
			for _, op := range ops {
				results = append(results, fmt.Sprintf("Block %d: File not found: %s", op.idx, pathStr))
			}
			continue
		}
		if info.Size() > maxSRFileSize {
			for _, op := range ops {
				results = append(results, fmt.Sprintf("Block %d: File too large (%d bytes): %s", op.idx, info.Size(), pathStr))
			}
			continue
		}

		raw, err := os.ReadFile(pathStr)
		if err != nil {
			for _, op := range ops {
				results = append(results, fmt.Sprintf("Block %d: Cannot read: %s", op.idx, pathStr))
			}
			continue
		}

		content := string(raw)
		hasCRLF := strings.Contains(content, "\r\n")
		if hasCRLF {
			content = strings.ReplaceAll(content, "\r\n", "\n")
		}

		// Validate and apply against evolving content — chained edits work if
		// each SEARCH matches exactly once in the content AFTER prior blocks.
		working := content
		applied := make(map[int]struct{})
		fileFailed := false

		for _, op := range ops {
			normalizedSearch := strings.ReplaceAll(op.search, "\r\n", "\n")
			count := strings.Count(working, normalizedSearch)
			if count == 0 {
				results = append(results, fmt.Sprintf("Block %d: SEARCH text not found in %s. ERROR: The exact character-sequence was not found. This is almost always caused by incorrect indentation, missing trailing whitespace, or hallucinated lines in your SEARCH block. Do not guess the whitespace. Use a read tool with line numbers to get the EXACT text.", op.idx, pathStr))
				fileFailed = true
				break
			}
			if count > 1 {
				results = append(results, fmt.Sprintf("Block %d: SEARCH text found %d times in %s. Must match exactly once. Provide more surrounding context to make the match unique.", op.idx, count, pathStr))
				fileFailed = true
				break
			}

			normalizedReplace := strings.ReplaceAll(op.replace, "\r\n", "\n")
			working = strings.Replace(working, normalizedSearch, normalizedReplace, 1)
			applied[op.idx] = struct{}{}
		}

		if fileFailed || len(applied) == 0 {
			continue
		}

		if hasCRLF {
			working = strings.ReplaceAll(working, "\n", "\r\n")
		}

		perm := info.Mode().Perm()
		if err := os.WriteFile(pathStr, []byte(working), perm); err != nil {
			results = append(results, fmt.Sprintf("%s: Write failed: %v", pathStr, err))
			continue
		}

		patchesApplied += len(applied)
		for _, op := range ops {
			if _, ok := applied[op.idx]; ok {
				results = append(results, fmt.Sprintf("Block %d: Successfully patched %s", op.idx, pathStr))
			}
		}
	}
doneSearch:

	output := strings.Join(results, "\n")
	if output == "" {
		output = "No patches applied."
	}
	return &tool.ToolResult{
		Title:  fmt.Sprintf("Applied %d patch(es)", patchesApplied),
		Output: output,
	}, nil
}
