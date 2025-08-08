package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_New(t *testing.T) {
	cache := New()
	require.NotNil(t, cache)

	// Verify initial state is zero-valued
	state := cache.GetState()
	assert.Equal(t, State{}, state)
}

func TestCache_UpdateState(t *testing.T) {
	cache := New()

	state := State{
		ValidatorName:  "test-validator",
		Hostname:       "test-host",
		PublicIP:       "192.168.1.1",
		Role:           "active",
		Status:         "healthy",
		PeerCount:      3,
		SelfInGossip:   true,
		FailoverStatus: "idle",
	}

	// Update state
	cache.UpdateState(state)

	// Get and verify state
	result := cache.GetState()
	assert.Equal(t, state.ValidatorName, result.ValidatorName)
	assert.Equal(t, state.Hostname, result.Hostname)
	assert.Equal(t, state.PublicIP, result.PublicIP)
	assert.Equal(t, state.Role, result.Role)
	assert.Equal(t, state.Status, result.Status)
	assert.Equal(t, state.PeerCount, result.PeerCount)
	assert.Equal(t, state.SelfInGossip, result.SelfInGossip)
	assert.Equal(t, state.FailoverStatus, result.FailoverStatus)
	assert.False(t, result.LastUpdated.IsZero(), "LastUpdated should be set")

	// Verify LastUpdated is recent (within last second)
	assert.True(t, time.Since(result.LastUpdated) < time.Second, "LastUpdated should be recent")
}

func TestCache_UpdateStateMultipleTimes(t *testing.T) {
	cache := New()

	// First update
	state1 := State{
		ValidatorName: "validator-1",
		PeerCount:     5,
		Role:          "active",
	}
	cache.UpdateState(state1)

	// Verify first update
	result1 := cache.GetState()
	assert.Equal(t, "validator-1", result1.ValidatorName)
	assert.Equal(t, 5, result1.PeerCount)
	assert.Equal(t, "active", result1.Role)

	// Second update
	state2 := State{
		ValidatorName: "validator-2",
		PeerCount:     10,
		Role:          "passive",
		Status:        "healthy",
	}
	cache.UpdateState(state2)

	// Verify second update
	result2 := cache.GetState()
	assert.Equal(t, "validator-2", result2.ValidatorName)
	assert.Equal(t, 10, result2.PeerCount)
	assert.Equal(t, "passive", result2.Role)
	assert.Equal(t, "healthy", result2.Status)

	// Verify LastUpdated is more recent for second update
	assert.True(t, result2.LastUpdated.After(result1.LastUpdated), "Second update should have more recent timestamp")
}

func TestCache_ConcurrentAccess(t *testing.T) {
	cache := New()

	// Test concurrent writes
	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			state := State{
				ValidatorName:  "validator-" + string(rune(id)),
				PeerCount:      id,
				Role:           "active",
				Status:         "healthy",
				SelfInGossip:   id%2 == 0,
				FailoverStatus: "idle",
			}
			cache.UpdateState(state)
		}(i)
	}
	wg.Wait()

	// Test concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state := cache.GetState()
			// Verify we can read the state
			assert.NotNil(t, state)
		}()
	}
	wg.Wait()

	// Verify we can still read after concurrent access
	state := cache.GetState()
	assert.NotEqual(t, "", state.ValidatorName, "expected state to be readable after concurrent access")
	assert.False(t, state.LastUpdated.IsZero(), "LastUpdated should be set")
}

func TestCache_StateIsolation(t *testing.T) {
	cache := New()

	originalState := State{
		ValidatorName:  "original",
		PeerCount:      5,
		Role:           "active",
		Status:         "healthy",
		SelfInGossip:   true,
		FailoverStatus: "idle",
	}

	cache.UpdateState(originalState)

	// Get state and modify it
	retrievedState := cache.GetState()
	retrievedState.ValidatorName = "modified"
	retrievedState.PeerCount = 10
	retrievedState.Role = "passive"

	// Verify original state is unchanged
	originalRetrieved := cache.GetState()
	assert.Equal(t, "original", originalRetrieved.ValidatorName, "expected original state to be unchanged")
	assert.Equal(t, 5, originalRetrieved.PeerCount, "expected original peer count to be unchanged")
	assert.Equal(t, "active", originalRetrieved.Role, "expected original role to be unchanged")
}

func TestCache_ZeroValueState(t *testing.T) {
	cache := New()

	// Test with zero-value state
	zeroState := State{}
	cache.UpdateState(zeroState)

	result := cache.GetState()
	assert.Equal(t, "", result.ValidatorName)
	assert.Equal(t, "", result.Hostname)
	assert.Equal(t, "", result.PublicIP)
	assert.Equal(t, "", result.Role)
	assert.Equal(t, "", result.Status)
	assert.Equal(t, 0, result.PeerCount)
	assert.False(t, result.SelfInGossip)
	assert.Equal(t, "", result.FailoverStatus)
	assert.False(t, result.LastUpdated.IsZero(), "LastUpdated should still be set even for zero state")
}

func TestCache_EdgeCases(t *testing.T) {
	cache := New()

	// Test with very long strings
	longState := State{
		ValidatorName:  "very-long-validator-name-that-exceeds-normal-length-expectations",
		Hostname:       "very-long-hostname-that-exceeds-normal-length-expectations.example.com",
		PublicIP:       "192.168.1.100",
		Role:           "active",
		Status:         "healthy",
		PeerCount:      999999,
		SelfInGossip:   true,
		FailoverStatus: "becoming_active",
	}

	cache.UpdateState(longState)
	result := cache.GetState()
	assert.Equal(t, longState.ValidatorName, result.ValidatorName)
	assert.Equal(t, longState.Hostname, result.Hostname)
	assert.Equal(t, longState.PeerCount, result.PeerCount)
	assert.Equal(t, longState.FailoverStatus, result.FailoverStatus)

	// Test with negative peer count
	negativeState := State{
		ValidatorName: "test",
		PeerCount:     -1,
	}
	cache.UpdateState(negativeState)
	result = cache.GetState()
	assert.Equal(t, -1, result.PeerCount)
}

func TestCache_AllRoles(t *testing.T) {
	cache := New()

	roles := []string{"active", "passive", "unknown"}

	for _, role := range roles {
		state := State{
			ValidatorName: "test-validator",
			Role:          role,
			Status:        "healthy",
		}

		cache.UpdateState(state)
		result := cache.GetState()
		assert.Equal(t, role, result.Role, "should handle role: %s", role)
	}
}

func TestCache_AllStatuses(t *testing.T) {
	cache := New()

	statuses := []string{"healthy", "unhealthy", "unknown"}

	for _, status := range statuses {
		state := State{
			ValidatorName: "test-validator",
			Role:          "active",
			Status:        status,
		}

		cache.UpdateState(state)
		result := cache.GetState()
		assert.Equal(t, status, result.Status, "should handle status: %s", status)
	}
}

func TestCache_AllFailoverStatuses(t *testing.T) {
	cache := New()

	failoverStatuses := []string{"idle", "becoming_active", "becoming_passive"}

	for _, failoverStatus := range failoverStatuses {
		state := State{
			ValidatorName:  "test-validator",
			Role:           "active",
			Status:         "healthy",
			FailoverStatus: failoverStatus,
		}

		cache.UpdateState(state)
		result := cache.GetState()
		assert.Equal(t, failoverStatus, result.FailoverStatus, "should handle failover status: %s", failoverStatus)
	}
}

func TestCache_ConcurrentReadWrite(t *testing.T) {
	cache := New()

	var wg sync.WaitGroup
	numOperations := 50

	// Start reader goroutines
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				state := cache.GetState()
				_ = state.ValidatorName // Access the state
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Start writer goroutines
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			state := State{
				ValidatorName: "concurrent-validator",
				PeerCount:     id,
				Role:          "active",
			}
			cache.UpdateState(state)
		}(i)
	}

	wg.Wait()

	// Verify final state is consistent
	finalState := cache.GetState()
	assert.NotNil(t, finalState)
	assert.False(t, finalState.LastUpdated.IsZero())
}

func TestCache_TimestampAccuracy(t *testing.T) {
	cache := New()

	beforeUpdate := time.Now()
	time.Sleep(time.Millisecond) // Ensure we have a clear before/after boundary

	state := State{
		ValidatorName: "test-validator",
		Role:          "active",
	}
	cache.UpdateState(state)

	afterUpdate := time.Now()

	result := cache.GetState()

	// Verify LastUpdated is between beforeUpdate and afterUpdate
	assert.True(t, result.LastUpdated.After(beforeUpdate) || result.LastUpdated.Equal(beforeUpdate),
		"LastUpdated should be after or equal to beforeUpdate")
	assert.True(t, result.LastUpdated.Before(afterUpdate) || result.LastUpdated.Equal(afterUpdate),
		"LastUpdated should be before or equal to afterUpdate")
}

func TestCache_MultipleInstances(t *testing.T) {
	// Test that multiple cache instances are independent
	cache1 := New()
	cache2 := New()

	state1 := State{
		ValidatorName: "validator-1",
		PeerCount:     5,
	}

	state2 := State{
		ValidatorName: "validator-2",
		PeerCount:     10,
	}

	cache1.UpdateState(state1)
	cache2.UpdateState(state2)

	result1 := cache1.GetState()
	result2 := cache2.GetState()

	assert.Equal(t, "validator-1", result1.ValidatorName)
	assert.Equal(t, 5, result1.PeerCount)

	assert.Equal(t, "validator-2", result2.ValidatorName)
	assert.Equal(t, 10, result2.PeerCount)

	// Verify they have different timestamps
	assert.NotEqual(t, result1.LastUpdated, result2.LastUpdated)
}
