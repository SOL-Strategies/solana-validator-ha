package gossip

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	solanagorpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

// State represents the state of the peers as seen by the solana network
type State struct {
	// PeerStatesRefreshedAt is the last time the peer states were refreshed
	PeerStatesRefreshedAt time.Time
	// peerStatesByName are the peers that are currently in the solana network, keyed by their name
	peerStatesByName       map[string]PeerState // these are the peers that are currently in the solana network, keyed by their name
	configPeers            config.Peers
	activePubkey           string
	selfIP                 string
	clusterRPC             *rpc.Client
	logger                 *log.Logger
	missingGossipIPs       []string
	lastActivePeer         PeerState
	activePeerLastSeenAt   time.Time
	LeaderlessSamplesCount int
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
	ClusterRPC   *rpc.Client
	ActivePubkey string
	SelfIP       string
	ConfigPeers  config.Peers
	LogPrefix    string
}

// NewState creates a new gossip state
func NewState(opts Options) *State {
	return &State{
		logger:           log.WithPrefix(fmt.Sprintf("[%s gossip_state]", opts.LogPrefix)),
		clusterRPC:       opts.ClusterRPC,
		activePubkey:     opts.ActivePubkey,
		selfIP:           opts.SelfIP,
		configPeers:      opts.ConfigPeers,
		peerStatesByName: make(map[string]PeerState),
	}
}

// Refresh the state of peers as seen by the solana network
func (p *State) Refresh() {
	p.logger.Debug("refreshing peers state")
	latestPeerStatesByName := make(map[string]PeerState)

	// get cluster nodes - if this fails we return an empty state, which should cause its consumer
	// to check for failovers
	clusterNodes, err := p.clusterRPC.GetClusterNodes(context.Background())
	if err != nil {
		p.peerStatesByName = latestPeerStatesByName
		p.PeerStatesRefreshedAt = time.Now().UTC()
		p.logger.Error("failed to get cluster nodes", "error", err)
		return
	}

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

		// if the peer is not the config, keep looking
		if !p.hasConfigPeerWithIP(nodeIP) {
			continue
		}

		// get the peer name from configPeers
		peerName, ok := p.peerNameFromIP(nodeIP)
		if !ok {
			p.logger.Warn("peer not found in config", "ip", nodeIP)
			continue
		}

		// if the node is not alive (can dial its gossip address) it's dead to us - gossip response is stale
		if !p.isNodeGossipAlive(*node) {
			p.logger.Debug("node gossip address not alive - excluding from state",
				"peer_name", peerName,
				"ip", nodeIP,
				"gossip_address", *node.Gossip,
				"pubkey", node.Pubkey.String(),
			)
			continue
		}

		// lastSeenActive
		isActivePeer := node.Pubkey.String() == p.activePubkey

		// a borked active peer might appear in gossip but not actually be voting
		// so we need to check for that and only proceed to add it to the state if it is not voting still
		if isActivePeer && !p.isNodeActiveAndVoting(*node) {
			p.logger.Warn("active peer appears in gossip but is not voting - excluding from state", "ip", nodeIP, "pubkey", node.Pubkey.String())
			continue
		}

		// now we know the peer is alive and voting (if it is an active node) - so we can add it to the state

		// add the peer to the peerEntries
		peerState := PeerState{
			Name:               peerName,
			IP:                 nodeIP,
			LastSeenAtUTC:      time.Now().UTC(),
			Pubkey:             node.Pubkey.String(),
			LastSeenActive:     isActivePeer,
			IsRecentlyInGossip: slices.Contains(p.missingGossipIPs, nodeIP),
		}

		// register the peer state
		latestPeerStatesByName[peerName] = peerState

		// update state's activePeerLastSeenAt
		if peerState.LastSeenActive {
			p.activePeerLastSeenAt = peerState.LastSeenAtUTC
			isLeaderlessSample = false
		}

		// log if is change of active peer
		if peerState.LastSeenActive && p.lastActivePeer.IP != "" && p.lastActivePeer.IP != peerState.IP {
			p.logger.Warn(fmt.Sprintf("active peer changed: %s (%s) -> %s (%s)",
				p.lastActivePeer.IP,
				p.lastActivePeer.Name,
				peerState.IP,
				peerState.Name,
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
			p.logger.Info("peer discovered in gossip",
				"name", peerState.Name,
				"ip", peerState.IP,
				"pubkey", peerState.Pubkey,
				"is_active", peerState.LastSeenActive,
				"last_seen_at", peerState.LastSeenAtString(),
			)
		}

		// if all peers from configPeers are in the peerEntries, we can stop looking
		if len(p.configPeers) == len(latestPeerStatesByName) {
			break
		}
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
			p.logger.Warn("peer lost from gossip", "name", name, "ip", ip)
			continue
		}

		// warn if it is the first time we've seen this peer missing from gossip
		if !slices.Contains(p.missingGossipIPs, ip) {
			p.logger.Warn("peer not found in gossip", "name", name, "ip", ip)
			continue
		}

		// peer _still_ missing from gossip - debug
		p.logger.Debug("peer still missing from gossip", "name", name, "ip", ip)
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
	p.logger.Debug("peers state refreshed", "peer_count", len(p.peerStatesByName))
}

// isNodeActiveAndVoting returns true if the node is active and voting
func (p *State) isNodeActiveAndVoting(node solanagorpc.GetClusterNodesResult) bool {
	// get the current slot
	currentSlot, err := p.clusterRPC.GetSlot(context.Background())
	if err != nil {
		p.logger.Error("failed to get current slot", "error", err)
		return true // forgive rpc error and assume innocence lest we trigger a false-positive failover
	}

	// get vote accounts to look for our node within
	voteAccounts, err := p.clusterRPC.GetVoteAccounts(context.Background())
	if err != nil {
		p.logger.Error("failed to get vote accounts", "error", err)
		return true // forgive rpc error and assume innocence lest we trigger a false-positive failover
	}

	// if the node is in the delinquent list - it is not voting, but forgive delinquency due to low balance
	// because failing over in this case definitely won't fix things anyway
	for _, delinquentVoteAccount := range voteAccounts.Delinquent {
		// not us - keep looking
		if !delinquentVoteAccount.NodePubkey.Equals(node.Pubkey) {
			continue
		}

		// ok we might be legit delinquent but let's check if the node's identity balance is below the rent-exempt balance
		balance, err := p.clusterRPC.GetBalance(context.Background(), delinquentVoteAccount.NodePubkey)
		if err != nil {
			p.logger.Error("failed to get balance", "error", err)
			return true // forgive rpc error and assume innocence lest we trigger a false-positive failover
		}
		// rent exempt min is 890880 lamports
		if balance.Value <= 890880 {
			p.logger.Error("node is delinquent from balance being below rent-exempt minimum - assuming still active to not trigger a false-positive failover - FIX balance pronto!",
				"gossip_address", *node.Gossip,
				"pubkey", node.Pubkey.String(),
				"current_slot", currentSlot,
				"balance", balance.Value,
			)
			return true
		}

		// ohhh shit! we're delinquent - snitch on this guy!
		p.logger.Debug("node is delinquent - not voting",
			"gossip_address", *node.Gossip,
			"pubkey", node.Pubkey.String(),
			"current_slot", currentSlot,
		)
		return false
	}

	// good good, node is not delinquent, let's see if it is voting
	var nodeVoteAccount *solanagorpc.VoteAccountsResult
	found := false

	// try to find our node in the retrieved current vote accounts
	for _, voteAccount := range voteAccounts.Current {
		// not us - keep looking
		if !voteAccount.NodePubkey.Equals(node.Pubkey) {
			continue
		}

		// it is us - let's see wtf is gong on
		found = true
		nodeVoteAccount = &voteAccount
		break
	}

	// if we didn't find our node - we're definitely inactive and not voting
	if !found {
		p.logger.Warn("no current or delinquent vote account found for node",
			"gossip_address", *node.Gossip,
			"pubkey", node.Pubkey.String(),
			"current_slot", currentSlot,
		)
		return false
	}

	// found us
	p.logger.Debug("node found in current vote accounts",
		"gossip_address", *node.Gossip,
		"pubkey", node.Pubkey.String(),
		"vote_account_pubkey", nodeVoteAccount.VotePubkey.String(),
		"last_voted_at_slot", nodeVoteAccount.LastVote,
		"current_slot", currentSlot,
	)

	return true
}

// isNodeGossipAlive returns true if the node's gossip address is alive
// Note: We use Gossip port instead of TPU because TPU ports are often firewalled
// and not reliable indicators of node liveness, while Gossip is more accessible
func (p *State) isNodeGossipAlive(node solanagorpc.GetClusterNodesResult) bool {
	// try to dial the gossip address
	p.logger.Debug("probing for node liveness on gossip address",
		"gossip_address", *node.Gossip,
		"pubkey", node.Pubkey.String(),
	)

	// if we can dial the gossip address, the node is alive
	conn, err := net.Dial("tcp", *node.Gossip)
	if err == nil {
		conn.Close()
		return true
	}

	return false
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

func (p *State) hasConfigPeerWithIP(ip string) bool {
	for _, peer := range p.configPeers {
		if peer.IP == ip {
			return true
		}
	}
	return false
}

// IPEquals returns true if the IP is equal to the peer's IP
func (p *PeerState) IPEquals(ip string) bool {
	return p.IP == ip
}
