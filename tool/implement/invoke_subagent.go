package implement

import (
	"encoding/json"
	"fmt"
	"quietforge/tool"
)

type InvokeSubagentTool struct {
	SpawnFunc func(prompt, agentType, parentSessionID string) (sessionID string, reportChan <-chan string, err error)
}

func (t *InvokeSubagentTool) ID() string {
	return "invoke_subagent"
}

func (t *InvokeSubagentTool) Description() string {
	return "Invokes a subagent to run in the background. Waits for them to complete. Use this for massive tasks."
}

func (t *InvokeSubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"subagents": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"prompt": map[string]interface{}{"type": "string", "description": "Detailed task prompt for the sub-agent"},
						"subagent_type": map[string]interface{}{"type": "string", "description": "Type of sub-agent to use (e.g., 'build', 'browser', 'plan'). Use 'browser' for automated UI testing and visual debugging tasks."},
					},
					"required": []string{"prompt", "subagent_type"},
				},
				"description": "List of subagents to spawn simultaneously.",
			},
		},
		"required": []string{"subagents"},
	}
}

func (t *InvokeSubagentTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Subagents []struct {
			Prompt       string `json:"prompt"`
			SubagentType string `json:"subagent_type"`
		} `json:"subagents"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	if t.SpawnFunc == nil {
		return &tool.ToolResult{Error: "not_configured", Output: "Subagent spawning is not configured"}, nil
	}

	if len(params.Subagents) == 0 {
		return &tool.ToolResult{Error: "invalid_args", Output: "No subagents specified"}, nil
	}

	type waitTask struct {
		sessionID string
		agentType string
		ch        <-chan string
	}
	var waits []waitTask
	var results []string

	for i, req := range params.Subagents {
		agentType := req.SubagentType
		if agentType == "" {
			agentType = "build"
		}

		sessionID, ch, err := t.SpawnFunc(req.Prompt, agentType, ctx.SessionID)
		if err != nil {
			results = append(results, fmt.Sprintf("[%d] Failed to spawn subagent (type: %s): %v", i, agentType, err))
		} else {
			waits = append(waits, waitTask{sessionID: sessionID, agentType: agentType, ch: ch})
		}
	}

	for _, w := range waits {
		report := <-w.ch
		results = append(results, fmt.Sprintf("=== Subagent %s (%s) Report ===\n%s", w.sessionID, w.agentType, report))
	}

	finalOutput := ""
	for _, res := range results {
		finalOutput += res + "\n\n"
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Invoked %d Subagents", len(params.Subagents)),
		Output: finalOutput,
	}, nil
}

var _ tool.Tool = (*InvokeSubagentTool)(nil)
