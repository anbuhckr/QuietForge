package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sashabaranov/go-openai"
)

func ToOpenAIMessages(messages []Message, disableVision bool) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0)
	var bufferedAttachments []openai.ChatCompletionMessage

	for _, msg := range messages {
		role := msg.Role

		if role != "tool" && len(bufferedAttachments) > 0 {
			result = append(result, bufferedAttachments...)
			bufferedAttachments = nil
		}

		var textParts []string
		var toolCalls []openai.ToolCall

		hasCompaction := false
		for _, part := range msg.Parts {
			switch part.Type {
			case "compaction":
				hasCompaction = true
				textParts = append(textParts, part.Content)

			case "text":
				textParts = append(textParts, part.Content)

			case "tool_use":
				args, ok := part.Arguments.(string)
				if !ok || !json.Valid([]byte(args)) {
					var preview string
					if ok && len(args) > 2048 {
						preview = args[:2048] + fmt.Sprintf("\n...(truncated, total: %d bytes)", len(args))
					} else if ok {
						preview = args
					} else {
						preview = fmt.Sprintf("(non-string arguments: %T)", part.Arguments)
					}
					textParts = append(textParts, fmt.Sprintf(
						"[Tool call error: '%s' (id=%s) had incomplete/invalid JSON arguments. The tool was not executed. Attempted payload preview:\n%s]",
						part.ToolName,
						part.ToolCallID,
						preview,
					))
					continue
				}
				toolCalls = append(toolCalls, openai.ToolCall{
					ID:   part.ToolCallID,
					Type: "function",
					Function: openai.FunctionCall{
						Name:      part.ToolName,
						Arguments: args,
					},
				})

			case "tool_result":
				msg := openai.ChatCompletionMessage{
					Role:       "tool",
					ToolCallID: part.ToolCallID,
					Content:    part.Content,
				}
				result = append(result, msg)

				if len(part.Attachments) > 0 {
					if disableVision {
						bufferedAttachments = append(bufferedAttachments, openai.ChatCompletionMessage{
							Role:    "user",
							Content: "Image attachment from tool result: [Base64 Image Omitted. Vision is disabled in config.]",
						})
					} else {
						var multi []openai.ChatMessagePart
						multi = append(multi, openai.ChatMessagePart{
							Type: openai.ChatMessagePartTypeText,
							Text: "Image attachment from tool result:",
						})
						for _, att := range part.Attachments {
							if url, ok := att["url"].(string); ok {
								multi = append(multi, openai.ChatMessagePart{
									Type:     openai.ChatMessagePartTypeImageURL,
									ImageURL: &openai.ChatMessageImageURL{URL: url},
								})
							}
						}
						bufferedAttachments = append(bufferedAttachments, openai.ChatCompletionMessage{
							Role:         "user",
							MultiContent: multi,
						})
					}
				}
			}
		}

		if hasCompaction {
			role = "user"
		}

		content := strings.Join(textParts, "")
		
		// Strip think tags from history so models don't repeat their own thoughts
		content = regexp.MustCompile(`(?is)<\/?(?:think|thought)>.*?<\/?(?:think|thought)>`).ReplaceAllString(content, "")
		// Handle any remaining unclosed tags just in case
		content = regexp.MustCompile(`(?is)<(?:think|thought)>.*`).ReplaceAllString(content, "")
		content = strings.TrimSpace(content)

		switch role {
		case "assistant":
			entry := openai.ChatCompletionMessage{
				Role:    "assistant",
				Content: content,
			}

			if len(toolCalls) > 0 {
				entry.ToolCalls = toolCalls
			}

			result = append(result, entry)

		case "user", "system":
			result = append(result, openai.ChatCompletionMessage{
				Role:    role,
				Content: content,
			})
		}
	}

	if len(bufferedAttachments) > 0 {
		result = append(result, bufferedAttachments...)
	}

	// Post-processing sanitization pass for strict OpenAI/DeepSeek schema rules
	var sanitized []openai.ChatCompletionMessage
	for i, msg := range result {
		// 1. Remove tool calls that have no corresponding tool message
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			var validCalls []openai.ToolCall
			for _, tc := range msg.ToolCalls {
				found := false
				for j := i + 1; j < len(result); j++ {
					if result[j].Role != "tool" {
						break // Tool messages must follow immediately
					}
					if result[j].ToolCallID == tc.ID {
						found = true
						break
					}
				}
				if found {
					validCalls = append(validCalls, tc)
				}
			}
			msg.ToolCalls = validCalls
			if len(msg.ToolCalls) == 0 && msg.Content == "" {
				continue // Drop empty assistant message
			}
		}

		// 2. Merge consecutive assistant messages
		if msg.Role == "assistant" && len(sanitized) > 0 && sanitized[len(sanitized)-1].Role == "assistant" {
			prev := &sanitized[len(sanitized)-1]
			if prev.Content != "" && msg.Content != "" {
				prev.Content += "\n\n" + msg.Content
			} else if msg.Content != "" {
				prev.Content = msg.Content
			}
			prev.ToolCalls = append(prev.ToolCalls, msg.ToolCalls...)
			continue
		}

		// 2.5 Merge consecutive user messages
		if msg.Role == "user" && len(sanitized) > 0 && sanitized[len(sanitized)-1].Role == "user" {
			prev := &sanitized[len(sanitized)-1]
			if len(prev.MultiContent) > 0 || len(msg.MultiContent) > 0 {
				var merged []openai.ChatMessagePart
				if len(prev.MultiContent) > 0 {
					merged = append(merged, prev.MultiContent...)
				} else if prev.Content != "" {
					merged = append(merged, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeText,
						Text: prev.Content,
					})
					prev.Content = ""
				}
				
				if len(msg.MultiContent) > 0 {
					merged = append(merged, msg.MultiContent...)
				} else if msg.Content != "" {
					merged = append(merged, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeText,
						Text: "\n\n" + msg.Content,
					})
				}
				prev.MultiContent = merged
			} else {
				if prev.Content != "" && msg.Content != "" {
					prev.Content += "\n\n" + msg.Content
				} else if msg.Content != "" {
					prev.Content = msg.Content
				}
			}
			continue
		}

		sanitized = append(sanitized, msg)
	}

	// 3. Ensure no message has an empty Content string to prevent 'omitempty' dropping the field
	for i := range sanitized {
		if sanitized[i].Content == "" {
			if sanitized[i].Role == "tool" {
				sanitized[i].Content = "(empty)"
			} else {
				sanitized[i].Content = " " // Single space satisfies strict schema validators
			}
		}
	}

	// 4. Global safety net for Jinja templates (like Qwen) that strictly require a user message
	hasValidUser := false
	for _, msg := range sanitized {
		if msg.Role == "user" {
			hasValidUser = true
			break
		}
	}
	
	if !hasValidUser {
		dummyUser := openai.ChatCompletionMessage{
			Role:    "user",
			Content: "[System auto-recovery]: Previous conversation context was truncated due to context limits. Please continue your task based on the current visible context.",
		}
		
		if len(sanitized) > 0 && sanitized[0].Role == "system" {
			// Insert after system
			sanitized = append(sanitized[:1], append([]openai.ChatCompletionMessage{dummyUser}, sanitized[1:]...)...)
		} else {
			// Prepend
			sanitized = append([]openai.ChatCompletionMessage{dummyUser}, sanitized...)
		}
	}

	return sanitized
}