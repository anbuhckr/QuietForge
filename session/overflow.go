package session

const (
	DefaultReservedBuffer = 20000
	PruneMinimum          = 20000
)

var ProtectedTools = []string{"skill"}

func GetUsableContextWindow(modelContext int, config map[string]any) int {
	context := modelContext
	if context == 0 {
		context = 128000
	}

	reserved := DefaultReservedBuffer
	hasConfigReserved := false
	if config != nil {
		if r, ok := config["reserved"].(float64); ok && r > 0 {
			reserved = int(r)
			hasConfigReserved = true
		} else if r, ok := config["reserved"].(int); ok && r > 0 {
			reserved = r
			hasConfigReserved = true
		}
	}

	if !hasConfigReserved && reserved >= context {
		reserved = context / 4
	}
	if reserved >= context {
		reserved = context / 2
	}

	return context - reserved
}

func GetTailTurns(config map[string]any) int {
	if config != nil {
		if t, ok := config["tail_turns"].(float64); ok {
			return int(t)
		} else if t, ok := config["tail_turns"].(int); ok {
			return t
		}
	}
	return 2
}

func GetToolTruncationLimit(config map[string]any) int {
	if config != nil {
		if t, ok := config["tool_truncation_limit"].(float64); ok {
			return int(t)
		} else if t, ok := config["tool_truncation_limit"].(int); ok {
			return t
		}
	}
	return 2000
}

func NeedsCompaction(totalTokens int, usableTokens int) bool {
	return totalTokens > usableTokens
}

func GetPruneTarget(totalTokens int, usableTokens int, config map[string]any) int {
	// If preserve_recent_tokens is configured and valid, use it directly as the target
	if config != nil {
		if p, ok := config["preserve_recent_tokens"].(float64); ok && p > 0 {
			target := int(p)
			if target < usableTokens {
				return target
			}
		} else if p, ok := config["preserve_recent_tokens"].(int); ok && p > 0 {
			target := p
			if target < usableTokens {
				return target
			}
		}
	}

	// Default to keeping only 30% of the usable context window (leaving 70% headroom)
	target := int(float64(usableTokens) * 0.3)
	minTarget := PruneMinimum
	if minTarget >= usableTokens {
		minTarget = int(float64(usableTokens) * 0.3)
	}
	if target < minTarget {
		return minTarget
	}
	return target
}

func GetSummaryReserve(config map[string]any) int {
	if config != nil {
		if r, ok := config["summary_reserve"].(float64); ok && r > 0 {
			return int(r)
		} else if r, ok := config["summary_reserve"].(int); ok && r > 0 {
			return r
		}
	}
	return 2000
}