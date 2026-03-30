package ha

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/cache"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/constants"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
	"github.com/sol-strategies/solana-validator-ha/internal/local"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
	"github.com/sol-strategies/solana-validator-ha/internal/prometheus"
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
	gossipState     *gossip.State
	localState      *local.State
	getPublicIPFunc func() (string, error)
	peerCount       int
	initialized     bool
	logPrefix       string
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
		cfg:       opts.Cfg,
		version:   opts.Version,
		metrics:   metrics,
		cache:     cache,
		logger:    logging.New(opts.Cfg.Validator.Name, "ha_manager"),
		ctx:       ctx,
		cancel:    cancel,
		peerCount: len(opts.Cfg.Failover.Peers),
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

	// peers config file must not declare ourselves
	if m.cfg.Failover.Peers.HasIP(publicIP) {
		return fmt.Errorf("failover.peers must not reference ourselves, found %s in failover.peers", publicIP)
	}

	// now we can set ourselves as a peer and continue
	m.logger.Debug("adding us to config peers", "name", m.cfg.Validator.Name, "ip", publicIP)
	m.peerSelf = &config.Peer{
		Name: m.cfg.Validator.Name,
		IP:   publicIP,
	}
	m.cfg.Failover.Peers.Add(*m.peerSelf)

	// initialize
	m.logger.Info("initializing",
		"version", m.version,
		"public_ip", publicIP,
		"cluster_rpc_urls", m.cfg.Cluster.RPCURLs,
		"validator_rpc_url", m.cfg.Validator.RPCURL,
		"active_pubkey", m.cfg.Validator.Identities.ActivePubkey(),
		"passive_pubkey", m.cfg.Validator.Identities.PassivePubkey(),
		"peers", m.cfg.Failover.Peers.String(),
		"failover_dry_run", m.cfg.Failover.DryRun,
		"prometheus_port", m.cfg.Prometheus.Port,
		"health_check_port", m.cfg.Prometheus.HealthCheckPort,
	)

	// create gossip state
	m.logger.Debug("creating gossip state")
	m.gossipState = gossip.NewState(gossip.Options{
		ClusterRPC:                     rpc.NewClient(m.logPrefix, m.cfg.Cluster.RPCURLs...),
		ActivePubkey:                   m.cfg.Validator.Identities.ActivePubkey(),
		ConfigPeers:                    m.cfg.Failover.Peers,
		DelinquentSlotDistanceOverride: m.cfg.Failover.DelinquentSlotDistanceOverride,
		SelfIP:                         m.peerSelf.IP,
		LogPrefix:                      m.logPrefix,
	})

	// create local state
	m.logger.Debug("creating local state")
	m.localState = local.NewState(local.Options{
		RPC:          rpc.NewClient(m.logPrefix, m.cfg.Validator.RPCURL),
		Cfg:          m.cfg.Failover.SelfHealthy,
		ActivePubkey: m.cfg.Validator.Identities.ActivePubkey(),
		Ctx:          m.ctx,
		LogPrefix:    m.logPrefix,
	})

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
	m.gossipState.Refresh()

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
			if fastPolling && m.gossipState.LeaderlessSamplesCount == m.cfg.Failover.LeaderlessSamplesThreshold-1 {
				m.logger.Warn("leaderless samples approaching threshold - switching to confirmation poll interval",
					"leaderless_samples_count", m.gossipState.LeaderlessSamplesCount,
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
			if m.gossipState.LeaderlessSamplesCount != m.cfg.Failover.LeaderlessSamplesThreshold-1 {
				return nil
			}
		}
	}
}

// ensureHAState implements basic HA logic
func (m *Manager) ensureHAState() {
	m.logger.Debug("ensuring HA")

	// refresh gossip state
	m.gossipState.Refresh()

	// refresh metrics
	m.refreshMetrics()

	// do nothing except warn if a config-undeclared active peer is found, this prevents false positive failovers
	// and prompts users to declare these so that the anti-race condition logic (based on IPs) can continue to work as intended
	if m.gossipState.HasConfigUndeclaredActivePeer() {
		configUndeclaredActivePeer := m.gossipState.GetConfigUndeclaredActivePeer()
		m.logger.Warn("active peer found not declared in HA cluster config - no failover required, but should be added to failover.peers", "ip", configUndeclaredActivePeer.IP, "pubkey", configUndeclaredActivePeer.Pubkey)
		return
	}

	// if there is an active peer found in the last failover.leaderless_samples_threshold - we are good
	// having a lookback grace period is important to allow for RPC glitches and other issues
	if !m.gossipState.LeaderlessSamplesExceedsThreshold(m.cfg.Failover.LeaderlessSamplesThreshold) {
		m.logger.Debug("active peer found - no failover required")
		return
	}

	// we see no active peer in the last failover.leaderless_samples_threshold, so we need to failover
	m.logger.Error(fmt.Sprintf("no active peer found in the last %d samples - failover required", m.gossipState.LeaderlessSamplesCount))

	// if we don't see ourselves in gossip - evaluate whether to become passive
	if m.isSelfNotInGossip() {
		// If RPC failed, we likely have network connectivity issues - become passive
		if m.gossipState.LastRefreshHadRPCError() {
			m.logger.Error("we do not appear in gossip due to RPC error (possible network connectivity issue) - ensuring we are passive")
			m.ensurePassive()
			return
		}

		// RPC succeeded but we're not in the results
		// Check if there are other peers visible that could take over
		if !m.gossipState.HasPeers(m.peerSelf.IP) {
			// No other peers visible either - we might be the last node standing
			// Don't call ensurePassive to avoid taking the entire cluster offline
			m.logger.Warn("we do not appear in gossip and no other peers are visible (but RPC is working) - skipping ensurePassive to avoid taking entire cluster offline")
			return
		}

		// Other peers are visible and could take over - safe to become passive
		m.logger.Error("we do not appear in gossip but other peers are visible - ensuring we are passive so a peer can take over")
		m.ensurePassive()
		return
	}
	m.logger.Debug("we are in gossip", "pubkey", m.selfGossipPubkey(), "public_ip", m.peerSelf.IP)

	// to participate in failover we must be healthy
	if !m.localState.IsSelfHealthy() {
		m.logger.Error("we are not healthy - unable to become active in failover")
		return
	}

	// we must have been healthy for long enough to rule out startup health flaps
	if !m.localState.IsSelfHealthyLongEnough() {
		m.logger.Warn("not healthy for long enough to be a failover candidate - standing by",
			"healthy_for", m.localState.SelfHealthyDuration(),
			"minimum_duration", m.cfg.Failover.SelfHealthy.MinimumDuration,
		)
		return
	}

	// one last check to ensure we are NOT already active
	if m.localState.IsSelfActive() {
		m.logger.Warn("we are already active - nothing to do")
		return
	}

	// at this point we know we are in gossip, healthy, and passive
	// so we begin checks to make sure none of our peers have already taken over as active

	// introduce a rank-based delay to safeguard against multiple nodes trying to become active at the same time
	delayApplied, err := m.delayTakeoverAsActive()
	if err != nil {
		m.logger.Error(err.Error())
		return
	}

	// refresh the peers state to ensure no one else has taken over already - this will reset the leaderless samples count
	// if a new leader is found.
	// rank-0 nodes skip this re-validation because zero time elapsed during their "delay", so no peer could have
	// taken over in the interim - avoiding an unnecessary RPC round trip on the hot path.
	if delayApplied {
		m.gossipState.Refresh()
	}

	// an undeclared active peer may have appeared during the delay - treat the same as the pre-delay check
	if m.gossipState.HasConfigUndeclaredActivePeer() {
		configUndeclaredActivePeer := m.gossipState.GetConfigUndeclaredActivePeer()
		m.logger.Warn("active peer found not declared in HA cluster config (post-delay re-check) - aborting takeover, should be added to failover.peers", "ip", configUndeclaredActivePeer.IP, "pubkey", configUndeclaredActivePeer.Pubkey)
		return
	}

	// if someone has already taken over as active - say so
	// TODO: refactor logic, it works but the situation is a little confusing
	if m.gossipState.LeaderlessSamplesBelowThreshold(m.cfg.Failover.LeaderlessSamplesThreshold) {
		activePeerState, err := m.gossipState.GetActivePeer()
		if err != nil {
			m.logger.Warn("failed to get active peer from state, but we know someone else already assumed active role", "error", err)
			return
		}
		m.logger.Warn(fmt.Sprintf("peer %s is active, seen at %s - nothing to do", activePeerState.Name, activePeerState.LastSeenAtString()),
			"ip", activePeerState.IP,
			"pubkey", activePeerState.Pubkey,
		)
		return
	}

	// now we know we are healthy, passive, and none of our peers have assumed active role
	// we can take over as active - this should be idempotent in setting the active role
	m.ensureActive()
}

// ensurePassive calls a user-specified command that should be idempotent in setting the passive role
// safest thing would be to to ensure validator service always starts with passive identity
// and the failover.passive.command simply retsarts the validator service or waits for it to start up
func (m *Manager) ensurePassive() {
	var err error
	passivePubkey := m.cfg.Validator.Identities.PassivePubkey()
	m.logger.Info("becoming passive", "pubkey", passivePubkey)

	// Update failover status in cache
	state := m.cache.GetState()
	state.FailoverStatus = constants.StatusBecomingPassive
	m.cache.UpdateState(state)

	// run pre hooks
	if len(m.cfg.Failover.Passive.Hooks.Pre) > 0 {
		m.logger.Debug("running pre-passive hooks")
		err = m.cfg.Failover.Passive.Hooks.RunPre(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "pre-passive",
			},
		})
	}
	if err != nil {
		m.logger.Error("failed to run pre-passive hooks", "error", err)
		return
	}

	// run passive command
	m.logger.Debug("running passive command")
	err = m.cfg.Failover.Passive.RunCommand(config.RoleCommandRunOptions{
		DryRun:       m.cfg.Failover.DryRun,
		LoggerPrefix: m.logPrefix,
		LoggerArgs: []any{
			"failover_stage", constants.RoleNamePassive,
			"passive_pubkey", passivePubkey,
		},
	})
	if err != nil {
		m.logger.Warn("failed to run passive command", "error", err)
		return
	}

	// run post hooks
	if len(m.cfg.Failover.Passive.Hooks.Post) > 0 {
		m.logger.Debug("running post-passive hooks")
		m.cfg.Failover.Passive.Hooks.RunPost(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "post-passive",
			},
		})
	}

	// check to ensure the call to the failover.passive.command was successful
	if !m.localState.IsSelfPassive() {
		m.logger.Error("we are not passive as reported by local rpc - unable to become active in failover",
			"passive_pubkey", passivePubkey,
		)
		return
	}

	m.logger.Debug("we are confirmed to be passive as reported by local rpc", "passive_pubkey", passivePubkey)

	// refresh gossip state to warn if we are in gossip but not passive
	m.gossipState.Refresh()

	// if we are not in gossip, warn - we may be starting up or dropped from the network
	if m.isSelfNotInGossip() {
		m.logger.Warn("we are not in gossip after becoming passive", "passive_pubkey", passivePubkey)
		return
	}

	// if we are in gossip but not passive, show error - failover.passive.command has likely fucked up
	if !m.localState.IsSelfPassive() {
		m.logger.Error("we are in gossip but not passive - this should not happen check failover.passive.command logic", "passive_pubkey", passivePubkey)
		return
	}

	// we are passive by local rpc and in gossip
	m.logger.Info("we are confirmed to be passive", "passive_pubkey", passivePubkey)
}

// ensureActive makes the node active - this should be idempotent in setting the  active role
// safest thing would be to to ensure validator service alywas starts with passive identity
// and the failover.passive.command simply retsarts the validator service
func (m *Manager) ensureActive() {
	var err error
	activePubkey := m.cfg.Validator.Identities.ActivePubkey()
	m.logger.Info("becoming active", "pubkey", activePubkey)

	// Update failover status in cache
	state := m.cache.GetState()
	state.FailoverStatus = constants.StatusBecomingActive
	m.cache.UpdateState(state)

	// run pre hooks
	if len(m.cfg.Failover.Active.Hooks.Pre) > 0 {
		m.logger.Debug("running pre-active hooks")
		err = m.cfg.Failover.Active.Hooks.RunPre(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "pre-active",
			},
		})
	}
	if err != nil {
		m.logger.Error("failed to run pre-active hooks", "error", err)
		return
	}

	// run active command
	m.logger.Debug("running active command")
	err = m.cfg.Failover.Active.RunCommand(config.RoleCommandRunOptions{
		DryRun:       m.cfg.Failover.DryRun,
		LoggerPrefix: m.logPrefix,
		LoggerArgs: []any{
			"failover_stage", constants.RoleNameActive,
			"active_pubkey", activePubkey,
		},
	})
	if err != nil {
		m.logger.Warn("failed to run active command", "error", err)
		return
	}

	// run post hooks
	if len(m.cfg.Failover.Active.Hooks.Post) > 0 {
		m.logger.Debug("running post-active hooks")
		m.cfg.Failover.Active.Hooks.RunPost(config.HooksRunOptions{
			DryRun:       m.cfg.Failover.DryRun,
			LoggerPrefix: m.logPrefix,
			LoggerArgs: []any{
				"failover_stage", "post-active",
			},
		})
	}

	// check to ensure the call to the failover.active.command was successful
	if !m.localState.IsSelfActive() {
		m.logger.Error("this node is not active as reported by local rpc - unable to become active in failover",
			"active_pubkey", activePubkey,
		)
		return
	}

	m.logger.Info("we are confirmed to be active", "active_pubkey", activePubkey)
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
func (m *Manager) isSelfInGossip() (isInGossip bool) {
	return m.gossipState.HasIP(m.peerSelf.IP)
}

// isSelfNotInGossip checks if the validator is not in the gossip state
func (m *Manager) isSelfNotInGossip() (isNotInGossip bool) {
	return !m.isSelfInGossip()
}

// selfGossipPubkey returns the pubkey of the validator in gossip
func (m *Manager) selfGossipPubkey() (pubkey string) {
	for _, peer := range m.gossipState.GetPeerStates() {
		if peer.IP == m.peerSelf.IP {
			return peer.Pubkey
		}
	}
	return ""
}

// refreshMetrics updates the cache with current state
func (m *Manager) refreshMetrics() {
	m.logger.Debug("refreshing metrics")

	// Determine role and status
	var role, status string
	if m.localState.IsSelfActive() {
		role = constants.RoleNameActive
	} else if m.localState.IsSelfPassive() {
		role = constants.RoleNamePassive
	} else {
		role = constants.RoleNameUnknown
	}

	if m.localState.IsSelfHealthy() {
		status = constants.StatusHealthy
	} else {
		status = constants.StatusUnhealthy
	}

	// Get peer count and self in gossip status
	peerCount := len(m.gossipState.GetPeerStates())
	selfInGossip := m.gossipState.HasIP(m.peerSelf.IP)

	// Update cache with current state
	state := cache.State{
		ValidatorName:  m.cfg.Validator.Name,
		PublicIP:       m.peerSelf.IP,
		Role:           role,
		Status:         status,
		PeerCount:      peerCount,
		SelfInGossip:   selfInGossip,
		FailoverStatus: constants.StatusIdle,
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
}

// delayTakeoverAsActive introduces a delay when there are multiple peers
// to safeguard against multiple nodes trying to become active at the same time.
// Returns (delayApplied, error): delayApplied is true only when the node actually slept
// (i.e. rank > 0). Rank-0 nodes return false so the caller can skip the post-delay
// re-validation gossip refresh - no time elapsed, so no peer could have taken over.
func (m *Manager) delayTakeoverAsActive() (delayApplied bool, err error) {
	// peerCount includes ourselves, so if we are the only peer, we don't need to delay
	peerCount := m.gossipState.PeerCount()
	if peerCount == 0 {
		return false, fmt.Errorf("no peers found - unable to delay takeover")
	}

	// Determine self rank: prefer explicit config priorities, fall back to IP-based ordering.
	// Config-based ranking is stable and does not shift when a peer briefly drops from gossip.
	rankedPeerIPs := m.cfg.Failover.PeerIPPriorityRankMap(m.peerSelf.IP)
	rankingSource := "config priority"
	if rankedPeerIPs == nil {
		rankedPeerIPs = m.gossipState.PeerIPRankMap()
		rankingSource = "IP address"
	}

	selfPeerRank, selfInRankedPeerIPs := rankedPeerIPs[m.peerSelf.IP]

	if !selfInRankedPeerIPs {
		return false, fmt.Errorf("unable to find this node's IP %s in the %s-ranked list of peers: %v", m.peerSelf.IP, rankingSource, rankedPeerIPs)
	}

	if selfPeerRank == 0 {
		m.logger.Info(fmt.Sprintf("this node is ranked 0/%d by %s - no takeover delay", peerCount, rankingSource))
		return false, nil
	}

	// peers with ranks 1 and over have a deterministic delay of rank*poll_interval_duration
	delay := time.Duration(selfPeerRank) * m.cfg.Failover.PollIntervalDuration

	m.logger.Warn(fmt.Sprintf("delaying takeover by %s (<rank %d (of %d peers) by %s> * <%s poll_interval_duration>) to avoid race condition with higher ranked peer", delay, selfPeerRank, peerCount, rankingSource, m.cfg.Failover.PollIntervalDuration))
	time.Sleep(delay)
	m.logger.Warn("takeover delay complete")
	return true, nil
}
