package gossip

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	solana "github.com/solana-foundation/solana-go/v2"
	solanagorpc "github.com/solana-foundation/solana-go/v2/rpc"
)

// voterSetRecheckInterval is how long an answer about voter-set membership is reused while the
// votes stay missing. Agave builds the voter set once per epoch, so it rarely changes.
const voterSetRecheckInterval = 10 * time.Minute

// voterSetCheck is the last answer about whether a vote account is in the Alpenglow voter set.
type voterSetCheck struct {
	votePubkey solana.PublicKey
	// excludedWhy says why the account is left out; empty when it is in the voter set.
	excludedWhy string
	checkedAt   time.Time
}

// voterSetExclusion says why a vote account is left out of the Alpenglow voter set, or returns
// "" when it is in it. A staked account can still be listed by getVoteAccounts while Agave leaves
// it out, and its votes then never count.
//
// Agave builds the voter set from the epoch's stake snapshot (BLSPubkeyToRankMap::new) and leaves
// out every staked account that has no BLS pubkey, shares its BLS pubkey with another staked
// account, or shares its node identity with another staked account. This check applies the same
// rules to the accounts' current state, which matches the snapshot unless a key or identity
// changed since it was taken. A BLS pubkey that is not a valid curve point is not detected.
func (p *State) voterSetExclusion(ctx context.Context, votePubkey solana.PublicKey) (string, error) {
	now := p.now()
	if c := p.voterSet; c.votePubkey.Equals(votePubkey) && now.Sub(c.checkedAt) < voterSetRecheckInterval {
		return c.excludedWhy, nil
	}
	accounts, err := p.clusterRPC.GetVoteAccounts(ctx, &solanagorpc.GetVoteAccountsOpts{Commitment: solanagorpc.CommitmentProcessed})
	if err != nil {
		return "", fmt.Errorf("failed to get vote accounts: %w", err)
	}
	staked := epochStakedVoteAccounts(accounts)
	stakedPubkeys := make([]solana.PublicKey, 0, len(staked))
	for _, account := range staked {
		stakedPubkeys = append(stakedPubkeys, account.VotePubkey)
	}
	keys, err := p.clusterRPC.GetVoteAccountBLSPubkeys(ctx, stakedPubkeys)
	if err != nil {
		return "", fmt.Errorf("failed to get BLS pubkeys: %w", err)
	}
	why := voterSetExclusionReason(votePubkey, staked, keys)
	p.voterSet = voterSetCheck{votePubkey: votePubkey, excludedWhy: why, checkedAt: now}
	return why, nil
}

// epochStakedVoteAccounts returns the vote accounts with stake in the current epoch's snapshot,
// the candidates for the voter set.
func epochStakedVoteAccounts(accounts *solanagorpc.GetVoteAccountsResult) []solanagorpc.VoteAccountsResult {
	var staked []solanagorpc.VoteAccountsResult
	for _, account := range slices.Concat(accounts.Current, accounts.Delinquent) {
		if account.EpochVoteAccount && account.ActivatedStake > 0 {
			staked = append(staked, account)
		}
	}
	return staked
}

// voterSetExclusionReason applies Agave's voter-set rules to votePubkey, given the staked vote
// accounts and their BLS pubkeys.
func voterSetExclusionReason(votePubkey solana.PublicKey, staked []solanagorpc.VoteAccountsResult, keys map[solana.PublicKey]rpc.BLSPubkey) string {
	i := slices.IndexFunc(staked, func(a solanagorpc.VoteAccountsResult) bool { return a.VotePubkey.Equals(votePubkey) })
	if i < 0 {
		return "it has no stake in the current epoch"
	}
	identity := staked[i].NodePubkey
	if n := countFunc(staked, func(a solanagorpc.VoteAccountsResult) bool { return a.NodePubkey.Equals(identity) }); n > 1 {
		return fmt.Sprintf("its identity %s is set on %d staked vote accounts", identity, n)
	}
	key, ok := keys[votePubkey]
	if !ok {
		return "it has no BLS pubkey"
	}
	if n := countFunc(staked, func(a solanagorpc.VoteAccountsResult) bool {
		other, ok := keys[a.VotePubkey]
		return ok && other == key
	}); n > 1 {
		return fmt.Sprintf("its BLS pubkey is set on %d staked vote accounts", n)
	}
	return ""
}

func countFunc[T any](items []T, match func(T) bool) int {
	n := 0
	for _, item := range items {
		if match(item) {
			n++
		}
	}
	return n
}

// unvotedHolders tracks a vote account whose votes have not landed, and every IP that has held
// the active identity since. HA peers share one vote account, so when the identity has moved and
// the votes still do not land, the fault is with the account and another failover cannot help.
type unvotedHolders struct {
	votePubkey solana.PublicKey
	ips        []string
}

// failoverIneffective records that the node at ip holds the active identity while votePubkey's
// votes are not landing, and reports whether the identity has already moved during this spell.
// why lists the IPs that held it. endUnvotedSpell ends the spell.
func (p *State) failoverIneffective(ip string, votePubkey solana.PublicKey) (why string, ineffective bool) {
	h := &p.unvotedHolders
	if !h.votePubkey.Equals(votePubkey) {
		*h = unvotedHolders{votePubkey: votePubkey}
	}
	if !slices.Contains(h.ips, ip) {
		h.ips = append(h.ips, ip)
	}
	if len(h.ips) < 2 {
		return "", false
	}
	return fmt.Sprintf("votes have not landed while the active identity was held by %s", strings.Join(h.ips, ", ")), true
}

// endUnvotedSpell is called when the active's votes land. It forgets the nodes that held the
// identity and the voter-set answer, so the next spell of missing votes starts afresh.
func (p *State) endUnvotedSpell() {
	p.unvotedHolders = unvotedHolders{}
	p.voterSet = voterSetCheck{}
}
