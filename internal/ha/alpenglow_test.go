package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
	"github.com/sol-strategies/solana-validator-ha/internal/recording"
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

// newTakeoverCandidate returns an initialized manager for a healthy passive node that sees
// itself and one passive peer in gossip, with the leaderless threshold reached. selfIP decides
// the node's rank against the peer at 185.0.0.2.
func newTakeoverCandidate(t *testing.T, cfg *config.Config, selfIP string) *Manager {
	t.Helper()
	const peerIP = "185.0.0.2"
	cfg.Failover.Peers = map[string]config.Peer{"peer1": {IP: peerIP, Name: "peer1"}}
	cfg.Failover.PollIntervalDuration = 10 * time.Millisecond // keeps the rank-1 delay short
	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: func() (string, error) { return selfIP, nil }})
	if err := manager.initialize(); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}

	manager.gossipState.SetRefreshNoOpForTest(true)
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"test-validator": {IP: selfIP, Name: "test-validator"},
		"peer1":          {IP: peerIP, Name: "peer1"},
	})
	manager.gossipState.LeaderlessSamplesCount = cfg.Failover.LeaderlessSamplesThreshold
	manager.localState.SetForceHealthyForTest(true)
	manager.localState.SetHealthySinceForTest(time.Now().Add(-time.Hour))
	return manager
}

// enableRecording makes failovers write recordings to a temporary directory it returns.
func enableRecording(cfg *config.Config, t *testing.T) string {
	dir := t.TempDir()
	cfg.Failover.Recording.Enabled = true
	cfg.Failover.Recording.OutputDir = dir
	return dir
}

// waitForRecordingOutcome returns the outcome of the single recording written to dir. It waits
// until no checkpoint is left, so the background writer is done with dir before the test ends.
func waitForRecordingOutcome(t *testing.T, dir string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*-recording.json"))
		partials, _ := filepath.Glob(filepath.Join(dir, "*.partial"))
		if len(matches) == 1 && len(partials) == 0 {
			raw, err := os.ReadFile(matches[0])
			if err != nil {
				t.Fatalf("reading recording: %v", err)
			}
			var event recording.FailoverEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatalf("parsing recording: %v", err)
			}
			if event.Outcome == nil {
				t.Fatal("recording has no outcome")
			}
			return event.Outcome.Result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no recording was written")
	return ""
}

func TestEnsureHAState_LocalAlpenglowGenesis(t *testing.T) {
	tests := []struct {
		name             string
		localGenesisCert any
		wantStatus       string
		wantOutcome      string // empty when the outcome is not checked
	}{
		{
			name:             "not migrated aborts the takeover",
			localGenesisCert: nil,
			wantStatus:       "idle",
			wantOutcome:      "aborted_local_not_migrated",
		},
		{
			name:             "migrated takes over",
			localGenesisCert: map[string]any{"block": map[string]any{"slot": 5000}},
			wantStatus:       "becoming_active",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpcURL := newJSONRPCStub(t, map[string]any{"getAgGenesisCert": tt.localGenesisCert})
			cfg := createTestConfig()
			cfg.Cluster.Consensus.Mode = config.ConsensusModeAlpenglow
			cfg.Cluster.RPCURLs = []string{rpcURL}
			cfg.Validator.RPCURL = rpcURL
			// only record when the outcome is checked, so no write outlives the test
			var dir string
			if tt.wantOutcome != "" {
				dir = enableRecording(cfg, t)
			}
			manager := newTakeoverCandidate(t, cfg, "185.0.0.1") // rank 0

			manager.ensureHAState()

			if got := manager.cache.GetState().FailoverStatus; got != tt.wantStatus {
				t.Errorf("FailoverStatus = %q, want %q", got, tt.wantStatus)
			}
			if tt.wantOutcome == "" {
				return
			}
			if got := waitForRecordingOutcome(t, dir); got != tt.wantOutcome {
				t.Errorf("recording outcome = %q, want %q", got, tt.wantOutcome)
			}
		})
	}
}

func TestEnsureHAState_PostDelayVeto(t *testing.T) {
	tests := []struct {
		name                  string
		veto                  string
		streakHasGossipAbsent bool
		wantStatus            string
		wantOutcome           string
	}{
		{
			name:        "vote-only streak aborts when the cluster stalled",
			veto:        gossip.VetoClusterStalled,
			wantStatus:  "idle",
			wantOutcome: "aborted_cluster_stalled",
		},
		{
			name:        "vote-only streak aborts when the vote account is excluded",
			veto:        gossip.VetoVoteAccountExcluded,
			wantStatus:  "idle",
			wantOutcome: "aborted_vote_account_excluded",
		},
		{
			name:        "vote-only streak aborts when an earlier failover did not help",
			veto:        gossip.VetoFailoverIneffective,
			wantStatus:  "idle",
			wantOutcome: "aborted_failover_ineffective",
		},
		{
			name:                  "streak with the active missing from gossip is never vetoed",
			veto:                  gossip.VetoClusterStalled,
			streakHasGossipAbsent: true,
			wantStatus:            "becoming_active",
		},
		{
			name:       "vote-only streak without a veto takes over",
			wantStatus: "becoming_active",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createTestConfig()
			// only record when the outcome is checked, so no write outlives the test
			var dir string
			if tt.wantOutcome != "" {
				dir = enableRecording(cfg, t)
			}
			manager := newTakeoverCandidate(t, cfg, "185.0.0.3") // rank 1, so the post-delay re-check runs
			manager.gossipState.SetVoteEvidenceForTest(gossip.LeaderlessReasonVoteLag, tt.veto, tt.streakHasGossipAbsent)

			manager.ensureHAState()

			if got := manager.cache.GetState().FailoverStatus; got != tt.wantStatus {
				t.Errorf("FailoverStatus = %q, want %q", got, tt.wantStatus)
			}
			if tt.wantOutcome == "" {
				return
			}
			if got := waitForRecordingOutcome(t, dir); got != tt.wantOutcome {
				t.Errorf("recording outcome = %q, want %q", got, tt.wantOutcome)
			}
		})
	}
}

func TestEnsureHAState_RechecksLocalGenesisBeforePromotion(t *testing.T) {
	tests := []struct {
		name   string
		selfIP string
	}{
		{name: "rank 0, no delay", selfIP: "185.0.0.1"},
		{name: "rank 1, after the delay", selfIP: "185.0.0.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// the cluster has no Alpenglow genesis on the first read and has one on every later
			// read, as if it migrated during the takeover
			var mu sync.Mutex
			certReads := 0
			cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Method string `json:"method"`
					ID     any    `json:"id"`
				}
				json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
				var result any
				switch req.Method {
				case "getAgGenesisCert":
					mu.Lock()
					certReads++
					if certReads > 1 {
						result = map[string]any{"block": map[string]any{"slot": 5000}}
					}
					mu.Unlock()
				case "getAccountInfo":
					result = map[string]any{"context": map[string]any{"slot": 1}, "value": nil}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}) //nolint:errcheck
			}))
			t.Cleanup(cluster.Close)
			cfg := createTestConfig()
			cfg.Cluster.Consensus.Mode = config.ConsensusModeAuto
			cfg.Cluster.RPCURLs = []string{cluster.URL}
			cfg.Cluster.RPCTimeoutDuration = time.Second
			cfg.Validator.RPCURL = newJSONRPCStub(t, map[string]any{"getAgGenesisCert": nil}) // not migrated
			dir := enableRecording(cfg, t)
			manager := newTakeoverCandidate(t, cfg, tt.selfIP)

			manager.ensureHAState()

			if got := manager.cache.GetState().FailoverStatus; got != "idle" {
				t.Errorf("FailoverStatus = %q, want %q", got, "idle")
			}
			if got := waitForRecordingOutcome(t, dir); got != "aborted_local_not_migrated" {
				t.Errorf("recording outcome = %q, want %q", got, "aborted_local_not_migrated")
			}
		})
	}
}

func TestEnsureHAState_DelinquencyBypassIgnoredOutsideTower(t *testing.T) {
	rpcURL := newJSONRPCStub(t, map[string]any{})
	cfg := createTestConfig()
	cfg.Cluster.Consensus.Mode = config.ConsensusModeAlpenglow
	cfg.Cluster.RPCURLs = []string{rpcURL}
	cfg.Validator.RPCURL = rpcURL
	cfg.Failover.DelinquencyBypass = true
	manager := newTakeoverCandidate(t, cfg, "185.0.0.1")
	manager.gossipState.LeaderlessSamplesCount = 1 // below the threshold; only the bypass could act
	manager.gossipState.SetActivePeerDelinquentForTest(true)

	manager.ensureHAState()

	if got := manager.cache.GetState().FailoverStatus; got != "idle" {
		t.Errorf("FailoverStatus = %q, want idle: delinquency_bypass must not act outside the TowerBFT phase", got)
	}
}
