package gossip

// SetPeerStatesForTest replaces the internal peer state map with the provided states.
// Intended for use in tests that need to control gossip state without a real RPC refresh.
func (s *State) SetPeerStatesForTest(states map[string]PeerState) {
	s.peerStatesByName = states
}
