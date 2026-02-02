package config

import (
	"fmt"
	"net"
	"time"
)

// Failover represents failover decision parameters
type Failover struct {
	DryRun                         bool                           `koanf:"dry_run"`
	PollIntervalDuration           time.Duration                  `koanf:"poll_interval_duration"`
	LeaderlessSamplesThreshold     int                            `koanf:"leaderless_samples_threshold"`
	TakeoverJitterDuration         time.Duration                  `koanf:"takeover_jitter_duration"`
	Active                         Role                           `koanf:"active"`
	Passive                        Role                           `koanf:"passive"`
	Peers                          Peers                          `koanf:"peers"`
	DelinquentSlotDistanceOverride DelinquentSlotDistanceOverride `koanf:"delinquent_slot_distance_override"`
	// PeerGossipMinPresenceDuration is how long a peer must be visible in gossip
	// to be considered "present" (smooths out brief network glitches)
	PeerGossipMinPresenceDuration time.Duration `koanf:"peer_gossip_min_presence_duration"`
	// SelfSlotsDeltaAllowed is minimum delta (local_slot - network_slot) for self to take over
	// delta > 0: ahead of network, delta = 0: at network, delta < 0: behind network
	// Self can take over if: delta >= SelfSlotsDeltaAllowed
	// Example: -2 means self can be at most 2 slots behind to take over
	SelfSlotsDeltaAllowed int64 `koanf:"self_slots_delta_allowed"`
}

// DelinquentSlotDistanceOverride represents an sdk override for the delinquent slot distance
type DelinquentSlotDistanceOverride struct {
	Enabled bool   `koanf:"enabled"`
	Value   uint64 `koanf:"value"`
}

func (f *Failover) Validate() error {
	// failover.poll_interval must be greater than zero
	if f.PollIntervalDuration == 0 {
		return fmt.Errorf("failover.poll_interval_duration must be greater than zero")
	}

	// failover.leaderless_samples_threshold must be greater than zero
	if f.LeaderlessSamplesThreshold <= 0 {
		return fmt.Errorf("failover.leaderless_samples_threshold must be positive and non-zero")
	}

	// failover.active.command must be defined
	if f.Active.Command == "" {
		return fmt.Errorf("failover.active.command must be defined")
	}

	// failover.active.hooks.pre must all be valid if defined
	for _, hook := range f.Active.Hooks.Pre {
		if hook.Name == "" {
			return fmt.Errorf("failover.active.hooks.pre must have a name")
		}
		if hook.Command == "" {
			return fmt.Errorf("failover.active.hooks.pre must have a command")
		}
	}

	// failover.active.hooks.post must all be valid if defined
	for _, hook := range f.Active.Hooks.Post {
		if hook.Name == "" {
			return fmt.Errorf("failover.active.hooks.post must have a name")
		}
		if hook.Command == "" {
			return fmt.Errorf("failover.active.hooks.post must have a command")
		}
	}

	// failover.passive.command must be defined
	if f.Passive.Command == "" {
		return fmt.Errorf("failover.passive.command must be defined")
	}

	// failover.passive.hooks.pre must all be valid if defined
	for _, hook := range f.Passive.Hooks.Pre {
		if hook.Name == "" {
			return fmt.Errorf("failover.passive.hooks.pre must have a name")
		}
		if hook.Command == "" {
			return fmt.Errorf("failover.passive.hooks.pre must have a command")
		}
	}

	// failover.passive.hooks.post must all be valid if defined
	for _, hook := range f.Passive.Hooks.Post {
		if hook.Name == "" {
			return fmt.Errorf("failover.passive.hooks.post must have a name")
		}
		if hook.Command == "" {
			return fmt.Errorf("failover.passive.hooks.post must have a command")
		}
	}

	// failover.peers must be at least 1
	if len(f.Peers) == 0 {
		return fmt.Errorf("failover.peers - at least one peer must be defined")
	}

	// failover.peers must have unique valid IP addresses
	ips := make(map[string]bool)
	for name, peer := range f.Peers {
		if net.ParseIP(peer.IP) == nil || net.ParseIP(peer.IP).To4() == nil {
			return fmt.Errorf("failover.peers - invalid IP address %s for peer %s", peer.IP, name)
		}
		if ips[peer.IP] {
			return fmt.Errorf("failover.peers - duplicate IP address %s found for peer %s", peer.IP, name)
		}
		ips[peer.IP] = true
	}

	// Note: DelinquentSlotDistanceOverride.Value is uint64, so it cannot be negative
	// No validation needed for negative values since uint64 cannot hold negative numbers

	// failover.self_slots_delta_allowed must be <= -2 (normal ops can have delta of -1 or -2)
	// Note: 0 is allowed as it means "use default" (will be set to -2 in SetDefaults)
	if f.SelfSlotsDeltaAllowed != 0 && f.SelfSlotsDeltaAllowed > -2 {
		return fmt.Errorf("failover.self_slots_delta_allowed must be <= -2 (value %d is too restrictive for normal operations where delta can be -1 or -2)", f.SelfSlotsDeltaAllowed)
	}

	return nil
}

// RenderRoleCommands renders the failover commands for a given role if they have templated strings
func (f *Failover) RenderRoleCommands(data RoleCommandTemplateData) (err error) {
	err = f.Active.RenderCommands(data)
	if err != nil {
		return fmt.Errorf("failed to render command template strings for failover.active.command: %w", err)
	}

	err = f.Passive.RenderCommands(data)
	if err != nil {
		return fmt.Errorf("failed to render command template strings for failover.passive.command: %w", err)
	}

	return nil
}

// SetDefaults sets default values for the failover configuration
func (f *Failover) SetDefaults() {
	// Set defaults for failover config
	if f.PollIntervalDuration == 0 {
		f.PollIntervalDuration = 5 * time.Second
	}
	if f.LeaderlessSamplesThreshold == 0 {
		f.LeaderlessSamplesThreshold = 3 //  3 x poll interval = (at least) 15 seconds
	}
	if f.PeerGossipMinPresenceDuration == 0 {
		f.PeerGossipMinPresenceDuration = 30 * time.Second // peer must be visible for 30s
	}
	if f.SelfSlotsDeltaAllowed == 0 {
		f.SelfSlotsDeltaAllowed = -2 // allow self to be up to 2 slots behind
	}

	// Set role names
	f.Active.Name = "active"
	f.Passive.Name = "passive"
}
