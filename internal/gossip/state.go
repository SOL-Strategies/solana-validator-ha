package gossip

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	solana "github.com/gagliardetto/solana-go"
	solanagorpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

// DelinquencyDetail holds the slot-distance data captured when the active peer is declared
// delinquent during a Refresh. It is reset to nil at the start of every Refresh.
type DelinquencyDetail struct {
	LastVoteSlot    uint64
	CurrentSlot     uint64
	SlotDistance    uint64
	AllowedDistance uint64
}

// undeclaredPeerName is the synthetic name used for active peers that appear in gossip but are
// not declared in the HA cluster config. Used both when registering the peer state and when
// labelling log lines (e.g. delinquency) where no configured name is available.
const undeclaredPeerName = "config-undeclared-active-peer"

// State represents the state of the peers as seen by the solana network
type State struct {
	// PeerStatesRefreshedAt is the last time the peer states were refreshed
	PeerStatesRefreshedAt time.Time
	// peerStatesByName are the peers that are currently in the solana network, keyed by their name
	peerStatesByName               map[string]PeerState // these are the peers that are currently in the solana network, keyed by their name
	configPeers                    config.Peers
	activePubkey                   string
	selfIP                         string
	clusterRPC                     *rpc.Client
	logger                         *log.Logger
	missingGossipIPs               []string
	lastActivePeer                 PeerState
	activePeerLastSeenAt           time.Time
	LeaderlessSamplesCount         int
	delinquentSlotDistanceOverride config.DelinquentSlotDistanceOverride
	lastRefreshHadRPCError         bool
	configUndeclaredActivePeer     PeerState
	// activePeerDelinquent is set to true during Refresh when the active peer is found in gossip
	// but is declared delinquent by the network (via getVoteAccounts) and the delinquency is NOT
	// due to low balance. It is reset to false at the start of every Refresh.
	activePeerDelinquent bool
	// lastDelinquencyDetail holds slot-distance info from the most recent delinquency detection.
	// Reset to nil at the start of every Refresh.
	lastDelinquencyDetail *DelinquencyDetail
	// peerLastSeenAtByName tracks the last time each peer was seen in gossip, persists even when peer goes missing
	peerLastSeenAtByName map[string]time.Time
	// lastLoggedPeersState is the last peersStateString() output that was logged, used to suppress duplicate logs
	lastLoggedPeersState string
	// legendPrinted tracks whether the emoji legend has been printed on the first Refresh call
	legendPrinted bool
	// votePubkeyCache maps identity pubkey -> vote account pubkey, populated on first getVoteAccounts call
	// to allow subsequent calls to use the votePubkey filter and avoid fetching all ~1500 validators
	votePubkeyCache map[string]solana.PublicKey
	// skipRefreshForTest, when true, makes Refresh a no-op so tests can seed state manually.
	skipRefreshForTest bool
}

// PeerState represents the state of a peer as seen by the solana network
type PeerState struct {
	// Name is the vanity name of the peer
	Name string
	// IP is the IP address of the peer
	IP string
	// Pubkey is the public key of the peer
	Pubkey string
	// LastSeenAt is the last time the peer was seen by the solana network
	LastSeenAtUTC time.Time
	// LastSeenActive is true if the peer was the active validator when it was last seen
	LastSeenActive bool
	// IsRecentlyInGossip is true if the peer was recently in gossip
	IsRecentlyInGossip bool
}

// Options are the options for peers state
type Options struct {
	ClusterRPC                     *rpc.Client
	DelinquentSlotDistanceOverride config.DelinquentSlotDistanceOverride
	ActivePubkey                   string
	SelfIP                         string
	ConfigPeers                    config.Peers
	LogPrefix                      string
}

// NewState creates a new gossip state
func NewState(opts Options) *State {
	return &State{
		logger:                         logging.New(opts.LogPrefix, "gossip_state"),
		clusterRPC:                     opts.ClusterRPC,
		activePubkey:                   opts.ActivePubkey,
		selfIP:                         opts.SelfIP,
		configPeers:                    opts.ConfigPeers,
		peerStatesByName:               make(map[string]PeerState),
		peerLastSeenAtByName:           make(map[string]time.Time),
		votePubkeyCache:                make(map[string]solana.PublicKey),
		delinquentSlotDistanceOverride: opts.DelinquentSlotDistanceOverride,
	}
}

// Refresh the state of peers as seen by the solana network
func (p *State) Refresh() {
	if p.skipRefreshForTest {
		return
	}
	if !p.legendPrinted {
		p.printStateEmojiKeyFormat()
		p.legendPrinted = true
	}
	p.logger.Debug("refreshing peers state")
	latestPeerStatesByName := make(map[string]PeerState)
	p.configUndeclaredActivePeer = PeerState{} // reset on every refresh
	p.activePeerDelinquent = false             // reset on every refresh
	p.lastDelinquencyDetail = nil              // reset on every refresh

	// get cluster nodes - if this fails we return an empty state, which should cause its consumer
	// to check for failovers
	clusterNodes, err := p.clusterRPC.GetClusterNodes(context.Background())
	if err != nil {
		p.lastRefreshHadRPCError = true
		p.peerStatesByName = latestPeerStatesByName
		p.PeerStatesRefreshedAt = time.Now().UTC()
		p.logger.Error("failed to get cluster nodes", "error", err)
		return
	}
	p.lastRefreshHadRPCError = false

	p.logger.Debug("looking for peers in gossip",
		"cluster_nodes_count", len(clusterNodes),
		"peers_count", len(p.configPeers),
		"peers", p.configPeers.String(),
		"active_pubkey", p.activePubkey,
	)

	// look through all the returned gossip nodes, looking for the ones that are in the config
	isLeaderlessSample := true
	for _, node := range clusterNodes {
		nodeIP := strings.Split(*node.Gossip, ":")[0]
		isActiveNode := node.Pubkey.String() == p.activePubkey

		// if the peer is not the config - enjoy the code stench of some nested if statements to figure shit out....
		if p.isNonConfigDeclaredPeerWithIP(nodeIP) {
			// register a non-config declared active peer if found to prevent false positive failovers
			// that will get you in the shit - but only if it is actually voting, otherwise let the
			// leaderless counter increment as normal so a legitimate failover can still fire
			if isActiveNode {
				if !p.isNodeActiveAndVoting(*node) {
					p.logger.Warn("undeclared active peer appears in gossip but is not voting - ignoring to allow legitimate failover", "ip", nodeIP, "pubkey", node.Pubkey.String())
					continue
				}

				// node is active and voting so register it as undeclared active peer
				isLeaderlessSample = false
				p.configUndeclaredActivePeer = PeerState{
					Name:           undeclaredPeerName,
					IP:             nodeIP,
					Pubkey:         node.Pubkey.String(),
					LastSeenAtUTC:  time.Now().UTC(),
					LastSeenActive: true,
				}
				p.activePeerLastSeenAt = p.configUndeclaredActivePeer.LastSeenAtUTC
				p.logger.Debug("active node discovered not declared as peer in HA cluster config at failover.peers", "ip", nodeIP, "pubkey", node.Pubkey.String())
			}

			// continue looking for peers declared in config
			continue
		}

		// get the peer name from configPeers
		peerName, ok := p.peerNameFromIP(nodeIP)
		if !ok {
			p.logger.Warn("peer not found in config", "ip", nodeIP)
			continue
		}

		// Note: We trust gossip presence as a liveness indicator. The gossip protocol
		// uses UDP and has built-in expiration for stale entries (GOSSIP_PULL_CRDS_TIMEOUT_MS).
		// TCP probing the gossip port is unreliable since providers may block TCP while
		// allowing UDP, causing false negatives for healthy nodes.

		// a borked active peer might appear in gossip but not actually be voting
		// so we need to check for that and only proceed to add it to the state if it is not voting still
		if isActiveNode && !p.isNodeActiveAndVoting(*node) {
			p.logger.Debug("active peer appears in gossip but is not voting - excluding from state", "ip", nodeIP, "pubkey", node.Pubkey.String())
			continue
		}

		// now we know the peer is in gossip and voting (if it is an active node) - so we can add it to the state

		// add the peer to the peerEntries
		peerState := PeerState{
			Name:               peerName,
			IP:                 nodeIP,
			LastSeenAtUTC:      time.Now().UTC(),
			Pubkey:             node.Pubkey.String(),
			LastSeenActive:     isActiveNode,
			IsRecentlyInGossip: slices.Contains(p.missingGossipIPs, nodeIP),
		}

		// Active presence takes precedence: if this peer was already confirmed active in this
		// refresh cycle (possible when both old and new CRDS identity entries briefly coexist
		// during a gossip identity transition), don't overwrite it with the stale passive entry.
		if existing, ok := latestPeerStatesByName[peerName]; ok && existing.LastSeenActive {
			continue
		}

		// register the peer state
		latestPeerStatesByName[peerName] = peerState

		// track last seen time for this peer (persists even when peer goes missing)
		p.peerLastSeenAtByName[peerName] = peerState.LastSeenAtUTC

		// update state's activePeerLastSeenAt
		if peerState.LastSeenActive {
			p.activePeerLastSeenAt = peerState.LastSeenAtUTC
			isLeaderlessSample = false
		}

		// log if is change of active peer
		if peerState.LastSeenActive && p.lastActivePeer.IP != "" && p.lastActivePeer.IP != peerState.IP {
			var fromUsThem string
			var toUsThem string
			if p.lastActivePeer.IP == p.selfIP {
				fromUsThem = "(us) "
			}
			if peerState.IP == p.selfIP {
				toUsThem = "(us) "
			}
			p.logger.Info(fmt.Sprintf("🔂 active peer changed %s%s %s → %s%s %s %s",
				fromUsThem,
				p.lastActivePeer.Name,
				p.lastActivePeer.IP,
				toUsThem,
				peerState.Name,
				peerState.IP,
				peerState.Pubkey[:7],
			))
		}

		// register the peer if active
		if peerState.LastSeenActive {
			p.lastActivePeer = peerState
			p.logger.Debug("active peer found",
				"name", peerState.Name,
				"ip", peerState.IP,
				"pubkey", peerState.Pubkey,
				"is_active", peerState.LastSeenActive,
				"last_seen_at", peerState.LastSeenAtString(),
			)
		}

		// tell us what we found
		// state didn't have this peer last time but now it does - so we need to log that
		if !p.HasIP(peerState.IP) {
			p.logger.Debug("peer found",
				"name", peerState.Name,
				"ip", peerState.IP,
				"pubkey", peerState.Pubkey,
				"is_active", peerState.LastSeenActive,
				"last_seen_at", peerState.LastSeenAtString(),
			)
		}

		// Note: no early break here. During a gossip identity transition both the old (passive)
		// and new (active) CRDS entries for a peer briefly coexist in getClusterNodes. Breaking
		// after the first (passive) occurrence would prevent the active entry from being processed.
		// The active-wins guard above handles the reverse ordering (active first, passive second).
		// With 2-3 configured peers across ~1500 cluster nodes each remaining iteration is a
		// handful of string comparisons — the performance cost is negligible.
	}

	// warn if any of the config peers are not in the peerEntries
	latestMissingGossipIPs := []string{}
	for name, peer := range p.configPeers {
		if _, ok := latestPeerStatesByName[name]; !ok {
			latestMissingGossipIPs = append(latestMissingGossipIPs, peer.IP)
		}
	}

	// warn when peer transitions from present to missing (was in old state, now missing)
	for _, ip := range latestMissingGossipIPs {
		name, ok := p.peerNameFromIP(ip)
		if !ok {
			continue
		}

		// warn if peer was in the old state but is now missing
		if p.HasIP(ip) {
			lastSeenAt := ""
			if lastSeen, ok := p.peerLastSeenAtByName[name]; ok {
				lastSeenAt = lastSeen.Format(time.RFC3339Nano)
			}
			p.logger.Debug("peer lost", "name", name, "ip", ip, "last_seen_at", lastSeenAt)
			continue
		}

		// warn if it is the first time we've seen this peer missing from gossip
		if !slices.Contains(p.missingGossipIPs, ip) {
			p.logger.Debug("peer not found", "name", name, "ip", ip)
			continue
		}

		// peer _still_ missing from gossip - debug
		p.logger.Debug("peer still missing", "name", name, "ip", ip)
	}

	// update state
	if isLeaderlessSample {
		p.LeaderlessSamplesCount++
		p.logger.Warn("no active peer found",
			"leaderless_samples_count", p.LeaderlessSamplesCount)
	} else {
		p.LeaderlessSamplesCount = 0
	}
	p.missingGossipIPs = latestMissingGossipIPs
	p.peerStatesByName = latestPeerStatesByName
	p.PeerStatesRefreshedAt = time.Now().UTC()
	if stateStr := p.peersStateString(); stateStr != p.lastLoggedPeersState {
		p.logger.Info(stateStr)
		p.lastLoggedPeersState = stateStr
	}
}

// printStateEmojiKeyFormat prints the emoji legend once, called automatically on first Refresh.
func (p *State) printStateEmojiKeyFormat() {
	p.logger.Info("🟢=active 🟡=passive ❓=missing")
}

// peersStateString returns a compact string representation of all configured peers for logging.
// format: 🟢 [(us)] <name> <ip> <pubkeyshort> | 🟡 [(us)] <name> <ip> <pubkeyshort> | ❓ [(us)] <name> <ip> <pubkeyshort>
// where emoji is 🟢 for ACTIVE, 🟡 for PASSIVE, and ❓ for MISSING, sorted by IP ascending
func (p *State) peersStateString() string {
	if len(p.configPeers) == 0 {
		return ""
	}

	type peerInfo struct {
		name        string
		ip          string
		discovered  bool
		active      bool
		isSelf      bool
		pubkeyShort string
	}

	peers := make([]peerInfo, 0, len(p.configPeers))
	for name, configPeer := range p.configPeers {
		info := peerInfo{name: name, ip: configPeer.IP, isSelf: configPeer.IP == p.selfIP, pubkeyShort: "unknown"}
		if state, ok := p.peerStatesByName[name]; ok {
			info.discovered = true
			info.active = state.LastSeenActive
			info.pubkeyShort = state.Pubkey[:7] // FN1Yjxx
		}
		peers = append(peers, info)
	}
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].ip < peers[j].ip
	})

	parts := make([]string, len(peers))
	for i, peer := range peers {
		emoji := "❓"
		if peer.discovered {
			if peer.active {
				emoji = "🟢"
			} else {
				emoji = "🟡"
			}
		}
		usThem := ""
		if peer.isSelf {
			usThem = "(us) "
		}
		parts[i] = strings.TrimSpace(fmt.Sprintf("%s %s%s %s %s", emoji, usThem, peer.name, peer.ip, peer.pubkeyShort))
	}

	return strings.Join(parts, " | ")
}

// PeerIPRankMap returns a map of IP addresses to their zero-indexed rank in the sorted list of IPs
func (p *State) PeerIPRankMap() map[string]int {
	ipRankMap := make(map[string]int)
	for ipIndex, ip := range p.getSortedIPs() {
		ipRankMap[ip] = ipIndex
	}
	return ipRankMap
}

// PeerCount returns the number of peers in the gossip state
func (p *State) PeerCount() int {
	return len(p.peerStatesByName)
}

// ActivePeerIsDelinquent returns true when the most recent Refresh found the active peer in gossip
// but the network (via getVoteAccounts) has declared it delinquent for a reason other than low
// balance. This is a strong, cluster-wide signal that the active validator is non-functional.
func (p *State) ActivePeerIsDelinquent() bool {
	return p.activePeerDelinquent
}

// GetDelinquencyDetail returns the slot-distance detail from the most recent delinquency
// detection, or nil if the last Refresh found no delinquency.
func (p *State) GetDelinquencyDetail() *DelinquencyDetail {
	return p.lastDelinquencyDetail
}

// GetLastActivePeer returns the most recently observed active peer across all Refresh calls.
// This persists even when the peer goes missing or becomes delinquent, making it useful
// for determining the "from" node in a failover recording. Returns a zero-value PeerState
// if no active peer has ever been observed.
func (p *State) GetLastActivePeer() PeerState {
	return p.lastActivePeer
}

// HasConfigUndeclaredActivePeer returns true if any of the peers are the active validator
func (p *State) HasConfigUndeclaredActivePeer() bool {
	return p.configUndeclaredActivePeer.IP != ""
}

// GetConfigUndeclaredActivePeer returns the config undeclared active peer
func (p *State) GetConfigUndeclaredActivePeer() PeerState {
	return p.configUndeclaredActivePeer
}

// getSortedIPs returns a an ascendings ordered list of IP addresses from the peerStatesByName map
func (p *State) getSortedIPs() []string {
	ips := []string{}
	for _, peerState := range p.peerStatesByName {
		ips = append(ips, peerState.IP)
	}
	sort.Strings(ips)
	return ips
}

// cacheVotePubkey upserts the vote pubkey for the given vote account, logging only when the value is new or changed.
func (p *State) cacheVotePubkey(voteAccount solanagorpc.VoteAccountsResult) {
	identityKey := voteAccount.NodePubkey.String()
	if existing, ok := p.votePubkeyCache[identityKey]; ok && existing.Equals(voteAccount.VotePubkey) {
		return
	}
	p.votePubkeyCache[identityKey] = voteAccount.VotePubkey
	p.logger.Debug("cached vote account pubkey", "vote_pubkey", voteAccount.VotePubkey.String(), "identity", identityKey)
}

// isNodeActiveAndVoting returns true if the node is active and voting.
// On the first call for a given identity pubkey, getVoteAccounts is called unfiltered and the
// discovered vote pubkey is cached. Subsequent calls use the votePubkey filter, reducing the
// response from ~1500 entries to 1. If a filtered call returns empty (e.g. after a key rotation),
// the cache entry is cleared and the call falls back to unfiltered for that cycle.
func (p *State) isNodeActiveAndVoting(node solanagorpc.GetClusterNodesResult) bool {
	// Agave default: DELINQUENT_VALIDATOR_SLOT_DISTANCE = 128 — used by getVoteAccounts when no delinquentSlotDistance is passed.
	// https://github.com/anza-xyz/agave/blob/master/rpc-client-types/src/request.rs
	// Since Agave v2.0, --health-check-slot-distance also defaults to 128 via the same constant:
	// https://github.com/anza-xyz/agave/blob/master/validator/src/commands/run/args/json_rpc_config.rs
	// Both thresholds agree. This variable is only used for the log line below;
	// opts.DelinquentSlotDistance is only set when the override is enabled.
	delinquentSlotDistance := uint64(128)
	identityKey := node.Pubkey.String()

	buildOpts := func(votePubkey *solana.PublicKey) solanagorpc.GetVoteAccountsOpts {
		opts := solanagorpc.GetVoteAccountsOpts{
			Commitment: solanagorpc.CommitmentProcessed,
			VotePubkey: votePubkey,
		}
		if p.delinquentSlotDistanceOverride.Enabled {
			delinquentSlotDistance = p.delinquentSlotDistanceOverride.Value
			opts.DelinquentSlotDistance = &p.delinquentSlotDistanceOverride.Value
		}
		return opts
	}

	// attempt a filtered call if we have a cached vote pubkey for this identity
	var voteAccounts *solanagorpc.GetVoteAccountsResult
	if cached, ok := p.votePubkeyCache[identityKey]; ok {
		opts := buildOpts(&cached)
		result, err := p.clusterRPC.GetVoteAccounts(context.Background(), &opts)
		if err != nil {
			p.logger.Error("failed to get vote accounts", "error", err)
			return true // forgive rpc error and assume innocence lest we trigger a false-positive failover
		}
		if len(result.Current) == 0 && len(result.Delinquent) == 0 {
			// cached vote pubkey is stale (key rotation?) - clear and fall back to unfiltered
			p.logger.Warn("cached vote pubkey returned empty result, re-discovering", "identity_pubkey", identityKey)
			delete(p.votePubkeyCache, identityKey)
		} else {
			voteAccounts = result
		}
	}

	// unfiltered call - used on first call or after cache eviction; also discovers and caches vote pubkey
	if voteAccounts == nil {
		opts := buildOpts(nil)
		result, err := p.clusterRPC.GetVoteAccounts(context.Background(), &opts)
		if err != nil {
			p.logger.Error("failed to get vote accounts", "error", err)
			return true // forgive rpc error and assume innocence lest we trigger a false-positive failover
		}
		voteAccounts = result
	}

	// if the node is in the delinquent list - it is not voting, but forgive delinquency due to low balance
	// because failing over in this case definitely won't fix things anyway
	for _, delinquentVoteAccount := range voteAccounts.Delinquent {
		if !delinquentVoteAccount.NodePubkey.Equals(node.Pubkey) {
			continue
		}

		p.cacheVotePubkey(delinquentVoteAccount)

		// ok we might be legit delinquent but let's check if the node's identity balance is below the rent-exempt balance
		balance, err := p.clusterRPC.GetBalance(context.Background(), delinquentVoteAccount.NodePubkey)
		if err != nil {
			p.logger.Error("failed to get balance", "error", err)
			return true // forgive rpc error and assume innocence lest we trigger a false-positive failover
		}
		// rent exempt min is 890880 lamports
		if balance.Value <= 890880 {
			p.logger.Error("‼️ node is delinquent from balance being below rent-exempt minimum - assuming still active to not trigger a false-positive failover - FIX balance pronto!",
				"gossip_address", *node.Gossip,
				"pubkey", node.Pubkey.String(),
				"balance", balance.Value,
			)
			return true
		}

		// ohhh shit! we're delinquent - snitch on this guy!
		nodeIP := strings.Split(*node.Gossip, ":")[0]
		label := undeclaredPeerName + " " + nodeIP
		if name, ok := p.peerNameFromIP(nodeIP); ok {
			label = name + " " + nodeIP
		}
		distanceStr := fmt.Sprintf("behind %d slots or more", delinquentSlotDistance)
		if currentSlot, err := p.clusterRPC.GetSlot(context.Background()); err == nil {
			distance := currentSlot - delinquentVoteAccount.LastVote
			distanceStr = fmt.Sprintf("behind %d slots >= %d slots allowed", distance, delinquentSlotDistance)
			p.lastDelinquencyDetail = &DelinquencyDetail{
				LastVoteSlot:    delinquentVoteAccount.LastVote,
				CurrentSlot:     currentSlot,
				SlotDistance:    distance,
				AllowedDistance: delinquentSlotDistance,
			}
		}
		p.logger.Error(fmt.Sprintf("‼️ %s delinquent (%s)", label, distanceStr),
			"last_voted_at_slot", delinquentVoteAccount.LastVote,
		)
		// signal to ensureHAState that the network has authoritatively confirmed this peer is
		// delinquent — failover can bypass the leaderless sample threshold
		p.activePeerDelinquent = true
		return false
	}

	// good good, node is not delinquent, let's see if it is voting
	var nodeVoteAccount *solanagorpc.VoteAccountsResult
	found := false

	for _, voteAccount := range voteAccounts.Current {
		if !voteAccount.NodePubkey.Equals(node.Pubkey) {
			continue
		}
		p.cacheVotePubkey(voteAccount)
		found = true
		nodeVoteAccount = &voteAccount
		break
	}

	// if we didn't find our node - we're definitely inactive and not voting
	if !found {
		p.logger.Warn("no current or delinquent vote account found for node",
			"gossip_address", *node.Gossip,
			"pubkey", node.Pubkey.String(),
		)
		return false
	}

	p.logger.Debug("node found in current vote accounts",
		"gossip_address", *node.Gossip,
		"pubkey", node.Pubkey.String(),
		"vote_account_pubkey", nodeVoteAccount.VotePubkey.String(),
		"last_voted_at_slot", nodeVoteAccount.LastVote,
	)

	return true
}

// HasActivePeer returns true if any of the peers are the active validator
func (p *State) HasActivePeer() bool {
	for name, peer := range p.peerStatesByName {
		if peer.LastSeenActive {
			p.logger.Debug(fmt.Sprintf("active peer found - last seen at %s", peer.LastSeenAtString()), "name", name, "ip", peer.IP, "pubkey", peer.Pubkey)
			return true
		}
	}
	return false
}

// LeaderlessSamplesExceedsThreshold allows for up to n samples without an active peer before declaring leaderless
func (p *State) LeaderlessSamplesExceedsThreshold(n int) bool {
	return p.LeaderlessSamplesCount >= n
}

// LeaderlessSamplesBelowThreshold allows for up to n samples without an active peer before declaring leaderless
func (p *State) LeaderlessSamplesBelowThreshold(n int) bool {
	return p.LeaderlessSamplesCount < n
}

// HasIP returns true if the IP is in the peers gossip state
func (p *State) HasIP(ip string) bool {
	for _, peer := range p.peerStatesByName {
		if peer.IP == ip {
			return true
		}
	}
	return false
}

// GetActivePeer returns the active peer state
func (p *State) GetActivePeer() (state PeerState, err error) {
	for _, state := range p.peerStatesByName {
		if state.LastSeenActive {
			return state, nil
		}
	}
	return PeerState{}, fmt.Errorf("no active peer found")
}

// HasPeers returns true if the IP has any peers in the gossip state
// that is, any peers in that state that are not the passed IP address
func (p *State) HasPeers(ip string) bool {
	// if the self IP is in the gossip state, we have peers
	for _, peer := range p.peerStatesByName {
		if peer.IP != ip {
			return true
		}
	}
	return false
}

// LastRefreshHadRPCError returns true if the last Refresh() call failed due to RPC error
func (p *State) LastRefreshHadRPCError() bool {
	return p.lastRefreshHadRPCError
}

// GetPeerStates returns the current peer states
func (p *State) GetPeerStates() map[string]PeerState {
	return p.peerStatesByName
}

// LastSeenAtString returns the last seen at time as a string
func (p *PeerState) LastSeenAtString() string {
	return p.LastSeenAtUTC.Format(time.RFC3339)
}

func (p *State) peerNameFromIP(ip string) (string, bool) {
	for name, peer := range p.configPeers {
		if peer.IP == ip {
			return name, true
		}
	}
	return "", false
}

func (p *State) isConfigDeclaredPeerWithIP(ip string) bool {
	for _, peer := range p.configPeers {
		if peer.IP == ip {
			return true
		}
	}
	return false
}

func (p *State) isNonConfigDeclaredPeerWithIP(ip string) bool {
	return !p.isConfigDeclaredPeerWithIP(ip)
}

// IPEquals returns true if the IP is equal to the peer's IP
func (p *PeerState) IPEquals(ip string) bool {
	return p.IP == ip
}
