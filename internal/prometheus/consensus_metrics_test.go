package prometheus

import (
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/sol-strategies/solana-validator-ha/internal/cache"
)

// gatherByName returns the gathered metric families keyed by name.
func gatherByName(t *testing.T, m *Metrics) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := m.GetRegistry().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		byName[family.GetName()] = family
	}
	return byName
}

// metricValue returns the gauge or counter value of the series in family whose labels include
// all of wantLabels.
func metricValue(t *testing.T, family *dto.MetricFamily, wantLabels map[string]string) float64 {
	t.Helper()
	if family == nil {
		t.Fatalf("metric family not found, want a series with labels %v", wantLabels)
	}
	for _, metric := range family.GetMetric() {
		labels := make(map[string]string)
		for _, pair := range metric.GetLabel() {
			labels[pair.GetName()] = pair.GetValue()
		}
		matches := true
		for name, value := range wantLabels {
			if labels[name] != value {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		if metric.GetCounter() != nil {
			return metric.GetCounter().GetValue()
		}
		return metric.GetGauge().GetValue()
	}
	t.Fatalf("%s has no series with labels %v", family.GetName(), wantLabels)
	return 0
}

func newConsensusTestMetrics(state cache.State) (*Metrics, *cache.Cache) {
	c := cache.New()
	state.ValidatorName = "test-validator"
	state.PublicIP = "192.168.1.100"
	c.UpdateState(state)
	return New(Options{Config: createTestConfig(), Logger: createTestLogger(), Cache: c}), c
}

func TestRefreshMetrics_ConsensusPhase(t *testing.T) {
	m, _ := newConsensusTestMetrics(cache.State{ConsensusPhase: "tower"})
	m.RefreshMetrics()
	families := gatherByName(t, m)

	for phase, want := range map[string]float64{"unknown": 0, "tower": 1, "migrating": 0, "alpenglow": 0} {
		got := metricValue(t, families[metricsNamespacePrefix+"consensus_phase"], map[string]string{"phase": phase})
		if got != want {
			t.Errorf("consensus_phase{phase=%q} = %g, want %g", phase, got, want)
		}
	}
	for _, name := range []string{"active_vote_lag_slots", "local_alpenglow_genesis_match", "cluster_finalized_slot", "cluster_live", "network_current_stake_ratio"} {
		if family, ok := families[metricsNamespacePrefix+name]; ok {
			t.Errorf("%s is exported outside the Alpenglow phase: %v", name, family)
		}
	}
}

func TestRefreshMetrics_AlpenglowSignals(t *testing.T) {
	lag := uint64(12)
	m, c := newConsensusTestMetrics(cache.State{
		ConsensusPhase:       "alpenglow",
		AlpenglowGenesisSlot: 6000,
		ActiveVoteLagSlots:   &lag,
		Alpenglow: &cache.AlpenglowState{
			LocalGenesisMatch:      true,
			FinalizedSlot:          7000,
			ClusterLive:            true,
			NetworkStakeRatio:      0.95,
			NetworkStakeRatioKnown: true,
		},
	})
	m.RefreshMetrics()
	families := gatherByName(t, m)

	for name, want := range map[string]float64{
		"alpenglow_genesis_slot":        6000,
		"active_vote_lag_slots":         12,
		"local_alpenglow_genesis_match": 1,
		"cluster_finalized_slot":        7000,
		"cluster_live":                  1,
		"network_current_stake_ratio":   0.95,
	} {
		if got := metricValue(t, families[metricsNamespacePrefix+name], nil); got != want {
			t.Errorf("%s = %g, want %g", name, got, want)
		}
	}

	// the series disappear once the values are no longer tracked
	state := c.GetState()
	state.ActiveVoteLagSlots = nil
	state.Alpenglow = nil
	c.UpdateState(state)
	m.RefreshMetrics()
	families = gatherByName(t, m)
	for _, name := range []string{"active_vote_lag_slots", "cluster_live"} {
		if _, ok := families[metricsNamespacePrefix+name]; ok {
			t.Errorf("%s is still exported after its value went away", name)
		}
	}
}

func TestIncAlpenglowVerdict(t *testing.T) {
	m, _ := newConsensusTestMetrics(cache.State{})
	m.IncAlpenglowVerdict("voting")
	m.IncAlpenglowVerdict("voting")
	m.IncAlpenglowVerdict("not_voting")
	families := gatherByName(t, m)

	for verdict, want := range map[string]float64{"voting": 2, "not_voting": 1} {
		got := metricValue(t, families[metricsNamespacePrefix+"alpenglow_vote_verdicts_total"], map[string]string{"verdict": verdict, "validator_name": "test-validator"})
		if got != want {
			t.Errorf("alpenglow_vote_verdicts_total{verdict=%q} = %g, want %g", verdict, got, want)
		}
	}
}

func TestIncFailoverVeto(t *testing.T) {
	m, _ := newConsensusTestMetrics(cache.State{})
	m.IncFailoverVeto("cluster_stalled")
	m.IncFailoverVeto("cluster_stalled")
	m.IncFailoverVeto("local_not_migrated")
	families := gatherByName(t, m)

	for reason, want := range map[string]float64{"cluster_stalled": 2, "local_not_migrated": 1} {
		got := metricValue(t, families[metricsNamespacePrefix+"failover_vetoes_total"], map[string]string{"reason": reason, "validator_name": "test-validator"})
		if got != want {
			t.Errorf("failover_vetoes_total{reason=%q} = %g, want %g", reason, got, want)
		}
	}
}
