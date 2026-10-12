package ha

import (
	"context"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

func (s *localValidatorStub) setSlot(slot uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slot = slot
}

func TestIsolationMonitor(t *testing.T) {
	const (
		threshold  = 3
		stallAfter = 15 * time.Second
		pollEvery  = 5 * time.Second
	)
	tests := []struct {
		name string
		// polls lists, per poll, whether the cluster RPC failed and whether the local slot moved.
		polls        []struct{ clusterRPCFailed, localSlotMoved bool }
		wantIsolated bool
	}{
		{
			name:         "cluster RPC down but local slot moving: provider outage",
			polls:        []struct{ clusterRPCFailed, localSlotMoved bool }{{true, true}, {true, true}, {true, true}, {true, true}, {true, true}},
			wantIsolated: false,
		},
		{
			name:         "cluster RPC down and local slot stalled: isolated",
			polls:        []struct{ clusterRPCFailed, localSlotMoved bool }{{false, true}, {true, false}, {true, false}, {true, false}, {true, false}},
			wantIsolated: true,
		},
		{
			name:         "local slot stalled but cluster RPC answering",
			polls:        []struct{ clusterRPCFailed, localSlotMoved bool }{{false, true}, {false, false}, {false, false}, {false, false}, {false, false}},
			wantIsolated: false,
		},
		{
			name:         "stalled but fewer failures than the threshold",
			polls:        []struct{ clusterRPCFailed, localSlotMoved bool }{{false, true}, {false, false}, {false, false}, {true, false}, {true, false}},
			wantIsolated: false,
		},
		{
			name:         "cluster RPC recovered on the last poll",
			polls:        []struct{ clusterRPCFailed, localSlotMoved bool }{{false, true}, {true, false}, {true, false}, {true, false}, {false, false}},
			wantIsolated: false,
		},
		{
			name:         "failures reached but stalled for less than the stall duration",
			polls:        []struct{ clusterRPCFailed, localSlotMoved bool }{{true, true}, {true, true}, {true, false}, {true, false}},
			wantIsolated: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := newLocalValidatorStub(t, 1000, "")
			monitor := newIsolationMonitor(rpc.NewClient("test", local.url), threshold, stallAfter)
			clock := time.Date(2026, 9, 28, 11, 31, 0, 0, time.UTC)
			monitor.now = func() time.Time { return clock }
			slot := uint64(1000)

			for _, poll := range tt.polls {
				if poll.localSlotMoved {
					slot += 12
					local.setSlot(slot)
				}
				monitor.observe(context.Background(), poll.clusterRPCFailed)
				clock = clock.Add(pollEvery)
			}
			clock = clock.Add(-pollEvery) // judge at the time of the last poll

			if got := monitor.isolated(); got != tt.wantIsolated {
				t.Errorf("isolated() = %t, want %t (cluster RPC failures %d, local slot stalled for %s)",
					got, tt.wantIsolated, monitor.clusterRPCFailures, monitor.localSlotStalledFor())
			}
		})
	}
}

func TestIsolationMonitor_UnreadableLocalSlotIsNeverIsolated(t *testing.T) {
	monitor := newIsolationMonitor(rpc.NewClient("test", unreachableRPCURL(t)), 1, time.Second)
	clock := time.Date(2026, 9, 28, 11, 31, 0, 0, time.UTC)
	monitor.now = func() time.Time { return clock }

	for range 5 {
		monitor.observe(context.Background(), true)
		clock = clock.Add(time.Minute)
	}

	if monitor.isolated() {
		t.Error("isolated() = true with the local RPC unreachable, want false")
	}
}

// newIsolatedActiveCandidate returns a manager whose node is active, sees the cluster RPC fail
// and whose isolation monitor has already seen the local slot stand still for an hour.
func newIsolatedActiveCandidate(t *testing.T, local *localValidatorStub) *Manager {
	t.Helper()
	cfg := createTestConfig()
	cfg.Validator.RPCURL = local.url
	cfg.Failover.Isolation = config.Isolation{Enabled: true, LocalSlotStallDuration: 15 * time.Second}
	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	if err := manager.initialize(); err != nil {
		t.Fatalf("initialize() error = %v", err)
	}
	local.mu.Lock()
	local.identity = cfg.Validator.Identities.ActivePubkey()
	local.mu.Unlock()

	manager.gossipState.SetRefreshNoOpForTest(true)
	manager.gossipState.SetLastRefreshHadRPCErrorForTest(true)
	manager.isolation.clusterRPCFailures = cfg.Failover.LeaderlessSamplesThreshold - 1 // this poll reaches it
	local.mu.Lock()
	manager.isolation.localSlot = local.slot
	local.mu.Unlock()
	manager.isolation.localSlotChangedAt = time.Now().Add(-time.Hour)
	return manager
}

func TestEnsureHAState_IsolatedActiveDemotesItself(t *testing.T) {
	local := newLocalValidatorStub(t, 1000, "")
	manager := newIsolatedActiveCandidate(t, local)

	manager.ensureHAState()

	if got := manager.cache.GetState().FailoverStatus; got != "becoming_passive" {
		t.Errorf("FailoverStatus = %q, want becoming_passive", got)
	}
}

func TestEnsureHAState_ActiveWithMovingSlotSurvivesClusterRPCOutage(t *testing.T) {
	local := newLocalValidatorStub(t, 1000, "")
	manager := newIsolatedActiveCandidate(t, local)
	local.setSlot(1100) // the node still receives blocks: only the RPC provider is down

	manager.ensureHAState()

	if got := manager.cache.GetState().FailoverStatus; got != "idle" {
		t.Errorf("FailoverStatus = %q, want idle", got)
	}
}

func TestEnsureHAState_IsolationDisabled(t *testing.T) {
	local := newLocalValidatorStub(t, 1000, "")
	manager := newIsolatedActiveCandidate(t, local)
	manager.cfg.Failover.Isolation.Enabled = false

	manager.ensureHAState()

	if got := manager.cache.GetState().FailoverStatus; got != "idle" {
		t.Errorf("FailoverStatus = %q, want idle", got)
	}
}
