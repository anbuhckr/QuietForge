package provider

import "strings"

type ModelInfo struct {
	Context   int     `json:"context"`
	MaxOutput int     `json:"max_output"`
	CostInput float64 `json:"cost_input"`
	CostOut   float64 `json:"cost_output"`
}

var ModelCatalog = map[string]ModelInfo{
	"gpt-4o":              {Context: 128000, MaxOutput: 16384, CostInput: 2.50, CostOut: 10.00},
	"gpt-4o-mini":         {Context: 128000, MaxOutput: 16384, CostInput: 0.15, CostOut: 0.60},
	"gpt-4-turbo":         {Context: 128000, MaxOutput: 4096, CostInput: 10.00, CostOut: 30.00},
	"gpt-4":               {Context: 8192, MaxOutput: 4096, CostInput: 30.00, CostOut: 60.00},
	"gpt-3.5-turbo":       {Context: 16385, MaxOutput: 4096, CostInput: 0.50, CostOut: 1.50},
	"claude-3-opus-20240229":   {Context: 200000, MaxOutput: 4096, CostInput: 15.00, CostOut: 75.00},
	"claude-3-sonnet-20240229": {Context: 200000, MaxOutput: 4096, CostInput: 3.00, CostOut: 15.00},
	"claude-3-haiku-20240307":  {Context: 200000, MaxOutput: 4096, CostInput: 0.25, CostOut: 1.25},
	"google/gemini-2.5-pro":    {Context: 2000000, MaxOutput: 8192, CostInput: 0.00, CostOut: 0.00},
	"google/gemini-2.5-flash":  {Context: 2000000, MaxOutput: 8192, CostInput: 0.00, CostOut: 0.00},
}

func ResolveModel(modelID string) (*ModelInfo, bool) {
	info, ok := ModelCatalog[modelID]
	if ok {
		return &info, true
	}
	
	// Fuzzy fallback for unknown provider prefixes (e.g. openrouter/deepseek-coder)
	idLower := strings.ToLower(modelID)
	if strings.Contains(idLower, "deepseek") {
		info = ModelInfo{Context: 128000, MaxOutput: 8192}
		return &info, true
	}
	if strings.Contains(idLower, "gpt-4") {
		info = ModelInfo{Context: 128000, MaxOutput: 16384}
		return &info, true
	}
	if strings.Contains(idLower, "claude-3") {
		info = ModelInfo{Context: 200000, MaxOutput: 8192}
		return &info, true
	}
	if strings.Contains(idLower, "gemini") {
		info = ModelInfo{Context: 2000000, MaxOutput: 8192}
		return &info, true
	}

	return nil, false
}

func EstimateCost(modelID string, inputTokens, outputTokens int) *float64 {
	info, ok := ModelCatalog[modelID]
	if !ok {
		return nil
	}
	cost := (float64(inputTokens) / 1_000_000) * info.CostInput
	cost += (float64(outputTokens) / 1_000_000) * info.CostOut
	return &cost
}
