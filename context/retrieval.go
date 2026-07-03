package context

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"quietforge/config"
	"quietforge/storage"
	"quietforge/util"
	"quietforge/workspace"
)

type searchResult struct {
	Record workspace.EmbeddingRecord
	Score  float32
}

func dotProduct(a, b []float32) float32 {
	var sum float32
	for i := 0; i < len(a) && i < len(b); i++ {
		sum += a[i] * b[i]
	}
	return sum
}

type RetrievalProvider struct {
	Repo *storage.Repository
}

func (p *RetrievalProvider) ID() string {
	return "retrieval"
}

func (p *RetrievalProvider) SoftLimit() int {
	return 800 // High priority context
}

func readSymbolCode(workspace, path string, lineStart, lineEnd int) string {
	jailed, err := util.JailPath(workspace, path)
	if err != nil {
		return ""
	}

	data, err := os.ReadFile(jailed)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	start := lineStart - 1
	end := lineEnd
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return ""
	}

	return strings.Join(lines[start:end], "\n")
}

func (p *RetrievalProvider) Gather(req ContextRequest) ([]ContextFragment, error) {
	if req.Workspace == "" || req.Prompt == "" {
		return nil, nil
	}

	var fragments []ContextFragment
	seen := make(map[string]bool)

	lowerPrompt := strings.ToLower(req.Prompt)

	// 1. BM25 / Substring Search (Fast, exact matches)
	symRows, err := p.Repo.DB.Conn.Query("SELECT id, name, type, path, line_start, line_end FROM workspace_symbols WHERE workspace = ?", req.Workspace)
	if err == nil {
		for symRows.Next() {
			var id, name, typ, path string
			var lineStart, lineEnd int
			symRows.Scan(&id, &name, &typ, &path, &lineStart, &lineEnd)

			if len(name) > 3 && strings.Contains(lowerPrompt, strings.ToLower(name)) {
				fragID := fmt.Sprintf("sym:%s", id)
				seen[fragID] = true
				codeBody := readSymbolCode(req.Workspace, path, lineStart, lineEnd)
				fragments = append(fragments, ContextFragment{
					ProviderID: p.ID(),
					ID:         fragID,
					Priority:   70.0,
					Confidence: 0.8,
					TokenCost:  15,
					Data: map[string]any{
						"symbol": name,
						"type":   typ,
						"file":   path,
						"match":  "bm25",
						"code":   codeBody,
					},
				})
			}
		}
		symRows.Close()
	}

	// 2. Vector Search (Semantic)
	cfg := config.LoadConfig(".")
	if cfg.Embedding != nil && cfg.Embedding.Enabled {
		queryVector, err := workspace.GenerateSingleEmbedding(req.Prompt, cfg.Embedding)
		if err == nil && len(queryVector) > 0 {
			records := workspace.GetWorkspaceEmbeddings(req.Workspace)
			
			var results []searchResult
			for _, rec := range records {
				score := dotProduct(queryVector, rec.Embedding)
				results = append(results, searchResult{Record: rec, Score: score})
			}

			// Sort by descending score
			sort.Slice(results, func(i, j int) bool {
				return results[i].Score > results[j].Score
			})

			topK := 3
			if len(results) < topK {
				topK = len(results)
			}

			for i := 0; i < topK; i++ {
				res := results[i]
				if res.Score < 0.3 {
					continue
				}

				fragID := fmt.Sprintf("sym:%s", res.Record.ObjectID)
				if seen[fragID] {
					continue // Already found by BM25, semantic just confirms it
				}
				seen[fragID] = true

				// Find file path and lines from workspace_symbols using objectID
				symbolName := res.Record.ObjectID
				filePath := ""
				lineStart := 0
				lineEnd := 0

				// Parse ObjectID (path:symName)
				if strings.Contains(symbolName, ":") {
					parts := strings.SplitN(symbolName, ":", 2)
					filePath = parts[0]
					symbolName = parts[1]
				}

				if filePath != "" {
					var sID string
					row := p.Repo.DB.Conn.QueryRow("SELECT id, line_start, line_end FROM workspace_symbols WHERE workspace = ? AND path = ? AND name = ?", req.Workspace, filePath, symbolName)
					if err := row.Scan(&sID, &lineStart, &lineEnd); err == nil {
						fragID = fmt.Sprintf("sym:%s", sID)
					}
				}

				codeBody := ""
				if filePath != "" {
					codeBody = readSymbolCode(req.Workspace, filePath, lineStart, lineEnd)
				}

				fragments = append(fragments, ContextFragment{
					ProviderID: p.ID(),
					ID:         fragID,
					Priority:   65.0,
					Confidence: float64(res.Score),
					TokenCost:  20,
					Data: map[string]any{
						"symbol": symbolName,
						"kind":   res.Record.Kind,
						"score":  res.Score,
						"match":  "semantic",
						"file":   filePath,
						"code":   codeBody,
					},
				})
			}
		}
	}

	return fragments, nil
}
