package local

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	solanagorpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
)

// State tracks the local validator node's health and identity state.
type State struct {
	healthySince              time.Time
	mu                        sync.RWMutex
	minDurationReached        bool
	unhealthyLogged           bool
	consecutiveUnhealthyCount int
	rpc                       *rpc.Client
	cfg                       config.SelfHealthy
	activePubkey              string
	logger                    *log.Logger
	ctx                       context.Context
	// forceHealthyForTest, when true, makes checkHealth() return (true, nil) without an RPC call.
	// Set via SetForceHealthyForTest; intended for unit tests only.
	forceHealthyForTest bool
}

// Options are the options for creating a new local State.
type Options struct {
	RPC          *rpc.Client
	Cfg          config.SelfHealthy
	ActivePubkey string
	Ctx          context.Context
	LogPrefix    string
}

// NewState creates a new local State.
func NewState(opts Options) *State {
	return &State{
		rpc:          opts.RPC,
		cfg:          opts.Cfg,
		activePubkey: opts.ActivePubkey,
		logger:       logging.New(opts.LogPrefix, "local_state"),
		ctx:          opts.Ctx,
	}
}

// SampleSelf samples the local validator health and updates the continuous healthy streak.
// Logs only on state transitions to avoid log noise during restarts or sustained unhealthy periods.
func (s *State) SampleSelf() {
	healthy, healthErr := s.checkHealth()
	s.mu.Lock()
	defer s.mu.Unlock()

	if healthy {
		s.consecutiveUnhealthyCount = 0
		if s.healthySince.IsZero() {
			s.healthySince = time.Now()
			s.minDurationReached = false
			s.unhealthyLogged = false
			s.logger.Info(fmt.Sprintf("we are healthy but waiting for continuous %s healthy streak...",
				s.cfg.MinimumDuration,
			))
		}
		healthyFor := time.Since(s.healthySince).Round(time.Second)
		minDuration := s.cfg.MinimumDuration
		if !s.minDurationReached && healthyFor >= minDuration {
			s.minDurationReached = true
			msg := fmt.Sprintf("we are continulously healthy for at least %s", minDuration)
			if s.IsSelfPassive() {
				msg += " - eligible for failover"
			}
			s.logger.Info(msg)
		}
	} else {
		s.consecutiveUnhealthyCount++
		if s.consecutiveUnhealthyCount <= s.cfg.UnhealthyGraceCount {
			// Within grace — log a warning but preserve the streak so a single transient
			// RPC timeout or blip does not destroy an established healthy window.
			if healthErr != nil {
				s.logger.Warn("unhealthy sample within grace count - streak preserved",
					"consecutive_unhealthy", s.consecutiveUnhealthyCount,
					"grace_count", s.cfg.UnhealthyGraceCount,
					"error", healthErr,
				)
			} else {
				s.logger.Warn("unhealthy sample within grace count - streak preserved",
					"consecutive_unhealthy", s.consecutiveUnhealthyCount,
					"grace_count", s.cfg.UnhealthyGraceCount,
				)
			}
			return
		}
		// Grace exhausted — reset the streak.
		if !s.healthySince.IsZero() {
			s.healthySince = time.Time{}
			s.minDurationReached = false
			s.unhealthyLogged = false
		}
		if !s.unhealthyLogged {
			if healthErr != nil {
				s.logger.Error("we are unhealthy", "error", healthErr)
			} else {
				s.logger.Error("we are unhealthy")
			}
			s.unhealthyLogged = true
		}
	}
}

// IsSelfHealthyLongEnough returns true if the local validator has been continuously healthy
// for at least the configured minimum duration.
func (s *State) IsSelfHealthyLongEnough() bool {
	s.mu.RLock()
	since := s.healthySince
	s.mu.RUnlock()
	if since.IsZero() {
		return false
	}
	return time.Since(since) >= s.cfg.MinimumDuration
}

// SelfHealthyDuration returns how long the local validator has been continuously healthy.
// Returns 0 if not currently in a healthy streak.
func (s *State) SelfHealthyDuration() time.Duration {
	s.mu.RLock()
	since := s.healthySince
	s.mu.RUnlock()
	if since.IsZero() {
		return 0
	}
	return time.Since(since)
}

// IsSelfHealthy returns true if the local validator is currently healthy.
func (s *State) IsSelfHealthy() bool {
	healthy, _ := s.checkHealth()
	return healthy
}

// IsSelfActive returns true if the validator is running with the active identity.
func (s *State) IsSelfActive() bool {
	identity, err := s.rpc.GetIdentity(s.ctx)
	if err != nil {
		s.logger.Debug("GetIdentity failed", "error", err)
		return false
	}
	return identity.Identity.String() == s.activePubkey
}

// IsSelfPassive returns true if the validator is running with the passive identity.
func (s *State) IsSelfPassive() bool {
	identity, err := s.rpc.GetIdentity(s.ctx)
	if err != nil {
		s.logger.Debug("GetIdentity failed", "error", err)
		return false
	}
	return identity.Identity.String() != s.activePubkey
}

// checkHealth performs a raw health check against the local RPC.
func (s *State) checkHealth() (bool, error) {
	if s.forceHealthyForTest {
		return true, nil
	}
	healthStatus, err := s.rpc.GetHealth(s.ctx)
	if err != nil {
		return false, err
	}
	return healthStatus == solanagorpc.HealthOk, nil
}
