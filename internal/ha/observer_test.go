package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
)

// newJSONRPCStub starts a JSON-RPC server answering each method with a fixed result. Methods
// without an answer get a -32601 "method not found" error.
func newJSONRPCStub(t *testing.T, results map[string]any) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": results[req.Method]}
		if _, ok := results[req.Method]; !ok {
			response = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response) //nolint:errcheck
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// newAlpenglowObserver returns an initialized manager pinned to the Alpenglow phase, for a
// healthy passive rank-0 node that sees itself and one passive peer in gossip. Its local
// validator reports no Alpenglow genesis certificate.
func newAlpenglowObserver(t *testing.T, cfg *config.Config) *Manager {
	t.Helper()
	const selfIP, peerIP = "185.0.0.1", "185.0.0.2"
	rpcURL := newJSONRPCStub(t, map[string]any{"getAgGenesisCert": nil})
	cfg.Cluster.Consensus.Mode = config.ConsensusModeAlpenglow
	cfg.Cluster.RPCURLs = []string{rpcURL}
	cfg.Validator.RPCURL = rpcURL
	cfg.Failover.Peers = map[string]config.Peer{"peer1": {IP: peerIP, Name: "peer1"}}
	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: func() (string, error) { return selfIP, nil }})
	if err := manager.initialize(); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}

	manager.gossipState.SetRefreshNoOpForTest(true)
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"test-validator": {IP: selfIP, Name: "test-validator"},
		"peer1":          {IP: peerIP, Name: "peer1"},
	})
	manager.localState.SetForceHealthyForTest(true)
	manager.localState.SetHealthySinceForTest(time.Now().Add(-time.Hour))
	return manager
}

// In this release the Alpenglow rules are only observed, so failover decisions under Alpenglow
// must match TowerBFT ones.
func TestObserveOnly_AlpenglowPhaseKeepsTowerDecisions(t *testing.T) {
	t.Run("delinquency_bypass still acts", func(t *testing.T) {
		cfg := createTestConfig()
		cfg.Failover.DelinquencyBypass = true
		manager := newAlpenglowObserver(t, cfg)
		manager.gossipState.LeaderlessSamplesCount = 1 // below the threshold; only the bypass can act
		manager.gossipState.SetActivePeerDelinquentForTest(true)

		manager.ensureHAState()

		if got := manager.cache.GetState().FailoverStatus; got != "becoming_active" {
			t.Errorf("FailoverStatus = %q, want becoming_active", got)
		}
	})

	t.Run("a local validator without Alpenglow genesis is still promoted", func(t *testing.T) {
		manager := newAlpenglowObserver(t, createTestConfig())
		manager.gossipState.LeaderlessSamplesCount = manager.cfg.Failover.LeaderlessSamplesThreshold

		manager.ensureHAState()

		if eligible, _ := manager.detector.LocalEligibility(); eligible {
			t.Fatal("the local validator unexpectedly counts as migrated; the test setup is wrong")
		}
		if got := manager.cache.GetState().FailoverStatus; got != "becoming_active" {
			t.Errorf("FailoverStatus = %q, want becoming_active", got)
		}
	})
}
