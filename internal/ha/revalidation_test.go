package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnsureHAState_PostDelayRefreshRPCFailureAndRecovery(t *testing.T) {
	cfg := createTestConfig()  // TowerBFT: the revalidation guard applies in every phase.
	const selfIP = "185.0.0.3" // rank 1 against the peer at 185.0.0.2
	passivePubkey := cfg.Validator.Identities.PassivePubkey()
	var clusterCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode RPC request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		var result any
		switch req.Method {
		case "getIdentity":
			result = map[string]any{"identity": passivePubkey}
		case "getClusterNodes":
			// The first poll sees both passive peers, then loses RPC during revalidation.
			// Both refreshes on the following poll succeed.
			if clusterCalls.Add(1) == 2 {
				http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
				return
			}
			result = []any{
				map[string]any{"pubkey": passivePubkey, "gossip": selfIP + ":8001"},
				map[string]any{"pubkey": "Vote111111111111111111111111111111111111111", "gossip": "185.0.0.2:8001"},
			}
		default:
			t.Errorf("unexpected RPC method: %s", req.Method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			t.Errorf("encode RPC response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	cfg.Cluster.RPCURLs = []string{server.URL}
	cfg.Cluster.RPCTimeoutDuration = time.Second
	cfg.Cluster.RPCURLCooldownDuration = time.Minute
	cfg.Validator.RPCURL = server.URL
	dir := enableRecording(cfg, t)
	manager := newTakeoverCandidate(t, cfg, selfIP)
	manager.gossipState.SetRefreshNoOpForTest(false)

	manager.ensureHAState()
	// Wait for background writes before assertions that could end the test early.
	outcome := waitForRecordingOutcome(t, dir)

	require.Equal(t, int32(2), clusterCalls.Load(), "initial and post-delay refreshes must reach RPC")
	require.True(t, manager.gossipState.LastRefreshHadRPCError())
	require.Equal(t, "idle", manager.cache.GetState().FailoverStatus, "failed revalidation must abort promotion")
	require.Equal(t, "aborted_revalidation_failed", outcome)

	// The abort recording has finished. Disable recording for the dry-run promotion so
	// no background writes outlive the test.
	cfg.Failover.Recording.Enabled = false
	manager.ensureHAState()

	require.Equal(t, int32(4), clusterCalls.Load(), "the next poll must refresh and revalidate again")
	require.False(t, manager.gossipState.LastRefreshHadRPCError(), "successful refresh must clear the RPC error")
	require.Equal(t, "becoming_active", manager.cache.GetState().FailoverStatus, "recovery must allow another takeover attempt")
}
