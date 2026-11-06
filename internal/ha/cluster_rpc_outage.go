package ha

import "time"

// clusterRPCOutageTransition is what a poll changed about the cluster RPC outage state.
type clusterRPCOutageTransition int

const (
	clusterRPCOutageUnchanged clusterRPCOutageTransition = iota
	clusterRPCOutageStarted
	clusterRPCOutageEnded
)

// clusterRPCOutageMonitor reports when every cluster RPC endpoint has been failing for
// consecutive polls. Failed polls deliberately count as neither leaderless nor healthy samples,
// so a provider outage cannot take a healthy active offline. The cost is that a node cannot
// notice a missing active peer, or take over from one, until the endpoints answer again, while
// its failover status still reads idle. This monitor only makes that state visible: it takes no
// part in any failover decision.
// A clusterRPCOutageMonitor is not safe for concurrent use.
type clusterRPCOutageMonitor struct {
	// threshold is how many consecutive polls must fail before it counts as an outage.
	threshold int
	now       func() time.Time

	consecutiveFailures int
	// firstFailureAt is when the current run of failed polls began.
	firstFailureAt time.Time
	active         bool
}

func newClusterRPCOutageMonitor(threshold int) *clusterRPCOutageMonitor {
	return &clusterRPCOutageMonitor{threshold: threshold, now: time.Now}
}

// observe records one poll and returns the transition it caused.
func (o *clusterRPCOutageMonitor) observe(failed bool) clusterRPCOutageTransition {
	if !failed {
		wasActive := o.active
		o.consecutiveFailures = 0
		o.active = false
		if wasActive {
			return clusterRPCOutageEnded
		}
		return clusterRPCOutageUnchanged
	}

	if o.consecutiveFailures == 0 {
		o.firstFailureAt = o.now()
	}
	o.consecutiveFailures++
	if !o.active && o.consecutiveFailures >= o.threshold {
		o.active = true
		return clusterRPCOutageStarted
	}
	return clusterRPCOutageUnchanged
}

// duration returns how long the latest run of failed polls has lasted.
func (o *clusterRPCOutageMonitor) duration() time.Duration {
	return o.now().Sub(o.firstFailureAt)
}
