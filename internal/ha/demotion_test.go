package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
)

// localValidatorStub is a local validator RPC whose processed slot and identity a test can set.
type localValidatorStub struct {
	mu       sync.Mutex
	slot     uint64
	identity string
	url      string
}

func newLocalValidatorStub(t *testing.T, slot uint64, identity string) *localValidatorStub {
	t.Helper()
	stub := &localValidatorStub{slot: slot, identity: identity}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		stub.mu.Lock()
		results := map[string]any{"getSlot": stub.slot, "getIdentity": map[string]any{"identity": stub.identity}}
		stub.mu.Unlock()
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": results[req.Method]}
		if _, ok := results[req.Method]; !ok {
			response = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response) //nolint:errcheck
	}))
	t.Cleanup(server.Close)
	stub.url = server.URL
	return stub
}

// unreachableRPCURL returns the URL of a server that is already closed.
func unreachableRPCURL(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	return server.URL
}

// newDemotionCandidate returns a manager that must demote itself on the next poll: the
// leaderless threshold is reached, it is missing from gossip and a peer is visible. Its local
// validator RPC is at localRPCURL. Recordings go to recordingDir; empty disables recording.
func newDemotionCandidate(t *testing.T, localRPCURL string, recordingDir string) *Manager {
	t.Helper()
	cfg := createTestConfig()
	cfg.Validator.RPCURL = localRPCURL
	if recordingDir != "" {
		cfg.Failover.Recording.Enabled = true
		cfg.Failover.Recording.OutputDir = recordingDir
	}
	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	if err := manager.initialize(); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}
	manager.gossipState.SetRefreshNoOpForTest(true)
	seedGossipPeers(manager, map[string]gossip.PeerState{"peer1": {IP: "192.168.1.101", Name: "peer1"}})
	manager.gossipState.LeaderlessSamplesCount = cfg.Failover.LeaderlessSamplesThreshold
	return manager
}

func TestEnsurePassive_Outcomes(t *testing.T) {
	tests := []struct {
		name     string
		identity func(cfg *config.Config) string // nil: the local RPC is unreachable
		want     string
	}{
		{name: "passive identity confirmed", identity: func(cfg *config.Config) string { return cfg.Validator.Identities.PassivePubkey() }, want: outcomeDemotedPassive},
		{name: "still active", identity: func(cfg *config.Config) string { return cfg.Validator.Identities.ActivePubkey() }, want: outcomeDemotionFailed},
		{name: "validator not answering", want: outcomeDemotedValidatorDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpcURL := unreachableRPCURL(t)
			var local *localValidatorStub
			if tt.identity != nil {
				local = newLocalValidatorStub(t, 1000, "")
				rpcURL = local.url
			}
			manager := newDemotionCandidate(t, rpcURL, "")
			if local != nil {
				local.mu.Lock()
				local.identity = tt.identity(manager.cfg)
				local.mu.Unlock()
			}

			if got := manager.ensurePassive(); got != tt.want {
				t.Errorf("ensurePassive() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnsureHAState_DemotionWithValidatorDownIsNotRepeated(t *testing.T) {
	dir := t.TempDir()
	manager := newDemotionCandidate(t, unreachableRPCURL(t), dir)

	manager.ensureHAState()
	if got := manager.cache.GetState().FailoverStatus; got != "becoming_passive" {
		t.Fatalf("first poll: FailoverStatus = %q, want becoming_passive", got)
	}
	if got := waitForRecordingOutcome(t, dir); got != outcomeDemotedValidatorDown {
		t.Errorf("recording outcome = %q, want %q", got, outcomeDemotedValidatorDown)
	}
	manager.cfg.Failover.Recording.Enabled = false // the next poll opens a new incident nobody reads

	manager.ensureHAState()
	if got := manager.cache.GetState().FailoverStatus; got != "idle" {
		t.Errorf("second poll: FailoverStatus = %q, want idle: the passive command must not run again while the validator is down", got)
	}
}

func TestEnsureHAState_DemotesAgainOnceValidatorAnswers(t *testing.T) {
	local := newLocalValidatorStub(t, 1000, "")
	manager := newDemotionCandidate(t, local.url, "")
	manager.demotedWithLocalRPCDown = true // an earlier demotion happened while the validator was down
	local.mu.Lock()
	local.identity = manager.cfg.Validator.Identities.ActivePubkey() // it came back active
	local.mu.Unlock()

	manager.ensureHAState()

	if got := manager.cache.GetState().FailoverStatus; got != "becoming_passive" {
		t.Errorf("FailoverStatus = %q, want becoming_passive", got)
	}
	if manager.demotedWithLocalRPCDown {
		t.Error("demotedWithLocalRPCDown is still set after the local RPC answered")
	}
}
