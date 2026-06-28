package implement

import (
	"quietforge/session"
	"quietforge/tool"
	"encoding/json"
	"fmt"
)

type PlanExitTool struct{}

func (t *PlanExitTool) ID() string {
	return "plan_exit"
}

func (t *PlanExitTool) Description() string {
	return "Exit plan mode and switch back to the build agent."
}

func (t *PlanExitTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"summary":   map[string]interface{}{"type": "string", "description": "Summary of what was accomplished during planning"},
			"plan_path": map[string]interface{}{"type": "string", "description": "Path to the plan file created"},
		},
		"required": []string{"summary"},
	}
}

func (t *PlanExitTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Summary  string `json:"summary"`
		PlanPath string `json:"plan_path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	if s, ok := ctx.Extra["session"].(*session.Session); ok {
		s.SetAgent("build")
	}

	return &tool.ToolResult{
		Title:  "Switching to build agent",
		Output: fmt.Sprintf("Plan completed: %s\nSwitching to build agent.", params.Summary),
		Metadata: map[string]interface{}{
			"summary":   params.Summary,
			"plan_path": params.PlanPath,
		},
	}, nil
}

type InvalidTool struct{}

func (t *InvalidTool) ID() string {
	return "invalid"
}

func (t *InvalidTool) Description() string {
	return "Reports that a tool call was invalid or not recognized."
}

func (t *InvalidTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool":  map[string]interface{}{"type": "string", "description": "The attempted tool name"},
			"error": map[string]interface{}{"type": "string", "description": "Why the call was invalid"},
		},
		"required": []string{"tool", "error"},
	}
}

func (t *InvalidTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Tool  string `json:"tool"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	return &tool.ToolResult{
		Title:  "Invalid tool call",
		Output: fmt.Sprintf("Tool '%s' is not available: %s", params.Tool, params.Error),
	}, nil
}
