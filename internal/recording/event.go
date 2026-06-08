package recording

import "time"

const SchemaVersion = "1"

// NodeInfo identifies the node that produced this recording.
type NodeInfo struct {
	Name          string `json:"name"`
	IP            string `json:"ip"`
	Profile       string `json:"profile,omitempty"`
	ActivePubkey  string `json:"active_pubkey"`
	PassivePubkey string `json:"passive_pubkey"`
}

// ConfigSnapshot captures the failover-relevant config values at the time of the event.
type ConfigSnapshot struct {
	Profile                            string  `json:"profile,omitempty"`
	VotePubkey                         string  `json:"vote_pubkey,omitempty"`
	AuthorizedVoterPubkey              string  `json:"authorized_voter,omitempty"`
	PollIntervalDuration               string  `json:"poll_interval_duration"`
	LeaderlessSamplesThreshold         int     `json:"leaderless_samples_threshold"`
	LeaderlessConfirmationPollDuration string  `json:"leaderless_confirmation_poll_duration"`
	DelinquencyBypass                  bool    `json:"delinquency_bypass"`
	DelinquentSlotDistanceOverride     *uint64 `json:"delinquent_slot_distance_override,omitempty"`
}

// PeerSnapshot is the observed state of a single peer at a given gossip sample.
type PeerSnapshot struct {
	Name         string  `json:"name"`
	IP           string  `json:"ip"`
	Pubkey       string  `json:"pubkey,omitempty"`
	Role         string  `json:"role"` // "active", "passive", "busy", "missing"
	BusyProfile  string  `json:"busy_profile,omitempty"`
	LastVoteSlot *uint64 `json:"last_vote_slot,omitempty"`
	CurrentSlot  *uint64 `json:"current_slot,omitempty"`
	SlotDistance *uint64 `json:"slot_distance,omitempty"`
}

// GossipSample captures the network state at a single poll tick.
type GossipSample struct {
	SampledAt              time.Time      `json:"sampled_at"`
	Peers                  []PeerSnapshot `json:"peers"`
	LeaderlessSamplesCount int            `json:"leaderless_samples_count"`
	ActivePeerDelinquent   bool           `json:"active_peer_delinquent"`
	RPCError               bool           `json:"rpc_error"`
}

// TimelineEntry records a single decision or action during a failover.
type TimelineEntry struct {
	At     time.Time `json:"at"`
	Event  string    `json:"event"`
	Detail string    `json:"detail,omitempty"`
}

// Outcome describes what happened at the end of the failover attempt on this node.
type Outcome struct {
	// Result is one of: "became_active", "became_active_unconfirmed",
	// "aborted_peer_took_over", "aborted_not_healthy", "aborted_not_healthy_long_enough",
	// "aborted_already_active", "aborted_self_not_in_gossip", "aborted_delay_error"
	Result   string `json:"result"`
	FromNode string `json:"from_node"` // node name that was active before this failover
	ToNode   string `json:"to_node"`   // node name that became active
}

// FailoverEvent is the top-level structure serialised to the recording file.
type FailoverEvent struct {
	SchemaVersion string          `json:"schema_version"`
	Node          NodeInfo        `json:"node"`
	Config        ConfigSnapshot  `json:"config"`
	DetectedAt    time.Time       `json:"detected_at"`
	GossipSamples []GossipSample  `json:"gossip_samples"`
	Timeline      []TimelineEntry `json:"timeline"`
	Outcome       *Outcome        `json:"outcome,omitempty"`
}
