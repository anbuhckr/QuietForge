package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"quietforge/provider"

	"github.com/sashabaranov/go-openai"
)

type ExecutionEpisode struct {
	Goal          string   `json:"goal"`
	FilesModified []string `json:"files_modified"`
	Commands      []string `json:"commands"`
	Errors        []string `json:"errors"`
	Resolved      bool     `json:"resolved"`
	Duration      string   `json:"duration"`
}

func ExtractExecutionEpisode(ctx context.Context, turnMessages []Message, client *provider.Client) (string, error) {
	if len(turnMessages) == 0 {
		return "", fmt.Errorf("no messages")
	}

	var contexts []string
	for _, msg := range turnMessages {
		// Only care about assistant/tool/user messages relevant to execution
		s := SerializeMessage(msg, 2000)
		if s != "" {
			contexts = append(contexts, s)
		}
	}
	contextStr := strings.Join(contexts, "\n\n")

	promptText := fmt.Sprintf(`CRITICAL: You MUST strictly output ONLY a valid JSON object summarizing the provided execution trace.
Schema:
{
  "goal": "Short description of what the user wanted",
  "files_modified": ["auth.go", "router.go"],
  "commands": ["go build", "go test"], // exclude trivial commands like ls, pwd, cat
  "errors": ["undefined LoginHandler"], // significant compiler or test errors
  "resolved": true, // did they succeed?
  "duration": "2 min" // estimate based on trace
}

Execution Trace:
%s`, contextStr)

	req := openai.ChatCompletionRequest{
		Model: client.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    "system",
				Content: promptText,
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
		MaxTokens:   500,
		Temperature: 0.1,
	}

	resp, err := client.RawClient().CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) > 0 {
		content := resp.Choices[0].Message.Content
		// validate json
		var ep ExecutionEpisode
		if err := json.Unmarshal([]byte(content), &ep); err == nil {
			return content, nil
		}
	}
	return "", fmt.Errorf("failed to extract episode")
}
