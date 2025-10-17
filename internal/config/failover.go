package config

import (
	"fmt"
	"net"
	"time"
)

// Failover represents failover decision parameters
type Failover struct {
	DryRun                      bool          `koanf:"dry_run"`
	PollIntervalDuration        time.Duration `koanf:"poll_interval_duration"`
	LeaderlessThresholdDuration time.Duration `koanf:"leaderless_threshold_duration"`
	TakeoverJitterSeconds       int           `koanf:"takeover_jitter_seconds"`
	Active                      Role          `koanf:"active"`
	Passive                     Role          `koanf:"passive"`
	Peers                       Peers         `koanf:"peers"`
}

func (f *Failover) Validate() error {
	// failover.poll_interval must be greater than zero
	if f.PollIntervalDuration == 0 {
		return fmt.Errorf("failover.poll_interval_duration must be greater than zero")
	}

	// failover.leaderless_threshold_duration must be greater than zero
	if f.LeaderlessThresholdDuration <= 0 {
		return fmt.Errorf("failover.leaderless_threshold_duration must be positive and non-zero")
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
	if f.LeaderlessThresholdDuration == 0 {
		f.LeaderlessThresholdDuration = 15 * time.Second
	}
	if f.TakeoverJitterSeconds == 0 {
		f.TakeoverJitterSeconds = 3
	}

	// Set role names
	f.Active.Name = "active"
	f.Passive.Name = "passive"
}
