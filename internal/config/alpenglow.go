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
