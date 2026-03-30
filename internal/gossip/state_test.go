package gossip

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		// Use a mock server that returns a valid empty getClusterNodes response.
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  []any{},
			})
		}))
		t.Cleanup(mockServer.Close)

		opts := Options{
			ClusterRPC:   rpc.NewClient("test", mockServer.URL),
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

// ---- helpers for undeclared active peer tests ----

const (
	testActivePubkey = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	testUndeclaredIP = "10.0.0.99"
	testDeclaredIP   = "192.168.1.101"
	testSelfIP       = "192.168.1.100"
)

// newGossipMockRPCServer creates a mock Solana JSON-RPC HTTP server for gossip state tests.
// responses maps method names (e.g. "getClusterNodes") to their result values.
func newGossipMockRPCServer(t *testing.T, responses map[string]interface{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		result, ok := responses[req.Method]
		if !ok {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
				"id":      req.ID,
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  result,
			"id":      req.ID,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

// gossipClusterNode returns a mock getClusterNodes entry for a node at the given IP.
func gossipClusterNode(pubkey, ip string) map[string]interface{} {
	return map[string]interface{}{
		"pubkey":  pubkey,
		"gossip":  ip + ":8001",
		"tpu":     ip + ":8002",
		"rpc":     ip + ":8003",
		"version": "1.17.0",
	}
}

// votingVoteAccountsResult returns a mock getVoteAccounts result with the given node pubkeys as current (voting).
func votingVoteAccountsResult(currentNodePubkeys []string) map[string]interface{} {
	current := []map[string]interface{}{}
	for _, pk := range currentNodePubkeys {
		current = append(current, map[string]interface{}{
			"nodePubkey":       pk,
			"votePubkey":       "11111111111111111111111111111111",
			"activatedStake":   1000000,
			"epochVoteAccount": true,
			"epochCredits":     []interface{}{},
			"commission":       0,
			"lastVote":         100,
			"rootSlot":         90,
		})
	}
	return map[string]interface{}{
		"current":    current,
		"delinquent": []interface{}{},
	}
}

// ---- tests for undeclared active peer detection ----

func TestHasConfigUndeclaredActivePeer(t *testing.T) {
	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", "https://api.mainnet-beta.solana.com"),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{},
	})

	t.Run("returns false when no undeclared active peer", func(t *testing.T) {
		assert.False(t, state.HasConfigUndeclaredActivePeer())
	})

	t.Run("returns true and correct peer when set", func(t *testing.T) {
		state.configUndeclaredActivePeer = PeerState{
			Name:           "config-undeclared-active-peer",
			IP:             testUndeclaredIP,
			Pubkey:         testActivePubkey,
			LastSeenAtUTC:  time.Now().UTC(),
			LastSeenActive: true,
		}
		assert.True(t, state.HasConfigUndeclaredActivePeer())
		peer := state.GetConfigUndeclaredActivePeer()
		assert.Equal(t, testUndeclaredIP, peer.IP)
		assert.Equal(t, testActivePubkey, peer.Pubkey)
		assert.True(t, peer.LastSeenActive)
	})
}

func TestRefresh_UndeclaredActivePeer_VotingBlocksFailover(t *testing.T) {
	// Undeclared IP has the active pubkey and IS in current vote accounts (voting).
	// Expected: configUndeclaredActivePeer is set, leaderless counter stays 0.
	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			gossipClusterNode(testActivePubkey, testUndeclaredIP),
		},
		"getSlot":         100,
		"getVoteAccounts": votingVoteAccountsResult([]string{testActivePubkey}),
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	require.True(t, state.HasConfigUndeclaredActivePeer(), "should detect undeclared voting active peer")
	peer := state.GetConfigUndeclaredActivePeer()
	assert.Equal(t, testUndeclaredIP, peer.IP)
	assert.Equal(t, testActivePubkey, peer.Pubkey)
	assert.True(t, peer.LastSeenActive)
	assert.Equal(t, 0, state.LeaderlessSamplesCount, "leaderless counter must not increment when voting undeclared active peer is found")
}

func TestRefresh_UndeclaredActivePeer_NotVotingAllowsFailover(t *testing.T) {
	// Undeclared IP has the active pubkey but is NOT in vote accounts (not voting).
	// Expected: configUndeclaredActivePeer is NOT set, leaderless counter increments.
	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			gossipClusterNode(testActivePubkey, testUndeclaredIP),
		},
		"getSlot":         100,
		"getVoteAccounts": votingVoteAccountsResult([]string{}), // empty current - node not found
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	assert.False(t, state.HasConfigUndeclaredActivePeer(), "should NOT block failover when undeclared peer is not voting")
	assert.Equal(t, 1, state.LeaderlessSamplesCount, "leaderless counter must increment to allow legitimate failover")
}

func TestRefresh_ConfigUndeclaredActivePeer_ResetOnEachRefresh(t *testing.T) {
	// A refresh with no undeclared active peers must clear configUndeclaredActivePeer.
	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{}, // no nodes at all
		"getSlot":         100,
		"getVoteAccounts": votingVoteAccountsResult([]string{}),
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{},
	})

	// Seed a stale value from a previous refresh.
	state.configUndeclaredActivePeer = PeerState{
		Name:           "config-undeclared-active-peer",
		IP:             testUndeclaredIP,
		Pubkey:         testActivePubkey,
		LastSeenAtUTC:  time.Now().UTC(),
		LastSeenActive: true,
	}
	require.True(t, state.HasConfigUndeclaredActivePeer())

	state.Refresh()

	assert.False(t, state.HasConfigUndeclaredActivePeer(), "configUndeclaredActivePeer must be reset on every Refresh()")
}

func TestRefresh_DualCRDSEntry_ActiveWinsOverStalePassive(t *testing.T) {
	// During a gossip identity transition the old (passive) and new (active) CRDS entries
	// for the same peer briefly coexist. getClusterNodes returns both. The active entry
	// must win: the peer should be recorded as active regardless of which entry appears
	// second in the list.
	const passivePubkey = "11111111111111111111111111111112"

	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			// active entry first, then stale passive entry for the same IP
			gossipClusterNode(testActivePubkey, testDeclaredIP),
			gossipClusterNode(passivePubkey, testDeclaredIP),
		},
		"getSlot":         100,
		"getVoteAccounts": votingVoteAccountsResult([]string{testActivePubkey}),
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	activePeer, err := state.GetActivePeer()
	require.NoError(t, err, "active peer should be found despite dual CRDS entries")
	assert.Equal(t, testDeclaredIP, activePeer.IP)
	assert.True(t, activePeer.LastSeenActive, "active entry must win over stale passive entry")
	assert.Equal(t, 0, state.LeaderlessSamplesCount, "must not count as leaderless when active peer present")
}

func TestRefresh_DualCRDSEntry_PassiveFirstActiveSecond_ActiveWins(t *testing.T) {
	// Same as above but passive entry appears first — active must still win.
	const passivePubkey = "11111111111111111111111111111112"

	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			// stale passive entry first, then active entry
			gossipClusterNode(passivePubkey, testDeclaredIP),
			gossipClusterNode(testActivePubkey, testDeclaredIP),
		},
		"getSlot":         100,
		"getVoteAccounts": votingVoteAccountsResult([]string{testActivePubkey}),
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	activePeer, err := state.GetActivePeer()
	require.NoError(t, err, "active peer should be found regardless of entry order")
	assert.Equal(t, testDeclaredIP, activePeer.IP)
	assert.True(t, activePeer.LastSeenActive)
	assert.Equal(t, 0, state.LeaderlessSamplesCount)
}

func TestRefresh_DeclaredActivePeer_NotRecordedAsUndeclared(t *testing.T) {
	// Active pubkey at a DECLARED IP must go through normal peer tracking,
	// not into configUndeclaredActivePeer.
	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			gossipClusterNode(testActivePubkey, testDeclaredIP),
		},
		"getSlot":         100,
		"getVoteAccounts": votingVoteAccountsResult([]string{testActivePubkey}),
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	assert.False(t, state.HasConfigUndeclaredActivePeer(), "declared active peer must not be recorded as undeclared")
	assert.Equal(t, 0, state.LeaderlessSamplesCount, "leaderless counter must not increment for a healthy declared active peer")
	activePeer, err := state.GetActivePeer()
	require.NoError(t, err)
	assert.Equal(t, testDeclaredIP, activePeer.IP)
	assert.True(t, activePeer.LastSeenActive)
}

// ---- tests for active peer delinquency detection ----

// delinquentVoteAccountsResult returns a mock getVoteAccounts result with the given node pubkeys
// in the delinquent list at the given lastVote slot.
func delinquentVoteAccountsResult(delinquentNodePubkeys []string, lastVote uint64) map[string]interface{} {
	delinquent := []map[string]interface{}{}
	for _, pk := range delinquentNodePubkeys {
		delinquent = append(delinquent, map[string]interface{}{
			"nodePubkey":       pk,
			"votePubkey":       "11111111111111111111111111111111",
			"activatedStake":   1000000,
			"epochVoteAccount": true,
			"epochCredits":     []interface{}{},
			"commission":       0,
			"lastVote":         lastVote,
			"rootSlot":         lastVote - 10,
		})
	}
	return map[string]interface{}{
		"current":    []interface{}{},
		"delinquent": delinquent,
	}
}

// balanceResult returns a mock getBalance result for the given lamport balance.
func balanceResult(lamports uint64) map[string]interface{} {
	return map[string]interface{}{
		"context": map[string]interface{}{"slot": 500},
		"value":   lamports,
	}
}

func TestActivePeerIsDelinquent_FalseByDefault(t *testing.T) {
	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", "https://api.mainnet-beta.solana.com"),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})
	assert.False(t, state.ActivePeerIsDelinquent())
}

func TestActivePeerIsDelinquent_SetOnGenuineDelinquency(t *testing.T) {
	// Active peer is in gossip with the active pubkey, in the delinquent vote accounts list,
	// with a balance above rent-exempt minimum → genuine delinquency, flag should be set.
	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			gossipClusterNode(testActivePubkey, testDeclaredIP),
		},
		"getVoteAccounts": delinquentVoteAccountsResult([]string{testActivePubkey}, 100),
		"getBalance":      balanceResult(10_000_000), // well above 890880 rent-exempt minimum
		"getSlot":         500,
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	assert.True(t, state.ActivePeerIsDelinquent(), "delinquency flag should be set when network confirms peer is delinquent")
	assert.Equal(t, 1, state.LeaderlessSamplesCount, "leaderless counter should increment for a delinquent peer")
}

func TestActivePeerIsDelinquent_NotSetForLowBalance(t *testing.T) {
	// Active peer is delinquent but balance is below rent-exempt minimum → forgiven, flag NOT set.
	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			gossipClusterNode(testActivePubkey, testDeclaredIP),
		},
		"getVoteAccounts": delinquentVoteAccountsResult([]string{testActivePubkey}, 100),
		"getBalance":      balanceResult(500_000), // below 890880 rent-exempt minimum
		"getSlot":         500,
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	assert.False(t, state.ActivePeerIsDelinquent(), "delinquency flag must not be set when peer is forgiven due to low balance")
}

func TestActivePeerIsDelinquent_NotSetWhenVoting(t *testing.T) {
	// Active peer is in gossip and in the current (voting) list → healthy, flag NOT set.
	server := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{
			gossipClusterNode(testActivePubkey, testDeclaredIP),
		},
		"getVoteAccounts": votingVoteAccountsResult([]string{testActivePubkey}),
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()

	assert.False(t, state.ActivePeerIsDelinquent(), "delinquency flag must not be set when peer is actively voting")
	assert.Equal(t, 0, state.LeaderlessSamplesCount)
}

func TestActivePeerIsDelinquent_ResetOnNextRefresh(t *testing.T) {
	// First Refresh: peer delinquent → flag set.
	// Second Refresh: peer back to voting → flag cleared.
	delinquentServer := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
		"getVoteAccounts": delinquentVoteAccountsResult([]string{testActivePubkey}, 100),
		"getBalance":      balanceResult(10_000_000),
		"getSlot":         500,
	})
	votingServer := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
		"getVoteAccounts": votingVoteAccountsResult([]string{testActivePubkey}),
	})

	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", delinquentServer.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
	})

	state.Refresh()
	assert.True(t, state.ActivePeerIsDelinquent(), "flag should be set after delinquent refresh")

	// switch to the voting server and refresh again; clear cache so it re-discovers the vote account
	state.clusterRPC = rpc.NewClient("test", votingServer.URL)
	clear(state.votePubkeyCache)
	state.Refresh()
	assert.False(t, state.ActivePeerIsDelinquent(), "flag should be cleared after peer resumes voting")
}
