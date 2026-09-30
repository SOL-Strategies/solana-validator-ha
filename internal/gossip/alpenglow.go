package gossip

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/consensus"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	solana "github.com/solana-foundation/solana-go/v2"
	solanagorpc "github.com/solana-foundation/solana-go/v2/rpc"
)

// Reasons a sample found no active peer.
const (
	// LeaderlessReasonGossipAbsent means the active identity was not in gossip.
	LeaderlessReasonGossipAbsent = "gossip_absent"
	// LeaderlessReasonVoteLag means the active's Alpenglow votes stopped landing.
	LeaderlessReasonVoteLag = "vote_lag"
	// LeaderlessReasonNoVoteAccount means the active identity has no vote account (TowerBFT).
	LeaderlessReasonNoVoteAccount = "no_vote_account"
	// LeaderlessReasonDelinquent means the active is delinquent (TowerBFT).
	LeaderlessReasonDelinquent = "delinquent"
)

// Reasons the active's missing votes were not held against it, because a failover could not help.
const (
	// VetoClusterStalled means the whole cluster stopped finalizing, so nobody's votes land.
	VetoClusterStalled = "cluster_stalled"
	// VetoVoteAccountExcluded means the active's vote account cannot vote under Alpenglow at all,
	// for example because it is unstaked or left out of the voter set. Any node using it would fail.
	VetoVoteAccountExcluded = "vote_account_excluded"
)

// Verdicts of the Alpenglow vote rule. VetoClusterStalled and VetoVoteAccountExcluded are
// verdicts too.
const (
	VerdictVoting    = "voting"
	VerdictNotVoting = "not_voting"
	// VerdictUnknown means the evidence could not be read; the node is assumed to be voting.
	VerdictUnknown = "unknown"
)

// stakeRatioStaleAfterIntervals is how many check intervals a network stake ratio stays usable.
const stakeRatioStaleAfterIntervals = 3

// AlpenglowSignals are the cluster-health signals behind Alpenglow vote evidence.
type AlpenglowSignals struct {
	FinalizedSlot uint64
	// ClusterLive is true when the finalized slot advanced within the configured stall duration.
	ClusterLive bool
	// InWarmup is true until the cluster finalizes far enough past the Alpenglow genesis slot.
	InWarmup bool
	// NetworkStakeRatio is the share of stake that is current. Only set when NetworkStakeRatioKnown.
	NetworkStakeRatio      float64
	NetworkStakeRatioKnown bool
}

// AlpenglowSignals returns the signals from the last Refresh. ok is false outside the Alpenglow
// phase, where they are not tracked.
func (p *State) AlpenglowSignals() (signals AlpenglowSignals, ok bool) {
	if p.consensus.Phase != consensus.PhaseAlpenglow {
		return AlpenglowSignals{}, false
	}
	now := p.now()
	return AlpenglowSignals{
		FinalizedSlot:          p.finalization.maxSlot,
		ClusterLive:            p.finalization.isLive(now, p.alpenglowCfg.FinalizationStallDuration),
		InWarmup:               p.inAlpenglowWarmup(),
		NetworkStakeRatio:      p.stakeRatio.value,
		NetworkStakeRatioKnown: p.stakeRatio.isFresh(now, p.alpenglowCfg.NetworkStakeCheckIntervalDuration),
	}, true
}

// finalizationTracker keeps the highest finalized slot seen and when it last advanced. Taking
// the maximum smooths over RPC nodes that are a few slots behind each other.
type finalizationTracker struct {
	maxSlot    uint64
	advancedAt time.Time
}

// observe records a finalized slot. The first observation is only a baseline, so the cluster
// counts as live only after an advance has actually been seen.
func (t *finalizationTracker) observe(slot uint64, now time.Time) {
	if slot <= t.maxSlot {
		return
	}
	if t.maxSlot != 0 {
		t.advancedAt = now
	}
	t.maxSlot = slot
}

// isLive reports whether the finalized slot advanced within stallAfter of now.
func (t *finalizationTracker) isLive(now time.Time, stallAfter time.Duration) bool {
	return !t.advancedAt.IsZero() && now.Sub(t.advancedAt) <= stallAfter
}

// stakeRatioSample is the last computed share of stake that is current.
type stakeRatioSample struct {
	value      float64
	computedAt time.Time
}

func (s stakeRatioSample) isDue(now time.Time, interval time.Duration) bool {
	return s.computedAt.IsZero() || now.Sub(s.computedAt) >= interval
}

func (s stakeRatioSample) isFresh(now time.Time, interval time.Duration) bool {
	return !s.computedAt.IsZero() && now.Sub(s.computedAt) <= stakeRatioStaleAfterIntervals*interval
}

// currentStakeRatio returns the share of activated stake held by current vote accounts. ok is
// false when there is no stake at all.
func currentStakeRatio(accounts *solanagorpc.GetVoteAccountsResult) (ratio float64, ok bool) {
	var current, total uint64
	for _, account := range accounts.Current {
		current += account.ActivatedStake
	}
	total = current
	for _, account := range accounts.Delinquent {
		total += account.ActivatedStake
	}
	if total == 0 {
		return 0, false
	}
	return float64(current) / float64(total), true
}

// refreshAlpenglowSignals samples the finalized slot, and the network stake ratio when due.
// Errors keep the previous values, so the cluster drifts towards "stalled" while RPC is failing.
func (p *State) refreshAlpenglowSignals(ctx context.Context) {
	now := p.now()
	finalized, err := p.clusterRPC.GetSlotWithCommitment(ctx, solanagorpc.CommitmentFinalized)
	if err != nil {
		p.logger.Warn("failed to get finalized slot", "error", err)
	} else {
		p.finalization.observe(finalized, now)
	}

	if p.alpenglowCfg.NetworkCurrentStakeRatioMin == 0 || !p.stakeRatio.isDue(now, p.alpenglowCfg.NetworkStakeCheckIntervalDuration) {
		return
	}
	accounts, err := p.clusterRPC.GetVoteAccounts(ctx, &solanagorpc.GetVoteAccountsOpts{Commitment: solanagorpc.CommitmentProcessed})
	if err != nil {
		p.logger.Warn("failed to get vote accounts for the network stake ratio", "error", err)
		return
	}
	ratio, ok := currentStakeRatio(accounts)
	if !ok {
		p.logger.Warn("vote accounts report no activated stake - network stake ratio unavailable")
		return
	}
	p.stakeRatio = stakeRatioSample{value: ratio, computedAt: now}
}

// inAlpenglowWarmup reports whether the cluster has not yet finalized far enough past the
// Alpenglow genesis slot. Certificates only cover slots after it, so everyone's last vote stalls
// around the switch. Without a known genesis slot there is nothing to wait for.
func (p *State) inAlpenglowWarmup() bool {
	if p.consensus.GenesisSlot == 0 {
		return false
	}
	return p.finalization.maxSlot <= p.consensus.GenesisSlot+p.alpenglowCfg.WarmupSlots
}

// isActiveNodeVoting judges whether the node holding the active identity is voting, using the
// evidence the current consensus phase supports.
func (p *State) isActiveNodeVoting(node solanagorpc.GetClusterNodesResult) bool {
	switch {
	case p.consensus.Phase == consensus.PhaseTower:
		return p.isNodeActiveAndVoting(node)
	case p.consensus.Phase == consensus.PhaseAlpenglow && !p.inAlpenglowWarmup():
		return p.applyAlpenglowVerdict(node, p.evaluateAlpenglowVoting(node))
	default:
		// While the phase is unknown, during the migration and during the Alpenglow warm-up,
		// votes stop landing for every validator, so only gossip presence is trusted.
		return true
	}
}

// alpenglowVerdict is the outcome of judging a node by the Alpenglow vote rule.
type alpenglowVerdict struct {
	// name is VerdictVoting, VerdictNotVoting, VerdictUnknown, VetoClusterStalled or
	// VetoVoteAccountExcluded.
	name string
	// lag is set when lagKnown is true.
	lag      rpc.VoteLag
	lagKnown bool
	// why explains a veto or an unknown verdict, for the logs.
	why string
}

// evaluateAlpenglowVoting judges a node by the Alpenglow vote rule: it counts as not voting only
// when its votes have stopped landing while the rest of the cluster keeps finalizing close to
// the tip.
//
// A finalization certificate needs at least 60% of stake. If the cluster keeps finalizing and
// the node's last vote does not advance, its votes are not reaching leaders or certificates, so
// the fault is local to the node. If the cluster is not finalizing, everyone's last vote freezes
// and a failover cannot help.
func (p *State) evaluateAlpenglowVoting(node solanagorpc.GetClusterNodesResult) alpenglowVerdict {
	ctx := context.Background()
	votePubkey, found, err := p.findVotePubkey(ctx, node.Pubkey)
	if err != nil {
		return alpenglowVerdict{name: VerdictUnknown, why: "failed to get vote accounts: " + err.Error()}
	}
	if !found {
		return alpenglowVerdict{name: VetoVoteAccountExcluded, why: "no vote account found for identity"}
	}

	lag, err := p.clusterRPC.GetVoteLag(ctx, votePubkey)
	if err != nil {
		return alpenglowVerdict{name: VerdictUnknown, why: "failed to get vote lag: " + err.Error()}
	}
	if !lag.Found || lag.ActivatedStake == 0 {
		// Forget the vote account so a replacement is discovered on the next Refresh.
		delete(p.votePubkeyCache, node.Pubkey.String())
		return alpenglowVerdict{name: VetoVoteAccountExcluded,
			why: fmt.Sprintf("vote account %s found=%t activated_stake=%d", votePubkey, lag.Found, lag.ActivatedStake)}
	}

	verdict := alpenglowVerdict{lag: lag, lagKnown: true}
	lagSlots := lag.Slots()
	threshold := p.alpenglowCfg.VoteLagSlotsThreshold
	now := p.now()
	// Finalization trailing the tip by more than the threshold explains the vote lag on its own:
	// last votes freeze as soon as finalization stops, before the stall duration has passed.
	finalizationLag := lag.ProcessedSlot - min(p.finalization.maxSlot, lag.ProcessedSlot)
	ratio, stakeGateOpen := p.networkStakeGateOpen(now)
	switch {
	case lagSlots <= threshold:
		verdict.name = VerdictVoting
	case !p.finalization.isLive(now, p.alpenglowCfg.FinalizationStallDuration):
		verdict.name = VetoClusterStalled
		verdict.why = fmt.Sprintf("finalized slot %d has not advanced", p.finalization.maxSlot)
	case finalizationLag > threshold:
		verdict.name = VetoClusterStalled
		verdict.why = fmt.Sprintf("finalized slot %d trails the processed slot by %d slots", p.finalization.maxSlot, finalizationLag)
	case !stakeGateOpen:
		verdict.name = VetoClusterStalled
		verdict.why = fmt.Sprintf("network stake ratio %.3f is stale or below the minimum %.3f", ratio, p.alpenglowCfg.NetworkCurrentStakeRatioMin)
	default:
		verdict.name = VerdictNotVoting
	}
	return verdict
}

// applyAlpenglowVerdict acts on a verdict and reports whether the node counts as voting.
func (p *State) applyAlpenglowVerdict(node solanagorpc.GetClusterNodesResult, verdict alpenglowVerdict) bool {
	p.recordAlpenglowVerdict(verdict)
	threshold := p.alpenglowCfg.VoteLagSlotsThreshold
	switch verdict.name {
	case VerdictUnknown:
		p.logger.Error("failed to judge the active peer's votes - assuming it is voting", "why", verdict.why)
		return true // forgive rpc error and assume innocence lest we trigger a false-positive failover
	case VetoVoteAccountExcluded:
		p.vetoReason = VetoVoteAccountExcluded
		p.logger.Error(fmt.Sprintf("‼️ %s vote account cannot vote under Alpenglow - a failover cannot fix this, so it is not counted against the active peer", p.nodeLabel(node)),
			"detail", verdict.why,
		)
		return true
	case VetoClusterStalled:
		p.vetoReason = VetoClusterStalled
		p.logger.Warn("active peer votes are not landing but the cluster looks stalled - not counting it against the active peer",
			"why", verdict.why, "vote_lag_slots", verdict.lag.Slots())
		return true
	case VerdictVoting:
		p.logger.Debug("active peer votes are landing", "vote_lag_slots", verdict.lag.Slots(), "threshold", threshold)
		return true
	}

	lag := verdict.lag
	p.leaderlessReason = LeaderlessReasonVoteLag
	p.activePeerDelinquent = true
	p.lastDelinquencyDetail = &DelinquencyDetail{
		LastVoteSlot:    lag.LastVote,
		CurrentSlot:     lag.ProcessedSlot,
		SlotDistance:    lag.Slots(),
		AllowedDistance: threshold,
	}
	p.logger.Error(fmt.Sprintf("‼️ %s votes are not landing (behind %d slots > %d allowed)", p.nodeLabel(node), lag.Slots(), threshold),
		"last_voted_at_slot", lag.LastVote,
	)
	return false
}

// recordAlpenglowVerdict keeps a verdict's name and measured vote lag for the last Refresh.
func (p *State) recordAlpenglowVerdict(verdict alpenglowVerdict) {
	p.alpenglowVerdict = verdict.name
	if verdict.lagKnown {
		p.activeVoteLag = verdict.lag.Slots()
		p.activeVoteLagKnown = true
	}
}

// networkStakeGateOpen reports whether enough of the network's stake is current for vote
// evidence to be trusted. A ratio that could not be refreshed recently keeps the gate closed.
// The gate is always open when failover.alpenglow.network_current_stake_ratio_min is 0.
func (p *State) networkStakeGateOpen(now time.Time) (ratio float64, open bool) {
	minimum := p.alpenglowCfg.NetworkCurrentStakeRatioMin
	if minimum == 0 {
		return 0, true
	}
	if !p.stakeRatio.isFresh(now, p.alpenglowCfg.NetworkStakeCheckIntervalDuration) {
		return 0, false
	}
	return p.stakeRatio.value, p.stakeRatio.value >= minimum
}

// findVotePubkey returns the vote account of an identity, discovering and caching it with an
// unfiltered getVoteAccounts call on first use. Unstaked and delinquent accounts are included,
// so found is false only when the RPC knows no vote account for the identity.
func (p *State) findVotePubkey(ctx context.Context, identity solana.PublicKey) (votePubkey solana.PublicKey, found bool, err error) {
	if cached, ok := p.votePubkeyCache[identity.String()]; ok {
		return cached, true, nil
	}
	keepUnstakedDelinquents := true
	accounts, err := p.clusterRPC.GetVoteAccounts(ctx, &solanagorpc.GetVoteAccountsOpts{
		Commitment:              solanagorpc.CommitmentProcessed,
		KeepUnstakedDelinquents: &keepUnstakedDelinquents,
	})
	if err != nil {
		return solana.PublicKey{}, false, err
	}
	for _, account := range slices.Concat(accounts.Current, accounts.Delinquent) {
		if account.NodePubkey.Equals(identity) {
			p.cacheVotePubkey(account)
			return account.VotePubkey, true, nil
		}
	}
	return solana.PublicKey{}, false, nil
}

// recordLeaderlessSample counts a leaderless sample and tracks whether the streak includes
// samples where the active identity was missing from gossip.
func (p *State) recordLeaderlessSample() {
	if p.leaderlessReason == "" {
		p.leaderlessReason = LeaderlessReasonGossipAbsent
	}
	if p.LeaderlessSamplesCount == 0 {
		p.streakHasGossipAbsent = false
	}
	if p.leaderlessReason == LeaderlessReasonGossipAbsent {
		p.streakHasGossipAbsent = true
	}
	p.LeaderlessSamplesCount++
}

// nodeLabel returns "<peer name> <ip>" for log lines, using a placeholder name for peers that
// are not declared in the config.
func (p *State) nodeLabel(node solanagorpc.GetClusterNodesResult) string {
	nodeIP := strings.Split(*node.Gossip, ":")[0]
	if name, ok := p.peerNameFromIP(nodeIP); ok {
		return name + " " + nodeIP
	}
	return undeclaredPeerName + " " + nodeIP
}
