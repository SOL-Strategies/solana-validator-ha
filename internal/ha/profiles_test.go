package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/recording"
	solana "github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"
)

func TestSharedBackupAttemptsOnlyHighestPriorityProfile(t *testing.T) {
	cfg := sharedBackupConfig()
	cfg.Failover.Recording.Enabled = true
	cfg.Failover.Recording.OutputDir = t.TempDir()
	cfg.Failover.DryRun = false
	attempts := filepath.Join(t.TempDir(), "attempts")
	t.Cleanup(func() { waitForRecordingWrites(t) })
	cfg.Failover.Active = config.Role{
		Name:    "active",
		Command: "/bin/sh",
		Args:    []string{"-c", `printf '%s\n' "$1" >> "$2"; exit 7`, "sh", "{{ .ProfileName }}", attempts},
	}
	rpc := sharedBackupRPC(t, cfg, func(_ int64) string {
		return cfg.Validator.Identities.PassivePubkey()
	}, false)
	manager := startSharedBackupTest(t, cfg, rpc)
	manager.ensureHAState()

	data, err := os.ReadFile(attempts)
	require.NoError(t, err)
	require.Equal(t, "main\n", string(data))
	waitForRecordingWrites(t)
	files, err := filepath.Glob(filepath.Join(cfg.Failover.Recording.OutputDir, "*-recording.json"))
	require.NoError(t, err)
	outcomes := make(map[string]string)
	for _, path := range files {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var event recording.FailoverEvent
		require.NoError(t, json.Unmarshal(data, &event))
		require.NotNil(t, event.Outcome)
		outcomes[event.Node.Profile] = event.Outcome.Result
	}
	require.Equal(t, map[string]string{"main": "activation_failed", "secondary": "aborted_profile_priority"}, outcomes)
}

func TestSharedBackupRevalidatesClusterAndLocalOccupancy(t *testing.T) {
	for _, failRecheck := range []bool{false, true} {
		name := "local identity no longer passive"
		if failRecheck {
			name = "cluster RPC fails during revalidation"
		}
		t.Run(name, func(t *testing.T) {
			cfg := sharedBackupConfig()
			rpc := sharedBackupRPC(t, cfg, func(read int64) string {
				if !failRecheck && read > 1 {
					profile := cfg.Profiles["secondary"]
					return profile.ActivePubkey()
				}
				return cfg.Validator.Identities.PassivePubkey()
			}, failRecheck)
			manager := startSharedBackupTest(t, cfg, rpc)
			manager.ensureHAState()

			require.Equal(t, "idle", manager.cache.GetState().FailoverStatus)
			require.Equal(t, 1, manager.profiles[1].gossipState.LeaderlessSamplesCount)
			require.Zero(t, manager.clusterRPCOutage.consecutiveFailures, "revalidation is not a poll")
		})
	}
}

func sharedBackupConfig() *config.Config {
	cfg := createTestConfig()
	cfg.Validator.Identities.ActiveKeyPair = nil
	cfg.Failover.Peers = nil
	cfg.Failover.LeaderlessSamplesThreshold = 1
	cfg.Failover.PollIntervalDuration = 5 * time.Millisecond
	cfg.Cluster.RPCTimeoutDuration = time.Second
	cfg.Profiles = make(config.Profiles)
	for rank, name := range []string{"main", "secondary"} {
		cfg.Profiles[name] = config.Profile{
			Priority:              intPtr(rank),
			Identities:            config.ValidatorIdentities{ActivePubkeyStr: solana.NewWallet().PublicKey().String()},
			VotePubkeyStr:         solana.NewWallet().PublicKey().String(),
			AuthorizedVoterPubkey: solana.NewWallet().PublicKey().String(),
			Peers: config.Peers{
				"test-validator": {IP: "192.168.1.100", Priority: intPtr(1)},
				"peer":           {IP: "192.168.1.101", Priority: intPtr(0)},
			},
		}
	}
	return cfg
}

func startSharedBackupTest(t *testing.T, cfg *config.Config, rpcURL string) *Manager {
	t.Helper()
	cfg.Validator.RPCURL = rpcURL
	cfg.Cluster.RPCURLs = []string{rpcURL}
	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())
	t.Cleanup(manager.cancel)
	manager.localState.SetForceHealthyForTest(true)
	manager.localState.SetHealthySinceForTest(time.Now().Add(-time.Hour))
	return manager
}

func sharedBackupRPC(t *testing.T, cfg *config.Config, identity func(int64) string, failRecheck bool) string {
	t.Helper()
	var nodeReads, identityReads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "getIdentity":
			result = map[string]string{"identity": identity(identityReads.Add(1))}
		case "getClusterNodes":
			if nodeReads.Add(1) > 1 && failRecheck {
				http.Error(w, "RPC unavailable", http.StatusBadGateway)
				return
			}
			result = []map[string]string{{"pubkey": cfg.Validator.Identities.PassivePubkey(), "gossip": "192.168.1.100:8001"}}
		default:
			t.Errorf("unexpected RPC method %s", request.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return server.URL
}
