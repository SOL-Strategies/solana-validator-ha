package ha

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/cache"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/consensus"
	"github.com/sol-strategies/solana-validator-ha/internal/constants"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
	"github.com/sol-strategies/solana-validator-ha/internal/local"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
	"github.com/sol-strategies/solana-validator-ha/internal/prometheus"
	"github.com/sol-strategies/solana-validator-ha/internal/recording"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	"github.com/sol-strategies/solana-validator-ha/internal/updater"
	solana "github.com/solana-foundation/solana-go/v2"
)

// NewManagerOptions is a struct that contains the configuration for the manager
type NewManagerOptions struct {
	Cfg             *config.Config
	Version         string
	GetPublicIPFunc func() (string, error)
}

// Manager handles high availability logic
type Manager struct {
	cfg             *config.Config
	version         string
	metrics         *prometheus.Metrics
	cache           *cache.Cache
	logger          *log.Logger
	ctx             context.Context
	peerSelf        *config.Peer
	cancel          context.CancelFunc
	profiles        []*profileRuntime
	clusterRPC      *rpc.Client
	localState      *local.State
	getPublicIPFunc func() (string, error)
	initialized     bool
	logPrefix       string
	// recordingOutputDir is shared by all profile recordings.
	recordingOutputDir string
	// detector tracks the cluster's consensus phase (TowerBFT or Alpenglow).
	detector            *consensus.Detector
	warnedBypassIgnored bool
	// isolation detects that this node, while active, has lost the network.
	isolation *isolationMonitor
	// clusterRPCOutage reports when every cluster RPC endpoint keeps failing.
	clusterRPCOutage *clusterRPCOutageMonitor
	// demotedWithLocalRPCDown is set when the passive command succeeded but the local RPC did
	// not answer, i.e. the validator was stopped. It stops the passive command from being re-run
	// on every poll until the local RPC answers again.
	demotedWithLocalRPCDown bool
}

// profileRuntime holds the mutable state and rendered commands of one HA group.
type profileRuntime struct {
	config.Profile
	logger      *log.Logger
	gossipState *gossip.State
	peerRanks   map[string]int
	activeRole  config.Role
	passiveRole config.Role
	// ring holds the last N gossip samples for pre-failover context in recordings.
	ring           *recording.Ring
	activeRecorder *recording.Recorder
	// repeatOutcome is the no-action outcome the last incident ended with, while no recovery has
	// been seen since. An incident ending the same way again is discarded rather than written, so
	// a condition that persists across polls leaves one recording instead of one per poll.
	repeatOutcome string
	// repeatOutcomeWriteFailed belongs to the write attempt that enabled suppression.
	// The writer sets it on failure; only the HA goroutine changes which attempt we track.
	repeatOutcomeWriteFailed *atomic.Bool
	// lastLeaderlessReason is from the previous sample, so a recording gets one vote-lag event
	// per streak rather than one per sample.
	lastLeaderlessReason string
}

// Recording outcomes of this node running the passive command.
const (
	outcomeDemotedPassive = "demoted_passive"
	// outcomeDemotedValidatorDown means the passive command succeeded while the local validator
	// was not answering RPC, so its identity could not be confirmed.
	outcomeDemotedValidatorDown = "demoted_validator_down"
	outcomeDemotionFailed       = "demotion_failed"
	// outcomeNotVotingRetainedActive means our votes were not landing but no peer was visible to
	// take over, so we stayed active.
	outcomeNotVotingRetainedActive = "not_voting_retained_active"
)

// vetoOutcomes maps a gossip veto reason to the recording outcome of the failover it aborts.
var vetoOutcomes = map[string]string{
	gossip.VetoClusterStalled:      "aborted_cluster_stalled",
	gossip.VetoVoteAccountExcluded: "aborted_vote_account_excluded",
	gossip.VetoFailoverIneffective: "aborted_failover_ineffective",
}

// repeatableOutcomes are the recording outcomes where this node took no action. A persisting
// condition reaches them again on every poll.
var repeatableOutcomes = map[string]bool{
	"aborted_delay_error":                        true,
	"aborted_local_not_migrated":                 true,
	"aborted_not_passive":                        true,
	"aborted_self_not_in_gossip":                 true,
	"aborted_profile_priority":                   true,
	"aborted_not_healthy":                        true,
	"aborted_not_healthy_long_enough":            true,
	"aborted_revalidation_failed":                true,
	"aborted_undeclared_active":                  true,
	"already_active":                             true,
	"last_node_standing_retained_active":         true,
	outcomeNotVotingRetainedActive:               true,
	vetoOutcomes[gossip.VetoClusterStalled]:      true,
	vetoOutcomes[gossip.VetoVoteAccountExcluded]: true,
	vetoOutcomes[gossip.VetoFailoverIneffective]: true,
}

// NewManager creates a new HA manager from options
func NewManager(opts NewManagerOptions) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	// Create cache
	cache := cache.New()

	// Create metrics with cache
	metrics := prometheus.New(prometheus.Options{
		Config: opts.Cfg,
		Logger: logging.New(opts.Cfg.Validator.Name, "metrics"),
		Cache:  cache,
	})

	manager := &Manager{
		cfg:     opts.Cfg,
		version: opts.Version,
		metrics: metrics,
		cache:   cache,
		logger:  logging.New(opts.Cfg.Validator.Name, "ha_manager"),
		ctx:     ctx,
		cancel:  cancel,
	}

	if opts.GetPublicIPFunc != nil {
		manager.getPublicIPFunc = opts.GetPublicIPFunc
	}

	return manager
}

// Run starts the HA manager
func (m *Manager) Run() error {
	// initialize
	err := m.initialize()
	if err != nil {
		return err
	}
	defer func() {
		for _, profile := range m.profiles {
			m.finishRecording(profile, "interrupted", "unknown", "unknown")
		}
	}()
	if m.cfg.Failover.Recording.Enabled && m.cfg.Failover.Recording.Retention.Cleanup.Enabled {
		go m.recordingRetentionLoop()
	}

	// start metrics server
	go m.startMetricsServer()

	// start self health tracker goroutine - runs independently of the main HA monitor loop
	// so that the healthy streak timer is not affected by gossip refresh latency
	m.startHealthyTracker()

	// start periodic update checker
	if m.cfg.Update.CheckEnabled {
		updater.StartPeriodicCheck(m.ctx, m.version, m.cfg.Update.CheckIntervalDuration, func(latestVersion string) {
			m.cache.SetUpdateAvailable(latestVersion != "")
		})
	}

	// start monitoring loop
	return m.haMonitorLoop()
}

// initialize initializes the manager
func (m *Manager) initialize() error {
	m.logger.Debug("initializing manager")

	// Check if already initialized
	if m.initialized {
		m.logger.Debug("manager already initialized, skipping")
		return nil
	}

	// get public IP
	publicIP, err := m.getPublicIP()
	if err != nil {
		return err
	}

	// set global log prefix to pass everywhere
	m.logPrefix = m.cfg.Validator.Name
	m.logger = logging.New(m.logPrefix, "ha_manager")

	m.peerSelf = &config.Peer{Name: m.cfg.Validator.Name, IP: publicIP}

	// Use source URIs for file-backed endpoints in startup diagnostics.
	labels := m.cfg.Cluster.RPCURLLabels()
	displayURL := func(endpoint string) string {
		if label, ok := labels[endpoint]; ok {
			return label
		}
		return endpoint
	}
	clusterRPCLabels := make([]string, len(m.cfg.Cluster.RPCURLs))
	for i, endpoint := range m.cfg.Cluster.RPCURLs {
		clusterRPCLabels[i] = displayURL(endpoint)
	}

	// initialize
	m.logger.Info("initializing",
		"version", m.version,
		"public_ip", publicIP,
		"cluster_rpc_urls", clusterRPCLabels,
		"validator_rpc_url", displayURL(m.cfg.Validator.RPCURL),
		"active_pubkey", m.cfg.Validator.Identities.ActivePubkey(),
		"passive_pubkey", m.cfg.Validator.Identities.PassivePubkey(),
		"peers", m.cfg.Failover.Peers.String(),
		"failover_dry_run", m.cfg.Failover.DryRun,
		"prometheus_port", m.cfg.Prometheus.Port,
		"health_check_port", m.cfg.Prometheus.HealthCheckPort,
	)

	// the gossip state and the consensus detector both run on the HA monitor goroutine, so they
	// can share one cluster RPC client and its URL rotation
	m.clusterRPC = rpc.NewClient(m.logPrefix, m.cfg.Cluster.RPCURLs...).
		WithURLLabels(labels).
		WithTimeout(m.cfg.Cluster.RPCTimeoutDuration).
		WithCooldown(m.cfg.Cluster.RPCURLCooldownDuration)

	if err := m.initializeProfiles(); err != nil {
		return err
	}

	// the consensus detector and the isolation monitor get their own local RPC client, shared on
	// the HA monitor goroutine, because the local state's client is used by the health tracker
	// goroutine
	localRPC := rpc.NewClient(m.logPrefix, m.cfg.Validator.RPCURL).WithURLLabels(labels)

	// create consensus detector
	m.logger.Debug("creating consensus detector", "mode", m.cfg.Cluster.Consensus.Mode)
	m.detector = consensus.NewDetector(consensus.Options{
		Mode:              m.cfg.Cluster.Consensus.Mode,
		DetectionInterval: m.cfg.Cluster.Consensus.DetectionIntervalDuration,
		ClusterRPC:        m.clusterRPC,
		LocalRPC:          localRPC,
		LogPrefix:         m.logPrefix,
	})

	m.isolation = newIsolationMonitor(localRPC, m.cfg.Failover.LeaderlessSamplesThreshold, m.cfg.Failover.Isolation.LocalSlotStallDuration)
	m.clusterRPCOutage = newClusterRPCOutageMonitor(m.cfg.Failover.LeaderlessSamplesThreshold)

	// create local state
	m.logger.Debug("creating local state")
	m.localState = local.NewState(local.Options{
		RPC:          rpc.NewClient(m.logPrefix, m.cfg.Validator.RPCURL).WithURLLabels(labels),
		Cfg:          m.cfg.Failover.SelfHealthy,
		ActivePubkey: m.cfg.Validator.Identities.ActivePubkey(),
		Ctx:          m.ctx,
		LogPrefix:    m.logPrefix,
	})

	// resolve recording output dir once so it is ready when a failover fires
	if m.cfg.Failover.Recording.Enabled {
		if err := m.cfg.Failover.Recording.Prepare(m.cfg.File); err != nil {
			return err
		}
		m.recordingOutputDir = m.cfg.Failover.Recording.ResolvedOutputDir(m.cfg.File)
		retention := m.cfg.Failover.Recording.Retention
		m.logger.Info("recording retention events configured",
			"failover", retention.Events.Keeps("became_active"),
			"recovery", retention.Events.Keeps("recovered_no_failover"),
			"interruption", retention.Events.Keeps("interrupted"))
		if retention.Cleanup.Enabled {
			// Prepare validated these durations above, including the default interval.
			interval, _ := retention.Cleanup.IntervalDuration()
			maxAge, _ := retention.Cleanup.MaxAgeDuration()
			m.logger.Info("recording cleanup enabled", "interval", interval, "max_age", maxAge)
		}
		recovered, recoverErr := recording.RecoverPartials(m.recordingOutputDir, m.cfg.Failover.Recording.Retention.Events.Keeps("interrupted"))
		if recoverErr != nil {
			m.logger.Error("failed to recover partial failover recordings", "error", recoverErr)
		}
		for _, path := range recovered {
			m.logger.Warn("recovered interrupted failover recording", "path", path)
		}
		m.pruneRecordings()
	}

	m.logger.Debug("initialized")
	m.initialized = true
	return nil
}

func (m *Manager) initializeProfiles() error {
	profiles := m.cfg.Profiles.Enabled()
	if m.cfg.Profiles == nil {
		if m.cfg.Failover.Peers.HasIP(m.peerSelf.IP) {
			return fmt.Errorf("failover.peers must not reference ourselves, found %s in failover.peers", m.peerSelf.IP)
		}
		peers := maps.Clone(m.cfg.Failover.Peers)
		if peers == nil {
			peers = make(config.Peers)
		}
		peers.Add(config.Peer{Name: m.peerSelf.Name, IP: m.peerSelf.IP, Priority: m.cfg.Failover.Priority})
		profiles = []config.Profile{{Identities: m.cfg.Validator.Identities, Peers: peers}}
	} else if err := m.cfg.Profiles.ValidateSelfPeer(m.peerSelf.Name, m.peerSelf.IP); err != nil {
		return err
	}

	if len(profiles) == 0 {
		return fmt.Errorf("no enabled HA profiles")
	}
	runtimes := make([]*profileRuntime, 0, len(profiles))
	known := m.cfg.ActiveIdentityNames()
	for _, profile := range profiles {
		runtime := &profileRuntime{
			Profile:     profile,
			logger:      m.logger,
			ring:        &recording.Ring{},
			peerRanks:   profile.PeerIPPriorityRankMap(),
			activeRole:  m.cfg.Failover.Active,
			passiveRole: m.cfg.Failover.Passive,
		}
		var votePubkey *solana.PublicKey
		logPrefix := m.logPrefix
		if profile.Name != "" {
			vote, err := solana.PublicKeyFromBase58(profile.VotePubkeyStr)
			if err != nil {
				return fmt.Errorf("profile %s vote account: %w", profile.Name, err)
			}
			votePubkey = &vote
			runtime.activeRole, runtime.passiveRole, err = m.cfg.ProfileRoles(profile)
			if err != nil {
				return err
			}
			runtime.logger = m.logger.With("profile", profile.Name)
			logPrefix += "/" + profile.Name
			runtime.logger.Info("monitoring profile", "active_pubkey", profile.ActivePubkey(), "peers", profile.Peers.String())
		}
		runtime.gossipState = gossip.NewState(gossip.Options{
			ClusterRPC:                     m.clusterRPC,
			ActivePubkey:                   profile.ActivePubkey(),
			VotePubkey:                     votePubkey,
			KnownActiveIdentities:          known,
			ConfigPeers:                    profile.Peers,
			DelinquentSlotDistanceOverride: m.cfg.Failover.DelinquentSlotDistanceOverride,
			SelfIP:                         m.peerSelf.IP,
			LogPrefix:                      logPrefix,
			Alpenglow:                      m.cfg.Failover.Alpenglow,
		})
		runtimes = append(runtimes, runtime)
	}
	m.profiles = runtimes
	return nil
}

func (m *Manager) refreshGossip() {
	if len(m.profiles) == 1 {
		m.profiles[0].gossipState.Refresh()
		return
	}
	nodes, err := m.clusterRPC.GetClusterNodes(m.ctx)
	for _, profile := range m.profiles {
		profile.gossipState.RefreshFromClusterNodes(nodes, err)
	}
}

func (m *Manager) needsConfirmationPoll() bool {
	for _, profile := range m.profiles {
		if profile.gossipState.LeaderlessSamplesCount == m.cfg.Failover.LeaderlessSamplesThreshold-1 {
			return true
		}
	}
	return false
}

func (m *Manager) profileRole(profile *profileRuntime, identity string) string {
	switch {
	case identity == "":
		return constants.RoleNameUnknown
	case identity == profile.ActivePubkey():
		return constants.RoleNameActive
	case profile.Name == "" || identity == m.cfg.Validator.Identities.PassivePubkey():
		return constants.RoleNamePassive
	default:
		for _, candidate := range m.profiles {
			if identity == candidate.ActivePubkey() {
				return "busy"
			}
		}
		return constants.RoleNameUnknown
	}
}

func (m *Manager) setTransitionStatus(profile *profileRuntime, status string) {
	state := m.cache.GetState()
	state.FailoverStatus = status
	if profile.Name != "" {
		state.Profiles = maps.Clone(state.Profiles)
		if state.Profiles == nil {
			state.Profiles = make(map[string]cache.ProfileState)
		}
		info := state.Profiles[profile.Name]
		info.FailoverStatus = status
		state.Profiles[profile.Name] = info
	}
	m.cache.UpdateState(state)
	if profile.Name != "" {
		m.metrics.RefreshMetrics()
	}
}

// getPublicIP returns the public IPv4 address using external services.
// It tries multiple services in order and returns the first successful result.
func (m *Manager) getPublicIP() (string, error) {
	// Use override if provided
	if m.getPublicIPFunc != nil {
		return m.getPublicIPFunc()
	}

	return m.cfg.Validator.PublicIP()
}

// startMetricsServer starts the Prometheus metrics server
func (m *Manager) startMetricsServer() {
	// Start the Prometheus metrics server
	go func() {
		if err := m.metrics.StartServer(m.cfg.Prometheus.Port); err != nil && err != http.ErrServerClosed {
			m.logger.Error("metrics server error", "error", err)
		}
	}()

	// Start health check server on a different port
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("healthy"))
		})

		port := strconv.Itoa(m.cfg.Prometheus.HealthCheckPort)
		healthServer := &http.Server{
			Addr:    ":" + port,
			Handler: mux,
		}

		m.logger.Debug("starting health check server", "port", port)

		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.logger.Error("health check server error", "error", err)
		}
	}()
}

// haMonitorLoop runs the main ha monitoring loop
func (m *Manager) haMonitorLoop() error {
	confirmationPoll := m.cfg.Failover.LeaderlessConfirmationPollDuration
	fastPolling := confirmationPoll < m.cfg.Failover.PollIntervalDuration

	if fastPolling {
		m.logger.Info("monitoring HA state",
			"poll_interval", m.cfg.Failover.PollIntervalDuration,
			"leaderless_confirmation_poll", confirmationPoll,
		)
	} else {
		m.logger.Info("monitoring HA state", "poll_interval", m.cfg.Failover.PollIntervalDuration)
	}

	// initial consensus phase and gossip state population
	m.refreshConsensus()
	m.refreshGossip()

	// start the monitor loop with ticker aligned to interval boundaries
	ticker := time.NewTicker(m.cfg.Failover.PollIntervalDuration)
	defer ticker.Stop()

	interval := m.cfg.Failover.PollIntervalDuration
	intervalNanos := int64(interval)

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Info("HA monitor loop done")
			return nil
		case <-ticker.C:
			// Wait until the next aligned interval before running.
			// This ensures all nodes run at the same synchronized times so that leaderless
			// sample counts stay in step across the cluster, keeping rank-based coordination safe.
			// For example, with 5s interval: all nodes run at 12:01:05, 12:01:10, etc.
			now := time.Now()
			nanosSinceEpoch := now.UnixNano()
			remainder := nanosSinceEpoch % intervalNanos

			if remainder != 0 {
				// Not aligned yet, wait until the next interval boundary
				waitDuration := interval - time.Duration(remainder)
				m.logger.Debug(fmt.Sprintf("synchronization, ensuring HA monitor loop runs at %s", now.Add(waitDuration).Format(time.RFC3339)))
				select {
				case <-m.ctx.Done():
					m.logger.Info("HA monitor loop done")
					return nil
				case <-time.After(waitDuration):
					// Now we're at the aligned time
				}
			}

			// Run at the aligned interval
			m.ensureHAState()

			// If fast-polling is configured and we are one sample below the threshold
			// (i.e. the slow-poll phase has already built enough confidence), switch to
			// the shorter confirmation poll interval for the final sample only.
			// We do NOT fast-poll on the first leaderless sample to avoid reacting to
			// transient gossip blips that resolve within a normal poll cycle.
			if fastPolling && m.needsConfirmationPoll() {
				m.logger.Warn("leaderless samples approaching threshold - switching to confirmation poll interval",
					"leaderless_samples_count", m.cfg.Failover.LeaderlessSamplesThreshold-1,
					"leaderless_samples_threshold", m.cfg.Failover.LeaderlessSamplesThreshold,
					"confirmation_poll_interval", confirmationPoll,
				)
				if err := m.runConfirmationPollLoop(confirmationPoll); err != nil {
					return err
				}
			}
		}
	}
}

// runConfirmationPollLoop polls at the faster confirmation interval until either the leaderless
// threshold is reached (triggering ensureHAState) or a peer reappears (resetting to the normal loop).
func (m *Manager) runConfirmationPollLoop(interval time.Duration) error {
	for {
		select {
		case <-m.ctx.Done():
			m.logger.Info("HA monitor loop done")
			return nil
		case <-time.After(interval):
			m.ensureHAState()
			// Exit the fast-poll loop once the leaderless count is no longer in the
			// "one below threshold" window — either it crossed threshold (failover fired
			// or was aborted) or a peer reappeared and the count reset.
			if !m.needsConfirmationPoll() {
				return nil
			}
		}
	}
}

// buildGossipSample converts the current gossip state into a recording.GossipSample.
// It must be called immediately after a gossipState.Refresh() while the state is fresh.
func (m *Manager) buildGossipSample(profile *profileRuntime) recording.GossipSample {
	peerStates := profile.gossipState.GetPeerStates()
	peers := make([]recording.PeerSnapshot, 0, len(profile.Peers))

	for name, cfgPeer := range profile.Peers {
		snap := recording.PeerSnapshot{Name: name, IP: cfgPeer.IP}
		if ps, ok := peerStates[name]; ok {
			snap.Pubkey = ps.Pubkey
			if ps.BusyProfile != "" {
				snap.Role, snap.BusyProfile = "busy", ps.BusyProfile
			} else if ps.LastSeenActive {
				snap.Role = "active"
			} else {
				snap.Role = "passive"
			}
		} else {
			snap.Role = "missing"
		}
		// A delinquent active is deliberately excluded from current peer state, so attach
		// its slot evidence using the persisted last-active peer identity.
		if profile.gossipState.ActivePeerIsDelinquent() && profile.gossipState.GetLastActivePeer().Name == name {
			if d := profile.gossipState.GetDelinquencyDetail(); d != nil {
				snap.LastVoteSlot = &d.LastVoteSlot
				snap.CurrentSlot = &d.CurrentSlot
				snap.SlotDistance = &d.SlotDistance
			}
		}
		peers = append(peers, snap)
	}

	localSnapshot := m.cache.GetState()
	view := m.detector.View()
	sample := recording.GossipSample{
		SampledAt:              profile.gossipState.PeerStatesRefreshedAt,
		Peers:                  peers,
		LeaderlessSamplesCount: profile.gossipState.LeaderlessSamplesCount,
		ActivePeerDelinquent:   profile.gossipState.ActivePeerIsDelinquent(),
		RPCError:               profile.gossipState.LastRefreshHadRPCError(),
		LocalRole:              localSnapshot.Role,
		LocalPubkey:            localSnapshot.RuntimePubkey,
		LocalHealthy:           localSnapshot.Status == constants.StatusHealthy,
		SelfInGossip:           m.isSelfInGossip(profile),
		GossipPubkey:           m.selfGossipPubkey(profile),
		ConsensusPhase:         view.Phase.String(),
		AlpenglowGenesisSlot:   view.GenesisSlot,
		LeaderlessReason:       profile.gossipState.LeaderlessReason(),
		Veto:                   profile.gossipState.VetoReason(),
	}
	if profile.Name != "" {
		sample.LocalRole = m.profileRole(profile, localSnapshot.RuntimePubkey)
	}
	if signals, ok := profile.gossipState.AlpenglowSignals(); ok {
		localGenesisMatch, _ := m.detector.LocalEligibility()
		sample.LocalGenesisMatch = &localGenesisMatch
		sample.FinalizedSlot = signals.FinalizedSlot
		sample.ClusterLive = &signals.ClusterLive
		if signals.NetworkStakeRatioKnown {
			sample.NetworkStakeRatio = &signals.NetworkStakeRatio
		}
	}
	return sample
}

// newRecorder creates a Recorder seeded with node/config context and the current ring snapshot.
func (m *Manager) newRecorder(profile *profileRuntime, detectedAt time.Time) *recording.Recorder {
	node := recording.NodeInfo{
		Name:          m.cfg.Validator.Name,
		IP:            m.peerSelf.IP,
		Profile:       profile.Name,
		ActivePubkey:  profile.ActivePubkey(),
		PassivePubkey: m.cfg.Validator.Identities.PassivePubkey(),
		BinaryVersion: m.version,
	}

	cfg := recording.ConfigSnapshot{
		Profile:                            profile.Name,
		VotePubkey:                         profile.VotePubkeyStr,
		AuthorizedVoterPubkey:              profile.AuthorizedVoterPubkey,
		PollIntervalDuration:               m.cfg.Failover.PollIntervalDuration.String(),
		LeaderlessSamplesThreshold:         m.cfg.Failover.LeaderlessSamplesThreshold,
		LeaderlessConfirmationPollDuration: m.cfg.Failover.LeaderlessConfirmationPollDuration.String(),
		DelinquencyBypass:                  m.cfg.Failover.DelinquencyBypass,
		ConsensusMode:                      string(m.cfg.Cluster.Consensus.Mode),
		VoteLagSlotsThreshold:              m.cfg.Failover.Alpenglow.VoteLagSlotsThreshold,
		WarmupSlots:                        m.cfg.Failover.Alpenglow.WarmupSlots,
		FinalizationStallDuration:          m.cfg.Failover.Alpenglow.FinalizationStallDuration.String(),
		NetworkCurrentStakeRatioMin:        m.cfg.Failover.Alpenglow.NetworkCurrentStakeRatioMin,
	}
	if m.cfg.Failover.DelinquentSlotDistanceOverride.Enabled {
		v := m.cfg.Failover.DelinquentSlotDistanceOverride.Value
		cfg.DelinquentSlotDistanceOverride = &v
	}

	return recording.New(node, cfg, detectedAt, profile.ring.Snapshot())
}

func (m *Manager) recordingError(err error) {
	m.metrics.IncRecordingWriteFailure()
	m.logger.Error("failed to persist failover recording", "error", err)
}

func (m *Manager) pruneRecordings() {
	if !m.cfg.Failover.Recording.Retention.Cleanup.Enabled {
		return
	}
	maxAge, err := m.cfg.Failover.Recording.Retention.Cleanup.MaxAgeDuration()
	if err == nil && maxAge > 0 {
		var removed int
		removed, err = recording.Prune(m.recordingOutputDir, maxAge, time.Now())
		if removed > 0 {
			m.logger.Info("pruned expired recordings", "removed", removed, "directory", m.recordingOutputDir, "max_age", maxAge)
		}
	}
	if err != nil {
		m.logger.Error("failed to prune expired recordings", "error", err)
	}
}

func (m *Manager) recordingRetentionLoop() {
	if !m.cfg.Failover.Recording.Retention.Cleanup.Enabled {
		return
	}
	interval, err := m.cfg.Failover.Recording.Retention.Cleanup.IntervalDuration()
	if err != nil {
		m.logger.Error("invalid recording cleanup interval", "error", err)
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.pruneRecordings()
		}
	}
}

func (m *Manager) recordStep(profile *profileRuntime, event string, started time.Time, err error) {
	if profile.activeRecorder == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error=" + err.Error()
	}
	profile.activeRecorder.AddEvent(event, fmt.Sprintf("duration=%s result=%s", time.Since(started).Round(time.Millisecond), result))
	m.checkpointRecording(profile)
}

func (m *Manager) checkpointRecording(profile *profileRuntime) {
	if profile.activeRecorder != nil {
		profile.activeRecorder.CheckpointAsync(m.recordingOutputDir, m.recordingError)
	}
}

func (m *Manager) finishRecording(profile *profileRuntime, result, fromNode, toNode string) {
	m.finishRecordingOutcome(profile, recording.Outcome{Result: result, FromNode: fromNode, ToNode: toNode})
}

func (m *Manager) finishRecordingOutcome(profile *profileRuntime, outcome recording.Outcome) {
	result := outcome.Result
	if profile.activeRecorder == nil {
		return
	}
	if !m.cfg.Failover.Recording.Retention.Events.Keeps(result) {
		profile.activeRecorder.DiscardAsync(m.recordingOutputDir, m.recordingError)
		profile.activeRecorder = nil
		profile.repeatOutcome = ""
		profile.repeatOutcomeWriteFailed = nil
		return
	}
	if result == profile.repeatOutcome && profile.repeatOutcomeWriteFailed != nil && !profile.repeatOutcomeWriteFailed.Load() {
		profile.logger.Debug("incident ended with the same no-action outcome as the previous one - not writing another recording", "outcome", result)
		profile.activeRecorder.DiscardAsync(m.recordingOutputDir, m.recordingError)
		profile.activeRecorder = nil
		return
	}
	profile.repeatOutcome = ""
	profile.repeatOutcomeWriteFailed = nil
	onError := m.recordingError
	if repeatableOutcomes[result] {
		profile.repeatOutcome = result
		failed := &atomic.Bool{}
		profile.repeatOutcomeWriteFailed = failed
		onError = func(err error) {
			// Capture this attempt's flag: a late failure must not reset suppression
			// for a newer outcome or for an incident after recovery.
			failed.Store(true)
			m.recordingError(err)
		}
	}
	profile.activeRecorder.FinishAsync(m.recordingOutputDir, outcome, onError)
	profile.activeRecorder = nil
}

// observeRecording keeps an incident open from the first anomalous network observation.
// It returns true when the current sample is anomalous.
func (m *Manager) observeRecording(profile *profileRuntime, sample recording.GossipSample, undeclaredActive bool) bool {
	anomalous := sample.RPCError || sample.LeaderlessSamplesCount > 0 ||
		!sample.SelfInGossip || sample.ActivePeerDelinquent || undeclaredActive
	if !m.cfg.Failover.Recording.Enabled {
		return anomalous
	}
	if anomalous {
		if profile.activeRecorder == nil {
			profile.activeRecorder = m.newRecorder(profile, sample.SampledAt)
			profile.activeRecorder.AddEvent("incident_started", fmt.Sprintf(
				"rpc_error=%t leaderless_count=%d self_in_gossip=%t delinquent=%t undeclared_active=%t",
				sample.RPCError, sample.LeaderlessSamplesCount, sample.SelfInGossip, sample.ActivePeerDelinquent, undeclaredActive))
		} else {
			profile.activeRecorder.AddSample(sample)
		}
		m.checkpointRecording(profile)
		return true
	}
	profile.repeatOutcome = "" // the condition cleared, so the next incident is a new one
	profile.repeatOutcomeWriteFailed = nil
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddSample(sample)
		// a vetoed sample counts the active as present, which also ends the incident
		detail := ""
		if sample.Veto != "" {
			detail = "veto=" + sample.Veto
		}
		profile.activeRecorder.AddEvent("network_recovered", detail)
		m.finishRecording(profile, "recovered_no_failover", "unknown", "unknown")
	}
	return false
}

// ensureHAState implements basic HA logic
func (m *Manager) ensureHAState() {
	m.refreshConsensus()
	m.refreshGossip()
	rpcFailed := m.profiles[0].gossipState.LastRefreshHadRPCError()
	m.observeClusterRPCOutage(rpcFailed)

	identity, active := m.refreshMetrics()
	for _, profile := range m.profiles {
		sample := m.buildGossipSample(profile)
		profile.ring.Add(sample)
		m.observeRecording(profile, sample, profile.gossipState.HasConfigUndeclaredActivePeer())
		m.observeAlpenglowEvidence(profile, sample)
	}

	if m.cfg.Failover.Isolation.Enabled {
		m.isolation.observe(m.ctx, rpcFailed)
		if active != nil && !active.gossipState.HasConfigUndeclaredActivePeer() && m.demoteIfIsolated(active) {
			return
		}
	}

	// Give any held profile exclusive access to the validator for this poll.
	if active != nil {
		m.evaluateProfile(active, identity)
		return
	}

	var selected *profileRuntime
	var selectedFrom string
	for _, profile := range m.profiles {
		from, eligible := m.evaluateProfile(profile, identity)
		if !eligible {
			continue
		}
		if selected == nil {
			selected, selectedFrom = profile, from
			continue
		}
		profile.logger.Info("deferring failover to higher priority profile", "selected_profile", selected.Name)
		m.finishRecording(profile, "aborted_profile_priority", from, "unknown")
	}
	if selected != nil {
		m.executeFailover(selected, selectedFrom)
	}
}

func (m *Manager) evaluateProfile(profile *profileRuntime, identity string) (string, bool) {
	// do nothing except warn if a config-undeclared active peer is found, this prevents false positive failovers
	// and prompts users to declare these so that the anti-race condition logic (based on IPs) can continue to work as intended
	if profile.gossipState.HasConfigUndeclaredActivePeer() {
		configUndeclaredActivePeer := profile.gossipState.GetConfigUndeclaredActivePeer()
		profile.logger.Warn("active peer found not declared in HA cluster config - no failover required, but should be added to failover.peers", "ip", configUndeclaredActivePeer.IP, "pubkey", configUndeclaredActivePeer.Pubkey)
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("aborted_undeclared_active", fmt.Sprintf("ip=%s pubkey=%s", configUndeclaredActivePeer.IP, configUndeclaredActivePeer.Pubkey))
			m.finishRecording(profile, "aborted_undeclared_active", "unknown", "unknown")
		}
		return "", false
	}

	// if there is an active peer found in the last failover.leaderless_samples_threshold - we are good
	// having a lookback grace period is important to allow for RPC glitches and other issues
	if !profile.gossipState.LeaderlessSamplesExceedsThreshold(m.cfg.Failover.LeaderlessSamplesThreshold) {
		// delinquency fast-path (opt-in): if failover.delinquency_bypass is enabled and the network
		// has authoritatively declared the active peer delinquent via getVoteAccounts — cluster-wide
		// consensus that the peer has not voted for at least delinquent_slot_distance slots, and not
		// due to a low balance — skip the leaderless sample threshold and trigger failover immediately.
		// ⚠️ Risk: a validator on a minority fork can appear delinquent but later recover. If it does,
		// this bypass may trigger an unnecessary failover. Only enable if your validator can enter a
		// "ghost" state (alive in gossip, not voting) that it cannot recover from on its own.
		if m.cfg.Failover.DelinquencyBypass && profile.gossipState.ActivePeerIsDelinquent() && m.delinquencyBypassAllowed() {
			profile.logger.Error("active peer declared delinquent by network - bypassing leaderless sample threshold and triggering failover (delinquency_bypass enabled)")
			if profile.activeRecorder != nil {
				detail := fmt.Sprintf("leaderless_count=%d threshold=%d",
					profile.gossipState.LeaderlessSamplesCount, m.cfg.Failover.LeaderlessSamplesThreshold)
				if d := profile.gossipState.GetDelinquencyDetail(); d != nil {
					detail += fmt.Sprintf(" last_vote_slot=%d current_slot=%d slot_distance=%d allowed=%d",
						d.LastVoteSlot, d.CurrentSlot, d.SlotDistance, d.AllowedDistance)
				}
				profile.activeRecorder.AddEvent("delinquency_bypass_triggered", detail)
				m.checkpointRecording(profile)
			}
		} else {
			profile.logger.Debug("active peer found - no failover required")
			return "", false
		}
	} else {
		profile.logger.Error(fmt.Sprintf("no active peer found in the last %d samples - failover required", profile.gossipState.LeaderlessSamplesCount))
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("threshold_reached", fmt.Sprintf("leaderless_count=%d threshold=%d",
				profile.gossipState.LeaderlessSamplesCount, m.cfg.Failover.LeaderlessSamplesThreshold))
			m.checkpointRecording(profile)
		}
	}

	// fromNode is the peer that was active before this failover; used in the recording filename.
	fromNode := profile.gossipState.GetLastActivePeer().Name
	if fromNode == "" {
		fromNode = "unknown"
	}

	if profile.Name != "" && identity != profile.ActivePubkey() &&
		(identity != m.cfg.Validator.Identities.PassivePubkey() || m.isSelfNotInGossip(profile)) {
		outcome := "aborted_not_passive"
		if identity == m.cfg.Validator.Identities.PassivePubkey() {
			outcome = "aborted_self_not_in_gossip"
		}
		profile.logger.Debug("local validator is unavailable for this profile", "identity", identity, "outcome", outcome)
		m.finishRecording(profile, outcome, fromNode, "unknown")
		return "", false
	}

	// we hold the active identity in gossip but our votes are not landing - hand over if a peer can
	// take over. Without this check we would be judged by the not-in-gossip rules below, since a
	// not-voting active is left out of the peer states.
	if m.isSelfNotVotingActive(profile) && (profile.Name == "" || identity == profile.ActivePubkey()) {
		if m.alreadyDemotedWithLocalRPCDown() {
			profile.logger.Debug("already demoted and the local validator is still not answering RPC - not re-running the passive command")
			return "", false
		}
		if !profile.gossipState.HasPeers(m.peerSelf.IP) {
			profile.logger.Warn("our votes are not landing but no peer is visible to take over - staying active")
			if profile.activeRecorder != nil {
				profile.activeRecorder.AddEvent(outcomeNotVotingRetainedActive, "peers_visible=false")
				m.finishRecording(profile, outcomeNotVotingRetainedActive, fromNode, m.cfg.Validator.Name)
			}
			return "", false
		}
		profile.logger.Error("our votes are not landing and a peer is visible - ensuring we are passive so a peer can take over")
		m.finishRecording(profile, m.ensurePassive(profile), fromNode, "unknown")
		return "", false
	}

	// if we don't see ourselves in gossip - evaluate whether to become passive
	if m.isSelfNotInGossip(profile) {
		if m.alreadyDemotedWithLocalRPCDown() {
			profile.logger.Debug("already demoted and the local validator is still not answering RPC - not re-running the passive command")
			return "", false
		}

		// If RPC failed, we likely have network connectivity issues - become passive
		if profile.gossipState.LastRefreshHadRPCError() {
			if profile.Name != "" {
				return "", false
			}
			profile.logger.Error("we do not appear in gossip due to RPC error (possible network connectivity issue) - ensuring we are passive")
			m.finishRecording(profile, m.ensurePassive(profile), fromNode, "unknown")
			return "", false
		}

		// RPC succeeded but we're not in the results
		// Check if there are other peers visible that could take over
		if !profile.gossipState.HasPeers(m.peerSelf.IP) {
			// No other peers visible either - we might be the last node standing
			// Don't call ensurePassive to avoid taking the entire cluster offline
			profile.logger.Warn("we do not appear in gossip and no other peers are visible (but RPC is working) - skipping ensurePassive to avoid taking entire cluster offline")
			if profile.activeRecorder != nil {
				profile.activeRecorder.AddEvent("last_node_standing_retained_active", "rpc_ok=true peers_visible=false")
				m.finishRecording(profile, "last_node_standing_retained_active", fromNode, m.cfg.Validator.Name)
			}
			return "", false
		}

		// Other peers are visible and could take over - safe to become passive
		profile.logger.Error("we do not appear in gossip but other peers are visible - ensuring we are passive so a peer can take over")
		m.finishRecording(profile, m.ensurePassive(profile), fromNode, "unknown")
		return "", false
	}
	profile.logger.Debug("we are in gossip", "pubkey", m.selfGossipPubkey(profile), "public_ip", m.peerSelf.IP)

	// to participate in failover we must be healthy
	if !m.localState.IsSelfHealthy() {
		profile.logger.Error("we are not healthy - unable to become active in failover")
		m.finishRecording(profile, "aborted_not_healthy", fromNode, "unknown")
		return "", false
	}

	// we must have been healthy for long enough to rule out startup health flaps
	if !m.localState.IsSelfHealthyLongEnough() {
		profile.logger.Warn("not healthy for long enough to be a failover candidate - standing by",
			"healthy_for", m.localState.SelfHealthyDuration(),
			"minimum_duration", m.cfg.Failover.SelfHealthy.MinimumDuration,
		)
		m.finishRecording(profile, "aborted_not_healthy_long_enough", fromNode, "unknown")
		return "", false
	}

	// An active validator that is still in gossip keeps its role.
	if identity == profile.ActivePubkey() {
		profile.logger.Warn("we are already active - nothing to do")
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("already_active", "")
			m.finishRecording(profile, "already_active", fromNode, m.cfg.Validator.Name)
		}
		return "", false
	}

	if identity != m.cfg.Validator.Identities.PassivePubkey() {
		profile.logger.Warn("local RPC did not confirm the passive identity", "identity", identity)
		m.finishRecording(profile, "aborted_not_passive", fromNode, "unknown")
		return "", false
	}

	// under Alpenglow we must be on the same Alpenglow genesis as the cluster, otherwise our
	// votes cannot count; getHealth alone does not show this
	if !m.isLocalEligible(profile, fromNode, "pre_delay") {
		return "", false
	}

	return fromNode, true
}

func (m *Manager) executeFailover(profile *profileRuntime, fromNode string) {
	// remember whether the leaderless streak rested on voting evidence alone: the post-delay
	// refresh resets the streak if the active is then vetoed rather than judged not voting
	voteOnlyStreak := profile.gossipState.LeaderlessStreakIsVoteOnly()

	// at this point we know we are in gossip, healthy, and passive
	// so we begin checks to make sure none of our peers have already taken over as active

	// introduce a rank-based delay to safeguard against multiple nodes trying to become active at the same time
	delayStart := time.Now()
	delayApplied, err := m.delayTakeoverAsActive(profile)
	if err != nil {
		profile.logger.Error(err.Error())
		m.finishRecording(profile, "aborted_delay_error", fromNode, "unknown")
		return
	}
	if profile.activeRecorder != nil {
		if delayApplied {
			profile.activeRecorder.AddEvent("delay_applied", fmt.Sprintf("duration=%s", time.Since(delayStart).Round(time.Millisecond)))
		} else {
			profile.activeRecorder.AddEvent("rank_0_no_delay", "")
		}
		m.checkpointRecording(profile)
	}

	// Refresh after a delay or phase change. Named profiles also refresh at rank zero,
	// because gathering evidence for the other profiles can take time.
	previousPhase := m.detector.Phase()
	m.refreshConsensusNow()
	revalidated := profile.Name != "" || delayApplied || m.detector.Phase() != previousPhase
	if revalidated {
		profile.gossipState.Refresh()
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddSample(m.buildGossipSample(profile))
			profile.activeRecorder.AddEvent("revalidation_refresh", "")
			m.checkpointRecording(profile)
		}

		// a failed re-validation keeps the pre-delay leaderless count and clears any veto, so the
		// checks below would pass without having seen the cluster - abort and retry on the next poll
		if profile.gossipState.LastRefreshHadRPCError() {
			profile.logger.Error("failed to re-check gossip after the takeover delay - aborting takeover")
			if profile.activeRecorder != nil {
				profile.activeRecorder.AddEvent("revalidation_failed", "rpc_error=true")
			}
			m.finishRecording(profile, "aborted_revalidation_failed", fromNode, "unknown")
			return
		}
	}

	// an undeclared active peer may have appeared during the delay - treat the same as the pre-delay check
	if profile.gossipState.HasConfigUndeclaredActivePeer() {
		configUndeclaredActivePeer := profile.gossipState.GetConfigUndeclaredActivePeer()
		profile.logger.Warn("active peer found not declared in HA cluster config (post-delay re-check) - aborting takeover, should be added to failover.peers", "ip", configUndeclaredActivePeer.IP, "pubkey", configUndeclaredActivePeer.Pubkey)
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("aborted_undeclared_active", fmt.Sprintf("ip=%s pubkey=%s", configUndeclaredActivePeer.IP, configUndeclaredActivePeer.Pubkey))
			m.finishRecording(profile, "aborted_undeclared_active", fromNode, "unknown")
		}
		return
	}

	// A streak built on voting evidence alone is abandoned if the re-validation shows a failover
	// cannot help: the whole cluster stalled, or the active's vote account cannot vote at all.
	// A streak with the active missing from gossip is never vetoed; a dead host is dead during a
	// stall too.
	if revalidated && voteOnlyStreak {
		if veto := profile.gossipState.VetoReason(); veto != "" {
			profile.logger.Warn("active peer's missing votes are no longer held against it after the takeover delay - aborting takeover", "veto", veto)
			m.metrics.IncFailoverVeto(veto)
			if profile.activeRecorder != nil {
				profile.activeRecorder.AddEvent("veto_"+veto, "stage=post_delay")
			}
			m.finishRecording(profile, vetoOutcomes[veto], fromNode, "unknown")
			return
		}
	}

	// A fresh sample resets the streak to zero when an active peer appears.
	// A nonzero count below the threshold can still be a valid delinquency bypass.
	if revalidated && profile.gossipState.LeaderlessSamplesCount == 0 {
		activePeerState, err := profile.gossipState.GetActivePeer()
		if err != nil {
			profile.logger.Warn("active peer appeared during takeover delay but could not be identified in state - aborting takeover", "error", err)
			m.finishRecording(profile, "aborted_peer_took_over", fromNode, "unknown")
			return
		}
		profile.logger.Warn(fmt.Sprintf("peer %s became active during takeover delay - aborting takeover", activePeerState.Name),
			"ip", activePeerState.IP,
			"pubkey", activePeerState.Pubkey,
			"seen_at", activePeerState.LastSeenAtString(),
		)
		m.finishRecording(profile, "aborted_peer_took_over", fromNode, activePeerState.Name)
		return
	}

	if !m.isLocalEligible(profile, fromNode, "pre_promotion") {
		return
	}

	identity, err := m.localState.SelfIdentity()
	if err != nil || identity != m.cfg.Validator.Identities.PassivePubkey() {
		profile.logger.Warn("local RPC did not confirm the passive identity before promotion", "identity", identity, "error", err)
		m.finishRecording(profile, "aborted_not_passive", fromNode, "unknown")
		return
	}
	if revalidated && profile.Name != "" && !m.isSelfInGossip(profile) {
		m.finishRecording(profile, "aborted_self_not_in_gossip", fromNode, "unknown")
		return
	}

	// now we know we are healthy, passive, and none of our peers have assumed active role
	// we can take over as active - this should be idempotent in setting the active role
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("ensure_active_start", "")
		m.checkpointRecording(profile)
	}
	ensureActiveStart := time.Now()
	activationErr := m.ensureActive(profile)
	if profile.activeRecorder != nil {
		duration := time.Since(ensureActiveStart).Round(time.Millisecond)
		if activationErr == nil {
			profile.activeRecorder.AddEvent("confirmed_active", fmt.Sprintf("duration=%s", duration))
			m.finishRecording(profile, "became_active", fromNode, m.cfg.Validator.Name)
		} else {
			profile.activeRecorder.AddEvent("ensure_active_failed", fmt.Sprintf("duration=%s error=%s", duration, activationErr))
			m.finishRecordingOutcome(profile, recording.Outcome{Result: "activation_failed", FromNode: fromNode, ToNode: "unknown", Error: activationErr.Error()})
		}
	}
}

// isLocalEligible reports whether the local validator may be promoted in the current consensus
// phase. When it may not, the veto is logged, counted and recorded, and the recording finished.
// stage names the point of the takeover the check ran at.
func (m *Manager) isLocalEligible(profile *profileRuntime, fromNode, stage string) bool {
	eligible, reason := m.detector.LocalEligibility()
	if eligible {
		return true
	}
	profile.logger.Error("we have not migrated to the cluster's Alpenglow genesis - unable to become active in failover",
		"reason", reason,
		"cluster_genesis_slot", m.detector.View().GenesisSlot,
		"stage", stage,
	)
	m.metrics.IncFailoverVeto(reason)
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("veto_local_genesis", fmt.Sprintf("reason=%s stage=%s", reason, stage))
	}
	m.finishRecording(profile, "aborted_local_not_migrated", fromNode, "unknown")
	return false
}

// refreshConsensus updates the consensus phase and hands it to the gossip state, so the next
// gossip Refresh judges voting evidence by the rules of that phase.
func (m *Manager) refreshConsensus() {
	m.applyConsensusRefresh(m.detector.Refresh)
}

// refreshConsensusNow is refreshConsensus without waiting for the detection interval.
func (m *Manager) refreshConsensusNow() {
	m.applyConsensusRefresh(m.detector.RefreshNow)
}

func (m *Manager) applyConsensusRefresh(refresh func(context.Context)) {
	previous := m.detector.Phase()
	refresh(m.ctx)
	view := m.detector.View()
	for _, profile := range m.profiles {
		profile.gossipState.SetConsensusView(view)
		if view.Phase != previous && profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("consensus_phase_changed", fmt.Sprintf("from=%s to=%s genesis_slot=%d", previous, view.Phase, view.GenesisSlot))
			m.checkpointRecording(profile)
		}
	}
}

// observeAlpenglowEvidence counts verdicts and vetoed samples, and adds a timeline event to the open recording
// when the active's vote lag first exceeds the threshold. A vetoed sample closes the recording
// itself, see observeRecording.
func (m *Manager) observeAlpenglowEvidence(profile *profileRuntime, sample recording.GossipSample) {
	if verdict := profile.gossipState.AlpenglowVerdict(); verdict != "" {
		m.metrics.IncAlpenglowVerdict(verdict)
	}
	if sample.Veto != "" {
		m.metrics.IncFailoverVeto(sample.Veto)
	}
	if profile.activeRecorder != nil && sample.LeaderlessReason == gossip.LeaderlessReasonVoteLag && profile.lastLeaderlessReason != gossip.LeaderlessReasonVoteLag {
		detail := fmt.Sprintf("threshold=%d", m.cfg.Failover.Alpenglow.VoteLagSlotsThreshold)
		if lag, ok := profile.gossipState.ActiveVoteLag(); ok {
			detail = fmt.Sprintf("vote_lag_slots=%d %s", lag, detail)
		}
		profile.activeRecorder.AddEvent("alpenglow_vote_lag_exceeded", detail)
		m.checkpointRecording(profile)
	}
	profile.lastLeaderlessReason = sample.LeaderlessReason
}

// delinquencyBypassAllowed reports whether failover.delinquency_bypass may skip the leaderless
// threshold. It only applies under TowerBFT: in the other phases the sample-by-sample vote
// evidence is either not trusted or already covers delinquency.
func (m *Manager) delinquencyBypassAllowed() bool {
	if m.detector.Phase() == consensus.PhaseTower {
		return true
	}
	if !m.warnedBypassIgnored {
		m.logger.Warn("failover.delinquency_bypass is ignored outside the TowerBFT phase", "phase", m.detector.Phase())
		m.warnedBypassIgnored = true
	}
	return false
}

// observeClusterRPCOutage logs when every cluster RPC endpoint starts and stops failing. It only
// reports: failed polls are already excluded from the failover decisions.
func (m *Manager) observeClusterRPCOutage(failed bool) {
	switch m.clusterRPCOutage.observe(failed) {
	case clusterRPCOutageStarted:
		m.logger.Error("all cluster RPC endpoints are failing - cannot detect a missing active peer or take over until they recover",
			"consecutive_failures", m.clusterRPCOutage.consecutiveFailures,
			"failing_for", m.clusterRPCOutage.duration().Round(time.Second),
		)
	case clusterRPCOutageEnded:
		m.logger.Info("cluster RPC endpoints recovered",
			"outage_duration", m.clusterRPCOutage.duration().Round(time.Second),
		)
	}
}

// demoteIfIsolated runs the passive command when this node is active and has lost the network:
// the cluster RPC has failed for failover.leaderless_samples_threshold consecutive polls and the
// local processed slot has not moved for failover.isolation.local_slot_stall_duration. It
// returns true when it acted. A cluster RPC outage alone, with the local slot still moving,
// leaves the node alone, so an RPC provider outage cannot take a healthy active offline.
func (m *Manager) demoteIfIsolated(profile *profileRuntime) bool {
	if !m.isolation.isolated() {
		return false
	}

	profile.logger.Error("cluster RPC unreachable and our local slot has stopped - we appear to have lost the network, ensuring we are passive",
		"cluster_rpc_failures", m.isolation.clusterRPCFailures,
		"local_slot", m.isolation.localSlot,
		"local_slot_stalled_for", m.isolation.localSlotStalledFor().Round(time.Second),
	)
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("isolation_detected", fmt.Sprintf("cluster_rpc_failures=%d local_slot=%d local_slot_stalled_for=%s",
			m.isolation.clusterRPCFailures, m.isolation.localSlot, m.isolation.localSlotStalledFor().Round(time.Second)))
		m.checkpointRecording(profile)
	}
	m.finishRecording(profile, m.ensurePassive(profile), m.cfg.Validator.Name, "unknown")
	return true
}

// alreadyDemotedWithLocalRPCDown reports whether an earlier demotion succeeded while the local
// validator was not answering RPC, and it still is not. It clears that state once RPC answers,
// so a validator that comes back active is demoted again.
func (m *Manager) alreadyDemotedWithLocalRPCDown() bool {
	if !m.demotedWithLocalRPCDown {
		return false
	}
	if _, err := m.localState.SelfIdentity(); err != nil {
		return true
	}
	m.demotedWithLocalRPCDown = false
	return false
}

// ensurePassive calls a user-specified command that should be idempotent in setting the passive role
// safest thing would be to to ensure validator service always starts with passive identity
// and the failover.passive.command simply retsarts the validator service or waits for it to start up.
// It returns the recording outcome: outcomeDemotedPassive, outcomeDemotedValidatorDown or
// outcomeDemotionFailed.
func (m *Manager) ensurePassive(profile *profileRuntime) (outcome string) {
	if profile.Name != "" {
		identity, err := m.localState.SelfIdentity()
		if err != nil || identity != profile.ActivePubkey() {
			profile.logger.Warn("local RPC did not confirm the profile still holds the active identity", "identity", identity, "error", err)
			return outcomeDemotionFailed
		}
	}

	var err error
	passivePubkey := m.cfg.Validator.Identities.PassivePubkey()
	profile.logger.Info("becoming passive", "pubkey", passivePubkey)
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("ensure_passive_start", fmt.Sprintf("passive_pubkey=%s", passivePubkey))
		m.checkpointRecording(profile)
	}

	// Update failover status in cache
	m.setTransitionStatus(profile, constants.StatusBecomingPassive)

	// run pre hooks
	if len(profile.passiveRole.Hooks.Pre) > 0 {
		started := time.Now()
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("pre_passive_hooks_start", fmt.Sprintf("count=%d", len(profile.passiveRole.Hooks.Pre)))
		}
		profile.logger.Debug("running pre-passive hooks")
		err = profile.passiveRole.Hooks.RunPre(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "pre-passive",
			},
		})
		m.recordStep(profile, "pre_passive_hooks_complete", started, err)
	}
	if err != nil {
		profile.logger.Error("failed to run pre-passive hooks", "error", err)
		return outcomeDemotionFailed
	}

	// run passive command
	profile.logger.Debug("running passive command")
	commandStarted := time.Now()
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("passive_command_start", "")
		m.checkpointRecording(profile)
	}
	err = profile.passiveRole.RunCommand(config.RoleCommandRunOptions{
		DryRun:       m.cfg.Failover.DryRun,
		LoggerPrefix: m.logPrefix,
		LoggerArgs: []any{
			"failover_stage", constants.RoleNamePassive,
			"passive_pubkey", passivePubkey,
		},
	})
	m.recordStep(profile, "passive_command_complete", commandStarted, err)
	if err != nil {
		profile.logger.Warn("failed to run passive command", "error", err)
		return outcomeDemotionFailed
	}

	// run post hooks
	if len(profile.passiveRole.Hooks.Post) > 0 {
		profile.logger.Debug("running post-passive hooks")
		started := time.Now()
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("post_passive_hooks_start", fmt.Sprintf("count=%d", len(profile.passiveRole.Hooks.Post)))
		}
		profile.passiveRole.Hooks.RunPost(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "post-passive",
			},
		})
		m.recordStep(profile, "post_passive_hooks_complete", started, nil)
	}

	// check to ensure the call to the failover.passive.command was successful
	identity, identityErr := m.localState.SelfIdentity()
	if identityErr != nil {
		// The passive command succeeded, so a local RPC that does not answer means the validator
		// is stopped or restarting, not that it is still active.
		profile.logger.Warn("passive command succeeded but the local validator is not answering RPC - treating it as stopped",
			"passive_pubkey", passivePubkey,
			"error", identityErr,
		)
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("passive_identity_unverified", "local_rpc=unreachable")
		}
		m.demotedWithLocalRPCDown = true
		return outcomeDemotedValidatorDown
	}
	if identity == profile.ActivePubkey() || profile.Name != "" && identity != passivePubkey {
		profile.logger.Error("passive command succeeded but local RPC did not confirm the passive identity",
			"passive_pubkey", passivePubkey, "runtime_pubkey", identity,
		)
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("passive_identity_unconfirmed", fmt.Sprintf("expected=%s", passivePubkey))
		}
		return outcomeDemotionFailed
	}

	profile.logger.Debug("we are confirmed to be passive as reported by local rpc", "passive_pubkey", passivePubkey)
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("passive_identity_confirmed", fmt.Sprintf("pubkey=%s", passivePubkey))
		m.checkpointRecording(profile)
	}

	// refresh gossip state to warn if we are in gossip but not passive
	profile.gossipState.Refresh()

	// if we are not in gossip, warn - we may be starting up or dropped from the network
	if m.isSelfNotInGossip(profile) {
		profile.logger.Warn("we are not in gossip after becoming passive", "passive_pubkey", passivePubkey)
		return outcomeDemotedPassive
	}

	// if we are in gossip but not passive, show error - failover.passive.command has likely fucked up
	identity, identityErr = m.localState.SelfIdentity()
	if identityErr != nil || identity == profile.ActivePubkey() || profile.Name != "" && identity != passivePubkey {
		profile.logger.Error("we are in gossip but not passive - this should not happen check failover.passive.command logic", "passive_pubkey", passivePubkey)
		return outcomeDemotionFailed
	}

	// we are passive by local rpc and in gossip
	profile.logger.Info("we are confirmed to be passive", "passive_pubkey", passivePubkey)
	return outcomeDemotedPassive
}

// ensureActive makes the node active - this should be idempotent in setting the  active role
// safest thing would be to to ensure validator service alywas starts with passive identity
// and the failover.passive.command simply retsarts the validator service
func (m *Manager) ensureActive(profile *profileRuntime) error {
	var err error
	activePubkey := profile.ActivePubkey()
	profile.logger.Info("becoming active", "pubkey", activePubkey)

	// Update failover status in cache
	m.setTransitionStatus(profile, constants.StatusBecomingActive)

	// run pre hooks
	if len(profile.activeRole.Hooks.Pre) > 0 {
		started := time.Now()
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("pre_active_hooks_start", fmt.Sprintf("count=%d", len(profile.activeRole.Hooks.Pre)))
		}
		profile.logger.Debug("running pre-active hooks")
		err = profile.activeRole.Hooks.RunPre(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "pre-active",
			},
		})
		m.recordStep(profile, "pre_active_hooks_complete", started, err)
	}
	if err != nil {
		profile.logger.Error("failed to run pre-active hooks", "error", err)
		return fmt.Errorf("pre-active hooks: %w", err)
	}

	// run active command
	profile.logger.Debug("running active command")
	commandStarted := time.Now()
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("active_command_start", "")
		m.checkpointRecording(profile)
	}
	err = profile.activeRole.RunCommand(config.RoleCommandRunOptions{
		DryRun:       m.cfg.Failover.DryRun,
		LoggerPrefix: m.logPrefix,
		LoggerArgs: []any{
			"failover_stage", constants.RoleNameActive,
			"active_pubkey", activePubkey,
		},
	})
	m.recordStep(profile, "active_command_complete", commandStarted, err)
	if err != nil {
		profile.logger.Warn("failed to run active command", "error", err)
		return fmt.Errorf("active command: %w", err)
	}

	// run post hooks
	if len(profile.activeRole.Hooks.Post) > 0 {
		profile.logger.Debug("running post-active hooks")
		started := time.Now()
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("post_active_hooks_start", fmt.Sprintf("count=%d", len(profile.activeRole.Hooks.Post)))
		}
		profile.activeRole.Hooks.RunPost(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "post-active",
			},
		})
		m.recordStep(profile, "post_active_hooks_complete", started, nil)
	}

	// check to ensure the call to the failover.active.command was successful
	identity, identityErr := m.localState.SelfIdentity()
	if identityErr != nil || identity != activePubkey {
		profile.logger.Error("this node is not active as reported by local rpc - unable to become active in failover",
			"active_pubkey", activePubkey,
		)
		if profile.activeRecorder != nil {
			profile.activeRecorder.AddEvent("active_identity_unconfirmed", fmt.Sprintf("expected=%s", activePubkey))
		}
		if identityErr != nil {
			return fmt.Errorf("confirm active identity %s: %w", activePubkey, identityErr)
		}
		return fmt.Errorf("local RPC did not confirm active identity: expected=%s actual=%s", activePubkey, identity)
	}

	profile.logger.Info("we are confirmed to be active", "active_pubkey", activePubkey)
	if profile.activeRecorder != nil {
		profile.activeRecorder.AddEvent("active_identity_confirmed", fmt.Sprintf("pubkey=%s", activePubkey))
		m.checkpointRecording(profile)
	}
	return nil
}

// startHealthyTracker starts a goroutine that samples the local validator health on its own
// independent interval. This decouples health streak tracking from the gossip poll loop,
// ensuring the streak timer is not skewed by the latency of gossip RPC calls.
func (m *Manager) startHealthyTracker() {
	m.logger.Info("monitoring local state",
		"poll_interval", m.cfg.Failover.SelfHealthy.PollIntervalDuration,
		"minimum_healthy_duration", m.cfg.Failover.SelfHealthy.MinimumDuration,
	)
	ticker := time.NewTicker(m.cfg.Failover.SelfHealthy.PollIntervalDuration)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.localState.SampleSelf()
			}
		}
	}()
}

// isSelfInGossip checks if the validator is in the gossip state
func (m *Manager) isSelfInGossip(profile *profileRuntime) (isInGossip bool) {
	return profile.gossipState.HasIP(m.peerSelf.IP) || m.isSelfNotVotingActive(profile)
}

// isSelfNotVotingActive checks if we hold the active identity in gossip while our votes are not landing
func (m *Manager) isSelfNotVotingActive(profile *profileRuntime) bool {
	peer, ok := profile.gossipState.NotVotingActivePeer()
	return ok && peer.IP == m.peerSelf.IP
}

// isSelfNotInGossip checks if the validator is not in the gossip state
func (m *Manager) isSelfNotInGossip(profile *profileRuntime) (isNotInGossip bool) {
	return !m.isSelfInGossip(profile)
}

// selfGossipPubkey returns the pubkey of the validator in gossip
func (m *Manager) selfGossipPubkey(profile *profileRuntime) (pubkey string) {
	for _, peer := range profile.gossipState.GetPeerStates() {
		if peer.IP == m.peerSelf.IP {
			return peer.Pubkey
		}
	}
	return ""
}

// refreshMetrics updates the cache with current state
func (m *Manager) refreshMetrics() (string, *profileRuntime) {
	m.logger.Debug("refreshing metrics")

	// The legacy gauges use the occupied profile, or the first profile by priority.
	runtimePubkey, _ := m.localState.SelfIdentity()
	var active *profileRuntime
	focus := m.profiles[0]
	for _, profile := range m.profiles {
		if runtimePubkey != "" && runtimePubkey == profile.ActivePubkey() {
			active, focus = profile, profile
			break
		}
	}
	role := m.profileRole(focus, runtimePubkey)
	var status string

	if m.localState.IsSelfHealthy() {
		status = constants.StatusHealthy
	} else {
		status = constants.StatusUnhealthy
	}

	// Get peer count and self in gossip status
	peerCount := len(focus.gossipState.GetPeerStates())
	selfInGossip := m.isSelfInGossip(focus)

	// Update cache with current state
	view := m.detector.View()
	state := cache.State{
		ValidatorName:        m.cfg.Validator.Name,
		PublicIP:             m.peerSelf.IP,
		Role:                 role,
		RuntimePubkey:        runtimePubkey,
		Status:               status,
		PeerCount:            peerCount,
		SelfInGossip:         selfInGossip,
		FailoverStatus:       constants.StatusIdle,
		ClusterRPCFailures:   m.clusterRPCOutage.consecutiveFailures,
		ClusterRPCOutage:     m.clusterRPCOutage.active,
		ConsensusPhase:       view.Phase.String(),
		AlpenglowGenesisSlot: view.GenesisSlot,
	}
	if lag, ok := focus.gossipState.ActiveVoteLag(); ok {
		state.ActiveVoteLagSlots = &lag
	}
	if signals, ok := focus.gossipState.AlpenglowSignals(); ok {
		localGenesisMatch, _ := m.detector.LocalEligibility()
		state.Alpenglow = &cache.AlpenglowState{
			LocalGenesisMatch:      localGenesisMatch,
			FinalizedSlot:          signals.FinalizedSlot,
			ClusterLive:            signals.ClusterLive,
			NetworkStakeRatio:      signals.NetworkStakeRatio,
			NetworkStakeRatioKnown: signals.NetworkStakeRatioKnown,
		}
	}

	if focus.Name != "" {
		state.Profiles = make(map[string]cache.ProfileState, len(m.profiles))
		for _, profile := range m.profiles {
			info := cache.ProfileState{
				Role:           m.profileRole(profile, runtimePubkey),
				PeerCount:      len(profile.gossipState.GetPeerStates()),
				SelfInGossip:   m.isSelfInGossip(profile),
				FailoverStatus: constants.StatusIdle,
			}
			if lag, ok := profile.gossipState.ActiveVoteLag(); ok {
				info.ActiveVoteLagSlots = &lag
			}
			state.Profiles[profile.Name] = info
		}
	}

	m.cache.UpdateState(state)

	// Refresh metrics from cache
	m.metrics.RefreshMetrics()

	m.logger.Debug("metrics refreshed",
		"role", role,
		"status", status,
		"peer_count", peerCount,
		"self_in_gossip", selfInGossip,
	)
	return runtimePubkey, active
}

// delayTakeoverAsActive introduces a delay when there are multiple peers
// to safeguard against multiple nodes trying to become active at the same time.
// Returns (delayApplied, error): delayApplied is true only when the node actually slept
// (i.e. rank > 0). Rank-0 nodes return false so the caller can skip the post-delay
// re-validation gossip refresh - no time elapsed, so no peer could have taken over.
func (m *Manager) delayTakeoverAsActive(profile *profileRuntime) (delayApplied bool, err error) {
	// peerCount includes ourselves, so if we are the only peer, we don't need to delay
	peerCount := profile.gossipState.PeerCount()
	if peerCount == 0 {
		return false, fmt.Errorf("no peers found - unable to delay takeover")
	}

	// Determine self rank: prefer explicit config priorities, fall back to IP-based ordering.
	// Config-based ranking is stable and does not shift when a peer briefly drops from gossip.
	rankedPeerIPs := profile.peerRanks
	rankingSource := "config priority"
	if rankedPeerIPs == nil {
		rankedPeerIPs = profile.gossipState.PeerIPRankMap()
		rankingSource = "IP address"
	}

	selfPeerRank, selfInRankedPeerIPs := rankedPeerIPs[m.peerSelf.IP]

	if !selfInRankedPeerIPs {
		return false, fmt.Errorf("unable to find this node's IP %s in the %s-ranked list of peers: %v", m.peerSelf.IP, rankingSource, rankedPeerIPs)
	}

	if selfPeerRank == 0 {
		profile.logger.Debug(fmt.Sprintf("this node is ranked 0/%d by %s - no takeover delay", peerCount, rankingSource))
		return false, nil
	}

	// peers with ranks 1 and over have a deterministic delay of rank*poll_interval_duration
	delay := time.Duration(selfPeerRank) * m.cfg.Failover.PollIntervalDuration

	profile.logger.Warn(fmt.Sprintf("delaying takeover by %s (<rank %d (of %d peers) by %s> * <%s poll_interval_duration>) to avoid race condition with higher ranked peer", delay, selfPeerRank, peerCount, rankingSource, m.cfg.Failover.PollIntervalDuration))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-m.ctx.Done():
		return false, m.ctx.Err()
	case <-timer.C:
		profile.logger.Warn("takeover delay complete")
		return true, nil
	}
}
