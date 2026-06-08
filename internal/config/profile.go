package config

import (
	"fmt"
	"net"
	"sort"

	solanago "github.com/gagliardetto/solana-go"
)

// Profiles is the canonical profile map. Each enabled profile represents one
// validator identity that this daemon may monitor and promote.
type Profiles map[string]Profile

// Profile represents one active validator identity managed by the HA daemon.
type Profile struct {
	Name                  string              `koanf:"-"`
	Enabled               *bool               `koanf:"enabled"`
	Priority              *int                `koanf:"priority"`
	Identities            ValidatorIdentities `koanf:"identities"`
	VotePubkeyStr         string              `koanf:"vote_pubkey"`
	AuthorizedVoterPubkey string              `koanf:"authorized_voter"`
	Peers                 Peers               `koanf:"peers"`
}

// EnabledValue returns true when enabled is omitted.
func (p Profile) EnabledValue() bool {
	return p.Enabled == nil || *p.Enabled
}

// ActivePubkey returns the profile's active identity pubkey.
func (p Profile) ActivePubkey() string {
	return p.Identities.ActivePubkey()
}

// Load resolves profile identity files and validates pubkey-only fields.
func (p *Profile) Load() error {
	if !p.EnabledValue() {
		return nil
	}
	if err := p.Identities.LoadActive(); err != nil {
		return fmt.Errorf("profiles.%s.identities: %w", p.Name, err)
	}
	if _, err := solanago.PublicKeyFromBase58(p.VotePubkeyStr); err != nil {
		return fmt.Errorf("profiles.%s.vote_pubkey must be a valid base58 public key: %w", p.Name, err)
	}
	if _, err := solanago.PublicKeyFromBase58(p.AuthorizedVoterPubkey); err != nil {
		return fmt.Errorf("profiles.%s.authorized_voter must be a valid base58 public key: %w", p.Name, err)
	}
	return nil
}

// Validate validates one enabled profile independent of cross-profile uniqueness.
func (p *Profile) Validate() error {
	if !p.EnabledValue() {
		return nil
	}
	if p.Name == "" {
		return fmt.Errorf("profile name must be defined")
	}
	if p.Priority == nil {
		return fmt.Errorf("profiles.%s.priority must be defined", p.Name)
	}
	if *p.Priority < 0 {
		return fmt.Errorf("profiles.%s.priority must be non-negative", p.Name)
	}
	if p.ActivePubkey() == "" {
		return fmt.Errorf("profiles.%s.identities.active or active_pubkey must be defined", p.Name)
	}
	if p.VotePubkeyStr == "" {
		return fmt.Errorf("profiles.%s.vote_pubkey must be defined", p.Name)
	}
	if p.AuthorizedVoterPubkey == "" {
		return fmt.Errorf("profiles.%s.authorized_voter must be defined", p.Name)
	}
	if len(p.Peers) == 0 {
		return fmt.Errorf("profiles.%s.peers - at least one peer must be defined", p.Name)
	}

	ips := make(map[string]string, len(p.Peers))
	peersWithPriority := 0
	for name, peer := range p.Peers {
		if net.ParseIP(peer.IP) == nil || net.ParseIP(peer.IP).To4() == nil {
			return fmt.Errorf("profiles.%s.peers - invalid IP address %s for peer %s", p.Name, peer.IP, name)
		}
		if owner, exists := ips[peer.IP]; exists {
			return fmt.Errorf("profiles.%s.peers - duplicate IP address %s found for peer %s and %s", p.Name, peer.IP, owner, name)
		}
		ips[peer.IP] = name
		if peer.Priority != nil {
			peersWithPriority++
			if *peer.Priority < 0 {
				return fmt.Errorf("profiles.%s.peers.%s.priority must be non-negative", p.Name, name)
			}
		}
	}

	if peersWithPriority > 0 && peersWithPriority < len(p.Peers) {
		return fmt.Errorf("profiles.%s.peers priorities - either all peers must declare a priority, or none should; %d of %d have one", p.Name, peersWithPriority, len(p.Peers))
	}
	if peersWithPriority == len(p.Peers) {
		seen := map[int]string{}
		for name, peer := range p.Peers {
			if owner, exists := seen[*peer.Priority]; exists {
				return fmt.Errorf("profiles.%s.peers.%s.priority %d is already used by %s", p.Name, name, *peer.Priority, owner)
			}
			seen[*peer.Priority] = name
		}
	}
	return nil
}

// Enabled returns enabled profiles sorted by profile priority, then name.
func (p Profiles) Enabled() []Profile {
	out := make([]Profile, 0, len(p))
	for name, profile := range p {
		profile.Name = name
		if profile.EnabledValue() {
			out = append(out, profile)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority == nil || out[j].Priority == nil {
			return out[i].Name < out[j].Name
		}
		if *out[i].Priority == *out[j].Priority {
			return out[i].Name < out[j].Name
		}
		return *out[i].Priority < *out[j].Priority
	})
	return out
}

// Load resolves all enabled profile identities.
func (p Profiles) Load() error {
	for name := range p {
		profile := p[name]
		profile.Name = name
		if err := profile.Load(); err != nil {
			return err
		}
		p[name] = profile
	}
	return nil
}

// Validate validates all profiles and cross-profile uniqueness.
func (p Profiles) Validate() error {
	if len(p) == 0 {
		return fmt.Errorf("profiles must be defined; migrate active identity and peers into profiles.<name>")
	}

	enabledCount := 0
	activePubkeys := map[string]string{}
	votePubkeys := map[string]string{}
	authorizedVoters := map[string]string{}
	profilePriorities := map[int]string{}

	for name := range p {
		profile := p[name]
		profile.Name = name
		if !profile.EnabledValue() {
			continue
		}
		enabledCount++
		if err := profile.Validate(); err != nil {
			return err
		}

		if owner, exists := activePubkeys[profile.ActivePubkey()]; exists {
			return fmt.Errorf("profiles.%s active identity %s is already used by %s", name, profile.ActivePubkey(), owner)
		}
		activePubkeys[profile.ActivePubkey()] = name

		if owner, exists := votePubkeys[profile.VotePubkeyStr]; exists {
			return fmt.Errorf("profiles.%s vote_pubkey %s is already used by %s", name, profile.VotePubkeyStr, owner)
		}
		votePubkeys[profile.VotePubkeyStr] = name

		if owner, exists := authorizedVoters[profile.AuthorizedVoterPubkey]; exists {
			return fmt.Errorf("profiles.%s authorized_voter %s is already used by %s", name, profile.AuthorizedVoterPubkey, owner)
		}
		authorizedVoters[profile.AuthorizedVoterPubkey] = name

		if owner, exists := profilePriorities[*profile.Priority]; exists {
			return fmt.Errorf("profiles.%s priority %d is already used by %s", name, *profile.Priority, owner)
		}
		profilePriorities[*profile.Priority] = name
	}

	if enabledCount == 0 {
		return fmt.Errorf("profiles must contain at least one enabled profile")
	}
	return nil
}

// ActivePubkeyProfileMap returns active identity pubkey -> profile name for enabled profiles.
func (p Profiles) ActivePubkeyProfileMap() map[string]string {
	out := map[string]string{}
	for name, profile := range p {
		profile.Name = name
		if profile.EnabledValue() {
			out[profile.ActivePubkey()] = name
		}
	}
	return out
}

// ValidateSelfPeer verifies that every enabled profile includes this node in its self-inclusive
// peers map exactly once by configured validator name and discovered public IP.
func (p Profiles) ValidateSelfPeer(selfName, selfIP string) error {
	for name, profile := range p {
		profile.Name = name
		if !profile.EnabledValue() {
			continue
		}
		selfPeer, ok := profile.Peers[selfName]
		if !ok {
			return fmt.Errorf("profiles.%s.peers must include this node by validator.name %q", name, selfName)
		}
		if selfPeer.IP != selfIP {
			return fmt.Errorf("profiles.%s.peers.%s.ip must match this node's discovered public IP %s, got %s", name, selfName, selfIP, selfPeer.IP)
		}
		for peerName, peer := range profile.Peers {
			if peerName != selfName && peer.IP == selfIP {
				return fmt.Errorf("profiles.%s.peers contains this node's public IP %s more than once (%s and %s)", name, selfIP, selfName, peerName)
			}
		}
	}
	return nil
}

// PeerIPPriorityRankMap returns an IP-to-rank map derived from explicit peer priorities.
// Returns nil when no peer priorities are configured, signalling callers to fall back to IP ranking.
func (p Profile) PeerIPPriorityRankMap() map[string]int {
	if len(p.Peers) == 0 {
		return nil
	}
	for _, peer := range p.Peers {
		if peer.Priority == nil {
			return nil
		}
	}
	type entry struct {
		ip       string
		priority int
	}
	entries := make([]entry, 0, len(p.Peers))
	for _, peer := range p.Peers {
		entries = append(entries, entry{ip: peer.IP, priority: *peer.Priority})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].priority < entries[j].priority
	})
	rankMap := make(map[string]int, len(entries))
	for rank, e := range entries {
		rankMap[e.ip] = rank
	}
	return rankMap
}
