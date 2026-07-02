package context

import (
	"fmt"
	"math"
	"sort"

	"quietforge/config"
	"quietforge/workspace"
)

type ExecutionProvider struct {
	workspace string
	cfg       config.Config
}

func NewExecutionProvider(workspace string, cfg config.Config) *ExecutionProvider {
	return &ExecutionProvider{
		workspace: workspace,
		cfg:       cfg,
	}
}

func (p *ExecutionProvider) ID() string {
	return "execution"
}

func (p *ExecutionProvider) SoftLimit() int {
	return 600 // We don't want to blow out context, 600 tokens is ~2 episodes
}

func (p *ExecutionProvider) Gather(req ContextRequest) ([]ContextFragment, error) {
	if req.Workspace == "" || req.Prompt == "" || p.cfg.Embedding == nil || !p.cfg.Embedding.Enabled {
		return nil, nil
	}

	queryVector, err := workspace.GenerateSingleEmbedding(req.Prompt, p.cfg.Embedding)
	if err != nil || len(queryVector) == 0 {
		return nil, nil
	}

	records := workspace.GetWorkspaceEmbeddings(p.workspace)
	if len(records) == 0 {
		return nil, nil
	}

	var results []searchResult
	for _, rec := range records {
		if rec.Kind != "execution" {
			continue
		}
		score := dotProduct(queryVector, rec.Embedding)
		results = append(results, searchResult{Record: rec, Score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	topK := 2
	if len(results) < topK {
		topK = len(results)
	}

	var fragments []ContextFragment
	for i := 0; i < topK; i++ {
		res := results[i]
		if res.Score < 0.3 {
			continue
		}

		fragments = append(fragments, ContextFragment{
			ProviderID: p.ID(),
			ID:         fmt.Sprintf("exec:%s", res.Record.ObjectID),
			Priority:   85.0, // High priority because past execution history is very useful
			Confidence: float64(res.Score),
			TokenCost:  int(math.Ceil(float64(len(res.Record.Hash)) / 4.0)), // Will update TokenCost via actual data size later
			Data: map[string]any{
				"episode": res.Record.ObjectID,
				"score":   res.Score,
			},
		})
	}

	return fragments, nil
}
