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
	metricsNamespacePrefix    = "solana_validator_ha_"
	validatorNameLabelName    = "validator_name"
	publicIPLabelName         = "public_ip"
	validatorRoleLabelName    = "validator_role"
	validatorStatusLabelName  = "validator_status"
	failoverStatusLabelName   = "status"
	peerCountLabelName        = "peer_count"
	selfInGossipLabelName     = "self_in_gossip"
	profileLabelName          = "profile"
	occupancyLabelName        = "occupancy"
	occupancyProfileLabelName = "occupancy_profile"
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
	metadata              *prometheus.GaugeVec
	occupancy             *prometheus.GaugeVec
	peerCount             *prometheus.GaugeVec
	selfInGossip          *prometheus.GaugeVec
	failoverStatus        *prometheus.GaugeVec
	profileMetadata       *prometheus.GaugeVec
	profilePeerCount      *prometheus.GaugeVec
	profileSelfInGossip   *prometheus.GaugeVec
	profileFailoverStatus *prometheus.GaugeVec
	updateAvailable       *prometheus.GaugeVec
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

	// Occupancy metric
	occupancyLabelNames := []string{
		occupancyLabelName,
		occupancyProfileLabelName,
	}
	occupancyLabelNames = append(occupancyLabelNames, m.commonLabelNames...)
	m.occupancy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "occupancy",
			Help: "Current local validator occupancy, always 1 with occupancy labels",
		},
		occupancyLabelNames,
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

	// Profile metadata metric
	profileMetadataLabelNames := []string{
		profileLabelName,
		validatorRoleLabelName,
	}
	profileMetadataLabelNames = append(profileMetadataLabelNames, m.commonLabelNames...)
	m.profileMetadata = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "profile_metadata",
			Help: "Metadata about each configured HA profile, always 1 with profile labels",
		},
		profileMetadataLabelNames,
	)

	// Profile peer count metric
	profileCommonLabelNames := []string{profileLabelName}
	profileCommonLabelNames = append(profileCommonLabelNames, m.commonLabelNames...)
	m.profilePeerCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "profile_peer_count",
			Help: "Number of peers seen in gossip for each profile",
		},
		profileCommonLabelNames,
	)

	// Profile self in gossip metric
	m.profileSelfInGossip = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "profile_self_in_gossip",
			Help: "Whether this node sees itself in gossip for each profile (1 = yes, 0 = no)",
		},
		profileCommonLabelNames,
	)

	// Profile failover status metric
	profileFailoverLabelNames := []string{
		profileLabelName,
		failoverStatusLabelName,
	}
	profileFailoverLabelNames = append(profileFailoverLabelNames, m.commonLabelNames...)
	m.profileFailoverStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "profile_failover_status",
			Help: "Current failover status for each profile",
		},
		profileFailoverLabelNames,
	)

	// Update available metric
	m.updateAvailable = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricsNamespacePrefix + "update_available",
			Help: "Whether a newer version of solana-validator-ha is available (1 = yes, 0 = no)",
		},
		m.commonLabelNames,
	)

	// Register all metrics
	m.registry.MustRegister(m.metadata)
	m.registry.MustRegister(m.occupancy)
	m.registry.MustRegister(m.peerCount)
	m.registry.MustRegister(m.selfInGossip)
	m.registry.MustRegister(m.failoverStatus)
	m.registry.MustRegister(m.profileMetadata)
	m.registry.MustRegister(m.profilePeerCount)
	m.registry.MustRegister(m.profileSelfInGossip)
	m.registry.MustRegister(m.profileFailoverStatus)
	m.registry.MustRegister(m.updateAvailable)

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
	m.exportMetricOccupancy(&state)
	m.exportMetricPeerCount(&state)
	m.exportMetricSelfInGossip(&state)
	m.exportMetricFailoverStatus(&state)
	m.exportProfileMetrics(&state)
	m.exportMetricUpdateAvailable(&state)

	m.logger.Debug("metrics refreshed",
		validatorRoleLabelName, state.Role,
		validatorStatusLabelName, state.Status,
		peerCountLabelName, state.PeerCount,
		selfInGossipLabelName, state.SelfInGossip,
		failoverStatusLabelName, state.FailoverStatus,
		occupancyLabelName, state.Occupancy,
		occupancyProfileLabelName, state.OccupancyProfile,
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

func (m *Metrics) exportMetricOccupancy(state *cache.State) {
	m.occupancy.Reset()
	m.occupancy.
		With(
			m.mergeLabels(
				prometheus.Labels{
					occupancyLabelName:        state.Occupancy,
					occupancyProfileLabelName: state.OccupancyProfile,
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

func (m *Metrics) exportProfileMetrics(state *cache.State) {
	m.profileMetadata.Reset()
	m.profilePeerCount.Reset()
	m.profileSelfInGossip.Reset()
	m.profileFailoverStatus.Reset()

	for profile, profileState := range state.Profiles {
		profileLabels := m.getProfileCommonLabels(state, profile)
		m.profileMetadata.
			With(
				m.mergeLabels(
					prometheus.Labels{
						profileLabelName:       profile,
						validatorRoleLabelName: profileState.Role,
					},
					m.getCommonLabels(state),
				),
			).
			Set(1)
		m.profilePeerCount.
			With(profileLabels).
			Set(float64(profileState.PeerCount))

		var selfInGossipValue float64
		if profileState.SelfInGossip {
			selfInGossipValue = 1
		}
		m.profileSelfInGossip.
			With(profileLabels).
			Set(selfInGossipValue)
		m.profileFailoverStatus.
			With(
				m.mergeLabels(
					prometheus.Labels{
						profileLabelName:        profile,
						failoverStatusLabelName: profileState.FailoverStatus,
					},
					m.getCommonLabels(state),
				),
			).
			Set(1)
	}
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

func (m *Metrics) getProfileCommonLabels(state *cache.State, profile string) prometheus.Labels {
	labels := prometheus.Labels{
		profileLabelName: profile,
	}
	return m.mergeLabels(labels, m.getCommonLabels(state))
}
