package implement

import (
	"quietforge/tool"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"
)

type WebSearchTool struct{}

func (t *WebSearchTool) ID() string {
	return "websearch"
}

func (t *WebSearchTool) Description() string {
	return "Search the web for current information."
}

func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query":      map[string]interface{}{"type": "string", "description": "Search query"},
			"numResults": map[string]interface{}{"type": "integer", "description": "Number of results to return"},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Query      string `json:"query"`
		NumResults int    `json:"numResults"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	query := params.Query
	num := params.NumResults
	if num <= 0 {
		num = 8
	}

	apiKey := os.Getenv("SERPAPI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}

	if apiKey != "" {
		return t.searchWithAPI(query, num, apiKey, ctx)
	}

	return t.searchBasic(query, num, ctx)
}

func (t *WebSearchTool) searchWithAPI(query string, num int, apiKey string, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var searchUrl string

	if os.Getenv("SERPAPI_API_KEY") != "" {
		searchUrl = fmt.Sprintf("https://serpapi.com/search?q=%s&api_key=%s&num=%d", url.QueryEscape(query), apiKey, num)
	} else {
		cx := os.Getenv("GOOGLE_CSE_ID")
		n := num
		if n > 10 {
			n = 10
		}
		searchUrl = fmt.Sprintf("https://www.googleapis.com/customsearch/v1?q=%s&key=%s&cx=%s&num=%d", url.QueryEscape(query), apiKey, cx, n)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequestWithContext(ctx.Context, "GET", searchUrl, nil)
	resp, err := client.Do(req)
	if err != nil {
		return &tool.ToolResult{Error: "api_error", Output: fmt.Sprintf("Search API error: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &tool.ToolResult{Error: "api_error", Output: fmt.Sprintf("Search API error: HTTP %d", resp.StatusCode)}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	var data map[string]interface{}
	_ = json.Unmarshal(body, &data)

	items, ok := data["items"].([]interface{})
	if !ok {
		return &tool.ToolResult{Title: fmt.Sprintf("Search: %s", query), Output: "(no results)"}, nil
	}

	var results string
	for _, it := range items {
		item, ok := it.(map[string]interface{})
		if ok {
			title, _ := item["title"].(string)
			link, _ := item["link"].(string)
			results += fmt.Sprintf("- %s: %s\n", title, link)
		}
	}

	if results == "" {
		results = "(no results)"
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Search: %s", query),
		Output: results,
	}, nil
}

func (t *WebSearchTool) searchBasic(query string, num int, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	searchUrl := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	// Short timeout for DDG; Wikipedia fallback handles the rest
	ddgClient := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(ctx.Context, "GET", searchUrl, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")

	resp, err := ddgClient.Do(req)
	if err == nil {
		if resp.StatusCode == 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			re := regexp.MustCompile(`<a rel="nofollow" class="result__a" href="(.*?)">(.*?)</a>`)
			matches := re.FindAllStringSubmatch(string(body), -1)

			if len(matches) > 0 {
				var results string
				count := 0
				for _, m := range matches {
					if count >= num {
						break
					}
					results += fmt.Sprintf("- %s: %s\n", stripTags(m[2]), m[1])
					count++
				}
				return &tool.ToolResult{Title: fmt.Sprintf("Search: %s", query), Output: results}, nil
			}
		} else {
			resp.Body.Close()
		}
	}

	wikiUrl := fmt.Sprintf("https://en.wikipedia.org/w/api.php?action=opensearch&search=%s&limit=%d&namespace=0&format=json", url.QueryEscape(query), num)
	wReq, _ := http.NewRequestWithContext(ctx.Context, "GET", wikiUrl, nil)
	wReq.Header.Set("User-Agent", "QuietForgeBot/1.0 (https://github.com/quietforge; bot@quietforge.com)")

	wikiClient := &http.Client{Timeout: 10 * time.Second}
	wResp, wErr := wikiClient.Do(wReq)
	if wErr == nil && wResp.StatusCode == 200 {
		wBody, _ := io.ReadAll(wResp.Body)
		wResp.Body.Close()
		var wData []interface{}
		if err := json.Unmarshal(wBody, &wData); err == nil && len(wData) >= 4 {
			titles, ok1 := wData[1].([]interface{})
			links, ok2 := wData[3].([]interface{})
			if ok1 && ok2 && len(titles) > 0 {
				var results string
				for i := range titles {
					if i < len(links) {
						results += fmt.Sprintf("- %v: %v\n", titles[i], links[i])
					}
				}
				return &tool.ToolResult{Title: fmt.Sprintf("Search (Wikipedia Fallback): %s", query), Output: results}, nil
			}
		}
	}

	errOutput := "Basic search failed.\n\nYour server IP is likely blocked by DuckDuckGo's anti-bot protection. To fix this permanently, please configure 'SERPAPI_API_KEY' or 'GOOGLE_API_KEY' in your config.json or .env file."
	return &tool.ToolResult{Error: "execution_error", Output: errOutput}, nil
}

func stripTags(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(s, "")
}
