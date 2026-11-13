package ha

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/recording"
	"github.com/stretchr/testify/require"
)

func TestActivationRecording(t *testing.T) {
	const reason = "cannot positively fence peer; staying passive"
	for _, scenario := range []string{"pre-hook", "command", "command-then-active", "identity-mismatch", "identity-rpc-error", "success", "post-hook"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := createTestConfig()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID any `json:"id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				// Metrics and candidacy read the passive identity before activation confirmation.
				identity := cfg.Validator.Identities.PassivePubkey()
				if calls.Add(1) > 2 {
					if scenario == "success" || scenario == "post-hook" || scenario == "command-then-active" {
						identity = cfg.Validator.Identities.ActivePubkey()
					}
					if scenario == "identity-rpc-error" {
						_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "local validator unavailable"}})
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"identity": identity}})
			}))
			t.Cleanup(server.Close)
			cfg.Validator.RPCURL = server.URL
			cfg.Failover.DryRun = false
			cfg.Failover.Active = config.Role{Name: "active", Command: "/bin/true"}
			failing := []string{"-c", "printf '%s' '" + reason + "' >&2; exit 7"}
			switch scenario {
			case "pre-hook":
				cfg.Failover.Active.Hooks.Pre = []config.Hook{{Name: "fence", Command: "/bin/sh", Args: failing, MustSucceed: true}}
			case "command", "command-then-active":
				cfg.Failover.Active.Command, cfg.Failover.Active.Args = "/bin/sh", failing
			case "post-hook":
				cfg.Failover.Active.Hooks.Post = []config.Hook{{Name: "notify", Command: "/bin/sh", Args: failing}}
			}
			dir := enableRecording(cfg, t)
			manager := newTakeoverCandidate(t, cfg, "185.0.0.1")
			t.Cleanup(manager.cancel)
			manager.ensureHAState()
			want := "activation_failed"
			if scenario == "success" || scenario == "post-hook" {
				want = "became_active"
			}
			require.Equal(t, want, waitForRecordingOutcome(t, dir))
			files, err := filepath.Glob(filepath.Join(dir, "*-recording.json"))
			require.NoError(t, err)
			require.Len(t, files, 1)
			raw, err := os.ReadFile(files[0])
			require.NoError(t, err)
			var event recording.FailoverEvent
			require.NoError(t, json.Unmarshal(raw, &event))
			last := event.Timeline[len(event.Timeline)-1]
			if want == "became_active" {
				require.Empty(t, event.Outcome.Error)
				require.Equal(t, cfg.Validator.Name, event.Outcome.ToNode)
				require.Equal(t, "confirmed_active", last.Event)
				require.Equal(t, int32(3), calls.Load(), "confirmation must not be repeated")
			} else {
				require.Equal(t, "unknown", event.Outcome.ToNode)
				require.NotEmpty(t, event.Outcome.Error)
				require.Equal(t, "ensure_active_failed", last.Event)
				require.Contains(t, last.Detail, event.Outcome.Error)
				switch scenario {
				case "pre-hook", "command", "command-then-active":
					require.Contains(t, event.Outcome.Error, reason)
					require.Contains(t, event.Outcome.Error, "exit status 7")
					require.Equal(t, int32(2), calls.Load(), "failed commands must not be reclassified by another identity check")
					if scenario == "command-then-active" {
						require.True(t, manager.localState.IsSelfActive())
					}
				case "identity-mismatch":
					require.Contains(t, event.Outcome.Error, "actual="+cfg.Validator.Identities.PassivePubkey())
				case "identity-rpc-error":
					require.Contains(t, event.Outcome.Error, "local validator unavailable")
				}
			}
		})
	}
}

func TestEnsureActivePreservesExitError(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.DryRun = false
	cfg.Failover.Active = config.Role{Name: "active", Command: "/bin/sh", Args: []string{"-c", "exit 7"}}
	manager := newTakeoverCandidate(t, cfg, "185.0.0.1")
	t.Cleanup(manager.cancel)
	err := manager.ensureActive()
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 7, exitErr.ExitCode())
}
