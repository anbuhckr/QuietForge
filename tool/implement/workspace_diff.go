package implement

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"quietforge/tool"
	"strings"
)

type WorkspaceDiffTool struct{}

func (t *WorkspaceDiffTool) ID() string {
	return "workspace_diff"
}

func (t *WorkspaceDiffTool) Description() string {
	return "Shows a git diff of the changes made by the agent during the current session, ignoring any pre-existing uncommitted changes made by the user. Use this to verify exactly what you have modified."
}

func (t *WorkspaceDiffTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type": "string",
				"description": "Optional specific file or directory path to diff. If empty, diffs the entire workspace.",
			},
		},
	}
}

func (t *WorkspaceDiffTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	if ctx.Workspace == "" {
		return &tool.ToolResult{
			Error:  "no_workspace",
			Output: "Cannot check diff because no active workspace directory is set for this session.",
		}, nil
	}

	snapHashVal, ok := ctx.Extra["snapHash"]
	if !ok {
		return &tool.ToolResult{
			Error:  "no_snapshot",
			Output: "Cannot check diff because no initial snapshot was taken for this run.",
		}, nil
	}

	snapHash, ok := snapHashVal.(string)
	if !ok || snapHash == "" {
		return &tool.ToolResult{
			Error:  "invalid_snapshot",
			Output: "Invalid snapshot hash format or empty snapshot hash.",
		}, nil
	}

	var params struct {
		Path string `json:"path"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %v", err)
		}
	}

	gitArgs := []string{"diff", "refs/agent/" + snapHash}
	if params.Path != "" {
		gitArgs = append(gitArgs, "--", params.Path)
	}

	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = ctx.Workspace

	output, err := cmd.CombinedOutput()
	result := string(output)
	
	if err != nil {
		if strings.Contains(result, "fatal: bad revision") || strings.Contains(result, "fatal: ambiguous argument") {
			return &tool.ToolResult{
				Error:  "git_error",
				Output: fmt.Sprintf("Failed to run diff: %s", result),
			}, nil
		}
	}

	if strings.TrimSpace(result) == "" {
		result = "No changes found since the start of this session."
	}

	// Truncate output if it's too long
	lines := strings.Split(result, "\n")
	if len(lines) > 800 {
		omitted := len(lines) - 600
		result = strings.Join(lines[:300], "\n") + fmt.Sprintf("\n\n... [%d lines omitted. Output heavily truncated!] ...\n\n", omitted) + strings.Join(lines[len(lines)-300:], "\n")
	}

	return &tool.ToolResult{
		Output: result,
	}, nil
}
