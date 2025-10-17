package config

import (
	"fmt"
	"sort"
	"strings"
)

// Peers is a map of peer names to their IP addresses
type Peers map[string]Peer

// Peer represents a peer validator
type Peer struct {
	IP   string `koanf:"ip"`
	Name string `koanf:"-"`
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

// GetIPs returns the IP addresses of the peers
func (p *Peers) GetIPs() []string {
	ips := []string{}
	for _, peer := range *p {
		ips = append(ips, peer.IP)
	}
	return ips
}

// GetRankedIPs returns the IP addresses in ascending order
// this is arbitrary but used to impose some portable guaranteed
// rank among peers without sharing any other configuration
func (p *Peers) GetRankedIPs() (rankedIPs map[string]int) {
	rankedIPs = make(map[string]int)
	ips := p.GetIPs()
	sort.Strings(ips)

	// ips are sorted in ascending order now
	for ipIndex, ip := range ips {
		rankedIPs[ip] = ipIndex + 1
	}

	return rankedIPs
}
