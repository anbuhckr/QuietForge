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
	if config != nil {
		if r, ok := config["reserved"].(float64); ok && r > 0 {
			reserved = int(r)
		} else if r, ok := config["reserved"].(int); ok && r > 0 {
			reserved = r
		}
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

func GetPruneTarget(totalTokens int, usableTokens int) int {
	excess := totalTokens - usableTokens
	target := totalTokens - excess - PruneMinimum
	if target < PruneMinimum {
		return PruneMinimum
	}
	return target
}