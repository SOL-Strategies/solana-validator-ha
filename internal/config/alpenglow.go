package config

import (
	"fmt"
	"time"
)

// ConsensusMode selects how the cluster's consensus protocol is determined.
type ConsensusMode string

const (
	// ConsensusModeAuto detects TowerBFT, the migration window and Alpenglow from RPC.
	ConsensusModeAuto ConsensusMode = "auto"
	// ConsensusModeTower pins TowerBFT behaviour and skips detection.
	ConsensusModeTower ConsensusMode = "tower"
	// ConsensusModeAlpenglow pins Alpenglow behaviour and skips detection.
	ConsensusModeAlpenglow ConsensusMode = "alpenglow"
)

const (
	defaultConsensusDetectionInterval = 60 * time.Second

	defaultVoteLagSlotsThreshold = 32
	// minVoteLagSlotsThreshold keeps the threshold clear of the healthy Alpenglow lag, which is
	// up to ~9 slots because rewards are paid 8 slots after the voted slot.
	minVoteLagSlotsThreshold           = 16
	defaultWarmupSlots                 = 64
	defaultFinalizationStallDuration   = 15 * time.Second
	defaultNetworkStakeCheckInterval   = 60 * time.Second
	defaultNetworkCurrentStakeRatioMin = 0.85
)

// Consensus is the cluster.consensus configuration.
type Consensus struct {
	// Mode is auto, tower or alpenglow. Default auto.
	Mode ConsensusMode `koanf:"mode"`
	// DetectionIntervalDuration is how often the consensus phase is queried before Alpenglow
	// is detected. Default 60s.
	DetectionIntervalDuration time.Duration `koanf:"detection_interval_duration"`
}

// Validate validates the cluster.consensus configuration.
func (c *Consensus) Validate() error {
	switch c.Mode {
	case ConsensusModeAuto, ConsensusModeTower, ConsensusModeAlpenglow:
	default:
		return fmt.Errorf("cluster.consensus.mode must be one of auto, tower or alpenglow, got %q", c.Mode)
	}
	if c.DetectionIntervalDuration <= 0 {
		return fmt.Errorf("cluster.consensus.detection_interval_duration must be greater than zero")
	}
	return nil
}

// SetDefaults sets default values for the cluster.consensus configuration.
func (c *Consensus) SetDefaults() {
	if c.Mode == "" {
		c.Mode = ConsensusModeAuto
	}
	if c.DetectionIntervalDuration == 0 {
		c.DetectionIntervalDuration = defaultConsensusDetectionInterval
	}
}

// Alpenglow is the failover.alpenglow configuration. It only affects failover decisions once
// the cluster runs Alpenglow consensus.
type Alpenglow struct {
	// VoteLagSlotsThreshold is how many slots the active's last vote may trail the processed
	// slot before it counts as not voting. Default 32, minimum 16.
	VoteLagSlotsThreshold uint64 `koanf:"vote_lag_slots_threshold"`
	// WarmupSlots is how many slots past the Alpenglow genesis slot the cluster must finalize
	// before vote evidence is trusted. Default 64.
	WarmupSlots uint64 `koanf:"warmup_slots"`
	// FinalizationStallDuration is how long the finalized slot may stand still before the
	// cluster counts as stalled. Default 15s, or the failover poll interval if that is longer.
	FinalizationStallDuration time.Duration `koanf:"finalization_stall_duration"`
	// NetworkCurrentStakeRatioMin is the minimum share of stake that must be current for vote
	// evidence to count. 0 disables the check. Defaults to 0.85 through the config loader, since
	// 0 is a meaningful value.
	NetworkCurrentStakeRatioMin float64 `koanf:"network_current_stake_ratio_min"`
	// NetworkStakeCheckIntervalDuration is how often the ratio above is recomputed. Default 60s.
	NetworkStakeCheckIntervalDuration time.Duration `koanf:"network_stake_check_interval_duration"`
}

// Validate validates the failover.alpenglow configuration. The stall duration must cover at
// least one poll, otherwise every sample would find the cluster stalled.
func (a *Alpenglow) Validate(pollInterval time.Duration) error {
	if a.VoteLagSlotsThreshold < minVoteLagSlotsThreshold {
		return fmt.Errorf("failover.alpenglow.vote_lag_slots_threshold must be at least %d, got %d",
			minVoteLagSlotsThreshold, a.VoteLagSlotsThreshold)
	}
	if a.FinalizationStallDuration < pollInterval {
		return fmt.Errorf("failover.alpenglow.finalization_stall_duration (%s) must not be less than failover.poll_interval_duration (%s)",
			a.FinalizationStallDuration, pollInterval)
	}
	if a.NetworkCurrentStakeRatioMin < 0 || a.NetworkCurrentStakeRatioMin > 1 {
		return fmt.Errorf("failover.alpenglow.network_current_stake_ratio_min must be between 0 and 1, got %g",
			a.NetworkCurrentStakeRatioMin)
	}
	if a.NetworkStakeCheckIntervalDuration <= 0 {
		return fmt.Errorf("failover.alpenglow.network_stake_check_interval_duration must be greater than zero")
	}
	return nil
}

// SetDefaults sets default values for the failover.alpenglow configuration. The stall duration
// defaults to at least pollInterval, so deployments with a slow poll stay valid.
// NetworkCurrentStakeRatioMin is defaulted by the config loader instead, see LoadFromFile.
func (a *Alpenglow) SetDefaults(pollInterval time.Duration) {
	if a.VoteLagSlotsThreshold == 0 {
		a.VoteLagSlotsThreshold = defaultVoteLagSlotsThreshold
	}
	if a.WarmupSlots == 0 {
		a.WarmupSlots = defaultWarmupSlots
	}
	if a.FinalizationStallDuration == 0 {
		a.FinalizationStallDuration = max(defaultFinalizationStallDuration, pollInterval)
	}
	if a.NetworkStakeCheckIntervalDuration == 0 {
		a.NetworkStakeCheckIntervalDuration = defaultNetworkStakeCheckInterval
	}
}
