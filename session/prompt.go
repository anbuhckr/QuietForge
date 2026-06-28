package session

import (
	"context"
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

	pm.SystemPrompt = BuildSystemPrompt(agentID, toolDefinitions, env, extraInstructions, workspace)
	return pm.SystemPrompt
}

func (pm *PromptManager) PrepareMessages(ctx context.Context, agentID string, modelContext int, client *provider.Client, onProgress func(string)) []Message {
	history := pm.Session.GetHistory()

	for i, msg := range history {
		if msg.Role == "assistant" {
			for j, part := range msg.Parts {
				if part.Type == "text" && part.Content != "" {
					history[i].Parts[j].Content = thinkPattern.ReplaceAllString(part.Content, "")
				}
			}
		}
	}

	compactCfg := make(map[string]any)
	if cc, ok := pm.Config["compaction"].(map[string]any); ok {
		compactCfg = cc
	}

	if enabled, ok := compactCfg["enabled"].(bool); !ok || enabled {
		history = CompactMessages(ctx, history, compactCfg, modelContext, client, onProgress)
	}

	systemMsg := Message{
		ID:        "system-0",
		SessionID: pm.Session.SessionID,
		Role:      "system",
		Parts:     []MessagePart{{Type: "text", Content: pm.SystemPrompt}},
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
