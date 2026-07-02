package context

import (
	"sync"
)

type cacheEntry struct {
	injectionCount int
	lastSeenTurn   int
}

type WorkingSetCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	turn    int
}

func NewWorkingSetCache() *WorkingSetCache {
	return &WorkingSetCache{
		entries: make(map[string]*cacheEntry),
		turn:    0,
	}
}

func (c *WorkingSetCache) AdvanceTurn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.turn++
}

func (c *WorkingSetCache) RecordInjection(fragmentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if entry, exists := c.entries[fragmentID]; exists {
		entry.injectionCount++
		entry.lastSeenTurn = c.turn
	} else {
		c.entries[fragmentID] = &cacheEntry{
			injectionCount: 1,
			lastSeenTurn:   c.turn,
		}
	}
}

func (c *WorkingSetCache) ComputeDynamicScore(priority, confidence float64, fragmentID string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	baseScore := priority * confidence
	entry, exists := c.entries[fragmentID]
	
	if !exists {
		// Novel context gets a freshness boost
		return baseScore * 1.5
	}

	// Penalize repeated injections, but decay penalty if it hasn't been seen in a while
	turnsSinceLast := c.turn - entry.lastSeenTurn
	penalty := float64(entry.injectionCount) * 10.0
	
	if turnsSinceLast > 3 {
		penalty = penalty / float64(turnsSinceLast)
	}

	score := baseScore - penalty
	if score < 0 {
		return 0
	}
	return score
}
