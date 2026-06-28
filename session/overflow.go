package session

import "quietforge/config"

const (
	DefaultReservedBuffer = 20000
	PruneMinimum          = 20000
	TailTurns             = 2
)

var ProtectedTools = []string{"skill"}

func GetUsableContextWindow(modelContext int, config *config.CompactionConfig) int {
	context := modelContext
	if context == 0 {
		context = 128000
	}

	reserved := DefaultReservedBuffer
	if config != nil && config.Reserved > 0 {
		reserved = config.Reserved
	}

	return context - reserved
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