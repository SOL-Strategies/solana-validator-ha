package config

import (
	"fmt"
	"sort"
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
	// UnhealthyGraceCount is how many consecutive unhealthy samples are tolerated before
	// the healthy streak is reset. Default 1 — a single transient RPC timeout or blip does
	// not destroy an established streak. Set to 0 to disable (any failure resets immediately).
	UnhealthyGraceCount int `koanf:"unhealthy_grace_count"`
}

// Failover represents failover decision parameters
type Failover struct {
	DryRun                             bool          `koanf:"dry_run"`
	PollIntervalDuration               time.Duration `koanf:"poll_interval_duration"`
	LeaderlessSamplesThreshold         int           `koanf:"leaderless_samples_threshold"`
	LeaderlessConfirmationPollDuration time.Duration `koanf:"leaderless_confirmation_poll_duration"`
	// DelinquencyBypass, when true, skips the leaderless sample threshold if the active peer is
	// declared delinquent by the network (and not due to low balance). Default false.
	// ⚠️ See "Delinquency Fast-Path" in the README for the fork-recovery risk before enabling.
	DelinquencyBypass              bool                           `koanf:"delinquency_bypass"`
	TakeoverJitterDuration         time.Duration                  `koanf:"takeover_jitter_duration"`
	Priority                       *int                           `koanf:"priority"`
	Active                         Role                           `koanf:"active"`
	Passive                        Role                           `koanf:"passive"`
	Peers                          Peers                          `koanf:"peers"`
	DelinquentSlotDistanceOverride DelinquentSlotDistanceOverride `koanf:"delinquent_slot_distance_override"`
	SelfHealthy                    SelfHealthy                    `koanf:"self_healthy"`
	Recording                      Recording                      `koanf:"recording"`
}

// leaderlessConfirmationPollFloor is the minimum allowed value for
// LeaderlessConfirmationPollDuration. Values below this are clamped and a warning is logged,
// because sub-second polling sits inside gossip propagation jitter and increases false-positive risk.
const leaderlessConfirmationPollFloor = 1 * time.Second

// DelinquentSlotDistanceOverride represents an sdk override for the delinquent slot distance
type DelinquentSlotDistanceOverride struct {
	Enabled bool   `koanf:"enabled"`
	Value   uint64 `koanf:"value"`
}

func (f *Failover) Validate() error {
	if f.Priority != nil {
		return fmt.Errorf("failover.priority is no longer supported; move peer priorities under profiles.<name>.peers.<peer>.priority")
	}
	if len(f.Peers) > 0 {
		return fmt.Errorf("failover.peers is no longer supported; move peers under profiles.<name>.peers")
	}

	// failover.poll_interval must be greater than zero
	if f.PollIntervalDuration == 0 {
		return fmt.Errorf("failover.poll_interval_duration must be greater than zero")
	}

	// failover.leaderless_samples_threshold must be greater than zero
	if f.LeaderlessSamplesThreshold <= 0 {
		return fmt.Errorf("failover.leaderless_samples_threshold must be positive and non-zero")
	}

	// failover.leaderless_confirmation_poll_duration must not exceed poll_interval_duration
	if f.LeaderlessConfirmationPollDuration > f.PollIntervalDuration {
		return fmt.Errorf(
			"failover.leaderless_confirmation_poll_duration (%s) must not exceed failover.poll_interval_duration (%s)",
			f.LeaderlessConfirmationPollDuration, f.PollIntervalDuration,
		)
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

// PeerIPPriorityRankMap returns an IP-to-rank map derived from explicit priorities configured
// for self (selfIP) and all peers. Rank 0 is highest priority (no takeover delay).
// Returns nil when no priorities are configured, signalling callers to fall back to IP-based ranking.
func (f *Failover) PeerIPPriorityRankMap(selfIP string) map[string]int {
	if f.Priority == nil {
		return nil
	}
	type entry struct {
		ip       string
		priority int
	}
	entries := make([]entry, 0, 1+len(f.Peers))
	entries = append(entries, entry{ip: selfIP, priority: *f.Priority})
	for _, peer := range f.Peers {
		if peer.Priority == nil {
			continue
		}
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

// SetDefaults sets default values for the failover configuration
func (f *Failover) SetDefaults() {
	if f.PollIntervalDuration == 0 {
		f.PollIntervalDuration = 5 * time.Second
	}
	if f.LeaderlessSamplesThreshold == 0 {
		f.LeaderlessSamplesThreshold = 3 //  3 x poll interval = (at least) 15 seconds
	}
	if f.SelfHealthy.MinimumDuration == 0 {
		f.SelfHealthy.MinimumDuration = 30 * time.Second
	}
	if f.SelfHealthy.PollIntervalDuration == 0 {
		f.SelfHealthy.PollIntervalDuration = 2 * time.Second
	}
	if f.SelfHealthy.UnhealthyGraceCount == 0 {
		f.SelfHealthy.UnhealthyGraceCount = 1
	}

	// Default to poll_interval_duration so existing deployments that omit this field
	// behave identically to before — fast-polling only activates when explicitly configured.
	if f.LeaderlessConfirmationPollDuration == 0 {
		f.LeaderlessConfirmationPollDuration = f.PollIntervalDuration
	}

	// Set role names
	f.Active.Name = "active"
	f.Passive.Name = "passive"
}
