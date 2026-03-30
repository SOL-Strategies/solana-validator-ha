package gossip

// SetPeerStatesForTest replaces the internal peer state map with the provided states.
// Intended for use in tests that need to control gossip state without a real RPC refresh.
func (s *State) SetPeerStatesForTest(states map[string]PeerState) {
	s.peerStatesByName = states
}

// SetActivePeerDelinquentForTest directly sets the activePeerDelinquent flag.
// Intended for use in tests that need to simulate a delinquency detection without a real RPC refresh.
func (s *State) SetActivePeerDelinquentForTest(v bool) {
	s.activePeerDelinquent = v
}

// SetRefreshNoOpForTest makes Refresh() a no-op when v is true, preserving any state seeded via
// SetPeerStatesForTest and SetActivePeerDelinquentForTest across ensureHAState calls.
func (s *State) SetRefreshNoOpForTest(v bool) {
	s.skipRefreshForTest = v
}
