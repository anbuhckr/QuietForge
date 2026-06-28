package session

import (
	"context"
	"encoding/json"
	"fmt"
	"quietforge/provider"
	"slices"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

type MessagePart struct {
	Type       string         `json:"type"`
	Content    string         `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Arguments  any         	  `json:"arguments,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Attachments []map[string]any `json:"attachments,omitempty"`
}

type Message struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Role      string         `json:"role"`
	Parts     []MessagePart  `json:"parts"`
	CreatedAt int64          `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func CompactMessages(ctx context.Context, messages []Message, modelContext int, client *provider.Client, onProgress func(string)) []Message {
	usable := GetUsableContextWindow(modelContext, nil)
	totalTokens := EstimateTokens(messages)
	if !NeedsCompaction(totalTokens, usable) {
		return messages
	}
	if client != nil {
		return CompactWithLLM(ctx, messages, usable, client, onProgress)
	}
	return PruneMessages(messages, usable)
}

func SerializeMessage(msg Message) string {
	switch msg.Role {

	case "user":
		var b strings.Builder

		for _, p := range msg.Parts {
			switch p.Type {
			case "text":
				b.WriteString(p.Content)

			case "file":
				b.WriteString("\n[Attached file: ")
				b.WriteString(p.Content)
				b.WriteString("]")
			}
		}

		return "[User]: " + b.String()

	case "assistant":
		var lines []string

		for _, p := range msg.Parts {
			switch p.Type {

			case "text":
				lines = append(lines,
					fmt.Sprintf("[Assistant]: %s", p.Content),
				)

			case "tool_use":
				var args string

				switch v := p.Arguments.(type) {
				case string:
					args = v
				default:
					data, _ := json.Marshal(v)
					args = string(data)
				}

				lines = append(lines,
					fmt.Sprintf("[Assistant tool call]: %s(%s)", p.ToolName, args),
				)

			case "compaction":
				// Ignore.
			}
		}

		return strings.Join(lines, "\n")

	case "tool":
		var lines []string

		for _, p := range msg.Parts {
			if p.Type != "tool_result" {
				continue
			}

			content := p.Content
			if len(content) > 2000 {
				content = content[:2000] + "\n[truncated]"
			}

			lines = append(lines,
				fmt.Sprintf("[Tool result]: %s", content),
			)
		}

		return strings.Join(lines, "\n")

	case "system":
		if len(msg.Parts) > 0 {
			return "[System update]: " + msg.Parts[0].Content
		}
		return "[System update]: "

	default:
		return ""
	}
}

func CompactWithLLM(ctx context.Context, messages []Message, targetTokens int, client *provider.Client, onProgress func(string)) []Message {
	if len(messages) == 0 {
		return messages
	}

	tailTurns := TailTurns
	tailStart := len(messages) - tailTurns*2
	if tailStart < 0 {
		tailStart = 0
	}

	if tailStart <= 0 {
		return PruneMessages(messages, targetTokens)
	}

	head := messages[:tailStart]
	recent := messages[tailStart:]

	var previousSummary string
	headContextMsgs := make([]Message, 0, len(head))

	for _, msg := range head {
		isCompaction := false

		for _, part := range msg.Parts {
			if part.Type == "compaction" {
				previousSummary = part.Content
				isCompaction = true
				break
			}
		}

		if !isCompaction {
			headContextMsgs = append(headContextMsgs, msg)
		}
	}

	var contexts []string
	for _, msg := range headContextMsgs {
		s := SerializeMessage(msg)
		if s != "" {
			contexts = append(contexts, s)
		}
	}
	contextStr := strings.Join(contexts, "\n\n")

	var promptText string
	jsonSchema := `CRITICAL: You MUST strictly output ONLY a valid JSON object with the following schema:
{
  "user_requests": ["Array of recent raw user prompts"],
  "outstanding_requests": ["Array of tasks the user asked for that are not yet finished"],
  "work_accomplished": ["Array of completed steps"],
  "files_and_code": {
    "edited_files": ["Array of files modified"],
    "viewed_files": ["Array of files read"]
  },
  "current_work_and_next_steps": "String detailing exactly what the agent was doing before compaction and what to do immediately next"
}
Do not include markdown formatting or any other text outside the JSON object.`

	if previousSummary != "" {
		promptText = fmt.Sprintf(
			`Update the anchored summary below using the conversation history above.
Preserve still-true details, remove stale details, and merge in the new facts.
%s
<previous-summary>
%s
</previous-summary>

%s`,
			jsonSchema,
			previousSummary,
			contextStr,
		)
	} else {
		promptText = fmt.Sprintf(
			`Create a new anchored summary from the conversation history.
%s

%s`,
			jsonSchema,
			contextStr,
		)
	}

	compactionSystem := LoadPromptTemplate("compaction_system")
	if compactionSystem == "" {
		compactionSystem = "You are an anchored context summarization assistant for coding sessions."
	}

	systemMsg := Message{
		ID:        "sys-compaction",
		SessionID: "",
		Role:      "system",
		Parts: []MessagePart{
			{
				Type:    "text",
				Content: compactionSystem,
			},
		},
	}

	userMsg := Message{
		ID:        "user-compaction",
		SessionID: "",
		Role:      "user",
		Parts: []MessagePart{
			{
				Type:    "text",
				Content: promptText,
			},
		},
	}

	llmMsgs := ToOpenAIMessages([]Message{
		systemMsg,
		userMsg,
	}, false)

	var builder strings.Builder

	if onProgress != nil {
		onProgress("Compacting memory...")
	}
	defer func() {
		if onProgress != nil {
			onProgress("Compaction complete")
		}
	}()

	ev, err := client.Stream(ctx, llmMsgs, []openai.Tool{})
	if err != nil {
		return PruneMessages(messages, targetTokens)
	}

	for e := range ev {
		if e.Type == "text" && e.Text != "" {
			builder.WriteString(e.Text)
		}
	}

	newSummary := strings.TrimSpace(builder.String())
	if newSummary == "" {
		return PruneMessages(messages, targetTokens)
	}

	cleanJson := strings.TrimPrefix(newSummary, "```json")
	cleanJson = strings.TrimPrefix(cleanJson, "```")
	cleanJson = strings.TrimSuffix(cleanJson, "```")
	cleanJson = strings.TrimSpace(cleanJson)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(cleanJson), &parsed); err == nil {
		if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
			newSummary = "```json\n" + string(pretty) + "\n```"
		}
	}

	now := time.Now().UnixMilli()

	compactionMsg := Message{
		ID:        fmt.Sprintf("compaction-%d", now),
		SessionID: messages[0].SessionID,
		Role:      "user",
		CreatedAt: now,
		Parts: []MessagePart{
			{
				Type:    "compaction",
				Content: newSummary,
			},
		},
	}
	var protectedMsgs []Message
	var protectedToolCallIDs []string

	for _, msg := range head {
		isProtected := false
		for _, part := range msg.Parts {
			if part.Type == "tool_use" && slices.Contains(ProtectedTools, part.ToolName) {
				isProtected = true
				break
			}
		}
		if isProtected {
			protectedMsgs = append(protectedMsgs, msg)
			for _, part := range msg.Parts {
				if part.Type == "tool_use" {
					protectedToolCallIDs = append(protectedToolCallIDs, part.ToolCallID)
				}
			}
			continue
		}

		if msg.Role == "tool" {
			hasProtectedResult := false
			for _, part := range msg.Parts {
				if part.Type == "tool_result" && slices.Contains(protectedToolCallIDs, part.ToolCallID) {
					hasProtectedResult = true
					break
				}
			}
			if hasProtectedResult {
				protectedMsgs = append(protectedMsgs, msg)
			}
		}
	}

	result := make([]Message, 0, len(protectedMsgs)+1+len(recent))
	result = append(result, protectedMsgs...)
	result = append(result, compactionMsg)
	result = append(result, recent...)

	return result
}

func PruneMessages(messages []Message, targetTokens int) []Message {
	if len(messages) == 0 {
		return messages
	}

	protected := make(map[int]struct{})
	protectedCallIDs := make(map[string]struct{})

	for i, msg := range messages {
		isProtected := false
		for _, part := range msg.Parts {
			if part.Type == "tool_use" && slices.Contains(ProtectedTools, part.ToolName) {
				isProtected = true
				break
			}
		}
		if isProtected {
			protected[i] = struct{}{}
			for _, part := range msg.Parts {
				if part.Type == "tool_use" {
					protectedCallIDs[part.ToolCallID] = struct{}{}
				}
			}
		}
	}

	for i, msg := range messages {
		if msg.Role == "tool" {
			for _, part := range msg.Parts {
				if part.Type == "tool_result" {
					if _, ok := protectedCallIDs[part.ToolCallID]; ok {
						protected[i] = struct{}{}
					}
				}
			}
		}
	}

	tailTurns := TailTurns
	tailStart := len(messages) - tailTurns*2
	if tailStart < 0 {
		tailStart = 0
	}

	var result []Message
	keptTokens := 0
	keptToolCallIDs := make(map[string]struct{})

	for i := len(messages) - 1; i >= tailStart; i-- {
		msg := messages[i]
		msgTokens := EstimateTokens([]Message{msg})
		_, isProtected := protected[i]

		keep := false
		if keptTokens+msgTokens <= targetTokens || isProtected {
			keep = true
		}

		if keep && msg.Role == "assistant" && !isProtected {
			for _, part := range msg.Parts {
				if part.Type == "tool_use" {
					if _, ok := keptToolCallIDs[part.ToolCallID]; !ok {
						keep = false
						break
					}
				}
			}
		}

		if keep || isProtected {
			result = append([]Message{msg}, result...)
			keptTokens += msgTokens

			if msg.Role == "tool" {
				for _, part := range msg.Parts {
					if part.Type == "tool_result" {
						keptToolCallIDs[part.ToolCallID] = struct{}{}
					}
				}
			}
		}
	}

	if keptTokens < targetTokens && tailStart > 0 {
		for i := tailStart - 1; i >= 0; i-- {
			msg := messages[i]
			if _, ok := protected[i]; ok {
				result = append([]Message{msg}, result...)
			}
		}
	}

	// Safety fallback.
	if len(result) < 2 && len(messages) > 0 {
		start := len(messages) - 4
		if start < 0 {
			start = 0
		}
		result = append([]Message(nil), messages[start:]...)
	}

	return result
}

func EstimateTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		parts := make([]map[string]any, 0, len(msg.Parts))
		for _, p := range msg.Parts {
			parts = append(parts, map[string]any{
				"type":      p.Type,
				"content":   p.Content,
				"arguments": p.Arguments,
			})
		}
		content, err := json.Marshal(map[string]any{
			"role":  msg.Role,
			"parts": parts,
		})
		if err != nil {
			continue
		}
		total += len(content) / 4
	}
	return total
}

