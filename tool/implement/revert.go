package implement

import (
	"quietforge/tool"
	"quietforge/util"
)

type RevertTool struct{}

func (t *RevertTool) ID() string {
	return "revert_workspace"
}

func (t *RevertTool) Description() string {
	return "Reverts the entire workspace back to the initial pristine state from the start of the run. Use this ONLY if you are hopelessly stuck in an error loop or made catastrophic architectural mistakes and need to start over completely."
}

func (t *RevertTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *RevertTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	if ctx.Workspace == "" {
		return &tool.ToolResult{
			Error:  "no_workspace",
			Output: "Cannot revert because no active workspace directory is set for this session.",
		}, nil
	}

	snapHashVal, ok := ctx.Extra["snapHash"]
	if !ok {
		return &tool.ToolResult{
			Error:  "no_snapshot",
			Output: "Cannot revert because no initial snapshot was taken for this run.",
		}, nil
	}

	snapHash, ok := snapHashVal.(string)
	if !ok || snapHash == "" {
		return &tool.ToolResult{
			Error:  "invalid_snapshot",
			Output: "Invalid snapshot hash format or empty snapshot hash.",
		}, nil
	}

	snapManager := util.NewSnapshotManager(ctx.Workspace)
	success := snapManager.Restore(snapHash)

	if !success {
		return &tool.ToolResult{
			Error:  "restore_failed",
			Output: "Failed to restore workspace using git. You may need to ask the user for help.",
		}, nil
	}

	return &tool.ToolResult{
		Title:  "Workspace Reverted",
		Output: "Successfully reverted the entire workspace back to its original state! All changes made during this run have been erased. You can now try a completely different approach.",
	}, nil
}
