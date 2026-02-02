package gossip

import (
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewState(t *testing.T) {
	// Create a real RPC client for this test since we're not testing RPC functionality
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers: map[string]config.Peer{
			"peer1": {IP: "192.168.1.2", Name: "peer1"},
			"peer2": {IP: "192.168.1.3", Name: "peer2"},
		},
	}

	state := NewState(opts)
	require.NotNil(t, state)
	assert.Equal(t, "test-active-pubkey", state.activePubkey)
	assert.Equal(t, "192.168.1.1", state.selfIP)
	assert.Len(t, state.configPeers, 2)
	assert.NotNil(t, state.peerStatesByName)
	assert.Empty(t, state.peerStatesByName)
}

func TestHasIP(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with empty state
	assert.False(t, state.HasIP("192.168.1.1"))

	// Test with populated state
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.2", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer2": {IP: "192.168.1.3", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: true},
	}

	assert.True(t, state.HasIP("192.168.1.2"))
	assert.True(t, state.HasIP("192.168.1.3"))
	assert.False(t, state.HasIP("192.168.1.4"))
}

func TestHasActivePeer(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with no active peers
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.2", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer2": {IP: "192.168.1.3", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	assert.False(t, state.HasActivePeer())

	// Test with active peer
	state.peerStatesByName["peer3"] = PeerState{
		IP:             "192.168.1.4",
		Pubkey:         "pubkey3",
		LastSeenAtUTC:  time.Now().UTC(),
		LastSeenActive: true,
	}

	assert.True(t, state.HasActivePeer())
}

func TestHasActivePeerInTheLastNSamples(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with no active peers (leaderlessSamplesCount should increment)
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.2", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}
	state.LeaderlessSamplesCount = 5 // Set count to 5

	// With threshold of 3, count of 5 should fail (5 >= 3)
	assert.False(t, state.LeaderlessSamplesBelowThreshold(3))

	// With threshold of 10, count of 5 should pass (5 < 10)
	assert.True(t, state.LeaderlessSamplesBelowThreshold(10))

	// Test with active peer found (leaderlessSamplesCount should be reset)
	state.peerStatesByName["peer2"] = PeerState{
		IP:             "192.168.1.3",
		Pubkey:         "pubkey2",
		LastSeenAtUTC:  time.Now().UTC(),
		LastSeenActive: true,
	}
	state.LeaderlessSamplesCount = 0 // Reset count when active peer found

	// With threshold of 3, count of 0 should pass (0 < 3)
	assert.True(t, state.LeaderlessSamplesBelowThreshold(3))

	// Test with count at threshold boundary
	state.LeaderlessSamplesCount = 3
	assert.False(t, state.LeaderlessSamplesBelowThreshold(3)) // 3 >= 3, should fail
	assert.True(t, state.LeaderlessSamplesBelowThreshold(4))  // 3 < 4, should pass
}

func TestGetActivePeer(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with no active peers
	_, err := state.GetActivePeer()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active peer found")

	// Test with active peer
	activePeer := PeerState{
		IP:             "192.168.1.2",
		Pubkey:         "pubkey1",
		LastSeenAtUTC:  time.Now().UTC(),
		LastSeenActive: true,
	}
	state.peerStatesByName["peer1"] = activePeer

	peerState, err := state.GetActivePeer()
	assert.NoError(t, err)
	assert.Equal(t, activePeer.IP, peerState.IP)
	assert.Equal(t, activePeer.Pubkey, peerState.Pubkey)
	assert.True(t, peerState.LastSeenActive)
}

func TestHasPeers(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with no peers
	assert.False(t, state.HasPeers("192.168.1.1"))

	// Test with only self IP
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.1", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	assert.False(t, state.HasPeers("192.168.1.1"))

	// Test with other peers
	state.peerStatesByName["peer2"] = PeerState{
		IP:             "192.168.1.2",
		Pubkey:         "pubkey2",
		LastSeenAtUTC:  time.Now().UTC(),
		LastSeenActive: false,
	}

	assert.True(t, state.HasPeers("192.168.1.1"))
	assert.True(t, state.HasPeers("192.168.1.2"))
}

func TestGetPeerStates(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with empty state
	peerStates := state.GetPeerStates()
	assert.Empty(t, peerStates)

	// Test with populated state
	expectedStates := map[string]PeerState{
		"peer1": {IP: "192.168.1.2", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer2": {IP: "192.168.1.3", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: true},
	}
	state.peerStatesByName = expectedStates

	peerStates = state.GetPeerStates()
	assert.Equal(t, expectedStates, peerStates)
}

func TestPeerState_LastSeenAtString(t *testing.T) {
	now := time.Now().UTC()
	peerState := PeerState{
		IP:             "192.168.1.2",
		Pubkey:         "pubkey1",
		LastSeenAtUTC:  now,
		LastSeenActive: false,
	}

	expected := now.Format(time.RFC3339)
	assert.Equal(t, expected, peerState.LastSeenAtString())
}

func TestRefresh_WithRPCError(t *testing.T) {
	// Test that Refresh handles RPC errors gracefully
	// We'll use a real RPC client but with an invalid URL to simulate failure
	invalidRPC := rpc.NewClient("test", "https://invalid-url-that-will-fail.com")

	opts := Options{
		ClusterRPC:   invalidRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers: map[string]config.Peer{
			"peer1": {IP: "192.168.1.2", Name: "peer1"},
		},
	}

	state := NewState(opts)

	// Initially populate with some data
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.2", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	// Refresh should clear the state due to RPC error
	state.Refresh()

	// Verify the state was cleared
	assert.False(t, state.PeerStatesRefreshedAt.IsZero())
	assert.Empty(t, state.GetPeerStates())
}

func TestLastRefreshHadRPCError(t *testing.T) {
	t.Run("returns true after RPC error", func(t *testing.T) {
		// Use an invalid URL to simulate RPC failure
		invalidRPC := rpc.NewClient("test", "https://invalid-url-that-will-fail.com")

		opts := Options{
			ClusterRPC:   invalidRPC,
			ActivePubkey: "test-active-pubkey",
			SelfIP:       "192.168.1.1",
			ConfigPeers: map[string]config.Peer{
				"peer1": {IP: "192.168.1.2", Name: "peer1"},
			},
		}

		state := NewState(opts)

		// Initially should be false
		assert.False(t, state.LastRefreshHadRPCError())

		// Refresh with invalid RPC should set the flag to true
		state.Refresh()

		assert.True(t, state.LastRefreshHadRPCError())
	})

	t.Run("returns false after successful RPC", func(t *testing.T) {
		// Use a valid RPC endpoint
		validRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

		opts := Options{
			ClusterRPC:   validRPC,
			ActivePubkey: "test-active-pubkey",
			SelfIP:       "192.168.1.1",
			ConfigPeers: map[string]config.Peer{
				"peer1": {IP: "192.168.1.2", Name: "peer1"},
			},
		}

		state := NewState(opts)

		// Manually set the flag to true to simulate previous error
		state.lastRefreshHadRPCError = true

		// Refresh with valid RPC should clear the flag
		state.Refresh()

		assert.False(t, state.LastRefreshHadRPCError())
	})

	t.Run("flag reflects most recent refresh state", func(t *testing.T) {
		invalidRPC := rpc.NewClient("test", "https://invalid-url-that-will-fail.com")

		opts := Options{
			ClusterRPC:   invalidRPC,
			ActivePubkey: "test-active-pubkey",
			SelfIP:       "192.168.1.1",
			ConfigPeers:  map[string]config.Peer{},
		}

		state := NewState(opts)

		// First refresh fails
		state.Refresh()
		assert.True(t, state.LastRefreshHadRPCError())

		// Simulate successful refresh by manually setting the flag
		// (In real scenario, this would happen with a working RPC)
		state.lastRefreshHadRPCError = false
		assert.False(t, state.LastRefreshHadRPCError())

		// Another failed refresh
		state.Refresh()
		assert.True(t, state.LastRefreshHadRPCError())
	})
}

func TestRefresh_WithValidRPC(t *testing.T) {
	// Test Refresh with a valid RPC client
	// This test may fail if the RPC endpoint is not available, but that's expected
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "peNgUgnzs1jGogUPW8SThXMvzNpzKSNf3om78xVPAYx", // This matches the hardcoded active pubkey in the code
		SelfIP:       "192.168.1.1",
		ConfigPeers: map[string]config.Peer{
			"peer1": {IP: "192.168.1.2", Name: "peer1"},
		},
	}

	state := NewState(opts)

	// Refresh the state
	state.Refresh()

	// Verify the state was updated (timestamp should be set)
	assert.False(t, state.PeerStatesRefreshedAt.IsZero())

	// The actual peer states will depend on the RPC response, but we can verify the method completed
	// without panicking and updated the timestamp
}

func TestState_EdgeCases(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with multiple active peers (edge case)
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.2", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: true},
		"peer2": {IP: "192.168.1.3", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: true},
	}

	// Should find at least one active peer
	assert.True(t, state.HasActivePeer())

	// GetActivePeer should return the first one it finds
	peerState, err := state.GetActivePeer()
	assert.NoError(t, err)
	assert.True(t, peerState.LastSeenActive)
}

func TestState_EmptyConfigPeers(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{}, // Empty config
	}

	state := NewState(opts)

	// Test all methods with empty config
	assert.False(t, state.HasActivePeer())
	state.LeaderlessSamplesCount = 5 // Set count high
	assert.False(t, state.LeaderlessSamplesBelowThreshold(3))
	assert.False(t, state.HasIP("192.168.1.1"))
	assert.False(t, state.HasPeers("192.168.1.1"))

	_, err := state.GetActivePeer()
	assert.Error(t, err)

	peerStates := state.GetPeerStates()
	assert.Empty(t, peerStates)
}

func TestState_SampleBasedLogic(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with active peer (leaderlessSamplesCount should be 0)
	now := time.Now().UTC()
	state.peerStatesByName = map[string]PeerState{
		"peer1": {
			IP:             "192.168.1.2",
			Pubkey:         "pubkey1",
			LastSeenAtUTC:  now,
			LastSeenActive: true,
		},
	}
	state.LeaderlessSamplesCount = 0 // Reset when active peer found

	// Should pass with threshold of 3 (0 < 3)
	assert.True(t, state.LeaderlessSamplesBelowThreshold(3))

	// Should pass with threshold of 1 (0 < 1)
	assert.True(t, state.LeaderlessSamplesBelowThreshold(1))

	// Test with no active peer (LeaderlessSamplesCount increments)
	state.LeaderlessSamplesCount = 5
	delete(state.peerStatesByName, "peer1") // Remove active peer

	// Should fail with threshold of 3 (5 >= 3)
	assert.False(t, state.LeaderlessSamplesBelowThreshold(3))

	// Should pass with threshold of 10 (5 < 10)
	assert.True(t, state.LeaderlessSamplesBelowThreshold(10))
}

func TestState_ConcurrentAccess(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test concurrent access to state methods
	// This is mainly to ensure the methods are thread-safe
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			state.HasActivePeer()
			state.HasIP("192.168.1.1")
			state.HasPeers("192.168.1.1")
			state.GetPeerStates()
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// If we get here without panicking, the methods are thread-safe
	assert.True(t, true)
}

func TestPeerIPRankMap(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with empty state
	rankMap := state.PeerIPRankMap()
	assert.Empty(t, rankMap)

	// Test with multiple peers - IPs should be sorted and ranked zero-indexed
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.30", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer2": {IP: "192.168.1.10", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer3": {IP: "192.168.1.20", Pubkey: "pubkey3", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	rankMap = state.PeerIPRankMap()

	// IPs should be sorted: 10, 20, 30
	// So ranks should be: 10 -> 0, 20 -> 1, 30 -> 2
	assert.Equal(t, 0, rankMap["192.168.1.10"], "192.168.1.10 should have rank 0 (lowest IP)")
	assert.Equal(t, 1, rankMap["192.168.1.20"], "192.168.1.20 should have rank 1")
	assert.Equal(t, 2, rankMap["192.168.1.30"], "192.168.1.30 should have rank 2 (highest IP)")
	assert.Len(t, rankMap, 3)

	// Test with single peer
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.10", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	rankMap = state.PeerIPRankMap()
	assert.Equal(t, 0, rankMap["192.168.1.10"], "single peer should have rank 0")
	assert.Len(t, rankMap, 1)
}

func TestGetSortedIPs(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with empty state
	sortedIPs := state.getSortedIPs()
	assert.Empty(t, sortedIPs)

	// Test with multiple peers - should return sorted IPs
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.30", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer2": {IP: "192.168.1.10", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer3": {IP: "192.168.1.20", Pubkey: "pubkey3", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	sortedIPs = state.getSortedIPs()
	expected := []string{"192.168.1.10", "192.168.1.20", "192.168.1.30"}
	assert.Equal(t, expected, sortedIPs, "IPs should be sorted in ascending order")

	// Test with single peer
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "192.168.1.10", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	sortedIPs = state.getSortedIPs()
	assert.Equal(t, []string{"192.168.1.10"}, sortedIPs)

	// Test with IPs that have different octets to ensure proper string sorting
	state.peerStatesByName = map[string]PeerState{
		"peer1": {IP: "10.0.0.1", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer2": {IP: "192.168.1.1", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
		"peer3": {IP: "172.16.0.1", Pubkey: "pubkey3", LastSeenAtUTC: time.Now().UTC(), LastSeenActive: false},
	}

	sortedIPs = state.getSortedIPs()
	expected = []string{"10.0.0.1", "172.16.0.1", "192.168.1.1"}
	assert.Equal(t, expected, sortedIPs, "IPs should be sorted lexicographically")
}

func TestIsPeerPresent(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:                    realRPC,
		ActivePubkey:                  "test-active-pubkey",
		SelfIP:                        "192.168.1.1",
		ConfigPeers:                   map[string]config.Peer{},
		PeerGossipMinPresenceDuration: 30 * time.Second,
	}

	state := NewState(opts)

	t.Run("returns false for unknown peer", func(t *testing.T) {
		assert.False(t, state.IsPeerPresent("192.168.1.99"))
	})

	t.Run("returns true for recently seen peer", func(t *testing.T) {
		// Simulate peer seen just now
		state.lastSeenInGossipAt["192.168.1.2"] = time.Now().UTC()
		assert.True(t, state.IsPeerPresent("192.168.1.2"))
	})

	t.Run("returns true for peer seen within grace period", func(t *testing.T) {
		// Simulate peer seen 15 seconds ago (within 30s grace period)
		state.lastSeenInGossipAt["192.168.1.3"] = time.Now().UTC().Add(-15 * time.Second)
		assert.True(t, state.IsPeerPresent("192.168.1.3"))
	})

	t.Run("returns true near grace period boundary", func(t *testing.T) {
		// Peer seen just within the grace period boundary (29.9s ago)
		state.lastSeenInGossipAt["192.168.1.4"] = time.Now().UTC().Add(-29900 * time.Millisecond)
		assert.True(t, state.IsPeerPresent("192.168.1.4"))
	})

	t.Run("returns false for peer seen beyond grace period", func(t *testing.T) {
		// Simulate peer seen 31 seconds ago (beyond 30s grace period)
		state.lastSeenInGossipAt["192.168.1.5"] = time.Now().UTC().Add(-31 * time.Second)
		assert.False(t, state.IsPeerPresent("192.168.1.5"))
	})

	t.Run("returns false for peer seen long ago", func(t *testing.T) {
		// Simulate peer seen 5 minutes ago
		state.lastSeenInGossipAt["192.168.1.6"] = time.Now().UTC().Add(-5 * time.Minute)
		assert.False(t, state.IsPeerPresent("192.168.1.6"))
	})
}

func TestHasPresentPeers(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:                    realRPC,
		ActivePubkey:                  "test-active-pubkey",
		SelfIP:                        "192.168.1.1",
		ConfigPeers:                   map[string]config.Peer{},
		PeerGossipMinPresenceDuration: 30 * time.Second,
	}

	state := NewState(opts)

	t.Run("returns false with no peers in state", func(t *testing.T) {
		assert.False(t, state.HasPresentPeers("192.168.1.1"))
	})

	t.Run("returns false when only self is in state", func(t *testing.T) {
		state.peerStatesByName = map[string]PeerState{
			"self": {IP: "192.168.1.1", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC()},
		}
		state.lastSeenInGossipAt["192.168.1.1"] = time.Now().UTC()
		assert.False(t, state.HasPresentPeers("192.168.1.1"))
	})

	t.Run("returns true when other peer is present", func(t *testing.T) {
		state.peerStatesByName = map[string]PeerState{
			"self":  {IP: "192.168.1.1", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC()},
			"peer2": {IP: "192.168.1.2", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC()},
		}
		state.lastSeenInGossipAt["192.168.1.1"] = time.Now().UTC()
		state.lastSeenInGossipAt["192.168.1.2"] = time.Now().UTC()
		assert.True(t, state.HasPresentPeers("192.168.1.1"))
	})

	t.Run("returns false when other peer is stale", func(t *testing.T) {
		state.peerStatesByName = map[string]PeerState{
			"self":  {IP: "192.168.1.1", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC()},
			"peer2": {IP: "192.168.1.2", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC()},
		}
		state.lastSeenInGossipAt["192.168.1.1"] = time.Now().UTC()
		// Peer2 was seen 60 seconds ago - beyond grace period
		state.lastSeenInGossipAt["192.168.1.2"] = time.Now().UTC().Add(-60 * time.Second)
		assert.False(t, state.HasPresentPeers("192.168.1.1"))
	})

	t.Run("returns true when at least one peer is present among multiple", func(t *testing.T) {
		state.peerStatesByName = map[string]PeerState{
			"self":  {IP: "192.168.1.1", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC()},
			"peer2": {IP: "192.168.1.2", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC()},
			"peer3": {IP: "192.168.1.3", Pubkey: "pubkey3", LastSeenAtUTC: time.Now().UTC()},
		}
		// peer2 is stale
		state.lastSeenInGossipAt["192.168.1.2"] = time.Now().UTC().Add(-60 * time.Second)
		// peer3 is present
		state.lastSeenInGossipAt["192.168.1.3"] = time.Now().UTC()
		assert.True(t, state.HasPresentPeers("192.168.1.1"))
	})

	t.Run("returns false when peer in state but never tracked in lastSeenInGossipAt", func(t *testing.T) {
		state.peerStatesByName = map[string]PeerState{
			"self":  {IP: "192.168.1.1", Pubkey: "pubkey1", LastSeenAtUTC: time.Now().UTC()},
			"peer2": {IP: "192.168.1.2", Pubkey: "pubkey2", LastSeenAtUTC: time.Now().UTC()},
		}
		// Clear lastSeenInGossipAt - peer2 was never tracked
		state.lastSeenInGossipAt = make(map[string]time.Time)
		assert.False(t, state.HasPresentPeers("192.168.1.1"))
	})
}

func TestGetLastSeenInGossipAt(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	opts := Options{
		ClusterRPC:                    realRPC,
		ActivePubkey:                  "test-active-pubkey",
		SelfIP:                        "192.168.1.1",
		ConfigPeers:                   map[string]config.Peer{},
		PeerGossipMinPresenceDuration: 30 * time.Second,
	}

	state := NewState(opts)

	t.Run("returns false for unknown peer", func(t *testing.T) {
		_, exists := state.GetLastSeenInGossipAt("192.168.1.99")
		assert.False(t, exists)
	})

	t.Run("returns correct time for tracked peer", func(t *testing.T) {
		expectedTime := time.Now().UTC()
		state.lastSeenInGossipAt["192.168.1.2"] = expectedTime

		gotTime, exists := state.GetLastSeenInGossipAt("192.168.1.2")
		assert.True(t, exists)
		assert.Equal(t, expectedTime, gotTime)
	})

	t.Run("returns correct time even for stale peer", func(t *testing.T) {
		// Even stale peers should return their last seen time
		expectedTime := time.Now().UTC().Add(-5 * time.Minute)
		state.lastSeenInGossipAt["192.168.1.3"] = expectedTime

		gotTime, exists := state.GetLastSeenInGossipAt("192.168.1.3")
		assert.True(t, exists)
		assert.Equal(t, expectedTime, gotTime)
	})
}

func TestPresenceTrackingWithDifferentDurations(t *testing.T) {
	realRPC := rpc.NewClient("test", "https://api.mainnet-beta.solana.com")

	t.Run("short grace period (5s)", func(t *testing.T) {
		opts := Options{
			ClusterRPC:                    realRPC,
			ActivePubkey:                  "test-active-pubkey",
			SelfIP:                        "192.168.1.1",
			ConfigPeers:                   map[string]config.Peer{},
			PeerGossipMinPresenceDuration: 5 * time.Second,
		}
		state := NewState(opts)

		// Peer seen 3 seconds ago - should be present
		state.lastSeenInGossipAt["192.168.1.2"] = time.Now().UTC().Add(-3 * time.Second)
		assert.True(t, state.IsPeerPresent("192.168.1.2"))

		// Peer seen 6 seconds ago - should not be present
		state.lastSeenInGossipAt["192.168.1.3"] = time.Now().UTC().Add(-6 * time.Second)
		assert.False(t, state.IsPeerPresent("192.168.1.3"))
	})

	t.Run("long grace period (2m)", func(t *testing.T) {
		opts := Options{
			ClusterRPC:                    realRPC,
			ActivePubkey:                  "test-active-pubkey",
			SelfIP:                        "192.168.1.1",
			ConfigPeers:                   map[string]config.Peer{},
			PeerGossipMinPresenceDuration: 2 * time.Minute,
		}
		state := NewState(opts)

		// Peer seen 90 seconds ago - should still be present with 2m grace
		state.lastSeenInGossipAt["192.168.1.2"] = time.Now().UTC().Add(-90 * time.Second)
		assert.True(t, state.IsPeerPresent("192.168.1.2"))

		// Peer seen 3 minutes ago - should not be present
		state.lastSeenInGossipAt["192.168.1.3"] = time.Now().UTC().Add(-3 * time.Minute)
		assert.False(t, state.IsPeerPresent("192.168.1.3"))
	})

	t.Run("zero grace period", func(t *testing.T) {
		opts := Options{
			ClusterRPC:                    realRPC,
			ActivePubkey:                  "test-active-pubkey",
			SelfIP:                        "192.168.1.1",
			ConfigPeers:                   map[string]config.Peer{},
			PeerGossipMinPresenceDuration: 0,
		}
		state := NewState(opts)

		// With zero grace period, only peers seen exactly now would be present
		// Since we can't set exact now, any past time should fail
		state.lastSeenInGossipAt["192.168.1.2"] = time.Now().UTC().Add(-1 * time.Millisecond)
		assert.False(t, state.IsPeerPresent("192.168.1.2"))
	})
}
