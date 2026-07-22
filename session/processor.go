package session

import (
	"context"
	"encoding/json"
	"fmt"

	"quietforge/agent"
	"quietforge/permission"
	"quietforge/provider"
	"quietforge/tool"
	"quietforge/util"
)

const (
	PermissionDeniedMessage = "Permission denied."
)

type AskPermissionFn func(toolName, toolInput, agentID string) (bool, error)

type SessionProcessor struct {
	registry        *tool.Registry
	permissionRules permission.Ruleset
	askPermission   AskPermissionFn
	workspace       string
	SnapHash        string
}

func NewSessionProcessor(registry *tool.Registry, permissionRules permission.Ruleset, askPermission AskPermissionFn, workspace string) *SessionProcessor {
	return &SessionProcessor{
		registry:        registry,
		permissionRules: permissionRules,
		askPermission:   askPermission,
		workspace:       workspace,
	}
}

func (sp *SessionProcessor) ProcessToolCall(tc provider.ToolCall, session *Session, agentID string) *tool.ToolResult {
	agentDef := agent.GetAgent(agentID)
	toolDefs := sp.registry.GetAll()

	allowed := false
	if agentDef != nil {
		for _, t := range toolDefs {
			if t.ID() == tc.Name && agent.IsToolAllowed(agentID, tc.Name) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return &tool.ToolResult{Output: fmt.Sprintf("Tool '%s' is not available in %s mode. Switch to build mode to make changes.", tc.Name, agentID), Error: "not_allowed"}
	}

	t, err := sp.registry.GetTool(tc.Name)
	if err != nil {
		return &tool.ToolResult{Output: fmt.Sprintf("Tool not found: %s", tc.Name), Error: "not_found"}
	}

	var argsMap map[string]any
	if err := json.Unmarshal([]byte(tc.Arguments), &argsMap); err != nil {
		return &tool.ToolResult{Output: fmt.Sprintf("Invalid JSON arguments: %v", err), Error: "parse_error"}
	}

	permAction := sp.checkPermission(tc.Name, tc.Arguments, agentID)
	switch permAction {
	case "deny":
		return &tool.ToolResult{Output: PermissionDeniedMessage, Error: "permission_denied"}
	case "ask":
		if sp.askPermission != nil {
			allowed, err := sp.askPermission(tc.Name, tc.Arguments, agentID)
			if err != nil || !allowed {
				return &tool.ToolResult{Output: "User denied permission to execute this tool.", Error: "permission_denied"}
			}
		}
	}

	toolCtx := &tool.ToolContext{
		SessionID: session.SessionID,
		Agent:     agentID,
		Workspace: sp.workspace,
		Context:   context.Background(),
		Extra: map[string]any{
			"session":  session,
			"repo":     session.Repo,
			"snapHash": sp.SnapHash,
		},
	}

	argsBytes, _ := json.Marshal(argsMap)
	result, err := t.Execute(argsBytes, toolCtx)
	if err != nil {
		return &tool.ToolResult{Output: fmt.Sprintf("Tool execution error: %v", err), Error: "execution_error"}
	}
	
	if result != nil && result.Output != "" {
		result.Output = util.TruncateToolOutput(result.Output)
	}
	
	return result
}

func (sp *SessionProcessor) checkPermission(toolName, toolInput, agentID string) string {
	if len(sp.permissionRules) > 0 {
		ev := permission.Evaluate(toolName, toolInput, sp.permissionRules)
		return string(ev.Action)
	}
	return "allowed"
}

