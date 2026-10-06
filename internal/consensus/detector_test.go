package consensus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

// stubRPC is a JSON-RPC server whose answers can change during a test. Methods without an
// answer get a -32601 "method not found" error, like an RPC node that does not implement them.
type stubRPC struct {
	mu      sync.Mutex
	results map[string]any
	calls   map[string]int
	server  *httptest.Server
}

func newStubRPC(t *testing.T, results map[string]any) *stubRPC {
	t.Helper()
	s := &stubRPC{results: results, calls: map[string]int{}}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.calls[req.Method]++
		result, ok := s.results[req.Method]
		s.mu.Unlock()

		response := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
		if !ok {
			response = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response) //nolint:errcheck
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubRPC) set(method string, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[method] = result
}

func (s *stubRPC) unset(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.results, method)
}

func (s *stubRPC) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, n := range s.calls {
		total += n
	}
	return total
}

func (s *stubRPC) callCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func (s *stubRPC) client() *rpc.Client {
	return rpc.NewClient("test", s.server.URL)
}

func genesisCert(slot uint64) map[string]any {
	return map[string]any{"block": map[string]any{"slot": slot}}
}

func featureAccount(activatedAt uint64) map[string]any {
	data := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := range 8 {
		data[1+i] = byte(activatedAt >> (8 * i))
	}
	return map[string]any{
		"context": map[string]any{"slot": 1},
		"value": map[string]any{
			"data":       []string{base64.StdEncoding.EncodeToString(data), "base64"},
			"executable": false,
			"lamports":   1,
			"owner":      "Feature111111111111111111111111111111111111",
			"rentEpoch":  0,
		},
	}
}

var missingAccount = map[string]any{"context": map[string]any{"slot": 1}, "value": nil}

const testDetectionInterval = time.Minute

// fakeClock is a settable time source for Detector.now.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestDetector(mode config.ConsensusMode, cluster, local *stubRPC, clock *fakeClock) *Detector {
	d := NewDetector(Options{
		Mode:              mode,
		DetectionInterval: testDetectionInterval,
		ClusterRPC:        cluster.client(),
		LocalRPC:          local.client(),
		LogPrefix:         "test",
	})
	d.now = clock.now
	return d
}

func TestDetector_AutoModeDetectsPhase(t *testing.T) {
	tests := []struct {
		name            string
		cluster         map[string]any
		local           map[string]any
		wantPhase       Phase
		wantGenesisSlot uint64
	}{
		{
			name:      "tower when the feature is not activated",
			cluster:   map[string]any{"getAgGenesisCert": nil, "getAccountInfo": missingAccount},
			wantPhase: PhaseTower,
		},
		{
			name:      "tower when no RPC implements getAgGenesisCert",
			cluster:   map[string]any{"getAccountInfo": missingAccount},
			wantPhase: PhaseTower,
		},
		{
			name:      "migrating when the feature is activated",
			cluster:   map[string]any{"getAgGenesisCert": nil, "getAccountInfo": featureAccount(1000)},
			wantPhase: PhaseMigrating,
		},
		{
			name:            "alpenglow when the genesis certificate exists",
			cluster:         map[string]any{"getAgGenesisCert": genesisCert(6000)},
			wantPhase:       PhaseAlpenglow,
			wantGenesisSlot: 6000,
		},
		{
			name:            "alpenglow from the local validator when no cluster RPC implements getAgGenesisCert",
			cluster:         map[string]any{"getAccountInfo": featureAccount(1000)},
			local:           map[string]any{"getAgGenesisCert": genesisCert(6000)},
			wantPhase:       PhaseAlpenglow,
			wantGenesisSlot: 6000,
		},
		{
			name:      "unknown when nothing answers",
			cluster:   map[string]any{},
			wantPhase: PhaseUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := tt.local
			if local == nil {
				local = map[string]any{}
			}
			d := newTestDetector(config.ConsensusModeAuto, newStubRPC(t, tt.cluster), newStubRPC(t, local), newFakeClock())

			d.Refresh(context.Background())

			if got := d.View(); got.Phase != tt.wantPhase || got.GenesisSlot != tt.wantGenesisSlot {
				t.Errorf("View() = %+v, want phase %s genesis slot %d", got, tt.wantPhase, tt.wantGenesisSlot)
			}
		})
	}
}

func TestDetector_AlpenglowIsSticky(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": genesisCert(6000)})
	clock := newFakeClock()
	d := newTestDetector(config.ConsensusModeAuto, cluster, newStubRPC(t, map[string]any{}), clock)
	d.Refresh(context.Background())

	// a lagging RPC that answers null, with the feature apparently gone
	cluster.set("getAgGenesisCert", nil)
	cluster.set("getAccountInfo", missingAccount)
	clock.advance(alpenglowReverifyInterval)
	d.Refresh(context.Background())
	if got := d.View(); got.Phase != PhaseAlpenglow || got.GenesisSlot != 6000 {
		t.Fatalf("after a null certificate, View() = %+v, want alpenglow with genesis slot 6000", got)
	}

	// a conflicting genesis slot is ignored, the first one wins
	cluster.set("getAgGenesisCert", genesisCert(7000))
	clock.advance(alpenglowReverifyInterval)
	d.Refresh(context.Background())
	if got := d.View(); got.Phase != PhaseAlpenglow || got.GenesisSlot != 6000 {
		t.Errorf("after a conflicting certificate, View() = %+v, want alpenglow with genesis slot 6000", got)
	}
}

func TestDetector_MovesFromTowerToMigratingToAlpenglow(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": nil, "getAccountInfo": missingAccount})
	clock := newFakeClock()
	d := newTestDetector(config.ConsensusModeAuto, cluster, newStubRPC(t, map[string]any{}), clock)

	steps := []struct {
		change    func()
		wantPhase Phase
	}{
		{change: func() {}, wantPhase: PhaseTower},
		{change: func() { cluster.set("getAccountInfo", featureAccount(1000)) }, wantPhase: PhaseMigrating},
		{change: func() { cluster.set("getAgGenesisCert", genesisCert(6000)) }, wantPhase: PhaseAlpenglow},
	}
	for i, step := range steps {
		step.change()
		d.Refresh(context.Background())
		if got := d.Phase(); got != step.wantPhase {
			t.Fatalf("step %d: Phase() = %s, want %s", i, got, step.wantPhase)
		}
		clock.advance(testDetectionInterval)
	}
}

func TestDetector_KeepsPhaseWhenFeatureQueryFails(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": nil, "getAccountInfo": missingAccount})
	clock := newFakeClock()
	d := newTestDetector(config.ConsensusModeAuto, cluster, newStubRPC(t, map[string]any{}), clock)
	d.Refresh(context.Background())

	cluster.unset("getAccountInfo")
	clock.advance(testDetectionInterval)
	d.Refresh(context.Background())

	if got := d.Phase(); got != PhaseTower {
		t.Errorf("Phase() after a failed feature query = %s, want tower", got)
	}
}

func TestDetector_QueriesClusterOnlyWhenDue(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": nil, "getAccountInfo": missingAccount})
	clock := newFakeClock()
	d := newTestDetector(config.ConsensusModeAuto, cluster, newStubRPC(t, map[string]any{}), clock)

	d.Refresh(context.Background())
	clock.advance(testDetectionInterval - time.Second)
	d.Refresh(context.Background())
	if got := cluster.callCount("getAccountInfo"); got != 1 {
		t.Fatalf("getAccountInfo calls before the interval elapsed = %d, want 1", got)
	}

	clock.advance(time.Second)
	d.Refresh(context.Background())
	if got := cluster.callCount("getAccountInfo"); got != 2 {
		t.Errorf("getAccountInfo calls after the interval elapsed = %d, want 2", got)
	}
}

func TestDetector_RefreshNow(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": nil, "getAccountInfo": missingAccount})
	local := newStubRPC(t, map[string]any{"getAgGenesisCert": nil})
	d := newTestDetector(config.ConsensusModeAuto, cluster, local, newFakeClock())
	d.Refresh(context.Background())

	// the cluster migrates within the detection interval
	cluster.set("getAgGenesisCert", genesisCert(6000))
	d.Refresh(context.Background())
	if got := d.Phase(); got != PhaseTower {
		t.Fatalf("Phase() after Refresh within the interval = %s, want tower", got)
	}
	d.RefreshNow(context.Background())
	if got := d.Phase(); got != PhaseAlpenglow {
		t.Fatalf("Phase() after RefreshNow = %s, want alpenglow", got)
	}
	if eligible, reason := d.LocalEligibility(); eligible || reason != ReasonLocalNotMigrated {
		t.Errorf("LocalEligibility() after RefreshNow = %t, %q; want false, %q", eligible, reason, ReasonLocalNotMigrated)
	}

	// once the phase is final, the cluster is not asked again
	calls := cluster.callCount("getAgGenesisCert")
	d.RefreshNow(context.Background())
	if got := cluster.callCount("getAgGenesisCert"); got != calls {
		t.Errorf("getAgGenesisCert calls after RefreshNow in the final phase = %d, want %d", got, calls)
	}
}

func TestDetector_PinnedTowerMakesNoCalls(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": genesisCert(6000)})
	local := newStubRPC(t, map[string]any{"getAgGenesisCert": genesisCert(6000)})
	d := newTestDetector(config.ConsensusModeTower, cluster, local, newFakeClock())

	d.Refresh(context.Background())

	if got := d.Phase(); got != PhaseTower {
		t.Errorf("Phase() = %s, want tower", got)
	}
	if calls := cluster.totalCalls() + local.totalCalls(); calls != 0 {
		t.Errorf("RPC calls = %d, want 0", calls)
	}
}

func TestDetector_PinnedAlpenglow(t *testing.T) {
	t.Run("phase is set before any refresh", func(t *testing.T) {
		d := newTestDetector(config.ConsensusModeAlpenglow, newStubRPC(t, map[string]any{}), newStubRPC(t, map[string]any{}), newFakeClock())
		if got := d.Phase(); got != PhaseAlpenglow {
			t.Errorf("Phase() = %s, want alpenglow", got)
		}
	})

	t.Run("genesis slot is fetched", func(t *testing.T) {
		cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": genesisCert(6000)})
		d := newTestDetector(config.ConsensusModeAlpenglow, cluster, newStubRPC(t, map[string]any{}), newFakeClock())
		d.Refresh(context.Background())
		if got := d.View(); got.GenesisSlot != 6000 {
			t.Errorf("View().GenesisSlot = %d, want 6000", got.GenesisSlot)
		}
	})

	t.Run("genesis slot is retried at the detection interval until found", func(t *testing.T) {
		cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": nil})
		clock := newFakeClock()
		d := newTestDetector(config.ConsensusModeAlpenglow, cluster, newStubRPC(t, map[string]any{}), clock)
		d.Refresh(context.Background())

		cluster.set("getAgGenesisCert", genesisCert(6000))
		clock.advance(testDetectionInterval)
		d.Refresh(context.Background())

		if got := d.View(); got.Phase != PhaseAlpenglow || got.GenesisSlot != 6000 {
			t.Errorf("View() = %+v, want alpenglow with genesis slot 6000", got)
		}
	})
}

func TestDetector_LocalEligibility(t *testing.T) {
	tests := []struct {
		name         string
		mode         config.ConsensusMode
		cluster      map[string]any
		local        map[string]any
		wantEligible bool
		wantReason   string
	}{
		{
			name:         "always eligible under tower",
			mode:         config.ConsensusModeAuto,
			cluster:      map[string]any{"getAgGenesisCert": nil, "getAccountInfo": missingAccount},
			local:        map[string]any{},
			wantEligible: true,
		},
		{
			name:       "local validator without a certificate",
			mode:       config.ConsensusModeAuto,
			cluster:    map[string]any{"getAgGenesisCert": genesisCert(6000)},
			local:      map[string]any{"getAgGenesisCert": nil},
			wantReason: ReasonLocalNotMigrated,
		},
		{
			name:       "local validator that cannot answer",
			mode:       config.ConsensusModeAuto,
			cluster:    map[string]any{"getAgGenesisCert": genesisCert(6000)},
			local:      map[string]any{},
			wantReason: ReasonLocalNotMigrated,
		},
		{
			name:       "local validator on another genesis",
			mode:       config.ConsensusModeAuto,
			cluster:    map[string]any{"getAgGenesisCert": genesisCert(6000)},
			local:      map[string]any{"getAgGenesisCert": genesisCert(5000)},
			wantReason: ReasonLocalGenesisMismatch,
		},
		{
			name:         "local validator on the cluster's genesis",
			mode:         config.ConsensusModeAuto,
			cluster:      map[string]any{"getAgGenesisCert": genesisCert(6000)},
			local:        map[string]any{"getAgGenesisCert": genesisCert(6000)},
			wantEligible: true,
		},
		{
			name:         "pinned alpenglow with the cluster genesis unknown only needs a local certificate",
			mode:         config.ConsensusModeAlpenglow,
			cluster:      map[string]any{},
			local:        map[string]any{"getAgGenesisCert": genesisCert(5000)},
			wantEligible: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDetector(tt.mode, newStubRPC(t, tt.cluster), newStubRPC(t, tt.local), newFakeClock())
			d.Refresh(context.Background())

			eligible, reason := d.LocalEligibility()
			if eligible != tt.wantEligible || reason != tt.wantReason {
				t.Errorf("LocalEligibility() = %t, %q; want %t, %q", eligible, reason, tt.wantEligible, tt.wantReason)
			}
		})
	}
}

func TestDetector_RechecksLocalGenesisUntilEligible(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": genesisCert(6000)})
	local := newStubRPC(t, map[string]any{"getAgGenesisCert": nil})
	d := newTestDetector(config.ConsensusModeAuto, cluster, local, newFakeClock())
	d.Refresh(context.Background())
	if eligible, _ := d.LocalEligibility(); eligible {
		t.Fatal("LocalEligibility() = eligible before the local validator migrated")
	}

	// no time passes: an ineligible node is re-checked on every refresh
	local.set("getAgGenesisCert", genesisCert(6000))
	d.Refresh(context.Background())

	if eligible, reason := d.LocalEligibility(); !eligible {
		t.Errorf("LocalEligibility() = ineligible (%s) after the local validator migrated", reason)
	}
}
