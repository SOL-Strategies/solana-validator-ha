package config

import (
	"fmt"
	"time"
)

const defaultLocalSlotStallDuration = 15 * time.Second

// Isolation is the failover.isolation configuration: when an active node that has lost the
// network demotes itself.
//
// A failing cluster RPC alone does not tell a node that lost the network apart from a node whose
// RPC provider is down. Only the isolated node also stops receiving blocks, so its local
// processed slot, read over loopback, stops moving.
type Isolation struct {
	// Enabled demotes an active node once the cluster RPC has failed for
	// failover.leaderless_samples_threshold consecutive polls and its local processed slot has
	// not moved for LocalSlotStallDuration. Defaults to true through the config loader.
	Enabled bool `koanf:"enabled"`
	// LocalSlotStallDuration is how long the local processed slot may stand still before an
	// active node without cluster RPC counts as isolated. Default 15s, or the failover poll
	// interval if that is longer.
	LocalSlotStallDuration time.Duration `koanf:"local_slot_stall_duration"`
}

// Validate validates the failover.isolation configuration. The stall duration must cover at
// least one poll, since the local slot is sampled once per poll.
func (i *Isolation) Validate(pollInterval time.Duration) error {
	if !i.Enabled {
		return nil
	}
	if i.LocalSlotStallDuration < pollInterval {
		return fmt.Errorf("failover.isolation.local_slot_stall_duration (%s) must not be less than failover.poll_interval_duration (%s)",
			i.LocalSlotStallDuration, pollInterval)
	}
	return nil
}

// SetDefaults sets default values for the failover.isolation configuration. Enabled is
// defaulted by the config loader instead, see LoadFromFile.
func (i *Isolation) SetDefaults(pollInterval time.Duration) {
	if i.LocalSlotStallDuration == 0 {
		i.LocalSlotStallDuration = max(defaultLocalSlotStallDuration, pollInterval)
	}
}
