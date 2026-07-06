package implement

import (
	"quietforge/tool"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

type WebFetchTool struct{}

func (t *WebFetchTool) ID() string {
	return "webfetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch content from a URL and optionally convert to markdown."
}

func (t *WebFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url":    map[string]interface{}{"type": "string", "description": "URL to fetch"},
			"format": map[string]interface{}{"type": "string", "enum": []string{"markdown", "text", "html"}, "description": "Output format"},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		URL    string `json:"url"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	url := params.URL

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx.Context, "GET", url, nil)
	if err != nil {
		return &tool.ToolResult{Error: "execution_error", Output: fmt.Sprintf("Fetch error: %v", err)}, nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return &tool.ToolResult{Error: "network_error", Output: fmt.Sprintf("Request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &tool.ToolResult{Error: "http_error", Output: fmt.Sprintf("HTTP error: %d", resp.StatusCode)}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &tool.ToolResult{Error: "execution_error", Output: fmt.Sprintf("Read error: %v", err)}, nil
	}

	if isBinary(body) {
		ct := resp.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "image/") {
			base64Data := base64.StdEncoding.EncodeToString(body)
			dataURL := fmt.Sprintf("data:%s;base64,%s", ct, base64Data)

			return &tool.ToolResult{
				Title:  fmt.Sprintf("Fetched Image: %s", url),
				Output: fmt.Sprintf("Successfully fetched image (%d bytes).", len(body)),
				Attachments: []map[string]interface{}{
					{"url": dataURL},
				},
				Metadata: map[string]interface{}{
					"url":    url,
					"status": resp.StatusCode,
					"size":   len(body),
				},
			}, nil
		}
		return &tool.ToolResult{
			Error:  "binary_content",
			Output: fmt.Sprintf("URL %s returned binary content (Content-Type: %s, %d bytes). Binary content cannot be displayed.", url, ct, len(body)),
		}, nil
	}

	content := string(body)
	if params.Format == "markdown" {
		md, err := htmltomarkdown.ConvertString(content)
		if err == nil {
			content = md
		}
	}
	if len(content) > 50000 {
		content = content[:50000]
	}

	content = strings.TrimSpace(content)

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Fetched: %s", url),
		Output: content,
		Metadata: map[string]interface{}{
			"url":    url,
			"status": resp.StatusCode,
			"size":   len(body),
		},
	}, nil
}
