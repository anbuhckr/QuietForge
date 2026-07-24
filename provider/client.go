package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

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
	Reasoning   string
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

type ProviderInstance struct {
	ID            string
	Client        *openai.Client
	Model         string
	DisableVision bool
}

type Client struct {
	clients              []ProviderInstance
	Model                string // global default model
	knownMaxTokens       int
	SuccessfulProviderID string
	OnEvent              func(msg string)
	mu                   sync.RWMutex
}

func NewClient(apiKey, baseURL, model string) *Client {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" && baseURL != "https://api.openai.com/v1" {
		config.BaseURL = baseURL
	}
	return &Client{
		clients: []ProviderInstance{
			{Client: openai.NewClientWithConfig(config), Model: model},
		},
		Model: model,
	}
}

func NewMultiClient(clients []ProviderInstance, model string) *Client {
	return &Client{
		clients: clients,
		Model:   model,
	}
}

func (c *Client) RawClient() *openai.Client {
	if len(c.clients) > 0 {
		return c.clients[0].Client
	}
	return nil
}

func (c *Client) GetSuccessfulProviderID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.SuccessfulProviderID
}

type callFunc func(instance ProviderInstance, req openai.ChatCompletionRequest) (interface{}, error)

func (c *Client) tryEachProvider(ctx context.Context, req *openai.ChatCompletionRequest, messages []openai.ChatCompletionMessage, label string, callFn callFunc) (interface{}, error) {
	maxTokensLimit := 4096

	c.mu.RLock()
	clients := make([]ProviderInstance, len(c.clients))
	copy(clients, c.clients)
	c.mu.RUnlock()

	for i, instance := range clients {
		req.Model = instance.Model
		if req.Model == "" {
			req.Model = c.Model
		}

		if instance.DisableVision {
			req.Messages = stripVision(copyMessages(messages))
		} else {
			req.Messages = messages
		}

		maxTokensReduced := false
		var err error
		for retries := 0; ; retries++ {
			result, callErr := callFn(instance, *req)
			if callErr == nil {
				c.mu.Lock()
				if req.MaxTokens > 0 && !maxTokensReduced && req.MaxTokens > c.knownMaxTokens {
					c.knownMaxTokens = req.MaxTokens
				}
				c.SuccessfulProviderID = instance.ID

				// If a fallback was successful, promote it to the front so subsequent tool calls in this engine run use it immediately
				if i > 0 {
					newClients := make([]ProviderInstance, 0, len(c.clients))
					newClients = append(newClients, c.clients[i])
					newClients = append(newClients, c.clients[:i]...)
					newClients = append(newClients, c.clients[i+1:]...)
					c.clients = newClients
				}
				
				c.mu.Unlock()
				return result, nil
			}
			err = callErr

			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "1214") || strings.Contains(errStr, "context length") || strings.Contains(errStr, "maximum context") {
				return nil, fmt.Errorf("context window exceeded: %w", err)
			}
			if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") || strings.Contains(errStr, "404") || strings.Contains(errStr, "invalid api key") || strings.Contains(errStr, "429") || strings.Contains(errStr, "quota") || strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "too many") {
				if retries >= 2 {
					break
				}
				msg := fmt.Sprintf("%s hard error: %v, retrying (Attempt %d/3) before fallback", label, err, retries+1)
				if Debug {
					log.Printf("[DEBUG] %s", msg)
				}
				if c.OnEvent != nil {
					c.OnEvent(msg)
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(3 * time.Second):
				}
				continue
			}

			if strings.Contains(errStr, "422") || strings.Contains(errStr, "max_tokens") {
				maxTokensReduced = true
				if req.MaxTokens == 0 {
					req.MaxTokens = maxTokensLimit
				} else {
					req.MaxTokens /= 2
				}
				if req.MaxTokens < 256 {
					break
				}
				if Debug {
					log.Printf("[DEBUG] %s 422 error, retrying with MaxTokens=%d", label, req.MaxTokens)
				}
				continue
			}

			if retries >= 2 {
				break
			}
			msg := fmt.Sprintf("%s transient error: %v, retrying in 3 seconds (Attempt %d)", label, err, retries+1)
			if Debug {
				log.Printf("[DEBUG] %s", msg)
			}
			if c.OnEvent != nil {
				c.OnEvent(msg)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}

		msg := fmt.Sprintf("%s provider attempt failed, falling back to next provider. Error: %v", label, err)
		if Debug {
			log.Printf("[DEBUG] %s", msg)
		}
		if c.OnEvent != nil {
			c.OnEvent(msg)
		}
	}

	return nil, fmt.Errorf("all providers failed for %s", label)
}

func (c *Client) Generate(ctx context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (*LLMResponse, error) {
	if Debug {
		log.Printf("[DEBUG] Generate: model=%s messages=%d tools=%d", c.Model, len(messages), len(tools))
	}
	if len(c.clients) == 0 {
		return nil, fmt.Errorf("no LLM provider configured or no API key provided")
	}

	req := openai.ChatCompletionRequest{
		Model:       c.Model,
		Messages:    messages,
		MaxTokens:   16384,
		Temperature: 0.0,
	}
	if len(tools) > 0 {
		req.Tools = tools
	}

	c.mu.RLock()
	if c.knownMaxTokens > 0 {
		req.MaxTokens = c.knownMaxTokens
	}
	c.mu.RUnlock()

	result, err := c.tryEachProvider(ctx, &req, messages, "Generate", func(instance ProviderInstance, req openai.ChatCompletionRequest) (interface{}, error) {
		r, e := instance.Client.CreateChatCompletion(ctx, req)
		if e != nil {
			return nil, e
		}
		return r, nil
	})
	if err != nil {
		return nil, err
	}

	resp := result.(openai.ChatCompletionResponse)
	out := &LLMResponse{
		Model: resp.Model,
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in response")
	}
	choice := resp.Choices[0]
	out.Content = choice.Message.Content
	out.Reasoning = choice.Message.ReasoningContent
	out.FinishReason = string(choice.FinishReason)

	for _, tc := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	out.Usage = &Usage{
		InputTokens:  int(resp.Usage.PromptTokens),
		OutputTokens: int(resp.Usage.CompletionTokens),
		TotalTokens:  int(resp.Usage.TotalTokens),
	}

	if Debug {
		log.Printf("[DEBUG] Generate response: content=%d toolCalls=%d finish=%s tokens=%d",
			len(out.Content), len(out.ToolCalls), out.FinishReason, out.Usage.TotalTokens)
	}
	return out, nil
}

// Stream generates a stream of responses from the LLM
func (c *Client) Stream(ctx context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (<-chan StreamEvent, error) {
	if Debug {
		log.Printf("[DEBUG] Stream: model=%s messages=%d tools=%d", c.Model, len(messages), len(tools))
	}
	req := openai.ChatCompletionRequest{
		Model:       c.Model,
		Messages:    messages,
		MaxTokens:   16384,
		Temperature: 0.0,
		Stream:      true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}
	if len(tools) > 0 {
		req.Tools = tools
	}

	c.mu.RLock()
	if c.knownMaxTokens > 0 {
		req.MaxTokens = c.knownMaxTokens
	}
	c.mu.RUnlock()

	result, err := c.tryEachProvider(ctx, &req, messages, "Stream", func(instance ProviderInstance, req openai.ChatCompletionRequest) (interface{}, error) {
		s, e := instance.Client.CreateChatCompletionStream(ctx, req)
		if e != nil {
			return nil, e
		}
		return s, nil
	})
	if err != nil {
		return nil, err
	}

	stream := result.(*openai.ChatCompletionStream)

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

				if delta.ReasoningContent != "" {
					eventChan <- StreamEvent{Type: "reasoning", Text: delta.ReasoningContent}
				}
				if delta.Content != "" {
					eventChan <- StreamEvent{Type: "text", Text: delta.Content}
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

func stripVision(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	var stripped []openai.ChatCompletionMessage
	for _, m := range msgs {
		newMsg := m
		if len(m.MultiContent) > 0 {
			newParts := make([]openai.ChatMessagePart, 0, len(m.MultiContent))
			for _, p := range m.MultiContent {
				if p.Type == openai.ChatMessagePartTypeImageURL {
					continue
				}
				newParts = append(newParts, p)
			}
			newMsg.MultiContent = newParts
		}
		stripped = append(stripped, newMsg)
	}
	return stripped
}

func copyMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		result[i] = m
		if len(m.MultiContent) > 0 {
			result[i].MultiContent = make([]openai.ChatMessagePart, len(m.MultiContent))
			copy(result[i].MultiContent, m.MultiContent)
		}
		if len(m.ToolCalls) > 0 {
			result[i].ToolCalls = make([]openai.ToolCall, len(m.ToolCalls))
			copy(result[i].ToolCalls, m.ToolCalls)
		}
	}
	return result
}
