package implement

import (
	"encoding/json"
	"fmt"
	"quietforge/tool"
)

type InvokeSubagentTool struct {
	SpawnFunc func(prompt, agentType, parentSessionID string) (sessionID string, err error)
}

func (t *InvokeSubagentTool) ID() string {
	return "invoke_subagent"
}

func (t *InvokeSubagentTool) Description() string {
	return "Invokes a subagent to run asynchronously in the background. Use this for massive tasks."
}

func (t *InvokeSubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"prompt":        map[string]interface{}{"type": "string", "description": "Detailed task prompt for the sub-agent"},
			"subagent_type": map[string]interface{}{"type": "string", "enum": []string{"explore", "general", "build", "plan"}, "description": "Type of sub-agent to use"},
		},
		"required": []string{"prompt", "subagent_type"},
	}
}

func (t *InvokeSubagentTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	if t.SpawnFunc == nil {
		return &tool.ToolResult{Error: "not_configured", Output: "Subagent spawning is not configured"}, nil
	}

	agentType := params.SubagentType
	if agentType == "" {
		agentType = "build"
	}

	sessionID, err := t.SpawnFunc(params.Prompt, agentType, ctx.SessionID)
	if err != nil {
		return &tool.ToolResult{
			Title:  "Subagent Failed",
			Output: fmt.Sprintf("Failed to spawn subagent: %v", err),
		}, nil
	}

	return &tool.ToolResult{
		Title:  "Subagent Invoked",
		Output: fmt.Sprintf("Subagent (type: %s) spawned successfully.\nSession ID: %s\nIt is now running asynchronously in the background. It will send a message to this chat when it finishes.", agentType, sessionID),
	}, nil
}

var _ tool.Tool = (*InvokeSubagentTool)(nil)
