package implement

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"quietforge/session"
	"quietforge/tool"
	"strings"
)

type UserDiffTool struct{}

func (t *UserDiffTool) ID() string {
	return "user_diff"
}

func (t *UserDiffTool) Description() string {
	return "Shows a git diff of the manual changes made by the user in the workspace since the agent last finished working. Use this ONLY when the user explicitly asks you to review or check what they have added/modified."
}

func (t *UserDiffTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type": "string",
				"description": "Optional specific file or directory path to diff.",
			},
		},
	}
}

func (t *UserDiffTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	if ctx.Workspace == "" {
		return &tool.ToolResult{
			Error:  "no_workspace",
			Output: "Cannot check user diff because no active workspace directory is set.",
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

	var lastMsgID string
	if sessVal, ok := ctx.Extra["session"]; ok {
		if sess, ok := sessVal.(*session.Session); ok {
			var userMsgIDs []string
			for _, m := range sess.Messages {
				if m.Role == "user" && m.Metadata != nil {
					if snapHash, ok := m.Metadata["snapshot"].(string); ok && strings.HasPrefix(snapHash, "pre-") {
						userMsgIDs = append(userMsgIDs, strings.TrimPrefix(snapHash, "pre-"))
					}
				}
			}
			
			if len(userMsgIDs) >= 2 {
				// The last item is the current message. We want the one before it.
				lastMsgID = userMsgIDs[len(userMsgIDs)-2]
			}
		}
	}

	gitArgs := []string{"diff"}
	if lastMsgID != "" {
		gitArgs = append(gitArgs, "refs/agent/post-"+lastMsgID)
	} else {
		gitArgs = append(gitArgs, "HEAD")
	}

	if params.Path != "" {
		gitArgs = append(gitArgs, "--", params.Path)
	}

	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = ctx.Workspace

	output, err := cmd.CombinedOutput()
	result := string(output)

	if err != nil {
		if strings.Contains(result, "fatal: bad revision") || strings.Contains(result, "fatal: ambiguous argument") {
			// fallback to HEAD if the post- reference doesn't exist for some reason
			cmd = exec.Command("git", append([]string{"diff", "HEAD", "--"}, params.Path)...)
			cmd.Dir = ctx.Workspace
			output, _ = cmd.CombinedOutput()
			result = string(output)
		}
	}

	if strings.TrimSpace(result) == "" {
		result = "No manual changes found by the user."
	}

	lines := strings.Split(result, "\n")
	if len(lines) > 800 {
		omitted := len(lines) - 600
		result = strings.Join(lines[:300], "\n") + fmt.Sprintf("\n\n... [%d lines omitted. Output heavily truncated!] ...\n\n", omitted) + strings.Join(lines[len(lines)-300:], "\n")
	}

	return &tool.ToolResult{
		Output: result,
	}, nil
}
