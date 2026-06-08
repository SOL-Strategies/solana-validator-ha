package ha

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/cache"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/constants"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
	"github.com/sol-strategies/solana-validator-ha/internal/local"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
	"github.com/sol-strategies/solana-validator-ha/internal/prometheus"
	"github.com/sol-strategies/solana-validator-ha/internal/recording"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	"github.com/sol-strategies/solana-validator-ha/internal/updater"
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
	profiles        map[string]*profileRuntime
	localState      *local.State
	getPublicIPFunc func() (string, error)
	peerCount       int
	initialized     bool
	logPrefix       string
	// profileFailoverStatuses holds transient per-profile status for the next metrics refresh.
	profileFailoverStatuses map[string]string
	// recordingOutputDir is the resolved output directory for failover recordings (empty if disabled).
	recordingOutputDir string
}

type profileRuntime struct {
	name        string
	cfg         config.Profile
	gossipState *gossip.State
	ring        *recording.Ring
}

type failoverCandidate struct {
	profile  *profileRuntime
	rec      *recording.Recorder
	fromNode string
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
		cfg:                     opts.Cfg,
		version:                 opts.Version,
		metrics:                 metrics,
		cache:                   cache,
		logger:                  logging.New(opts.Cfg.Validator.Name, "ha_manager"),
		ctx:                     ctx,
		cancel:                  cancel,
		peerCount:               len(opts.Cfg.Profiles),
		profiles:                make(map[string]*profileRuntime),
		profileFailoverStatuses: make(map[string]string),
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

	m.peerSelf = &config.Peer{
		Name: m.cfg.Validator.Name,
		IP:   publicIP,
	}
	if err := m.cfg.Profiles.ValidateSelfPeer(m.cfg.Validator.Name, publicIP); err != nil {
		return err
	}

	// initialize
	m.logger.Info("initializing",
		"version", m.version,
		"public_ip", publicIP,
		"cluster_rpc_urls", m.cfg.Cluster.RPCURLs,
		"validator_rpc_url", m.cfg.Validator.RPCURL,
		"passive_pubkey", m.cfg.Validator.Identities.PassivePubkey(),
		"profiles_count", len(m.cfg.Profiles.Enabled()),
		"failover_dry_run", m.cfg.Failover.DryRun,
		"prometheus_port", m.cfg.Prometheus.Port,
		"health_check_port", m.cfg.Prometheus.HealthCheckPort,
	)

	clusterRPC := rpc.NewClient(m.logPrefix, m.cfg.Cluster.RPCURLs...).
		WithTimeout(m.cfg.Cluster.RPCTimeoutDuration).
		WithCooldown(m.cfg.Cluster.RPCURLCooldownDuration)
	knownActivePubkeys := m.cfg.Profiles.ActivePubkeyProfileMap()
	activePubkeysByProfile := make(map[string]string, len(knownActivePubkeys))

	for _, profile := range m.cfg.Profiles.Enabled() {
		activePubkeysByProfile[profile.Name] = profile.ActivePubkey()
		m.logger.Info("initializing profile",
			"profile", profile.Name,
			"profile_priority", *profile.Priority,
			"active_pubkey", profile.ActivePubkey(),
			"vote_pubkey", profile.VotePubkeyStr,
			"authorized_voter", profile.AuthorizedVoterPubkey,
			"peers", profile.Peers.String(),
		)
		m.profiles[profile.Name] = &profileRuntime{
			name: profile.Name,
			cfg:  profile,
			gossipState: gossip.NewState(gossip.Options{
				ClusterRPC:                     clusterRPC,
				ActivePubkey:                   profile.ActivePubkey(),
				VotePubkey:                     profile.VotePubkeyStr,
				KnownActivePubkeys:             knownActivePubkeys,
				ConfigPeers:                    profile.Peers,
				DelinquentSlotDistanceOverride: m.cfg.Failover.DelinquentSlotDistanceOverride,
				SelfIP:                         m.peerSelf.IP,
				LogPrefix:                      m.logPrefix + " " + profile.Name,
			}),
			ring: &recording.Ring{},
		}
	}

	// create local state
	m.logger.Debug("creating local state")
	m.localState = local.NewState(local.Options{
		RPC:                    rpc.NewClient(m.logPrefix, m.cfg.Validator.RPCURL),
		Cfg:                    m.cfg.Failover.SelfHealthy,
		PassivePubkey:          m.cfg.Validator.Identities.PassivePubkey(),
		ActivePubkeysByProfile: activePubkeysByProfile,
		Ctx:                    m.ctx,
		LogPrefix:              m.logPrefix,
	})

	// resolve recording output dir once so it is ready when a failover fires
	if m.cfg.Failover.Recording.Enabled {
		m.recordingOutputDir = m.cfg.Failover.Recording.ResolvedOutputDir(m.cfg.File)
	}

	m.logger.Debug("initialized")
	m.initialized = true
	return nil
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

	// initial gossip state population
	for _, pr := range m.sortedProfiles() {
		pr.gossipState.Refresh()
	}

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
			if fastPolling && m.anyProfileNeedsConfirmationPoll() {
				m.logger.Warn("leaderless samples approaching threshold - switching to confirmation poll interval",
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
			if !m.anyProfileNeedsConfirmationPoll() {
				return nil
			}
		}
	}
}

func (m *Manager) anyProfileNeedsConfirmationPoll() bool {
	for _, pr := range m.profiles {
		if pr.gossipState.LeaderlessSamplesCount == m.cfg.Failover.LeaderlessSamplesThreshold-1 {
			return true
		}
	}
	return false
}

// buildGossipSample converts the current gossip state into a recording.GossipSample.
// It must be called immediately after a gossipState.Refresh() while the state is fresh.
func (m *Manager) buildGossipSample(pr *profileRuntime) recording.GossipSample {
	peerStates := pr.gossipState.GetPeerStates()
	peers := make([]recording.PeerSnapshot, 0, len(pr.cfg.Peers))

	for name, cfgPeer := range pr.cfg.Peers {
		snap := recording.PeerSnapshot{Name: name, IP: cfgPeer.IP}
		if ps, ok := peerStates[name]; ok {
			snap.Pubkey = ps.Pubkey
			if ps.Role == "busy" {
				snap.Role = "busy"
				snap.BusyProfile = ps.BusyProfile
			} else if ps.LastSeenActive {
				snap.Role = "active"
			} else {
				snap.Role = "passive"
			}
			// attach slot-distance detail to the peer that triggered delinquency
			if pr.gossipState.ActivePeerIsDelinquent() && ps.LastSeenActive {
				if d := pr.gossipState.GetDelinquencyDetail(); d != nil {
					snap.LastVoteSlot = &d.LastVoteSlot
					snap.CurrentSlot = &d.CurrentSlot
					snap.SlotDistance = &d.SlotDistance
				}
			}
		} else {
			snap.Role = "missing"
		}
		peers = append(peers, snap)
	}

	return recording.GossipSample{
		SampledAt:              pr.gossipState.PeerStatesRefreshedAt,
		Peers:                  peers,
		LeaderlessSamplesCount: pr.gossipState.LeaderlessSamplesCount,
		ActivePeerDelinquent:   pr.gossipState.ActivePeerIsDelinquent(),
		RPCError:               pr.gossipState.LastRefreshHadRPCError(),
	}
}

// newRecorder creates a Recorder seeded with node/config context and the current ring snapshot.
func (m *Manager) newRecorder(pr *profileRuntime) *recording.Recorder {
	node := recording.NodeInfo{
		Name:          m.cfg.Validator.Name,
		IP:            m.peerSelf.IP,
		Profile:       pr.name,
		ActivePubkey:  pr.cfg.ActivePubkey(),
		PassivePubkey: m.cfg.Validator.Identities.PassivePubkey(),
	}

	cfg := recording.ConfigSnapshot{
		Profile:                            pr.name,
		VotePubkey:                         pr.cfg.VotePubkeyStr,
		AuthorizedVoterPubkey:              pr.cfg.AuthorizedVoterPubkey,
		PollIntervalDuration:               m.cfg.Failover.PollIntervalDuration.String(),
		LeaderlessSamplesThreshold:         m.cfg.Failover.LeaderlessSamplesThreshold,
		LeaderlessConfirmationPollDuration: m.cfg.Failover.LeaderlessConfirmationPollDuration.String(),
		DelinquencyBypass:                  m.cfg.Failover.DelinquencyBypass,
	}
	if m.cfg.Failover.DelinquentSlotDistanceOverride.Enabled {
		v := m.cfg.Failover.DelinquentSlotDistanceOverride.Value
		cfg.DelinquentSlotDistanceOverride = &v
	}

	return recording.New(node, cfg, time.Now().UTC(), pr.ring.Snapshot())
}

// ensureHAState evaluates all enabled profiles and executes at most one failover.
func (m *Manager) ensureHAState() {
	m.logger.Debug("ensuring HA")

	candidates := []failoverCandidate{}
	for _, pr := range m.sortedProfiles() {
		if candidate := m.evaluateProfile(pr); candidate != nil {
			candidates = append(candidates, *candidate)
		}
	}

	if len(candidates) > 1 {
		for _, candidate := range candidates[1:] {
			m.logger.Warn("profile failover candidate deferred by profile priority",
				"profile", candidate.profile.name,
				"selected_profile", candidates[0].profile.name,
			)
			m.setProfileFailoverStatus(candidate.profile, "aborted_profile_priority")
			if candidate.rec != nil {
				candidate.rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
					Result: "aborted_profile_priority", FromNode: candidate.fromNode, ToNode: "unknown",
				})
			}
		}
	}

	m.refreshMetrics()
	if len(candidates) == 0 {
		return
	}

	m.executeFailover(candidates[0])
}

func (m *Manager) evaluateProfile(pr *profileRuntime) *failoverCandidate {
	m.logger.Debug("ensuring profile HA", "profile", pr.name)

	pr.gossipState.Refresh()
	pr.ring.Add(m.buildGossipSample(pr))

	if pr.gossipState.HasConfigUndeclaredActivePeer() {
		configUndeclaredActivePeer := pr.gossipState.GetConfigUndeclaredActivePeer()
		m.logger.Warn("active peer found not declared in HA profile config - no failover required, but should be added to profiles.<name>.peers",
			"profile", pr.name,
			"ip", configUndeclaredActivePeer.IP,
			"pubkey", configUndeclaredActivePeer.Pubkey,
		)
		return nil
	}

	var rec *recording.Recorder
	if !pr.gossipState.LeaderlessSamplesExceedsThreshold(m.cfg.Failover.LeaderlessSamplesThreshold) {
		if m.cfg.Failover.DelinquencyBypass && pr.gossipState.ActivePeerIsDelinquent() {
			m.logger.Error("active peer declared delinquent by network - bypassing leaderless sample threshold and triggering failover (delinquency_bypass enabled)",
				"profile", pr.name,
			)
			if m.cfg.Failover.Recording.Enabled {
				rec = m.newRecorder(pr)
				rec.AddEvent("bypass_triggered", fmt.Sprintf("leaderless_count=%d threshold=%d",
					pr.gossipState.LeaderlessSamplesCount, m.cfg.Failover.LeaderlessSamplesThreshold))
			}
		} else {
			m.logger.Debug("active peer found - no failover required", "profile", pr.name)
			return nil
		}
	} else {
		m.logger.Error(fmt.Sprintf("no active peer found in the last %d samples - failover required", pr.gossipState.LeaderlessSamplesCount),
			"profile", pr.name,
		)
		if m.cfg.Failover.Recording.Enabled {
			rec = m.newRecorder(pr)
			rec.AddEvent("threshold_reached", fmt.Sprintf("leaderless_count=%d threshold=%d",
				pr.gossipState.LeaderlessSamplesCount, m.cfg.Failover.LeaderlessSamplesThreshold))
		}
	}

	fromNode := pr.gossipState.GetLastActivePeer().Name
	if fromNode == "" {
		fromNode = "unknown"
	}

	occupancy := m.localState.Occupancy()
	if m.isSelfNotInGossip(pr) {
		if pr.gossipState.LastRefreshHadRPCError() {
			if occupancy.Status == local.OccupancyActive && occupancy.Profile == pr.name {
				m.logger.Error("we do not appear in gossip due to RPC error (possible network connectivity issue) - ensuring we are passive",
					"profile", pr.name,
				)
				m.ensurePassive(pr)
			}
			return nil
		}

		if !pr.gossipState.HasAvailablePeers(m.peerSelf.IP) {
			m.logger.Warn("we do not appear in gossip and no available backup peers are visible - staying active/passive and alerting",
				"profile", pr.name,
			)
			m.setProfileFailoverStatus(pr, "aborted_no_available_backup")
			if rec != nil {
				rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
					Result: "aborted_no_available_backup", FromNode: fromNode, ToNode: "unknown",
				})
			}
			return nil
		}

		if occupancy.Status == local.OccupancyActive && occupancy.Profile == pr.name {
			m.logger.Error("we do not appear in gossip but available peers are visible - ensuring we are passive so a peer can take over",
				"profile", pr.name,
			)
			m.ensurePassive(pr)
		}
		return nil
	}
	m.logger.Debug("we are in gossip", "profile", pr.name, "pubkey", m.selfGossipPubkey(pr), "public_ip", m.peerSelf.IP)

	if !m.localState.IsSelfHealthy() {
		m.logger.Error("we are not healthy - unable to become active in failover", "profile", pr.name)
		m.setProfileFailoverStatus(pr, "aborted_not_healthy")
		if rec != nil {
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "aborted_not_healthy", FromNode: fromNode, ToNode: "unknown",
			})
		}
		return nil
	}

	if !m.localState.IsSelfHealthyLongEnough() {
		m.logger.Warn("not healthy for long enough to be a failover candidate - standing by",
			"profile", pr.name,
			"healthy_for", m.localState.SelfHealthyDuration(),
			"minimum_duration", m.cfg.Failover.SelfHealthy.MinimumDuration,
		)
		m.setProfileFailoverStatus(pr, "aborted_not_healthy_long_enough")
		if rec != nil {
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "aborted_not_healthy_long_enough", FromNode: fromNode, ToNode: "unknown",
			})
		}
		return nil
	}

	occupancy = m.localState.Occupancy()
	if occupancy.Status == local.OccupancyActive && occupancy.Profile == pr.name {
		m.logger.Warn("we are already active - nothing to do", "profile", pr.name)
		return nil
	}
	if occupancy.Status == local.OccupancyActive {
		m.logger.Warn("local validator is already active for another profile - unable to become active",
			"profile", pr.name,
			"busy_profile", occupancy.Profile,
		)
		m.setProfileFailoverStatus(pr, "aborted_local_busy")
		if rec != nil {
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "aborted_local_busy", FromNode: fromNode, ToNode: occupancy.Profile,
			})
		}
		return nil
	}
	if occupancy.Status != local.OccupancyFree {
		m.logger.Error("local validator identity is unknown - unable to become active",
			"profile", pr.name,
			"identity", occupancy.Pubkey,
		)
		m.setProfileFailoverStatus(pr, "aborted_unknown_occupancy")
		if rec != nil {
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "aborted_unknown_occupancy", FromNode: fromNode, ToNode: "unknown",
			})
		}
		return nil
	}

	return &failoverCandidate{profile: pr, rec: rec, fromNode: fromNode}
}

func (m *Manager) executeFailover(candidate failoverCandidate) {
	pr := candidate.profile
	rec := candidate.rec
	fromNode := candidate.fromNode

	delayStart := time.Now()
	delayApplied, err := m.delayTakeoverAsActive(pr)
	if err != nil {
		m.logger.Error(err.Error())
		m.setProfileFailoverStatus(pr, "aborted_delay_error")
		if rec != nil {
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "aborted_delay_error", FromNode: fromNode, ToNode: "unknown",
			})
		}
		return
	}
	if rec != nil {
		if delayApplied {
			rec.AddEvent("delay_applied", fmt.Sprintf("duration=%s", time.Since(delayStart).Round(time.Millisecond)))
		} else {
			rec.AddEvent("rank_0_no_delay", "")
		}
	}

	if delayApplied {
		pr.gossipState.Refresh()
		if rec != nil {
			rec.AddSample(m.buildGossipSample(pr))
			rec.AddEvent("revalidation_refresh", "")
		}
	}

	if pr.gossipState.HasConfigUndeclaredActivePeer() {
		configUndeclaredActivePeer := pr.gossipState.GetConfigUndeclaredActivePeer()
		m.logger.Warn("active peer found not declared in HA profile config (post-delay re-check) - aborting takeover, should be added to profiles.<name>.peers",
			"profile", pr.name,
			"ip", configUndeclaredActivePeer.IP,
			"pubkey", configUndeclaredActivePeer.Pubkey,
		)
		m.setProfileFailoverStatus(pr, "aborted_config_undeclared_active_peer")
		return
	}

	if delayApplied && pr.gossipState.LeaderlessSamplesCount == 0 {
		activePeerState, err := pr.gossipState.GetActivePeer()
		if err != nil {
			m.logger.Warn("active peer appeared during takeover delay but could not be identified in state - aborting takeover", "error", err)
			m.setProfileFailoverStatus(pr, "aborted_peer_took_over")
			if rec != nil {
				rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
					Result: "aborted_peer_took_over", FromNode: fromNode, ToNode: "unknown",
				})
			}
			return
		}
		m.logger.Warn(fmt.Sprintf("peer %s became active during takeover delay - aborting takeover", activePeerState.Name),
			"ip", activePeerState.IP,
			"pubkey", activePeerState.Pubkey,
			"seen_at", activePeerState.LastSeenAtString(),
		)
		m.setProfileFailoverStatus(pr, "aborted_peer_took_over")
		if rec != nil {
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "aborted_peer_took_over", FromNode: fromNode, ToNode: activePeerState.Name,
			})
		}
		return
	}

	if rec != nil {
		rec.AddEvent("ensure_active_start", "")
	}
	ensureActiveStart := time.Now()
	m.ensureActive(pr)
	if rec != nil {
		duration := time.Since(ensureActiveStart).Round(time.Millisecond)
		if m.localState.IsSelfActive(pr.name) {
			rec.AddEvent("confirmed_active", fmt.Sprintf("duration=%s", duration))
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "became_active", FromNode: fromNode, ToNode: m.cfg.Validator.Name,
			})
		} else {
			rec.AddEvent("ensure_active_failed", fmt.Sprintf("duration=%s", duration))
			rec.WriteAsync(m.recordingOutputDir, recording.Outcome{
				Result: "became_active_unconfirmed", FromNode: fromNode, ToNode: m.cfg.Validator.Name,
			})
		}
	}
}

func (m *Manager) roleTemplateData(pr *profileRuntime) config.RoleCommandTemplateData {
	priority := 0
	if pr != nil && pr.cfg.Priority != nil {
		priority = *pr.cfg.Priority
	}
	data := config.RoleCommandTemplateData{
		ProfilePriority:            priority,
		PassiveIdentityKeypairFile: m.cfg.Validator.Identities.PassiveKeyPairFile,
		PassiveIdentityPubkey:      m.cfg.Validator.Identities.PassivePubkey(),
		SelfName:                   m.cfg.Validator.Name,
	}
	if pr != nil {
		data.ProfileName = pr.name
		data.VoteAccountPubkey = pr.cfg.VotePubkeyStr
		data.AuthorizedVoterPubkey = pr.cfg.AuthorizedVoterPubkey
		data.ActiveIdentityKeypairFile = pr.cfg.Identities.ActiveKeyPairFile
		data.ActiveIdentityPubkey = pr.cfg.ActivePubkey()
	}
	return data
}

func (m *Manager) renderedRole(role config.Role, pr *profileRuntime) (config.Role, error) {
	return role.RenderedCopy(m.roleTemplateData(pr))
}

// ensurePassive calls a user-specified command that should be idempotent in setting the shared passive role.
func (m *Manager) ensurePassive(pr *profileRuntime) {
	passivePubkey := m.cfg.Validator.Identities.PassivePubkey()
	m.logger.Info("becoming passive", "profile", pr.name, "pubkey", passivePubkey)

	role, err := m.renderedRole(m.cfg.Failover.Passive, pr)
	if err != nil {
		m.logger.Error("failed to render passive command", "profile", pr.name, "error", err)
		return
	}

	state := m.cache.GetState()
	state.FailoverStatus = constants.StatusBecomingPassive
	m.setProfileFailoverStatus(pr, constants.StatusBecomingPassive)
	setCachedProfileFailoverStatus(&state, pr, constants.StatusBecomingPassive)
	m.cache.UpdateState(state)

	if len(role.Hooks.Pre) > 0 {
		m.logger.Debug("running pre-passive hooks", "profile", pr.name)
		err = role.Hooks.RunPre(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "pre-passive",
				"profile", pr.name,
			},
		})
	}
	if err != nil {
		m.logger.Error("failed to run pre-passive hooks", "profile", pr.name, "error", err)
		return
	}

	m.logger.Debug("running passive command", "profile", pr.name)
	err = role.RunCommand(config.RoleCommandRunOptions{
		DryRun:       m.cfg.Failover.DryRun,
		LoggerPrefix: m.logPrefix,
		LoggerArgs: []any{
			"failover_stage", constants.RoleNamePassive,
			"profile", pr.name,
			"passive_pubkey", passivePubkey,
		},
	})
	if err != nil {
		m.logger.Warn("failed to run passive command", "profile", pr.name, "error", err)
		return
	}

	if len(role.Hooks.Post) > 0 {
		m.logger.Debug("running post-passive hooks", "profile", pr.name)
		role.Hooks.RunPost(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "post-passive",
				"profile", pr.name,
			},
		})
	}

	if !m.localState.IsSelfPassive() {
		m.logger.Error("we are not passive as reported by local rpc - unable to become active in failover",
			"profile", pr.name,
			"passive_pubkey", passivePubkey,
		)
		return
	}

	m.logger.Debug("we are confirmed to be passive as reported by local rpc", "profile", pr.name, "passive_pubkey", passivePubkey)
	pr.gossipState.Refresh()

	if m.isSelfNotInGossip(pr) {
		m.logger.Warn("we are not in gossip after becoming passive", "profile", pr.name, "passive_pubkey", passivePubkey)
		return
	}

	if !m.localState.IsSelfPassive() {
		m.logger.Error("we are in gossip but not passive - this should not happen check failover.passive.command logic", "profile", pr.name, "passive_pubkey", passivePubkey)
		return
	}

	m.logger.Info("we are confirmed to be passive", "profile", pr.name, "passive_pubkey", passivePubkey)
}

// ensureActive makes the node active for the selected profile.
func (m *Manager) ensureActive(pr *profileRuntime) {
	activePubkey := pr.cfg.ActivePubkey()
	m.logger.Info("becoming active", "profile", pr.name, "pubkey", activePubkey)

	role, err := m.renderedRole(m.cfg.Failover.Active, pr)
	if err != nil {
		m.logger.Error("failed to render active command", "profile", pr.name, "error", err)
		return
	}

	state := m.cache.GetState()
	state.FailoverStatus = constants.StatusBecomingActive
	m.setProfileFailoverStatus(pr, constants.StatusBecomingActive)
	setCachedProfileFailoverStatus(&state, pr, constants.StatusBecomingActive)
	m.cache.UpdateState(state)

	if len(role.Hooks.Pre) > 0 {
		m.logger.Debug("running pre-active hooks", "profile", pr.name)
		err = role.Hooks.RunPre(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "pre-active",
				"profile", pr.name,
			},
		})
	}
	if err != nil {
		m.logger.Error("failed to run pre-active hooks", "profile", pr.name, "error", err)
		return
	}

	m.logger.Debug("running active command", "profile", pr.name)
	err = role.RunCommand(config.RoleCommandRunOptions{
		DryRun:       m.cfg.Failover.DryRun,
		LoggerPrefix: m.logPrefix,
		LoggerArgs: []any{
			"failover_stage", constants.RoleNameActive,
			"profile", pr.name,
			"active_pubkey", activePubkey,
			"vote_pubkey", pr.cfg.VotePubkeyStr,
			"authorized_voter", pr.cfg.AuthorizedVoterPubkey,
		},
	})
	if err != nil {
		m.logger.Warn("failed to run active command", "profile", pr.name, "error", err)
		return
	}

	if len(role.Hooks.Post) > 0 {
		m.logger.Debug("running post-active hooks", "profile", pr.name)
		role.Hooks.RunPost(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "post-active",
				"profile", pr.name,
			},
		})
	}

	if !m.localState.IsSelfActive(pr.name) {
		m.logger.Error("this node is not active as reported by local rpc - unable to become active in failover",
			"profile", pr.name,
			"active_pubkey", activePubkey,
		)
		return
	}

	m.logger.Info("we are confirmed to be active", "profile", pr.name, "active_pubkey", activePubkey)
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
func (m *Manager) isSelfInGossip(pr *profileRuntime) (isInGossip bool) {
	return pr.gossipState.HasIP(m.peerSelf.IP)
}

// isSelfNotInGossip checks if the validator is not in the gossip state
func (m *Manager) isSelfNotInGossip(pr *profileRuntime) (isNotInGossip bool) {
	return !m.isSelfInGossip(pr)
}

// selfGossipPubkey returns the pubkey of the validator in gossip
func (m *Manager) selfGossipPubkey(pr *profileRuntime) (pubkey string) {
	for _, peer := range pr.gossipState.GetPeerStates() {
		if peer.IP == m.peerSelf.IP {
			return peer.Pubkey
		}
	}
	return ""
}

// refreshMetrics updates the cache with current state
func (m *Manager) refreshMetrics() {
	m.logger.Debug("refreshing metrics")

	previousState := m.cache.GetState()

	// Determine global role and status
	var role, status string
	occupancy := m.localState.Occupancy()
	if occupancy.Status == local.OccupancyActive {
		role = constants.RoleNameActive
	} else if occupancy.Status == local.OccupancyFree {
		role = constants.RoleNamePassive
	} else {
		role = constants.RoleNameUnknown
	}

	if m.localState.IsSelfHealthy() {
		status = constants.StatusHealthy
	} else {
		status = constants.StatusUnhealthy
	}
	failoverStatus := m.failoverStatusForMetrics(previousState.FailoverStatus, occupancy)

	peerCount := 0
	selfInGossip := false
	profileStates := map[string]cache.ProfileState{}
	for _, pr := range m.sortedProfiles() {
		ps := cache.ProfileState{
			PeerCount:      len(pr.gossipState.GetPeerStates()),
			SelfInGossip:   pr.gossipState.HasIP(m.peerSelf.IP),
			FailoverStatus: m.profileFailoverStatusForMetrics(pr, occupancy),
		}
		if pr.gossipState.HasActivePeer() {
			ps.Role = constants.RoleNameActive
		} else {
			ps.Role = constants.RoleNamePassive
		}
		profileStates[pr.name] = ps
		peerCount += ps.PeerCount
		selfInGossip = selfInGossip || ps.SelfInGossip
	}

	// Update cache with current state
	state := cache.State{
		ValidatorName:    m.cfg.Validator.Name,
		PublicIP:         m.peerSelf.IP,
		Role:             role,
		Status:           status,
		Occupancy:        string(occupancy.Status),
		OccupancyProfile: occupancy.Profile,
		PeerCount:        peerCount,
		SelfInGossip:     selfInGossip,
		FailoverStatus:   failoverStatus,
		Profiles:         profileStates,
	}

	m.cache.UpdateState(state)

	// Refresh metrics from cache
	m.metrics.RefreshMetrics()
	m.clearTransientProfileFailoverStatuses()

	m.logger.Debug("metrics refreshed",
		"role", role,
		"status", status,
		"peer_count", peerCount,
		"self_in_gossip", selfInGossip,
	)
}

func (m *Manager) failoverStatusForMetrics(previous string, occupancy local.Occupancy) string {
	switch previous {
	case constants.StatusBecomingActive:
		if occupancy.Status == local.OccupancyActive {
			return constants.StatusIdle
		}
		return previous
	case constants.StatusBecomingPassive:
		if occupancy.Status == local.OccupancyFree {
			return constants.StatusIdle
		}
		return previous
	default:
		return constants.StatusIdle
	}
}

func (m *Manager) setProfileFailoverStatus(pr *profileRuntime, status string) {
	if pr == nil || pr.name == "" || status == "" {
		return
	}
	if m.profileFailoverStatuses == nil {
		m.profileFailoverStatuses = make(map[string]string)
	}
	m.profileFailoverStatuses[pr.name] = status
}

func setCachedProfileFailoverStatus(state *cache.State, pr *profileRuntime, status string) {
	if pr == nil || pr.name == "" || status == "" {
		return
	}
	if state.Profiles == nil {
		state.Profiles = map[string]cache.ProfileState{}
	}
	profileState := state.Profiles[pr.name]
	profileState.FailoverStatus = status
	state.Profiles[pr.name] = profileState
}

func (m *Manager) profileFailoverStatusForMetrics(pr *profileRuntime, occupancy local.Occupancy) string {
	status := m.profileFailoverStatuses[pr.name]
	if status == "" {
		return constants.StatusIdle
	}

	switch status {
	case constants.StatusBecomingActive:
		if occupancy.Status == local.OccupancyActive && occupancy.Profile == pr.name {
			return constants.StatusIdle
		}
	case constants.StatusBecomingPassive:
		if occupancy.Status == local.OccupancyFree {
			return constants.StatusIdle
		}
	}
	return status
}

func (m *Manager) clearTransientProfileFailoverStatuses() {
	for profileName, status := range m.profileFailoverStatuses {
		if strings.HasPrefix(status, "aborted_") {
			delete(m.profileFailoverStatuses, profileName)
		}
	}
}

func (m *Manager) sortedProfiles() []*profileRuntime {
	profiles := make([]*profileRuntime, 0, len(m.profiles))
	for _, pr := range m.profiles {
		profiles = append(profiles, pr)
	}
	sort.Slice(profiles, func(i, j int) bool {
		pi := 0
		pj := 0
		if profiles[i].cfg.Priority != nil {
			pi = *profiles[i].cfg.Priority
		}
		if profiles[j].cfg.Priority != nil {
			pj = *profiles[j].cfg.Priority
		}
		if pi == pj {
			return profiles[i].name < profiles[j].name
		}
		return pi < pj
	})
	return profiles
}

// delayTakeoverAsActive introduces a delay when there are multiple peers
// to safeguard against multiple nodes trying to become active at the same time.
// Returns (delayApplied, error): delayApplied is true only when the node actually slept
// (i.e. rank > 0). Rank-0 nodes return false so the caller can skip the post-delay
// re-validation gossip refresh - no time elapsed, so no peer could have taken over.
func (m *Manager) delayTakeoverAsActive(pr *profileRuntime) (delayApplied bool, err error) {
	// peerCount includes ourselves, so if we are the only peer, we don't need to delay
	peerCount := pr.gossipState.PeerCount()
	if peerCount == 0 {
		return false, fmt.Errorf("no peers found - unable to delay takeover")
	}

	// Determine self rank: prefer explicit config priorities, fall back to IP-based ordering.
	// Config-based ranking is stable and does not shift when a peer briefly drops from gossip.
	rankedPeerIPs := pr.cfg.PeerIPPriorityRankMap()
	rankingSource := "config priority"
	if rankedPeerIPs == nil {
		rankedPeerIPs = pr.gossipState.PeerIPRankMap()
		rankingSource = "IP address"
	}

	selfPeerRank, selfInRankedPeerIPs := rankedPeerIPs[m.peerSelf.IP]

	if !selfInRankedPeerIPs {
		return false, fmt.Errorf("unable to find this node's IP %s in the %s-ranked list of peers: %v", m.peerSelf.IP, rankingSource, rankedPeerIPs)
	}

	if selfPeerRank == 0 {
		m.logger.Debug(fmt.Sprintf("this node is ranked 0/%d by %s - no takeover delay", peerCount, rankingSource), "profile", pr.name)
		return false, nil
	}

	// peers with ranks 1 and over have a deterministic delay of rank*poll_interval_duration
	delay := time.Duration(selfPeerRank) * m.cfg.Failover.PollIntervalDuration

	m.logger.Warn(fmt.Sprintf("delaying takeover by %s (<rank %d (of %d peers) by %s> * <%s poll_interval_duration>) to avoid race condition with higher ranked peer", delay, selfPeerRank, peerCount, rankingSource, m.cfg.Failover.PollIntervalDuration), "profile", pr.name)
	time.Sleep(delay)
	m.logger.Warn("takeover delay complete", "profile", pr.name)
	return true, nil
}
