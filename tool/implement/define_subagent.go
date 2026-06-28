package implement

import (
	"encoding/json"
	"fmt"
	"quietforge/agent"
	"quietforge/tool"
)

type DefineSubagentTool struct{}

func (t *DefineSubagentTool) ID() string {
	return "define_subagent"
}

func (t *DefineSubagentTool) Description() string {
	return "Defines a new type of custom subagent that can be spawned later using invoke_subagent."
}

func (t *DefineSubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Unique name/ID for the new subagent type (e.g. 'codebase_surveyor').",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Human-readable description.",
			},
			"system_prompt": map[string]interface{}{
				"type":        "string",
				"description": "Detailed system prompt string for this agent.",
			},
			"tools": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "List of tool IDs to enable for this agent.",
			},
		},
		"required": []string{"name", "system_prompt", "tools"},
	}
}

func (t *DefineSubagentTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		SystemPrompt string   `json:"system_prompt"`
		Tools        []string `json:"tools"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	if params.Name == "" || params.SystemPrompt == "" {
		return &tool.ToolResult{Error: "invalid_args", Output: "name and system_prompt are required"}, nil
	}

	def := &agent.AgentDefinition{
		ID:                   params.Name,
		Name:                 params.Name,
		SystemPromptTemplate: params.SystemPrompt,
		Tools:                params.Tools,
		PermissionProfiles:   make(map[string]string),
	}

	for _, toolID := range params.Tools {
		def.PermissionProfiles[toolID] = "allowed"
	}

	if err := agent.SaveCustomAgent(ctx.Workspace, def); err != nil {
		return &tool.ToolResult{
			Title:  "Failed to Define Subagent",
			Output: fmt.Sprintf("Error saving custom agent: %v", err),
		}, nil
	}

	return &tool.ToolResult{
		Title:  "Subagent Defined",
		Output: fmt.Sprintf("Successfully defined custom subagent type: '%s'. You can now use invoke_subagent with subagent_type='%s'.", params.Name, params.Name),
	}, nil
}

var _ tool.Tool = (*DefineSubagentTool)(nil)
