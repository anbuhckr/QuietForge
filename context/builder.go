package context

import (
	"encoding/json"
	"sort"
)

type scoredFragment struct {
	Fragment ContextFragment
	Score    float64
}

type PromptBuilder struct {
	GlobalLimit int
}

func NewPromptBuilder(globalLimit int) *PromptBuilder {
	return &PromptBuilder{GlobalLimit: globalLimit}
}

func (b *PromptBuilder) Build(providers []ContextProvider, fragments []ContextFragment, cache *WorkingSetCache) string {
	if len(fragments) == 0 {
		return ""
	}

	// 1. Score and sort all fragments
	var scored []scoredFragment
	for _, f := range fragments {
		score := cache.ComputeDynamicScore(f.Priority, f.Confidence, f.ID)
		if score > 0 {
			scored = append(scored, scoredFragment{Fragment: f, Score: score})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score // Descending
	})

	// 2. Group fragments by provider and map soft limits
	limits := make(map[string]int)
	for _, p := range providers {
		limits[p.ID()] = p.SoftLimit()
	}

	accepted := make(map[string][]ContextFragment)
	usedTokens := 0
	usedTokensPerProvider := make(map[string]int)

	// Phase 1: Allocate up to soft limits
	var remaining []scoredFragment
	for _, sf := range scored {
		f := sf.Fragment
		provID := f.ProviderID
		cost := f.TokenCost
		if cost == 0 {
			cost = 10 // Safe fallback
		}

		if usedTokensPerProvider[provID]+cost <= limits[provID] && usedTokens+cost <= b.GlobalLimit {
			accepted[provID] = append(accepted[provID], f)
			usedTokensPerProvider[provID] += cost
			usedTokens += cost
			cache.RecordInjection(f.ID)
		} else {
			remaining = append(remaining, sf)
		}
	}

	// Phase 2: Distribute remaining global budget to leftovers, regardless of soft limit
	for _, sf := range remaining {
		f := sf.Fragment
		provID := f.ProviderID
		cost := f.TokenCost
		if cost == 0 {
			cost = 10
		}

		if usedTokens+cost <= b.GlobalLimit {
			accepted[provID] = append(accepted[provID], f)
			usedTokens += cost
			cache.RecordInjection(f.ID)
		}
	}

	if len(accepted) == 0 {
		return ""
	}

	// 3. Marshal accepted into clean JSON
	out := map[string]any{"context": accepted}
	bytes, _ := json.MarshalIndent(out, "", "  ")
	return string(bytes)
}
