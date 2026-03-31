package recording

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Recorder accumulates a FailoverEvent and writes it to disk asynchronously when complete.
// It is not safe for concurrent use; callers must drive it from a single goroutine.
type Recorder struct {
	event FailoverEvent
}

// New creates a Recorder seeded with node metadata, config, and a pre-failover gossip ring
// snapshot. detectedAt is when the failover condition (delinquency / threshold) was first seen.
func New(node NodeInfo, cfg ConfigSnapshot, detectedAt time.Time, ringSnapshot []GossipSample) *Recorder {
	return &Recorder{
		event: FailoverEvent{
			SchemaVersion: SchemaVersion,
			Node:          node,
			Config:        cfg,
			DetectedAt:    detectedAt,
			GossipSamples: ringSnapshot,
		},
	}
}

// AddSample appends a gossip sample captured during the failover itself (e.g. post-delay
// revalidation). Pre-failover context is already in the ring snapshot passed to New.
func (r *Recorder) AddSample(s GossipSample) {
	r.event.GossipSamples = append(r.event.GossipSamples, s)
}

// AddEvent appends a named timeline entry with the current UTC time.
func (r *Recorder) AddEvent(event, detail string) {
	r.event.Timeline = append(r.event.Timeline, TimelineEntry{
		At:     time.Now().UTC(),
		Event:  event,
		Detail: detail,
	})
}

// WriteAsync sets the outcome and writes the recording file in a background goroutine.
// The file is named: svha-<timestamp>-<producer>-<from>-to-<to>-recording.json
// It returns immediately; the caller must not modify the recorder after this call.
func (r *Recorder) WriteAsync(outputDir string, outcome Outcome) {
	r.event.Outcome = &outcome
	snapshot := r.event // copy before spawning goroutine
	go func() {
		ts := snapshot.DetectedAt.UTC().Format("20060102T150405Z")
		filename := fmt.Sprintf("svha-%s-%s-%s-%s-to-%s-recording.json",
			snapshot.Node.ActivePubkey, ts, snapshot.Node.Name, outcome.FromNode, outcome.ToNode)
		path := filepath.Join(outputDir, filename)

		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return
		}
		// WriteFile is best-effort; a failure here is non-fatal — the failover already completed.
		os.WriteFile(path, data, 0644) //nolint:errcheck
	}()
}
