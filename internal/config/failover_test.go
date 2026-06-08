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
	assert.Equal(t, 30*time.Second, failover.SelfHealthy.MinimumDuration)
	assert.Equal(t, 2*time.Second, failover.SelfHealthy.PollIntervalDuration)
	// leaderless_confirmation_poll_duration defaults to poll_interval_duration (no behaviour change)
	assert.Equal(t, failover.PollIntervalDuration, failover.LeaderlessConfirmationPollDuration)
}

func TestFailover_SetDefaults_ConfirmationPollDefaultsToPollInterval(t *testing.T) {
	failover := &Failover{PollIntervalDuration: 10 * time.Second}
	failover.SetDefaults()
	assert.Equal(t, 10*time.Second, failover.LeaderlessConfirmationPollDuration,
		"confirmation poll should default to poll_interval_duration when not set")
}

func TestFailover_SetDefaults_ConfirmationPollPreservedWhenSet(t *testing.T) {
	failover := &Failover{
		PollIntervalDuration:               10 * time.Second,
		LeaderlessConfirmationPollDuration: 2 * time.Second,
	}
	failover.SetDefaults()
	assert.Equal(t, 2*time.Second, failover.LeaderlessConfirmationPollDuration,
		"explicitly set confirmation poll should not be overwritten by SetDefaults")
}

func TestFailover_Validate_ConfirmationPollCeiling(t *testing.T) {
	base := &Failover{
		PollIntervalDuration:               5 * time.Second,
		LeaderlessSamplesThreshold:         3,
		LeaderlessConfirmationPollDuration: 10 * time.Second, // exceeds poll_interval
		SelfHealthy:                        SelfHealthy{MinimumDuration: 30 * time.Second, PollIntervalDuration: 2 * time.Second},
		Active:                             Role{Command: "echo active"},
		Passive:                            Role{Command: "echo passive"},
	}

	err := base.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.leaderless_confirmation_poll_duration")
	assert.Contains(t, err.Error(), "must not exceed")
}

func TestFailover_Validate_ConfirmationPollAtCeiling(t *testing.T) {
	base := &Failover{
		PollIntervalDuration:               5 * time.Second,
		LeaderlessSamplesThreshold:         3,
		LeaderlessConfirmationPollDuration: 5 * time.Second, // equal to poll_interval — valid
		SelfHealthy:                        SelfHealthy{MinimumDuration: 30 * time.Second, PollIntervalDuration: 2 * time.Second},
		Active:                             Role{Command: "echo active"},
		Passive:                            Role{Command: "echo passive"},
	}

	err := base.Validate()
	assert.NoError(t, err)
}

func TestFailover_Validate_ConfirmationPollBelowCeiling(t *testing.T) {
	base := &Failover{
		PollIntervalDuration:               5 * time.Second,
		LeaderlessSamplesThreshold:         3,
		LeaderlessConfirmationPollDuration: 2 * time.Second, // below poll_interval — valid
		SelfHealthy:                        SelfHealthy{MinimumDuration: 30 * time.Second, PollIntervalDuration: 2 * time.Second},
		Active:                             Role{Command: "echo active"},
		Passive:                            Role{Command: "echo passive"},
	}

	err := base.Validate()
	assert.NoError(t, err)
}

func TestFailover_Validate(t *testing.T) {
	// Test with valid failover config
	failover := &Failover{
		DryRun:                     false,
		PollIntervalDuration:       30 * time.Second,
		LeaderlessSamplesThreshold: 10,
		TakeoverJitterDuration:     10 * time.Second,
		SelfHealthy: SelfHealthy{
			MinimumDuration:      45 * time.Second,
			PollIntervalDuration: 5 * time.Second,
		},
		Active: Role{
			Command: "systemctl start solana",
		},
		Passive: Role{
			Command: "systemctl stop solana",
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

	// Test with zero self_healthy minimum_duration
	failover.LeaderlessSamplesThreshold = 10
	failover.SelfHealthy.MinimumDuration = 0
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.self_healthy.minimum_duration must be greater than zero")

	// Test with zero self_healthy poll_interval_duration
	failover.SelfHealthy.MinimumDuration = 45 * time.Second
	failover.SelfHealthy.PollIntervalDuration = 0
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.self_healthy.poll_interval_duration must be greater than zero")

	// Test with empty active command
	failover.SelfHealthy.PollIntervalDuration = 5 * time.Second
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

	// Test with legacy top-level peers
	failover.Passive.Command = "systemctl stop solana"
	failover.Peers = Peers{"validator-1": {IP: "192.168.1.10"}}
	err = failover.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover.peers is no longer supported")

	// Test with delinquent slot distance override enabled with zero value (should pass)
	failover.Peers = nil
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

func intPtr(v int) *int { return &v }

func validFailoverBase() *Failover {
	return &Failover{
		PollIntervalDuration:       30 * time.Second,
		LeaderlessSamplesThreshold: 10,
		SelfHealthy: SelfHealthy{
			MinimumDuration:      45 * time.Second,
			PollIntervalDuration: 5 * time.Second,
		},
		Active:  Role{Command: "systemctl start solana"},
		Passive: Role{Command: "systemctl stop solana"},
	}
}

func TestFailover_ValidateLegacyPeerFields(t *testing.T) {
	t.Run("no legacy peer fields is valid", func(t *testing.T) {
		f := validFailoverBase()
		assert.NoError(t, f.Validate())
	})

	t.Run("top-level priority is rejected", func(t *testing.T) {
		f := validFailoverBase()
		f.Priority = intPtr(0)
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failover.priority is no longer supported")
	})

	t.Run("top-level peers are rejected", func(t *testing.T) {
		f := validFailoverBase()
		f.Peers = Peers{"validator-2": {IP: "192.168.1.11"}}
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failover.peers is no longer supported")
	})
}

func TestFailover_PeerIPPriorityRankMap(t *testing.T) {
	const selfIP = "192.168.1.10"

	t.Run("returns nil when no priorities configured", func(t *testing.T) {
		f := &Failover{
			Peers: Peers{
				"validator-2": {IP: "192.168.1.11"},
				"validator-3": {IP: "192.168.1.12"},
			},
		}
		assert.Nil(t, f.PeerIPPriorityRankMap(selfIP))
	})

	t.Run("self is rank 0 when priority 0", func(t *testing.T) {
		f := &Failover{
			Priority: intPtr(0),
			Peers: Peers{
				"validator-2": {IP: "192.168.1.11", Priority: intPtr(1)},
				"validator-3": {IP: "192.168.1.12", Priority: intPtr(2)},
			},
		}
		rankMap := f.PeerIPPriorityRankMap(selfIP)
		assert.NotNil(t, rankMap)
		assert.Equal(t, 0, rankMap[selfIP])
		assert.Equal(t, 1, rankMap["192.168.1.11"])
		assert.Equal(t, 2, rankMap["192.168.1.12"])
	})

	t.Run("self is rank 2 when priority 2", func(t *testing.T) {
		f := &Failover{
			Priority: intPtr(2),
			Peers: Peers{
				"validator-2": {IP: "192.168.1.11", Priority: intPtr(0)},
				"validator-3": {IP: "192.168.1.12", Priority: intPtr(1)},
			},
		}
		rankMap := f.PeerIPPriorityRankMap(selfIP)
		assert.NotNil(t, rankMap)
		assert.Equal(t, 2, rankMap[selfIP])
		assert.Equal(t, 0, rankMap["192.168.1.11"])
		assert.Equal(t, 1, rankMap["192.168.1.12"])
	})

	t.Run("non-contiguous priorities are ranked by value order", func(t *testing.T) {
		f := &Failover{
			Priority: intPtr(10),
			Peers: Peers{
				"validator-2": {IP: "192.168.1.11", Priority: intPtr(5)},
				"validator-3": {IP: "192.168.1.12", Priority: intPtr(99)},
			},
		}
		rankMap := f.PeerIPPriorityRankMap(selfIP)
		assert.NotNil(t, rankMap)
		// 5 < 10 < 99 → ranks 0, 1, 2
		assert.Equal(t, 1, rankMap[selfIP])
		assert.Equal(t, 0, rankMap["192.168.1.11"])
		assert.Equal(t, 2, rankMap["192.168.1.12"])
	})

	t.Run("single peer cluster", func(t *testing.T) {
		f := &Failover{
			Priority: intPtr(0),
			Peers: Peers{
				"validator-2": {IP: "192.168.1.11", Priority: intPtr(1)},
			},
		}
		rankMap := f.PeerIPPriorityRankMap(selfIP)
		assert.NotNil(t, rankMap)
		assert.Equal(t, 0, rankMap[selfIP])
		assert.Equal(t, 1, rankMap["192.168.1.11"])
	})
}

func TestFailover_ValidateWithHooks(t *testing.T) {
	failover := &Failover{
		PollIntervalDuration:       30 * time.Second,
		LeaderlessSamplesThreshold: 10,
		TakeoverJitterDuration:     10 * time.Second,
		SelfHealthy: SelfHealthy{
			MinimumDuration:      45 * time.Second,
			PollIntervalDuration: 5 * time.Second,
		},
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
