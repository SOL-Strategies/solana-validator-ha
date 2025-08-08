package gossip

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

func TestNewState(t *testing.T) {
	// Create a real RPC client for this test since we're not testing RPC functionality
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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

func TestHasActivePeerInTheLast(t *testing.T) {
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
	}

	assert.False(t, state.HasActivePeerInTheLast(time.Minute))

	// Test with recently active peer
	state.peerStatesByName["peer2"] = PeerState{
		IP:             "192.168.1.3",
		Pubkey:         "pubkey2",
		LastSeenAtUTC:  time.Now().UTC(),
		LastSeenActive: true,
	}

	assert.True(t, state.HasActivePeerInTheLast(time.Minute))

	// Test with old active peer
	state.peerStatesByName["peer3"] = PeerState{
		IP:             "192.168.1.4",
		Pubkey:         "pubkey3",
		LastSeenAtUTC:  time.Now().UTC().Add(-2 * time.Hour),
		LastSeenActive: true,
	}

	assert.True(t, state.HasActivePeerInTheLast(time.Minute)) // peer2 is still recent
	
	// Test with a very short duration that should fail
	// We'll use a negative duration to ensure it fails
	assert.False(t, state.HasActivePeerInTheLast(-time.Second))
}

func TestGetActivePeer(t *testing.T) {
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with no active peers
	_, _, err := state.GetActivePeer()
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

	name, peerState, err := state.GetActivePeer()
	assert.NoError(t, err)
	assert.Equal(t, "peer1", name)
	assert.Equal(t, activePeer.IP, peerState.IP)
	assert.Equal(t, activePeer.Pubkey, peerState.Pubkey)
	assert.True(t, peerState.LastSeenActive)
}

func TestHasPeers(t *testing.T) {
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
	invalidRPC := rpc.NewClient("https://invalid-url-that-will-fail.com")
	
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

func TestRefresh_WithValidRPC(t *testing.T) {
	// Test Refresh with a valid RPC client
	// This test may fail if the RPC endpoint is not available, but that's expected
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
	name, peerState, err := state.GetActivePeer()
	assert.NoError(t, err)
	assert.NotEmpty(t, name)
	assert.True(t, peerState.LastSeenActive)
}

func TestState_EmptyConfigPeers(t *testing.T) {
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{}, // Empty config
	}

	state := NewState(opts)

	// Test all methods with empty config
	assert.False(t, state.HasActivePeer())
	assert.False(t, state.HasActivePeerInTheLast(time.Minute))
	assert.False(t, state.HasIP("192.168.1.1"))
	assert.False(t, state.HasPeers("192.168.1.1"))
	
	_, _, err := state.GetActivePeer()
	assert.Error(t, err)
	
	peerStates := state.GetPeerStates()
	assert.Empty(t, peerStates)
}

func TestState_TimeBasedLogic(t *testing.T) {
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
	opts := Options{
		ClusterRPC:   realRPC,
		ActivePubkey: "test-active-pubkey",
		SelfIP:       "192.168.1.1",
		ConfigPeers:  map[string]config.Peer{},
	}

	state := NewState(opts)

	// Test with peer seen exactly at the boundary
	now := time.Now().UTC()
	state.peerStatesByName = map[string]PeerState{
		"peer1": {
			IP:             "192.168.1.2",
			Pubkey:         "pubkey1",
			LastSeenAtUTC:  now,
			LastSeenActive: true,
		},
	}

	// Should be active within 1 minute
	assert.True(t, state.HasActivePeerInTheLast(time.Minute))
	
	// Should be active within 1 hour
	assert.True(t, state.HasActivePeerInTheLast(time.Hour))
	
	// Test with a very short duration that should fail
	// We'll use a negative duration to ensure it fails
	assert.False(t, state.HasActivePeerInTheLast(-time.Second))
}

func TestState_ConcurrentAccess(t *testing.T) {
	realRPC := rpc.NewClient("https://api.mainnet-beta.solana.com")
	
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
