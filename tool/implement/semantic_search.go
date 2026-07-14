package implement

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"quietforge/config"
	"quietforge/tool"
	wspkg "quietforge/workspace"
)

type SemanticSearchTool struct{}

func (t *SemanticSearchTool) ID() string {
	return "semantic_search"
}

func (t *SemanticSearchTool) Description() string {
	return "Search the codebase and knowledge brain by conceptual meaning (Active RAG). Use this to find logic, concepts, past plans, or context when you don't know the exact keywords or file names."
}

func (t *SemanticSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "The concept, feature, or logic you are searching for (e.g., 'user authentication flow', 'password hashing', 'database migration')."},
			"limit": map[string]interface{}{"type": "integer", "description": "(Optional) Maximum number of results to return. Default is 5. Max is 15."},
		},
		"required": []string{"query"},
	}
}

// dotProduct calculates the cosine similarity for normalized vectors.
func dotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

func (t *SemanticSearchTool) Execute(argsJSON []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	if ctx.Workspace == "" {
		return &tool.ToolResult{Error: "semantic_search requires a workspace context"}, nil
	}

	cfg := config.LoadConfig(ctx.Workspace)
	if cfg.Embedding == nil || !cfg.Embedding.Enabled {
		return &tool.ToolResult{Error: "semantic embeddings are disabled in UI preferences. Ask the user to enable them in the UI config first"}, nil
	}

	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return &tool.ToolResult{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}

	if strings.TrimSpace(args.Query) == "" {
		return &tool.ToolResult{Error: "query cannot be empty"}, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 5
	} else if limit > 15 {
		limit = 15
	}

	queryVector, err := wspkg.GenerateSingleEmbedding(args.Query, cfg.Embedding)
	if err != nil || len(queryVector) == 0 {
		return &tool.ToolResult{Error: fmt.Sprintf("failed to generate embedding for query: %v", err)}, nil
	}

	records := wspkg.GetWorkspaceEmbeddings(ctx.Workspace)
	if len(records) == 0 {
		return &tool.ToolResult{Output: "The workspace index is currently empty. The background indexer may still be running, or no files were matched."}, nil
	}

	type searchResult struct {
		Record wspkg.EmbeddingRecord
		Score  float32
	}
	
	var results []searchResult
	for _, rec := range records {
		score := dotProduct(queryVector, rec.Embedding)
		if math.IsNaN(float64(score)) {
			continue
		}
		if score > 0.25 { // Minimum threshold
			results = append(results, searchResult{Record: rec, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) == 0 {
		return &tool.ToolResult{Output: "No semantically similar results found above the confidence threshold."}, nil
	}

	if len(results) > limit {
		results = results[:limit]
	}

	var outputBuilder strings.Builder
	outputBuilder.WriteString(fmt.Sprintf("Top %d semantic matches for: %q\n\n", len(results), args.Query))

	for i, res := range results {
		outputBuilder.WriteString(fmt.Sprintf("--- Match %d (Confidence: %.2f) ---\n", i+1, res.Score))
		outputBuilder.WriteString(fmt.Sprintf("Type: %s\n", res.Record.Kind))
		outputBuilder.WriteString(fmt.Sprintf("File/Object: %s\n", res.Record.ObjectID))
		outputBuilder.WriteString(fmt.Sprintf("Chunk: %d\n\n", res.Record.ChunkIndex))
	}
	
	outputBuilder.WriteString("\nNote: To see the actual content of these matched files/objects, use the 'read' tool on the file path or 'ast_search' for the object.")

	return &tool.ToolResult{Output: outputBuilder.String()}, nil
}
