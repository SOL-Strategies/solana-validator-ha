package gossip

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

// State represents the state of the peers as seen by the solana network
type State struct {
	// PeerStatesRefreshedAt is the last time the peer states were refreshed
	PeerStatesRefreshedAt time.Time
	// peerStatesByName are the peers that are currently in the solana network, keyed by their name
	peerStatesByName map[string]PeerState // these are the peers that are currently in the solana network, keyed by their name
	configPeers      config.Peers
	activePubkey     string
	selfIP           string
	clusterRPC       *rpc.Client
	logger           *log.Logger
}

// PeerState represents the state of a peer as seen by the solana network
type PeerState struct {
	// IP is the IP address of the peer
	IP string
	// Pubkey is the public key of the peer
	Pubkey string
	// LastSeenAt is the last time the peer was seen by the solana network
	LastSeenAtUTC time.Time
	// LastSeenActive is true if the peer was the active validator when it was last seen
	LastSeenActive bool
}

// Options are the options for peers state
type Options struct {
	ClusterRPC   *rpc.Client
	ActivePubkey string
	SelfIP       string
	ConfigPeers  config.Peers
}

// NewState creates a new gossip state
func NewState(opts Options) *State {
	return &State{
		logger:           log.WithPrefix("gossip_state"),
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

	// look through all the returned nodes, looking for the ones that are in the config
	for _, node := range clusterNodes {
		nodeIP := strings.Split(*node.Gossip, ":")[0]

		// if the peer is not the config, keep looking
		if !slices.Contains(p.configPeers.GetIPs(), nodeIP) {
			continue
		}

		// get the peer name from configPeers
		var peerName string
		for name, peer := range p.configPeers {
			if peer.IP == nodeIP {
				peerName = name
				break
			}
		}

		if peerName == "" {
			p.logger.Warn("peer not found in config", "ip", nodeIP)
			continue
		}

		// TODO: remove dirty hack while testing
		isActive := node.Pubkey.String() == "peNgUgnzs1jGogUPW8SThXMvzNpzKSNf3om78xVPAYx"
		// isActive := node.Pubkey.String() == p.activePubkey

		// add the peer to the peerEntries
		peerState := PeerState{
			IP:             nodeIP,
			LastSeenAtUTC:  time.Now().UTC(),
			Pubkey:         node.Pubkey.String(),
			LastSeenActive: isActive,
		}
		latestPeerStatesByName[peerName] = peerState
		// tell us what we found
		p.logger.Debug("peer found in gossip",
			"name", peerName,
			"ip", peerState.IP,
			"pubkey", peerState.Pubkey,
			"is_active", peerState.LastSeenActive,
			"last_seen_at", peerState.LastSeenAtString(),
		)

		// if all peers from configPeers are in the peerEntries, we can stop looking
		if len(p.configPeers) == len(latestPeerStatesByName) {
			break
		}
	}

	// warn if any of the config peers are not in the peerEntries
	for name, peer := range p.configPeers {
		if _, ok := latestPeerStatesByName[name]; !ok {
			p.logger.Warn("peer not found in gossip", "name", name, "ip", peer.IP)
		}
	}

	// update the peerStatesByName to reflect the latest state
	p.peerStatesByName = latestPeerStatesByName
	p.PeerStatesRefreshedAt = time.Now().UTC()
	p.logger.Debug("peers state refreshed", "peer_count", len(p.peerStatesByName))
}

// HasActivePeer returns true if any of the peers are the active validator
func (p *State) HasActivePeer() bool {
	for name, peer := range p.peerStatesByName {
		if peer.LastSeenActive {
			p.logger.Debug("active peer found", "name", name, "ip", peer.IP, "pubkey", peer.Pubkey)
			return true
		}
	}
	return false
}

// HasActivePeerInTheLast returns true if any of the peers are the active validator in the last duration
func (p *State) HasActivePeerInTheLast(duration time.Duration) bool {
	for name, peer := range p.peerStatesByName {
		if peer.LastSeenActive && time.Since(peer.LastSeenAtUTC) < duration {
			p.logger.Info(fmt.Sprintf("active peer last seen in the last %s", duration),
				"name", name,
				"ip", peer.IP,
				"pubkey", peer.Pubkey,
				"last_seen_at", peer.LastSeenAtString(),
			)
			return true
		}
	}
	return false
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
func (p *State) GetActivePeer() (name string, state PeerState, err error) {
	for name, state := range p.peerStatesByName {
		if state.LastSeenActive {
			return name, state, nil
		}
	}
	return "", PeerState{}, fmt.Errorf("no active peer found")
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
