package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type AgentDefinition struct {
	ID                   string
	Name                 string
	Model                string
	Temperature          float64
	SystemPromptTemplate string
	Tools                []string
	PermissionProfiles   map[string]string
}

const (
	AgentBuild      = "build"
	AgentPlan       = "plan"
	AgentGeneral    = "general"
	AgentExplore    = "explore"
	AgentCompaction = "compaction"
	AgentTitle      = "title"
	AgentSummary    = "summary"
)

var (
	builtinAgentsMu sync.RWMutex
)

var BuiltinAgents = map[string]*AgentDefinition{
	AgentBuild: {
		ID:                   AgentBuild,
		Name:                 "Build",
		SystemPromptTemplate: "default_system",
		Tools: []string{"read", "write", "edit", "apply_patch", "grep", "glob", "shell", "webfetch", "websearch", "invoke_subagent", "question", "todowrite", "skill", "lsp", "ast_search", "revert_workspace", "mcp", "plan_exit", "write_artifact", "invalid"},
		PermissionProfiles: map[string]string{
			"read":        "allowed",
			"write":       "allowed",
			"edit":        "allowed",
			"apply_patch": "allowed",
			"grep":        "allowed",
			"glob":        "allowed",
			"shell":       "prompt",
			"webfetch":    "allowed",
			"websearch":   "allowed",
			"invoke_subagent": "allowed",
			"question":    "allowed",
			"todowrite":   "allowed",
			"skill":       "allowed",
			"lsp":         "allowed",
			"ast_search":  "allowed",
			"mcp":         "allowed",
			"plan_exit":   "allowed",
			"write_artifact": "allowed",
			"invalid":     "allowed",
		},
	},
	AgentPlan: {
		ID:                   AgentPlan,
		Name:                 "Plan",
		SystemPromptTemplate: "plan_system",
		Tools: []string{"read", "grep", "glob", "shell", "webfetch", "websearch", "invoke_subagent", "question", "todowrite", "skill", "lsp", "ast_search", "revert_workspace", "mcp", "write_artifact", "invalid"},
		PermissionProfiles: map[string]string{
			"read":        "allowed",
			"grep":        "allowed",
			"glob":        "allowed",
			"shell":       "prompt",
			"webfetch":    "allowed",
			"websearch":   "allowed",
			"invoke_subagent": "allowed",
			"question":    "allowed",
			"todowrite":   "allowed",
			"skill":       "allowed",
			"lsp":         "allowed",
			"ast_search":  "allowed",
			"mcp":            "allowed",
			"write_artifact": "allowed",
			"invalid":        "allowed",
		},
	},
	AgentGeneral: {
		ID:                   AgentGeneral,
		Name:                 "General",
		SystemPromptTemplate: "default_system",
		Tools: []string{"read", "write", "edit", "apply_patch", "grep", "glob", "shell", "webfetch", "websearch", "invoke_subagent", "question", "todowrite", "skill", "lsp", "mcp", "invalid"},
		PermissionProfiles: map[string]string{
			"read":        "allowed",
			"write":       "prompt",
			"edit":        "prompt",
			"apply_patch": "prompt",
			"shell":       "prompt",
			"mcp":         "prompt",
		},
	},
	AgentExplore: {
		ID:                 AgentExplore,
		Name:               "Explore",
		Tools: []string{"read", "grep", "glob", "webfetch", "websearch", "mcp"},
		PermissionProfiles: map[string]string{
			"read":        	"allowed",
			"grep":       	"allowed",
			"glob":        	"allowed",
			"webfetch": 	"allowed",
			"websearch":    "allowed",
		},
	},
	AgentCompaction: {
		ID:                 AgentCompaction,
		Name:               "Compaction",
	},
	AgentTitle: {
		ID:                 AgentTitle,
		Name:               "Title",
	},
	AgentSummary: {
		ID:                 AgentSummary,
		Name:               "Summary",
	},
}

var (
	customAgentsMu sync.RWMutex
	customAgents   = make(map[string]*AgentDefinition)
)

func LoadCustomAgents(workspace string) error {
	agentsPath := filepath.Join(workspace, ".quietforge", "agents.json")
	b, err := os.ReadFile(agentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []*AgentDefinition
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}

	customAgentsMu.Lock()
	defer customAgentsMu.Unlock()
	for _, a := range list {
		customAgents[a.ID] = a
	}
	return nil
}

func SaveCustomAgent(workspace string, def *AgentDefinition) error {
	customAgentsMu.Lock()
	customAgents[def.ID] = def

	var list []*AgentDefinition
	for _, a := range customAgents {
		list = append(list, a)
	}
	customAgentsMu.Unlock()

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	agentsDir := filepath.Join(workspace, ".quietforge")
	os.MkdirAll(agentsDir, 0755)
	return os.WriteFile(filepath.Join(agentsDir, "agents.json"), b, 0644)
}

func GetAgent(id string) *AgentDefinition {
	builtinAgentsMu.RLock()
	if agent, ok := BuiltinAgents[id]; ok {
		builtinAgentsMu.RUnlock()
		return agent
	}
	builtinAgentsMu.RUnlock()
	customAgentsMu.RLock()
	defer customAgentsMu.RUnlock()
	if agent, ok := customAgents[id]; ok {
		return agent
	}
	return nil
}

func GetAgentTools(id string) []string {
	agent := GetAgent(id)
	if agent != nil {
		return agent.Tools
	}
	return []string{}
}

func IsToolAllowed(toolID string, tools []string) bool {
	hasMCP := false
	for _, p := range tools {
		if p == "mcp" {
			hasMCP = true
		}
		if p == toolID {
			return true
		}
		if len(p) > 0 && p[len(p)-1] == '*' && len(toolID) >= len(p)-1 {
			if toolID[:len(p)-1] == p[:len(p)-1] {
				return true
			}
		}
	}
	// Auto-allow any dynamically registered MCP tool (name__tool format)
	// when the agent has "mcp" in its tool list
	if hasMCP && strings.Contains(toolID, "__") {
		return true
	}
	return false
}

func GetAgentSystemPrompt(id string) string {
	agent := GetAgent(id)
	if agent != nil {
		return agent.SystemPromptTemplate
	}
	return ""
}