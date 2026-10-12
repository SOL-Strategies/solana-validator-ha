package ha

import (
	"context"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	solanagorpc "github.com/solana-foundation/solana-go/v2/rpc"
)

// isolationMonitor tells an active node that has lost the network apart from one whose cluster
// RPC provider is down. Both see cluster RPC calls fail, but only the isolated node also stops
// receiving blocks, so its local processed slot, read over loopback, stops moving.
//
// An isolated node never reaches the leaderless threshold, because failed cluster RPC calls are
// not counted as leaderless samples. Without this check it would stay staked until the network
// returns, and then run alongside the peer that replaced it.
// An isolationMonitor is not safe for concurrent use.
type isolationMonitor struct {
	localRPC *rpc.Client
	// failuresThreshold is how many consecutive polls the cluster RPC must fail.
	failuresThreshold int
	// stallAfter is how long the local processed slot must stand still.
	stallAfter time.Duration
	now        func() time.Time

	clusterRPCFailures int
	localSlot          uint64
	// localSlotChangedAt is when localSlot last changed. Zero means the local slot is unknown.
	localSlotChangedAt time.Time
}

func newIsolationMonitor(localRPC *rpc.Client, failuresThreshold int, stallAfter time.Duration) *isolationMonitor {
	return &isolationMonitor{
		localRPC:          localRPC,
		failuresThreshold: failuresThreshold,
		stallAfter:        stallAfter,
		now:               time.Now,
	}
}

// observe records one poll: whether the cluster RPC failed, and the local processed slot.
// A local RPC error makes the local slot unknown, which restarts the stall timer, so a node is
// never demoted on evidence it could not read.
func (i *isolationMonitor) observe(ctx context.Context, clusterRPCFailed bool) {
	if clusterRPCFailed {
		i.clusterRPCFailures++
	} else {
		i.clusterRPCFailures = 0
	}

	slot, err := i.localRPC.GetSlotWithCommitment(ctx, solanagorpc.CommitmentProcessed)
	if err != nil {
		i.localSlotChangedAt = time.Time{}
		return
	}
	// Any change counts, including a lower slot after a validator restart.
	if i.localSlotChangedAt.IsZero() || slot != i.localSlot {
		i.localSlot = slot
		i.localSlotChangedAt = i.now()
	}
}

// isolated reports whether the cluster RPC has failed for enough consecutive polls while the
// local processed slot has stood still for the stall duration.
func (i *isolationMonitor) isolated() bool {
	if i.clusterRPCFailures < i.failuresThreshold || i.localSlotChangedAt.IsZero() {
		return false
	}
	return i.localSlotStalledFor() >= i.stallAfter
}

func (i *isolationMonitor) localSlotStalledFor() time.Duration {
	return i.now().Sub(i.localSlotChangedAt)
}
