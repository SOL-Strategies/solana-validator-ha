package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterRPCOutageMonitor(t *testing.T) {
	const pollEvery = 5 * time.Second
	now := time.Date(2026, 11, 2, 4, 48, 55, 0, time.UTC)
	newMonitor := func() *clusterRPCOutageMonitor {
		o := newClusterRPCOutageMonitor(3)
		o.now = func() time.Time { return now }
		return o
	}

	t.Run("failures below the threshold are not an outage", func(t *testing.T) {
		o := newMonitor()
		assert.Equal(t, clusterRPCOutageUnchanged, o.observe(true))
		assert.Equal(t, clusterRPCOutageUnchanged, o.observe(true))
		assert.False(t, o.active)
		assert.Equal(t, 2, o.consecutiveFailures)

		assert.Equal(t, clusterRPCOutageUnchanged, o.observe(false))
		assert.Equal(t, 0, o.consecutiveFailures)
	})

	t.Run("a success restarts the count", func(t *testing.T) {
		o := newMonitor()
		for _, failed := range []bool{true, true, false, true, true} {
			assert.Equal(t, clusterRPCOutageUnchanged, o.observe(failed))
		}
		assert.False(t, o.active)
	})

	t.Run("reports start and end once each, and times the whole run", func(t *testing.T) {
		o := newMonitor()
		assert.Equal(t, clusterRPCOutageUnchanged, o.observe(true))
		now = now.Add(pollEvery)
		assert.Equal(t, clusterRPCOutageUnchanged, o.observe(true))
		now = now.Add(pollEvery)
		assert.Equal(t, clusterRPCOutageStarted, o.observe(true))
		assert.True(t, o.active)
		now = now.Add(pollEvery)
		assert.Equal(t, clusterRPCOutageUnchanged, o.observe(true))
		assert.Equal(t, 4, o.consecutiveFailures)
		assert.Equal(t, 3*pollEvery, o.duration())

		now = now.Add(pollEvery)
		assert.Equal(t, 4*pollEvery, o.duration())
		assert.Equal(t, clusterRPCOutageEnded, o.observe(false))
		assert.False(t, o.active)
		assert.Equal(t, clusterRPCOutageUnchanged, o.observe(false))
	})
}

// A standby whose cluster RPC is down must report the outage and stay put: failed polls are not
// leaderless samples, so it neither promotes nor counts towards promoting.
func TestEnsureHAState_ReportsClusterRPCOutageWithoutActing(t *testing.T) {
	cfg := createTestConfig()
	const selfIP = "185.0.0.3"
	passivePubkey := cfg.Validator.Identities.PassivePubkey()
	activePubkey := cfg.Validator.Identities.ActivePubkey()
	var clusterDown atomic.Bool
	clusterDown.Store(true)
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
			if clusterDown.Load() {
				http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
				return
			}
			result = []any{
				map[string]any{"pubkey": passivePubkey, "gossip": selfIP + ":8001"},
				map[string]any{"pubkey": activePubkey, "gossip": "185.0.0.2:8001"},
			}
		case "getVoteAccounts":
			result = map[string]any{"current": []any{}, "delinquent": []any{}}
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
	manager := newTakeoverCandidate(t, cfg, selfIP)
	manager.gossipState.SetRefreshNoOpForTest(false)
	manager.gossipState.LeaderlessSamplesCount = 0 // the active peer was visible until the outage began
	threshold := cfg.Failover.LeaderlessSamplesThreshold

	for poll := 1; poll < threshold; poll++ {
		manager.ensureHAState()
		state := manager.cache.GetState()
		require.Equal(t, poll, state.ClusterRPCFailures)
		require.False(t, state.ClusterRPCOutage, "poll %d is below the threshold", poll)
	}

	// Well past the threshold: a leaderless streak this long would have promoted.
	for poll := threshold; poll < threshold*3; poll++ {
		manager.ensureHAState()
		state := manager.cache.GetState()
		require.Equal(t, poll, state.ClusterRPCFailures)
		require.True(t, state.ClusterRPCOutage)
		require.Equal(t, "idle", state.FailoverStatus, "an RPC outage must not trigger a takeover")
		require.Zero(t, manager.gossipState.LeaderlessSamplesCount, "failed polls must not count as leaderless")
	}

	clusterDown.Store(false)
	manager.ensureHAState()
	state := manager.cache.GetState()
	require.Zero(t, state.ClusterRPCFailures)
	require.False(t, state.ClusterRPCOutage)
	require.Equal(t, "idle", state.FailoverStatus)
}
