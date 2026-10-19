package consensus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	"github.com/stretchr/testify/require"
)

// outageClusterRPC models two providers that either refuse connections or never answer.
func outageClusterRPC(t *testing.T, mode string) *rpc.Client {
	t.Helper()
	release := make(chan struct{})
	var servers []*httptest.Server
	var urls []string
	for range 2 {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		servers = append(servers, server)
		urls = append(urls, server.URL)
	}
	t.Cleanup(func() {
		close(release)
		for _, server := range servers {
			server.Close()
		}
	})
	if mode == "refused" {
		for _, server := range servers {
			server.Close()
		}
	}
	return rpc.NewClient("test", urls...).WithTimeout(40 * time.Millisecond)
}

func TestDetector_LocalGenesisDuringClusterRPCOutage(t *testing.T) {
	for _, mode := range []string{"refused", "timeout"} {
		for _, tt := range []struct {
			name             string
			local            map[string]any
			localUnavailable bool
			initialPhase     Phase
			wantPhase        Phase
			wantGenesis      uint64
		}{
			{name: "valid local certificate", local: map[string]any{"getAgGenesisCert": genesisCert(6000)}, wantPhase: PhaseAlpenglow, wantGenesis: 6000},
			{name: "no local certificate", local: map[string]any{"getAgGenesisCert": nil}, wantPhase: PhaseUnknown},
			{name: "local RPC error", local: map[string]any{}, wantPhase: PhaseUnknown},
			{name: "both sources unreachable", localUnavailable: true, wantPhase: PhaseUnknown},
			{name: "invalid local certificate", local: map[string]any{"getAgGenesisCert": genesisCert(0)}, wantPhase: PhaseUnknown},
			{name: "keep tower without a certificate", local: map[string]any{"getAgGenesisCert": nil}, initialPhase: PhaseTower, wantPhase: PhaseTower},
			{name: "keep migrating on local RPC error", local: map[string]any{}, initialPhase: PhaseMigrating, wantPhase: PhaseMigrating},
		} {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				local := newStubRPC(t, tt.local)
				d := NewDetector(Options{
					Mode:              config.ConsensusModeAuto,
					DetectionInterval: testDetectionInterval,
					ClusterRPC:        outageClusterRPC(t, mode),
					LocalRPC:          local.client(),
				})
				d.phase = tt.initialPhase
				if tt.localUnavailable {
					local.server.Close()
				}

				d.Refresh(context.Background())

				require.Equal(t, tt.wantPhase, d.Phase())
				require.Equal(t, tt.wantGenesis, d.View().GenesisSlot)
				if !tt.localUnavailable {
					require.Positive(t, local.callCount("getAgGenesisCert"), "the local validator must be consulted during the outage")
				}
				if tt.wantPhase == PhaseAlpenglow {
					eligible, reason := d.LocalEligibility()
					require.True(t, eligible, "matching local genesis should be eligible: %s", reason)
				}
			})
		}
	}
}

func TestDetector_ClusterWithoutGenesisDoesNotUseLocalCertificate(t *testing.T) {
	cluster := newStubRPC(t, map[string]any{"getAgGenesisCert": nil, "getAccountInfo": missingAccount})
	local := newStubRPC(t, map[string]any{"getAgGenesisCert": genesisCert(6000)})
	d := newTestDetector(config.ConsensusModeAuto, cluster, local, newFakeClock())

	d.Refresh(context.Background())

	require.Equal(t, PhaseTower, d.Phase(), "a successful pre-migration cluster response must keep its meaning")
	require.Zero(t, d.View().GenesisSlot)
	require.Zero(t, local.totalCalls())
}
