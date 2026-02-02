package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFailover_SetDefaults(t *testing.T) {
	failover := &Failover{}
	failover.SetDefaults()

	// Check that defaults are set
	assert.Equal(t, 5*time.Second, failover.PollIntervalDuration)
	assert.Equal(t, 3, failover.LeaderlessSamplesThreshold)
	// TakeoverJitterDuration is no longer set by default - it remains at zero value
	assert.Equal(t, time.Duration(0), failover.TakeoverJitterDuration)
	// Check new health stability defaults
	assert.Equal(t, 30*time.Second, failover.PeerGossipMinPresenceDuration)
	assert.Equal(t, int64(-2), failover.SelfSlotsDeltaAllowed)
}

func TestFailover_Validate(t *testing.T) {
	// Test with valid failover config
	failover := &Failover{
		DryRun:                     false,
		PollIntervalDuration:       30 * time.Second,
		LeaderlessSamplesThreshold: 10,
		TakeoverJitterDuration:     10 * time.Second,
		Active: Role{
			Command: "systemctl start solana",
		},
		Passive: Role{
			Command: "systemctl stop solana",
		},
		Peers: Peers{
			"validator-1": {IP: "192.168.1.10"},
			"validator-2": {IP: "192.168.1.11"},
		},
	}

	err := failover.Validate()
	assert.NoError(t, err)

	// Test with zero poll interval
	failover.PollIntervalDuration = 0
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.poll_interval_duration must be greater than zero")

	// Test with zero leaderless samples threshold
	failover.PollIntervalDuration = 30 * time.Second
	failover.LeaderlessSamplesThreshold = 0
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.leaderless_samples_threshold must be positive and non-zero")

	// Test with empty active command
	failover.LeaderlessSamplesThreshold = 10
	failover.Active.Command = ""
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.active.command must be defined")

	// Test with empty passive command
	failover.Active.Command = "systemctl start solana"
	failover.Passive.Command = ""
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.passive.command must be defined")

	// Test with no peers
	failover.Passive.Command = "systemctl stop solana"
	failover.Peers = Peers{}
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.peers - at least one peer must be defined")

	// Test with invalid IP address
	failover.Peers = Peers{
		"validator-1": {IP: "invalid-ip"},
	}
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.peers - invalid IP address")

	// Test with duplicate IP addresses
	failover.Peers = Peers{
		"validator-1": {IP: "192.168.1.10"},
		"validator-2": {IP: "192.168.1.10"},
	}
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.peers - duplicate IP address")

	// Test with delinquent slot distance override enabled with zero value (should pass)
	// Reset peers to valid values first
	failover.Peers = Peers{
		"validator-1": {IP: "192.168.1.10"},
		"validator-2": {IP: "192.168.1.11"},
	}
	failover.DelinquentSlotDistanceOverride = DelinquentSlotDistanceOverride{
		Enabled: true,
		Value:   0,
	}
	err = failover.Validate()
	assert.NoError(t, err)

	// Test with delinquent slot distance override enabled with reasonable positive value (should pass)
	failover.DelinquentSlotDistanceOverride = DelinquentSlotDistanceOverride{
		Enabled: true,
		Value:   1000,
	}
	err = failover.Validate()
	assert.NoError(t, err)

	// Test with delinquent slot distance override disabled (should pass regardless of value)
	failover.DelinquentSlotDistanceOverride = DelinquentSlotDistanceOverride{
		Enabled: false,
		Value:   0, // When disabled, value doesn't matter
	}
	err = failover.Validate()
	assert.NoError(t, err)
}

func TestFailover_ValidateSelfSlotsDeltaAllowed(t *testing.T) {
	// Create a valid base config
	failover := &Failover{
		PollIntervalDuration:       30 * time.Second,
		LeaderlessSamplesThreshold: 10,
		Active:                     Role{Command: "systemctl start solana"},
		Passive:                    Role{Command: "systemctl stop solana"},
		Peers:                      Peers{"validator-1": {IP: "192.168.1.10"}},
	}

	// Test with zero value (should pass - means use default)
	failover.SelfSlotsDeltaAllowed = 0
	err := failover.Validate()
	assert.NoError(t, err)

	// Test with -2 (should pass - at boundary)
	failover.SelfSlotsDeltaAllowed = -2
	err = failover.Validate()
	assert.NoError(t, err)

	// Test with -3 (should pass - more lenient)
	failover.SelfSlotsDeltaAllowed = -3
	err = failover.Validate()
	assert.NoError(t, err)

	// Test with -10 (should pass - very lenient)
	failover.SelfSlotsDeltaAllowed = -10
	err = failover.Validate()
	assert.NoError(t, err)

	// Test with -1 (should fail - too restrictive)
	failover.SelfSlotsDeltaAllowed = -1
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.self_slots_delta_allowed must be <= -2")

	// Test with 0 explicitly set vs zero value
	// (already tested above - 0 means use default)

	// Test with 1 (should fail - way too restrictive)
	failover.SelfSlotsDeltaAllowed = 1
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.self_slots_delta_allowed must be <= -2")
}

func TestFailover_ValidateWithHooks(t *testing.T) {
	failover := &Failover{
		PollIntervalDuration:       30 * time.Second,
		LeaderlessSamplesThreshold: 10,
		TakeoverJitterDuration:     10 * time.Second,
		Active: Role{
			Command: "systemctl start solana",
			Hooks: Hooks{
				Pre: []Hook{
					{Name: "pre-active", Command: "echo 'pre-active'"},
				},
				Post: []Hook{
					{Name: "post-active", Command: "echo 'post-active'"},
				},
			},
		},
		Passive: Role{
			Command: "systemctl stop solana",
			Hooks: Hooks{
				Pre: []Hook{
					{Name: "pre-passive", Command: "echo 'pre-passive'"},
				},
				Post: []Hook{
					{Name: "post-passive", Command: "echo 'post-passive'"},
				},
			},
		},
		Peers: Peers{
			"validator-1": {IP: "192.168.1.10"},
		},
	}

	err := failover.Validate()
	assert.NoError(t, err)

	// Test with invalid pre hook (empty name)
	failover.Active.Hooks.Pre[0].Name = ""
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.active.hooks.pre must have a name")

	// Test with invalid pre hook (empty command)
	failover.Active.Hooks.Pre[0].Name = "pre-active"
	failover.Active.Hooks.Pre[0].Command = ""
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.active.hooks.pre must have a command")
}
