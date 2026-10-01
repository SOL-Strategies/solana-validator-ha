package recording

import "time"

const SchemaVersion = "3"

// supportedSchemaVersions are the recording versions replay understands.
var supportedSchemaVersions = []string{"1", "2", SchemaVersion}

// NodeInfo identifies the node that produced this recording.
type NodeInfo struct {
	Name          string `json:"name"`
	IP            string `json:"ip"`
	ActivePubkey  string `json:"active_pubkey"`
	PassivePubkey string `json:"passive_pubkey"`
	BinaryVersion string `json:"binary_version,omitempty"`
}

// ConfigSnapshot captures the failover-relevant config values at the time of the event.
type ConfigSnapshot struct {
	PollIntervalDuration               string  `json:"poll_interval_duration"`
	LeaderlessSamplesThreshold         int     `json:"leaderless_samples_threshold"`
	LeaderlessConfirmationPollDuration string  `json:"leaderless_confirmation_poll_duration"`
	DelinquencyBypass                  bool    `json:"delinquency_bypass"`
	DelinquentSlotDistanceOverride     *uint64 `json:"delinquent_slot_distance_override,omitempty"`
	// Fields below were added in schema v3.
	ConsensusMode               string  `json:"consensus_mode,omitempty"`
	VoteLagSlotsThreshold       uint64  `json:"vote_lag_slots_threshold,omitempty"`
	WarmupSlots                 uint64  `json:"warmup_slots,omitempty"`
	FinalizationStallDuration   string  `json:"finalization_stall_duration,omitempty"`
	NetworkCurrentStakeRatioMin float64 `json:"network_current_stake_ratio_min,omitempty"`
}

// PeerSnapshot is the observed state of a single peer at a given gossip sample.
type PeerSnapshot struct {
	Name         string  `json:"name"`
	IP           string  `json:"ip"`
	Pubkey       string  `json:"pubkey,omitempty"`
	Role         string  `json:"role"` // "active", "passive", "missing"
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
	LocalRole              string         `json:"local_role,omitempty"`
	LocalPubkey            string         `json:"local_pubkey,omitempty"`
	LocalHealthy           bool           `json:"local_healthy"`
	SelfInGossip           bool           `json:"self_in_gossip"`
	GossipPubkey           string         `json:"gossip_pubkey,omitempty"`
	ElapsedMillis          int64          `json:"elapsed_millis,omitempty"`
	// Fields below were added in schema v3.
	ConsensusPhase       string `json:"consensus_phase,omitempty"`
	AlpenglowGenesisSlot uint64 `json:"alpenglow_genesis_slot,omitempty"`
	// LocalGenesisMatch, FinalizedSlot, ClusterLive and NetworkStakeRatio are only recorded in
	// the Alpenglow phase, where they are tracked.
	LocalGenesisMatch *bool    `json:"local_genesis_match,omitempty"`
	FinalizedSlot     uint64   `json:"finalized_slot,omitempty"`
	ClusterLive       *bool    `json:"cluster_live,omitempty"`
	NetworkStakeRatio *float64 `json:"network_stake_ratio,omitempty"`
	// LeaderlessReason says why the sample found no active peer, e.g. "gossip_absent" or "vote_lag".
	LeaderlessReason string `json:"leaderless_reason,omitempty"`
	// Veto says why the active's missing votes were disregarded, e.g. "cluster_stalled".
	Veto string `json:"veto,omitempty"`
}

// TimelineEntry records a single decision or action during a failover.
type TimelineEntry struct {
	At            time.Time `json:"at"`
	Event         string    `json:"event"`
	Detail        string    `json:"detail,omitempty"`
	ElapsedMillis int64     `json:"elapsed_millis,omitempty"`
}

// Outcome describes what happened at the end of the failover attempt on this node.
type Outcome struct {
	// Result is one of: "became_active", "became_active_unconfirmed",
	// "aborted_peer_took_over", "aborted_not_healthy", "aborted_not_healthy_long_enough",
	// "aborted_already_active", "aborted_self_not_in_gossip", "aborted_delay_error",
	// "aborted_cluster_stalled", "aborted_vote_account_excluded", "aborted_local_not_migrated"
	Result   string `json:"result"`
	FromNode string `json:"from_node"` // node name that was active before this failover
	ToNode   string `json:"to_node"`   // node name that became active
}

// FailoverEvent is the top-level structure serialised to the recording file.
type FailoverEvent struct {
	SchemaVersion string          `json:"schema_version"`
	IncidentID    string          `json:"incident_id,omitempty"`
	Node          NodeInfo        `json:"node"`
	Config        ConfigSnapshot  `json:"config"`
	DetectedAt    time.Time       `json:"detected_at"`
	GossipSamples []GossipSample  `json:"gossip_samples"`
	Timeline      []TimelineEntry `json:"timeline"`
	Outcome       *Outcome        `json:"outcome,omitempty"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
}
