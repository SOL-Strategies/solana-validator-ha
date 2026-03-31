package recording

const ringSize = 20

// Ring is a fixed-size circular buffer of GossipSamples used to retain pre-failover context.
// It is written on every gossip poll tick and carries no lock — callers must ensure
// single-writer access (the HA monitor goroutine in practice).
type Ring struct {
	samples [ringSize]GossipSample
	head    int
	size    int
}

// Add inserts a sample, overwriting the oldest entry when full.
func (r *Ring) Add(s GossipSample) {
	r.samples[r.head] = s
	r.head = (r.head + 1) % ringSize
	if r.size < ringSize {
		r.size++
	}
}

// Snapshot returns a copy of all stored samples in chronological order (oldest first).
func (r *Ring) Snapshot() []GossipSample {
	if r.size == 0 {
		return nil
	}
	out := make([]GossipSample, r.size)
	start := (r.head - r.size + ringSize) % ringSize
	for i := 0; i < r.size; i++ {
		out[i] = r.samples[(start+i)%ringSize]
	}
	return out
}
