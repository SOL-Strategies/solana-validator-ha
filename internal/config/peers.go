package config

import (
	"fmt"
	"strings"
)

// Peers is a map of peer names to their IP addresses
type Peers map[string]Peer

// Peer represents a peer validator
type Peer struct {
	IP       string `koanf:"ip"`
	Priority *int   `koanf:"priority"`
	Name     string `koanf:"-"`
}

// Add adds a peer to the peers map
func (p *Peers) Add(peer Peer) {
	(*p)[peer.Name] = peer
}

// HasIP returns true if the peers map has a peer with the given IP address
func (p *Peers) HasIP(ip string) bool {
	for _, peer := range *p {
		if peer.IP == ip {
			return true
		}
	}
	return false
}

// String returns a string representation of the peers
func (p *Peers) String() string {
	peerStrings := []string{}
	for name, peer := range *p {
		peerStrings = append(peerStrings, fmt.Sprintf("%s:%s", name, peer.IP))
	}
	return fmt.Sprintf("[%s]", strings.Join(peerStrings, " "))
}
