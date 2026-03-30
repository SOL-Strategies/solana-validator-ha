package local

import "time"

// SetForceHealthyForTest makes IsSelfHealthy() return true without an RPC call.
// Intended for unit tests that need to seed a healthy state without a real validator RPC.
func (s *State) SetForceHealthyForTest(v bool) {
	s.forceHealthyForTest = v
}

// SetHealthySinceForTest seeds the healthy streak start time, making
// IsSelfHealthyLongEnough() return true for any MinimumDuration <= time.Since(t).
// Intended for unit tests only.
func (s *State) SetHealthySinceForTest(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthySince = t
	s.minDurationReached = true
}
