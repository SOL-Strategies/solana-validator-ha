package ha

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	solanagorpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/sol-strategies/solana-validator-ha/internal/cache"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
	"github.com/sol-strategies/solana-validator-ha/internal/prometheus"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

// RPCClient interface for RPC operations
type RPCClient interface {
	GetClusterNodes(ctx context.Context) ([]*solanagorpc.GetClusterNodesResult, error)
	GetIdentity(ctx context.Context) (*solanagorpc.GetIdentityResult, error)
}

// NewManagerOptions is a struct that contains the configuration for the manager
type NewManagerOptions struct {
	Cfg             *config.Config
	GetPublicIPFunc func() (string, error)
}

// Manager handles high availability logic
type Manager struct {
	cfg             *config.Config
	metrics         *prometheus.Metrics
	cache           *cache.Cache
	logger          *log.Logger
	ctx             context.Context
	peerSelf        *config.Peer
	cancel          context.CancelFunc
	gossipState     *gossip.State
	getPublicIPFunc func() (string, error)
	localRPC        *rpc.Client
	peerCount       int
	initialized     bool
}

// NewManager creates a new HA manager from options
func NewManager(opts NewManagerOptions) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	// Create cache
	cache := cache.New()

	// Create metrics with cache
	metrics := prometheus.New(prometheus.Options{
		Config: opts.Cfg,
		Logger: log.WithPrefix("metrics"),
		Cache:  cache,
	})

	manager := &Manager{
		cfg:       opts.Cfg,
		metrics:   metrics,
		cache:     cache,
		logger:    log.WithPrefix("ha_manager"),
		localRPC:  rpc.NewClient(opts.Cfg.Validator.RPCURL),
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
		"public_ip", publicIP,
		"cluster_rpc_urls", m.cfg.Cluster.RPCURLs,
		"validator_rpc_url", m.cfg.Validator.RPCURL,
		"active_pubkey", m.cfg.Validator.Identities.ActiveKeyPair.PublicKey().String(),
		"passive_pubkey", m.cfg.Validator.Identities.PassiveKeyPair.PublicKey().String(),
		"peers", m.cfg.Failover.Peers.String(),
	)

	// create gossip state
	m.logger.Debug("creating gossip state")
	m.gossipState = gossip.NewState(gossip.Options{
		ClusterRPC:   rpc.NewClient(m.cfg.Cluster.RPCURLs...),
		ActivePubkey: m.cfg.Validator.Identities.ActiveKeyPair.PublicKey().String(),
		ConfigPeers:  m.cfg.Failover.Peers,
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

		port := strconv.Itoa(m.cfg.Prometheus.Port + 1) // Use next port for health check
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
	m.logger.Info("monitoring HA state", "poll_interval", m.cfg.Failover.PollIntervalDuration)

	// initial gossip state refresh to populate things and log if active peer found
	m.gossipState.Refresh()

	if m.gossipState.HasActivePeerInTheLast(m.cfg.Failover.LeaderlessThresholdDuration) {
		activePeerState, err := m.gossipState.GetActivePeer()
		if err != nil {
			m.logger.Warn("failed to get active peer from state", "error", err)
		} else {
			activePeerFoundMessage := "active peer found"
			if activePeerState.IP == m.peerSelf.IP {
				activePeerFoundMessage = "active peer found (us)"
			}
			m.logger.Info(activePeerFoundMessage, "name", activePeerState.Name, "public_ip", activePeerState.IP, "pubkey", activePeerState.Pubkey)
		}
	}

	ticker := time.NewTicker(m.cfg.Failover.PollIntervalDuration)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Info("HA monitor loop done")
			return nil
		case <-ticker.C:
			m.ensureHAState()
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

	// warn if we don't appear in gossip and ensure we are passive - disconnection or starting up
	if m.isSelfNotInGossip() {
		m.logger.Warn("we are not in gossip - ensuring we are passive", "public_ip", m.peerSelf.IP)
		m.ensurePassive()
		return
	}

	// tell us if we have recently been discovered in gossip
	if m.isSelfRecentlyInGossip() {
		m.logger.Info("we are in gossip", "pubkey", m.selfGossipPubkey(), "public_ip", m.peerSelf.IP)
		return
	}

	// if there is an active peer found in the last failover.leaderless_threshold_duratio - we are good
	// having a lookback grace period is important to allow for RPC glitches and other issues
	if m.gossipState.HasActivePeerInTheLast(m.cfg.Failover.LeaderlessThresholdDuration) {
		m.logger.Debug("active peer found - no failover required")
		return
	}

	// we see no active peer in the last failover.leaderless_threshold_duration, so we need to failover
	m.logger.Error("no active peer found in gossip - failover required")

	// if we don't see ourselves in gossip - ensure we are passive (might be starting up, have dropped from network, etc)
	// and bow out of the failover process until we are back in gossip
	if m.isSelfNotInGossip() {
		m.logger.Warn("we do not appear in gossip - ensuring we are passive")
		m.ensurePassive()
		return
	}

	// at this point we know we are in gossip and passive - begin checks to see if we can take over as active

	// to participate in failover we must be healthy
	if m.isSelfUnhealthy() {
		m.logger.Error("we are not healthy - unable to become active in failover")
		return
	}

	// one last check to ensure we are passive
	if m.isSelfActive() {
		m.logger.Warn("we are already active as reported by local rpc - unable to become active in failover and ensuring we are passive")
		m.ensurePassive()
		return
	}

	// introduce a delay based on IP to safeguard against multiple nodes trying to become active at the same time
	m.delayTakeover()

	// refresh the peers state to ensure no one else has taken over already if we know
	// there are at least 2 possible peers other than ourselves
	m.gossipState.Refresh()

	// if someone has already taken over as active - say so and return
	if m.gossipState.HasActivePeerInTheLast(m.cfg.Failover.LeaderlessThresholdDuration) {
		activePeerState, err := m.gossipState.GetActivePeer()
		if err != nil {
			m.logger.Warn("failed to get active peer fromn state, but we know someone else already assumed active role", "error", err)
			return
		}
		m.logger.Warn(fmt.Sprintf("peer became active in the last %s", m.cfg.Failover.LeaderlessThresholdDuration),
			"name", activePeerState.Name,
			"ip", activePeerState.IP,
			"pubkey", activePeerState.Pubkey,
		)
		return
	}

	// now we know we are heatlhy, passive, and no one else has assumed active role
	// we can take over as active - this should be idempotent in setting the active role
	m.logger.Info("becoming active", "pubkey", m.cfg.Validator.Identities.ActiveKeyPair.PublicKey().String())
	m.ensureActive()
}

// ensurePassive calls a user-specified command that should be idempotent in setting the passive role
// safest thing would be to to ensure validator service always starts with passive identity
// and the failover.passive.command simply retsarts the validator service or waits for it to start up
func (m *Manager) ensurePassive() {
	var err error
	passivePubkey := m.cfg.Validator.Identities.PassiveKeyPair.PublicKey().String()

	// Update failover status in cache
	state := m.cache.GetState()
	state.FailoverStatus = "becoming_passive"
	m.cache.UpdateState(state)

	// run pre hooks
	if len(m.cfg.Failover.Passive.Hooks.Pre) > 0 {
		m.logger.Debug("running pre-passive hooks")
		err = m.cfg.Failover.Passive.Hooks.RunPre(config.HooksRunOptions{
			DryRun: m.cfg.Failover.DryRun,
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
		DryRun: m.cfg.Failover.DryRun,
		LoggerArgs: []any{
			"failover_stage", "passive",
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
			DryRun: m.cfg.Failover.DryRun,
			LoggerArgs: []any{
				"failover_stage", "post-passive",
			},
		})
	}

	// check to ensure the call to the failover.passive.command was successful
	if m.isNotSelfPassive() {
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
	if m.isNotSelfPassive() {
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
	activePubkey := m.cfg.Validator.Identities.ActiveKeyPair.PublicKey().String()

	// Update failover status in cache
	state := m.cache.GetState()
	state.FailoverStatus = "becoming_active"
	m.cache.UpdateState(state)

	// run pre hooks
	if len(m.cfg.Failover.Active.Hooks.Pre) > 0 {
		m.logger.Debug("running pre-active hooks")
		err = m.cfg.Failover.Active.Hooks.RunPre(config.HooksRunOptions{
			DryRun: m.cfg.Failover.DryRun,
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
		DryRun: m.cfg.Failover.DryRun,
		LoggerArgs: []any{
			"failover_stage", "active",
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
			DryRun: m.cfg.Failover.DryRun,
			LoggerArgs: []any{
				"failover_stage", "post-active",
			},
		})
	}

	// check to ensure the call to the failover.active.command was successful
	if !m.isSelfActive() {
		m.logger.Error("this node is not active as reported by local rpc - unable to become active in failover",
			"active_pubkey", activePubkey,
		)
		return
	}

	m.logger.Info("we are confirmed to be active", "active_pubkey", activePubkey)
}

// isSelfHealthy checks if the validator is healthy by calling the local RPC client
func (m *Manager) isSelfHealthy() (isHealthy bool) {
	healthStatus, err := m.localRPC.GetHealth(m.ctx)
	if err != nil {
		m.logger.Error(err.Error())
		return false
	}

	isHealthy = healthStatus == solanagorpc.HealthOk
	m.logger.Debug("health status", "status", healthStatus, "is_healthy", isHealthy)

	if !isHealthy {
		m.logger.Warn("this node is unhealthy", "status", healthStatus)
	}

	return isHealthy
}

// isSelfUnhealthy checks if the validator is unhealthy by calling the local RPC client
func (m *Manager) isSelfUnhealthy() (isUnhealthy bool) {
	return !m.isSelfHealthy()
}

// isSelfActive checks if the validator is active by checking the local RPC client getIdentity response to confirm it is the active identity
func (m *Manager) isSelfActive() (isActive bool) {
	identity, err := m.localRPC.GetIdentity(m.ctx)
	if err != nil {
		m.logger.Error(err.Error())
		return false
	}

	return identity.Identity.String() == m.cfg.Validator.Identities.ActiveKeyPair.PublicKey().String()
}

// isSelfPassive checks if the validator is passive by checking the local RPC client getIdentity response to confirm it is not the active identity
func (m *Manager) isSelfPassive() bool {
	identity, err := m.localRPC.GetIdentity(m.ctx)
	if err != nil {
		m.logger.Error(err.Error())
		return false
	}

	return identity.Identity.String() != m.cfg.Validator.Identities.ActiveKeyPair.PublicKey().String()
}

// isNotSelfPassive checks if the validator is not passive by checking the local RPC client getIdentity response to confirm it is not the active identity
func (m *Manager) isNotSelfPassive() (isNotPassive bool) {
	return !m.isSelfPassive()
}

// isSelfInGossip checks if the validator is in the gossip state
func (m *Manager) isSelfInGossip() (isInGossip bool) {
	return m.gossipState.HasIP(m.peerSelf.IP)
}

// isSelfNotInGossip checks if the validator is not in the gossip state
func (m *Manager) isSelfNotInGossip() (isNotInGossip bool) {
	return !m.isSelfInGossip()
}

// isSelfRecentlyInGossip checks if the validator is in the gossip state and has been in the gossip state in the last failover.leaderless_threshold_duration
func (m *Manager) isSelfRecentlyInGossip() (isRecentlyInGossip bool) {
	return m.gossipState.IsRecentlyInGossip(m.peerSelf.IP)
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
	if m.isSelfActive() {
		role = "active"
	} else if m.isSelfPassive() {
		role = "passive"
	} else {
		role = "unknown"
	}

	if m.isSelfHealthy() {
		status = "healthy"
	} else {
		status = "unhealthy"
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
		FailoverStatus: "idle",
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

// delayTakeover introduces a delay when there are multiple peers
// to safeguard against multiple nodes trying to become active at the same time
func (m *Manager) delayTakeover() {
	if m.peerCount <= 1 {
		return
	}

	// arbitrary delay by declaration of peer order + failover.takeover_jitter_seconds
	var delaySeconds int
	for i, ip := range m.cfg.Failover.Peers.GetRankedIPs() {
		if ip == m.peerSelf.IP {
			delaySeconds = i + 1
			break
		}
	}

	delay := time.Duration(delaySeconds) * time.Second
	// add random jitter to the delay
	delay += time.Duration(rand.Intn(m.cfg.Failover.TakeoverJitterSeconds)) * time.Second
	m.logger.Debug("delaying takeover to avoid race conditions", "delay", delay)
	time.Sleep(delay)

}
