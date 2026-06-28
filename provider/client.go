package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"github.com/sashabaranov/go-openai"
)

var Debug bool

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type LLMResponse struct {
	Content      string
	ToolCalls    []ToolCall
	Usage         *Usage
	Model         string
	FinishReason string
}

type StreamEvent struct {
	Type     string    // "text", "tool_use", "usage"
	Text     string
	ToolCall *ToolCall
	Usage    *Usage
}

type Client struct {
	openaiClient   *openai.Client
	Model          string
	knownMaxTokens int
	mu             sync.RWMutex
}

func NewClient(apiKey, baseURL, model string) *Client {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" && baseURL != "https://api.openai.com/v1" {
		config.BaseURL = baseURL
	}
	return &Client{
		openaiClient: openai.NewClientWithConfig(config),
		Model:        model,
	}
}

func (c *Client) RawClient() *openai.Client {
	return c.openaiClient
}

func (c *Client) Generate(ctx context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (*LLMResponse, error) {
	if Debug {
		log.Printf("[DEBUG] Generate: model=%s messages=%d tools=%d", c.Model, len(messages), len(tools))
	}
	req := openai.ChatCompletionRequest{
		Model:       c.Model,
		Messages:    messages,
		Temperature: 0.0,
	}
	if len(tools) > 0 {
		req.Tools = tools
	}

	var resp openai.ChatCompletionResponse
	var err error
	maxTokensLimit := 4096

	c.mu.RLock()
	if c.knownMaxTokens > 0 {
		req.MaxTokens = c.knownMaxTokens
	}
	c.mu.RUnlock()

	for retries := 0; retries < 5; retries++ {
		resp, err = c.openaiClient.CreateChatCompletion(ctx, req)
		if err == nil {
			if req.MaxTokens > 0 {
				c.mu.Lock()
				c.knownMaxTokens = req.MaxTokens
				c.mu.Unlock()
			}
			break
		}
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "422") || strings.Contains(errStr, "max_tokens") {
			if req.MaxTokens == 0 {
				req.MaxTokens = maxTokensLimit
			} else {
				req.MaxTokens /= 2
			}
			if req.MaxTokens < 256 {
				break
			}
			if Debug {
				log.Printf("[DEBUG] Generate 422 error, retrying with MaxTokens=%d", req.MaxTokens)
			}
			continue
		}
		break
	}

	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "1214") || strings.Contains(errStr, "context length") || strings.Contains(errStr, "maximum context") || strings.Contains(errStr, "messages parameter") {
			return nil, fmt.Errorf("context window exceeded: %w", err)
		}
		if Debug {
			log.Printf("[DEBUG] Generate error: %v", err)
		}
		return nil, err
	}

	result := &LLMResponse{
		Model: resp.Model,
	}

	choice := resp.Choices[0]
	result.Content = choice.Message.Content
	result.FinishReason = string(choice.FinishReason)

	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	result.Usage = &Usage{
		InputTokens:  int(resp.Usage.PromptTokens),
		OutputTokens: int(resp.Usage.CompletionTokens),
		TotalTokens:  int(resp.Usage.TotalTokens),
	}

	if Debug {
		log.Printf("[DEBUG] Generate response: content=%d toolCalls=%d finish=%s tokens=%d",
			len(result.Content), len(result.ToolCalls), result.FinishReason, result.Usage.TotalTokens)
	}
	return result, nil
}

// Stream generates a stream of responses from the LLM
func (c *Client) Stream(ctx context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (<-chan StreamEvent, error) {
	if Debug {
		log.Printf("[DEBUG] Stream: model=%s messages=%d tools=%d", c.Model, len(messages), len(tools))
	}
	req := openai.ChatCompletionRequest{
		Model:       c.Model,
		Messages:    messages,
		Temperature: 0.0,
		Stream:      true,
	}
	if len(tools) > 0 {
		req.Tools = tools
	}

	var stream *openai.ChatCompletionStream
	var err error
	maxTokensLimit := 4096

	c.mu.RLock()
	if c.knownMaxTokens > 0 {
		req.MaxTokens = c.knownMaxTokens
	}
	c.mu.RUnlock()

	for retries := 0; retries < 5; retries++ {
		stream, err = c.openaiClient.CreateChatCompletionStream(ctx, req)
		if err == nil {
			if req.MaxTokens > 0 {
				c.mu.Lock()
				c.knownMaxTokens = req.MaxTokens
				c.mu.Unlock()
			}
			break
		}
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "422") || strings.Contains(errStr, "max_tokens") {
			if req.MaxTokens == 0 {
				req.MaxTokens = maxTokensLimit
			} else {
				req.MaxTokens /= 2
			}
			if req.MaxTokens < 256 {
				break
			}
			if Debug {
				log.Printf("[DEBUG] Stream 422 error, retrying with MaxTokens=%d", req.MaxTokens)
			}
			continue
		}
		break
	}

	if err != nil {
		if Debug {
			log.Printf("[DEBUG] Stream error: %v", err)
		}
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "1214") || strings.Contains(errStr, "context length") || strings.Contains(errStr, "maximum context") || strings.Contains(errStr, "messages parameter") {
			return nil, fmt.Errorf("context window exceeded: %w", err)
		}
		return nil, err
	}

	eventChan := make(chan StreamEvent, 100)
	go func() {
		defer stream.Close()
		defer close(eventChan)

		toolCallBuffer := make(map[int]*ToolCall)
		eventCount := 0
		var lastResp *openai.ChatCompletionStreamResponse

		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				if Debug {
					log.Printf("[DEBUG] Stream: EOF after %d events", eventCount)
				}
				break
			}
			if err != nil {
				if Debug {
					log.Printf("[DEBUG] Stream: recv error: %v", err)
				}
				break
			}

			lastResp = &resp

			if len(resp.Choices) > 0 {
				delta := resp.Choices[0].Delta

				if delta.Content != "" {
					eventChan <- StreamEvent{Type: "text", Text: delta.Content}
					eventCount++
				}

				for _, tc := range delta.ToolCalls {
					if tc.Index == nil {
						continue
					}
					idx := *tc.Index
					if toolCallBuffer[idx] == nil {
						toolCallBuffer[idx] = &ToolCall{}
					}
					buf := toolCallBuffer[idx]
					if tc.ID != "" {
						buf.ID = tc.ID
					}
					if tc.Function.Name != "" {
						buf.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						buf.Arguments += tc.Function.Arguments
					}
				}

				if resp.Choices[0].FinishReason != "" && resp.Choices[0].FinishReason != openai.FinishReasonNull {
					if Debug {
						log.Printf("[DEBUG] Stream: finish_reason=%s %d buffered tool calls", resp.Choices[0].FinishReason, len(toolCallBuffer))
					}
					for _, buf := range toolCallBuffer {
						if buf.ID != "" && buf.Name != "" {
							tc := *buf
							eventChan <- StreamEvent{Type: "tool_use", ToolCall: &tc}
							eventCount++
						}
					}
					toolCallBuffer = make(map[int]*ToolCall)
				}
			}
		}
		for _, buf := range toolCallBuffer {
			if buf.ID == "" || buf.Name == "" {
				continue
			}
			tc := *buf
			eventChan <- StreamEvent{Type: "tool_use", ToolCall: &tc}
			eventCount++
		}
		// Emit usage from the last chunk if available
		if lastResp != nil && lastResp.Usage != nil {
			eventChan <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:  lastResp.Usage.PromptTokens,
					OutputTokens: lastResp.Usage.CompletionTokens,
					TotalTokens:  lastResp.Usage.TotalTokens,
				},
			}
			if Debug {
				log.Printf("[DEBUG] Stream: usage emitted: in=%d out=%d total=%d",
					lastResp.Usage.PromptTokens, lastResp.Usage.CompletionTokens, lastResp.Usage.TotalTokens)
			}
		}
		if Debug {
			log.Printf("[DEBUG] Stream: goroutine exiting, total events=%d", eventCount)
		}
	}()

	return eventChan, nil
}
