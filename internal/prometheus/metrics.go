package prometheus

import (
	"fmt"
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sol-strategies/solana-validator-ha/internal/cache"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/consensus"
)

const (
	metricsNamespacePrefix   = "solana_validator_ha_"
	validatorNameLabelName   = "validator_name"
	publicIPLabelName        = "public_ip"
	validatorRoleLabelName   = "validator_role"
	validatorStatusLabelName = "validator_status"
	failoverStatusLabelName  = "status"
	peerCountLabelName       = "peer_count"
	selfInGossipLabelName    = "self_in_gossip"
	consensusPhaseLabelName  = "phase"
	verdictLabelName         = "verdict"
)

var (
	commonLabelNames = []string{
		validatorNameLabelName,
		publicIPLabelName,
	}
)

// Metrics manages Prometheus metrics for the HA manager
type Metrics struct {
	config           *config.Config
	logger           *log.Logger
	cache            *cache.Cache
	server           *http.Server
	registry         *prometheus.Registry
	commonLabelNames []string

	// Metrics
	metadata               *prometheus.GaugeVec
	peerCount              *prometheus.GaugeVec
	selfInGossip           *prometheus.GaugeVec
	failoverStatus         *prometheus.GaugeVec
	updateAvailable        *prometheus.GaugeVec
	recordingWriteFailures prometheus.Counter

	// Consensus metrics
	consensusPhase           *prometheus.GaugeVec
	alpenglowGenesisSlot     *prometheus.GaugeVec
	localAlpenglowGenesis    *prometheus.GaugeVec
	activeVoteLagSlots       *prometheus.GaugeVec
	clusterFinalizedSlot     *prometheus.GaugeVec
	clusterLive              *prometheus.GaugeVec
	networkCurrentStakeRatio *prometheus.GaugeVec
	alpenglowVerdicts        *prometheus.CounterVec
}

// Options for creating a new Metrics instance
type Options struct {
	Config *config.Config
	Logger *log.Logger
	Cache  *cache.Cache
}

// New creates a new Metrics instance
func New(opts Options) *Metrics {
	m := &Metrics{
		config:   opts.Config,
		logger:   opts.Logger,
		cache:    opts.Cache,
		registry: prometheus.NewRegistry(),
		commonLabelNames: []string{
			validatorNameLabelName,
			publicIPLabelName,
		},
	}

	// Add static labels names from config
	for labelName := range m.config.Prometheus.StaticLabels {
		m.commonLabelNames = append(m.commonLabelNames, labelName)
	}

	m.initMetrics()
	return m
}

// initMetrics initializes all Prometheus metrics
func (m *Metrics) initMetrics() {
	// Metadata metric - always 1 with metadata labels
	metadataLabelNames := []string{
		validatorRoleLabelName,
		validatorStatusLabelName,
	}
	metadataLabelNames = append(metadataLabelNames, m.commonLabelNames...)
	m.metadata = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "metadata",
			Help: "Metadata about the validator HA manager, always 1 with metadata labels",
		},
		metadataLabelNames,
	)

	// Peer count metric
	m.peerCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "peer_count",
			Help: "Number of peers seen in gossip this node is aware of, excluding self",
		},
		m.commonLabelNames,
	)

	// Self in gossip metric
	m.selfInGossip = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "self_in_gossip",
			Help: "Whether this node sees itself in gossip (1 = yes, 0 = no)",
		},
		m.commonLabelNames,
	)

	// Failover status metric
	failoverLabelNames := []string{
		failoverStatusLabelName,
	}
	failoverLabelNames = append(failoverLabelNames, m.commonLabelNames...)
	m.failoverStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "failover_status",
			Help: "Current failover status of the node",
		},
		failoverLabelNames,
	)

	// Update available metric
	m.updateAvailable = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "update_available",
			Help: "Whether a newer version of solana-validator-ha is available (1 = yes, 0 = no)",
		},
		m.commonLabelNames,
	)
	m.recordingWriteFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: metricsNamespacePrefix + "recording_write_failures_total",
		Help: "Total failover recording checkpoint or finalization write failures",
	})

	m.initConsensusMetrics()

	// Register all metrics
	m.registry.MustRegister(m.metadata)
	m.registry.MustRegister(m.peerCount)
	m.registry.MustRegister(m.selfInGossip)
	m.registry.MustRegister(m.failoverStatus)
	m.registry.MustRegister(m.updateAvailable)
	m.registry.MustRegister(m.recordingWriteFailures)

	m.registry.MustRegister(m.consensusPhase)
	m.registry.MustRegister(m.alpenglowGenesisSlot)
	m.registry.MustRegister(m.localAlpenglowGenesis)
	m.registry.MustRegister(m.activeVoteLagSlots)
	m.registry.MustRegister(m.clusterFinalizedSlot)
	m.registry.MustRegister(m.clusterLive)
	m.registry.MustRegister(m.networkCurrentStakeRatio)
	m.registry.MustRegister(m.alpenglowVerdicts)

	m.logger.Debug("initialized Prometheus metrics")
}

// initConsensusMetrics creates the metrics that describe the consensus phase and the Alpenglow
// signals failover decisions depend on.
func (m *Metrics) initConsensusMetrics() {
	newGauge := func(name, help string, extraLabelNames ...string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: metricsNamespacePrefix + name, Help: help},
			append(extraLabelNames, m.commonLabelNames...),
		)
	}
	m.consensusPhase = newGauge("consensus_phase", "Consensus phase of the cluster as seen by this node, 1 for the current phase", consensusPhaseLabelName)
	m.alpenglowGenesisSlot = newGauge("alpenglow_genesis_slot", "Alpenglow genesis slot of the cluster, 0 if unknown")
	m.localAlpenglowGenesis = newGauge("local_alpenglow_genesis_match", "Whether the local validator reports the cluster's Alpenglow genesis (1 = yes, 0 = no); Alpenglow phase only")
	m.activeVoteLagSlots = newGauge("active_vote_lag_slots", "Slots the active peer's last vote trailed the reference slot in the last sample, when measured")
	m.clusterFinalizedSlot = newGauge("cluster_finalized_slot", "Highest finalized slot seen on the cluster RPCs; Alpenglow phase only")
	m.clusterLive = newGauge("cluster_live", "Whether the cluster's finalized slot is advancing (1 = yes, 0 = stalled); Alpenglow phase only")
	m.networkCurrentStakeRatio = newGauge("network_current_stake_ratio", "Share of activated stake held by current vote accounts; Alpenglow phase only")
	m.alpenglowVerdicts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: metricsNamespacePrefix + "alpenglow_vote_verdicts_total",
			Help: "Verdicts of the Alpenglow vote rule on the active peer, one per sample in the Alpenglow phase (observe-only: not acted on)",
		},
		append([]string{verdictLabelName}, m.commonLabelNames...),
	)
}

// IncAlpenglowVerdict counts one verdict of the Alpenglow vote rule on the active peer.
func (m *Metrics) IncAlpenglowVerdict(verdict string) {
	state := m.cache.GetState()
	m.alpenglowVerdicts.With(m.mergeLabels(prometheus.Labels{verdictLabelName: verdict}, m.getCommonLabels(&state))).Inc()
}

// IncRecordingWriteFailure records a failed recording checkpoint or finalization.
func (m *Metrics) IncRecordingWriteFailure() {
	m.recordingWriteFailures.Inc()
}

// StartServer starts the Prometheus metrics HTTP server
func (m *Metrics) StartServer(port int) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))

	m.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	m.logger.Debug("starting Prometheus metrics server", "port", port)

	err := m.server.ListenAndServe()
	if err != nil {
		m.logger.Error("Prometheus metrics server failed", "error", err)
	}
	return err
}

// StopServer stops the Prometheus metrics HTTP server
func (m *Metrics) StopServer() error {
	if m.server != nil {
		return m.server.Close()
	}
	return nil
}

// GetRegistry returns the Prometheus registry for testing
func (m *Metrics) GetRegistry() *prometheus.Registry {
	return m.registry
}

// RefreshMetrics updates all metrics based on current cache state
func (m *Metrics) RefreshMetrics() {
	m.logger.Debug("refreshing metrics from cache")
	state := m.cache.GetState()

	m.exportMetricMetadata(&state)
	m.exportMetricPeerCount(&state)
	m.exportMetricSelfInGossip(&state)
	m.exportMetricFailoverStatus(&state)
	m.exportMetricUpdateAvailable(&state)
	m.exportConsensusMetrics(&state)

	m.logger.Debug("metrics refreshed",
		validatorRoleLabelName, state.Role,
		validatorStatusLabelName, state.Status,
		peerCountLabelName, state.PeerCount,
		selfInGossipLabelName, state.SelfInGossip,
		failoverStatusLabelName, state.FailoverStatus,
		"update_available", state.UpdateAvailable,
	)
}

func (m *Metrics) exportMetricMetadata(state *cache.State) {
	// Reset the metadata metric to remove old role/status combinations
	m.metadata.Reset()

	// Set the new metadata metric
	m.metadata.
		With(
			m.mergeLabels(
				prometheus.Labels{
					validatorRoleLabelName:   state.Role,
					validatorStatusLabelName: state.Status,
				},
				m.getCommonLabels(state),
			),
		).
		Set(1)
}

func (m *Metrics) exportMetricPeerCount(state *cache.State) {
	m.peerCount.
		With(m.getCommonLabels(state)).
		Set(float64(state.PeerCount))
}

func (m *Metrics) exportMetricSelfInGossip(state *cache.State) {
	var selfInGossipValue float64
	if state.SelfInGossip {
		selfInGossipValue = 1
	}
	m.selfInGossip.
		With(m.getCommonLabels(state)).
		Set(selfInGossipValue)
}

func (m *Metrics) exportMetricFailoverStatus(state *cache.State) {
	m.failoverStatus.
		With(
			m.mergeLabels(
				prometheus.Labels{
					failoverStatusLabelName: state.FailoverStatus,
				},
				m.getCommonLabels(state),
			),
		).
		Set(1)
}

func (m *Metrics) exportMetricUpdateAvailable(state *cache.State) {
	var value float64
	if state.UpdateAvailable {
		value = 1
	}
	m.updateAvailable.
		With(m.getCommonLabels(state)).
		Set(value)
}

func (m *Metrics) exportConsensusMetrics(state *cache.State) {
	commonLabels := m.getCommonLabels(state)
	for _, phase := range consensus.Phases {
		var value float64
		if phase.String() == state.ConsensusPhase {
			value = 1
		}
		m.consensusPhase.With(m.mergeLabels(prometheus.Labels{consensusPhaseLabelName: phase.String()}, commonLabels)).Set(value)
	}
	m.alpenglowGenesisSlot.With(commonLabels).Set(float64(state.AlpenglowGenesisSlot))

	// Series without a current value are removed rather than left at a stale one.
	m.activeVoteLagSlots.Reset()
	if state.ActiveVoteLagSlots != nil {
		m.activeVoteLagSlots.With(commonLabels).Set(float64(*state.ActiveVoteLagSlots))
	}

	m.localAlpenglowGenesis.Reset()
	m.clusterFinalizedSlot.Reset()
	m.clusterLive.Reset()
	m.networkCurrentStakeRatio.Reset()
	alpenglow := state.Alpenglow
	if alpenglow == nil {
		return
	}
	m.localAlpenglowGenesis.With(commonLabels).Set(boolToFloat(alpenglow.LocalGenesisMatch))
	m.clusterFinalizedSlot.With(commonLabels).Set(float64(alpenglow.FinalizedSlot))
	m.clusterLive.With(commonLabels).Set(boolToFloat(alpenglow.ClusterLive))
	if alpenglow.NetworkStakeRatioKnown {
		m.networkCurrentStakeRatio.With(commonLabels).Set(alpenglow.NetworkStakeRatio)
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// mergeLabels merges fromLabels into toLabels
func (m *Metrics) mergeLabels(toLabels prometheus.Labels, fromLabels prometheus.Labels) prometheus.Labels {
	for labelName, labelValue := range fromLabels {
		toLabels[labelName] = labelValue
	}
	return toLabels
}

func (m *Metrics) getCommonLabels(state *cache.State) prometheus.Labels {
	commonLabels := prometheus.Labels{
		publicIPLabelName:      state.PublicIP,
		validatorNameLabelName: state.ValidatorName,
	}
	for k, v := range m.config.Prometheus.StaticLabels {
		commonLabels[k] = v
	}
	return commonLabels
}
