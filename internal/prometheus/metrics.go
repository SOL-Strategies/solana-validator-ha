package prometheus

import (
	"fmt"
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sol-strategies/solana-validator-ha/internal/cache"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
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
	metadata       *prometheus.GaugeVec
	peerCount      *prometheus.GaugeVec
	selfInGossip   *prometheus.GaugeVec
	failoverStatus *prometheus.GaugeVec
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

	// Register all metrics
	m.registry.MustRegister(m.metadata)
	m.registry.MustRegister(m.peerCount)
	m.registry.MustRegister(m.selfInGossip)
	m.registry.MustRegister(m.failoverStatus)

	m.logger.Debug("initialized Prometheus metrics")
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

	m.logger.Debug("metrics refreshed",
		validatorRoleLabelName, state.Role,
		validatorStatusLabelName, state.Status,
		peerCountLabelName, state.PeerCount,
		selfInGossipLabelName, state.SelfInGossip,
		failoverStatusLabelName, state.FailoverStatus,
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
