package session

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"quietforge/provider"
)

var (
	planEnterPattern = regexp.MustCompile(`(?i)\[ENTER_PLAN_MODE\]`)
	planExitPattern  = regexp.MustCompile(`(?i)\[EXIT_PLAN_MODE\]`)
	thinkPattern     = regexp.MustCompile(`(?is)<(?:think|thought)>.*?</(?:think|thought)>`)
)

type PromptManager struct {
	Session      *Session
	Config       map[string]any
	SystemPrompt string
}

func NewPromptManager(session *Session, config ...map[string]any) *PromptManager {
	pm := &PromptManager{Session: session, Config: make(map[string]any)}
	if len(config) > 0 && config[0] != nil {
		pm.Config = config[0]
	}
	return pm
}

func (pm *PromptManager) BuildSystemPrompt(agentID string, toolDefinitions []map[string]any, workspace string) string {
	env := make(map[string]string)
	if e, ok := pm.Config["env"].(map[string]any); ok {
		for k, v := range e {
			if vs, ok := v.(string); ok {
				env[k] = vs
			}
		}
	}

	var extraInstructions []string
	if ei, ok := pm.Config["extra_instructions"].([]any); ok {
		for _, v := range ei {
			if s, ok := v.(string); ok {
				extraInstructions = append(extraInstructions, s)
			}
		}
	}

	pm.SystemPrompt = BuildSystemPrompt(agentID, toolDefinitions, env, extraInstructions, workspace, "") // Updated via main.go hook
	return pm.SystemPrompt
}

func (pm *PromptManager) PrepareMessages(ctx context.Context, agentID string, modelContext int, client *provider.Client, onProgress func(string), dynamicContext string) []Message {
	history := pm.Session.GetHistory()

	for i, msg := range history {
		if msg.Role == "assistant" {
			partsCopy := make([]MessagePart, len(msg.Parts))
			copy(partsCopy, msg.Parts)
			for j, part := range partsCopy {
				if part.Type == "text" && part.Content != "" {
					if thinkPattern.MatchString(part.Content) {
						partsCopy[j].Content = thinkPattern.ReplaceAllString(part.Content, "<think>\n[Thought process omitted for context limits]\n</think>")
					}
				}
			}
			history[i].Parts = partsCopy
		}
	}

	compactCfg := make(map[string]any)
	if cc, ok := pm.Config["compaction"].(map[string]any); ok {
		compactCfg = cc
	}

	autoCompaction := true
	if a, ok := compactCfg["auto"].(bool); ok {
		autoCompaction = a
	} else if enabled, ok := compactCfg["enabled"].(bool); ok {
		autoCompaction = enabled
	}

	if autoCompaction {
		compacted := CompactMessages(ctx, history, compactCfg, modelContext, client, onProgress)
		if len(compacted) < len(history) {
			pm.Session.ReplaceMessages(compacted)
			history = compacted
		}
	}

	todoStatus := ""
	if pm.Session != nil && pm.Session.Repo != nil {
		if todos, err := pm.Session.Repo.ListTodos(pm.Session.SessionID); err == nil && len(todos) > 0 {
			var lines []string
			for _, t := range todos {
				marker := "[ ]"
				switch t.Status {
				case "pending":
					marker = "[ ]"
				case "in_progress":
					marker = "[~]"
				case "completed":
					marker = "[x]"
				case "cancelled":
					marker = "[-]"
				}
				lines = append(lines, fmt.Sprintf("%s %s: %s", marker, t.ID, t.Content))
			}
			todoStatus = "\n\n# Active Tasks / Todo List\n" + strings.Join(lines, "\n")
		}
	}

	finalSystemPrompt := pm.SystemPrompt + todoStatus
	if dynamicContext != "" {
		finalSystemPrompt = finalSystemPrompt + "\n\n# Dynamic Turn Context\n" + dynamicContext
	}

	systemMsg := Message{
		ID:        "system-0",
		SessionID: pm.Session.SessionID,
		Role:      "system",
		Parts:     []MessagePart{{Type: "text", Content: finalSystemPrompt}},
	}

	allMsgs := append([]Message{systemMsg}, history...)
	return allMsgs
}

func (pm *PromptManager) ToOpenAI(messages []Message) []any {
	raw := ToOpenAIMessages(messages, false)
	result := make([]any, len(raw))
	for i, m := range raw {
		result[i] = m
	}
	return result
}

func DetectPlanEnter(text string) bool {
	return planEnterPattern.MatchString(text)
}

func DetectPlanExit(text string) bool {
	return planExitPattern.MatchString(text)
}

func StripPlanMarkers(text string) string {
	text = planEnterPattern.ReplaceAllString(text, "")
	text = planExitPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}
