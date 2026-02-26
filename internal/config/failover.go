package config

import (
	"fmt"
	"net"
	"time"
)

// SelfHealthy represents configuration for tracking the local validator's health streak
type SelfHealthy struct {
	// MinimumDuration is how long the local validator RPC must continuously report healthy
	// before this node is eligible to become active in a failover
	MinimumDuration time.Duration `koanf:"minimum_duration"`
	// PollIntervalDuration is how often to sample the local validator RPC health,
	// independent of the main failover poll_interval_duration
	PollIntervalDuration time.Duration `koanf:"poll_interval_duration"`
}

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
	SelfHealthy                    SelfHealthy                    `koanf:"self_healthy"`
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

	// failover.self_healthy.minimum_duration must be greater than zero
	if f.SelfHealthy.MinimumDuration <= 0 {
		return fmt.Errorf("failover.self_healthy.minimum_duration must be greater than zero")
	}

	// failover.self_healthy.poll_interval_duration must be greater than zero
	if f.SelfHealthy.PollIntervalDuration <= 0 {
		return fmt.Errorf("failover.self_healthy.poll_interval_duration must be greater than zero")
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
	if f.SelfHealthy.MinimumDuration == 0 {
		f.SelfHealthy.MinimumDuration = 45 * time.Second
	}
	if f.SelfHealthy.PollIntervalDuration == 0 {
		f.SelfHealthy.PollIntervalDuration = 2 * time.Second
	}

	// Set role names
	f.Active.Name = "active"
	f.Passive.Name = "passive"
}
