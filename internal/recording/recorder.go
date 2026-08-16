package recording

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Recorder accumulates a FailoverEvent and writes it to disk asynchronously when complete.
// It is not safe for concurrent use; callers must drive it from a single goroutine.
type Recorder struct {
	mu      sync.Mutex
	event   FailoverEvent
	started time.Time
}

type writeJob struct {
	path    string
	data    []byte
	final   bool
	onError func(error)
}

var writeQueue = make(chan writeJob, 128)
var incidentSequence atomic.Uint64

func init() {
	go func() {
		for job := range writeQueue {
			if err := atomicWrite(job.path, job.data); err != nil {
				if job.onError != nil {
					job.onError(err)
				}
				continue
			}
			if job.final {
				_ = os.Remove(job.path + ".partial")
			}
		}
	}()
}

// New creates a Recorder seeded with node metadata, config, and a pre-failover gossip ring
// snapshot. detectedAt is when the failover condition (delinquency / threshold) was first seen.
func New(node NodeInfo, cfg ConfigSnapshot, detectedAt time.Time, ringSnapshot []GossipSample) *Recorder {
	return &Recorder{
		started: time.Now(),
		event: FailoverEvent{
			SchemaVersion: SchemaVersion,
			IncidentID:    fmt.Sprintf("%x-%d", detectedAt.UnixNano(), incidentSequence.Add(1)),
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ElapsedMillis == 0 && !r.event.DetectedAt.IsZero() {
		s.ElapsedMillis = s.SampledAt.Sub(r.event.DetectedAt).Milliseconds()
	}
	r.event.GossipSamples = append(r.event.GossipSamples, s)
}

// AddEvent appends a named timeline entry with the current UTC time.
func (r *Recorder) AddEvent(event, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.event.Timeline = append(r.event.Timeline, TimelineEntry{
		At:            time.Now().UTC(),
		Event:         event,
		Detail:        detail,
		ElapsedMillis: time.Since(r.started).Milliseconds(),
	})
}

// CheckpointAsync atomically refreshes the recoverable .partial recording.
func (r *Recorder) CheckpointAsync(outputDir string, onError func(error)) {
	r.enqueue(outputDir, false, nil, onError)
}

// WriteAsync sets the outcome and writes the recording file in a background goroutine.
// The file is named: svha-<active-pubkey>-<timestamp>-<producer>-<incident-id>-recording.json
// It returns immediately; the caller must not modify the recorder after this call.
func (r *Recorder) WriteAsync(outputDir string, outcome Outcome) {
	r.enqueue(outputDir, true, &outcome, nil)
}

// FinishAsync finalizes a recording and reports persistence failures through onError.
func (r *Recorder) FinishAsync(outputDir string, outcome Outcome, onError func(error)) {
	r.enqueue(outputDir, true, &outcome, onError)
}

func (r *Recorder) enqueue(outputDir string, final bool, outcome *Outcome, onError func(error)) {
	r.mu.Lock()
	if outcome != nil {
		r.event.Outcome = outcome
		now := time.Now().UTC()
		r.event.CompletedAt = &now
	}
	data, err := json.MarshalIndent(r.event, "", "  ")
	path := filepath.Join(outputDir, r.filename())
	r.mu.Unlock()
	if err != nil {
		if onError != nil {
			onError(fmt.Errorf("marshal recording: %w", err))
		}
		return
	}
	if !final {
		path += ".partial"
	}
	job := writeJob{path: path, data: data, final: final, onError: onError}
	if final {
		select {
		case writeQueue <- job:
		default:
			// Never hold the HA decision path behind disk I/O. The latest partial remains
			// recoverable if the process exits before this queued final write completes.
			go func() { writeQueue <- job }()
		}
		return
	}
	select {
	case writeQueue <- job:
	default:
		if onError != nil {
			onError(fmt.Errorf("recording checkpoint queue is full"))
		}
	}
}

func (r *Recorder) filename() string {
	ts := r.event.DetectedAt.UTC().Format("20060102T150405.000Z")
	producerIP := strings.ReplaceAll(r.event.Node.IP, ".", "_")
	return fmt.Sprintf("svha-%s-%s-%s-%s-recording.json", r.event.Node.ActivePubkey, ts, producerIP, r.event.IncidentID)
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".svha-recording-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RecoverPartials finalizes valid checkpoint files as interrupted incidents.
func RecoverPartials(outputDir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(outputDir, "svha-*-recording.json.partial"))
	if err != nil {
		return nil, err
	}
	var recovered []string
	for _, partial := range paths {
		data, err := os.ReadFile(partial)
		if err != nil {
			return recovered, err
		}
		var event FailoverEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return recovered, fmt.Errorf("parse partial %s: %w", partial, err)
		}
		now := time.Now().UTC()
		event.CompletedAt = &now
		event.Outcome = &Outcome{Result: "interrupted", FromNode: "unknown", ToNode: "unknown"}
		finalData, err := json.MarshalIndent(event, "", "  ")
		if err != nil {
			return recovered, err
		}
		final := strings.TrimSuffix(partial, ".partial")
		if err := atomicWrite(final, finalData); err != nil {
			return recovered, err
		}
		if err := os.Remove(partial); err != nil && !os.IsNotExist(err) {
			return recovered, err
		}
		recovered = append(recovered, final)
	}
	return recovered, nil
}
