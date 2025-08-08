package cache

import (
	"sync"
	"time"
)

// State represents the current state of the HA manager
type State struct {
	// Metadata
	ValidatorName string
	Hostname      string
	PublicIP      string
	Role          string // "active", "passive", "unknown"
	Status        string // "healthy", "unhealthy", "unknown"

	// Peer information
	PeerCount    int
	SelfInGossip bool

	// Failover status
	FailoverStatus string // "idle", "becoming_active", "becoming_passive"

	// Timestamps
	LastUpdated time.Time
}

// Cache provides thread-safe access to the HA manager state
type Cache struct {
	mu    sync.RWMutex
	state State
}

// New creates a new cache instance
func New() *Cache {
	return &Cache{}
}

// UpdateState updates the cached state
func (c *Cache) UpdateState(state State) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state.LastUpdated = time.Now()
	c.state = state
}

// GetState returns a copy of the current state
func (c *Cache) GetState() State {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.state
}
