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
		Peers:                              Peers{"v1": {IP: "1.2.3.4"}},
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
		Peers:                              Peers{"v1": {IP: "1.2.3.4"}},
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
		Peers:                              Peers{"v1": {IP: "1.2.3.4"}},
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
		Peers: Peers{
			"validator-2": {IP: "192.168.1.11"},
			"validator-3": {IP: "192.168.1.12"},
		},
	}
}

func TestFailover_ValidatePriority(t *testing.T) {
	t.Run("no priorities is valid (backward compat)", func(t *testing.T) {
		f := validFailoverBase()
		assert.NoError(t, f.Validate())
	})

	t.Run("all priorities set is valid", func(t *testing.T) {
		f := validFailoverBase()
		f.Priority = intPtr(0)
		f.Peers["validator-2"] = Peer{IP: "192.168.1.11", Priority: intPtr(1)}
		f.Peers["validator-3"] = Peer{IP: "192.168.1.12", Priority: intPtr(2)}
		assert.NoError(t, f.Validate())
	})

	t.Run("partial priorities is an error", func(t *testing.T) {
		f := validFailoverBase()
		f.Priority = intPtr(0)
		// peers have no priority
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "either all nodes")
	})

	t.Run("peer has priority but self does not", func(t *testing.T) {
		f := validFailoverBase()
		f.Peers["validator-2"] = Peer{IP: "192.168.1.11", Priority: intPtr(1)}
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "either all nodes")
	})

	t.Run("duplicate priority between peers is an error", func(t *testing.T) {
		f := validFailoverBase()
		f.Priority = intPtr(0)
		f.Peers["validator-2"] = Peer{IP: "192.168.1.11", Priority: intPtr(1)}
		f.Peers["validator-3"] = Peer{IP: "192.168.1.12", Priority: intPtr(1)} // duplicate
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already used by")
	})

	t.Run("duplicate priority between self and peer is an error", func(t *testing.T) {
		f := validFailoverBase()
		f.Priority = intPtr(0)
		f.Peers["validator-2"] = Peer{IP: "192.168.1.11", Priority: intPtr(0)} // same as self
		f.Peers["validator-3"] = Peer{IP: "192.168.1.12", Priority: intPtr(2)}
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already used by self")
	})

	t.Run("negative self priority is an error", func(t *testing.T) {
		f := validFailoverBase()
		f.Priority = intPtr(-1)
		f.Peers["validator-2"] = Peer{IP: "192.168.1.11", Priority: intPtr(1)}
		f.Peers["validator-3"] = Peer{IP: "192.168.1.12", Priority: intPtr(2)}
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failover.priority must be non-negative")
	})

	t.Run("negative peer priority is an error", func(t *testing.T) {
		f := validFailoverBase()
		f.Priority = intPtr(0)
		f.Peers["validator-2"] = Peer{IP: "192.168.1.11", Priority: intPtr(-1)}
		f.Peers["validator-3"] = Peer{IP: "192.168.1.12", Priority: intPtr(2)}
		err := f.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "priority must be non-negative")
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
