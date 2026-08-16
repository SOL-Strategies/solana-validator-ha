package recording

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Ring ────────────────────────────────────────────────────────────────────

func TestRing_EmptySnapshot(t *testing.T) {
	var r Ring
	if got := r.Snapshot(); got != nil {
		t.Fatalf("expected nil snapshot from empty ring, got %v", got)
	}
}

func TestRing_PartialFill(t *testing.T) {
	var r Ring
	for i := 0; i < 5; i++ {
		r.Add(GossipSample{LeaderlessSamplesCount: i})
	}
	snap := r.Snapshot()
	if len(snap) != 5 {
		t.Fatalf("expected 5 samples, got %d", len(snap))
	}
	for i, s := range snap {
		if s.LeaderlessSamplesCount != i {
			t.Errorf("sample[%d]: want count=%d, got %d", i, i, s.LeaderlessSamplesCount)
		}
	}
}

func TestRing_FullFill(t *testing.T) {
	var r Ring
	for i := 0; i < ringSize; i++ {
		r.Add(GossipSample{LeaderlessSamplesCount: i})
	}
	snap := r.Snapshot()
	if len(snap) != ringSize {
		t.Fatalf("expected %d samples, got %d", ringSize, len(snap))
	}
	for i, s := range snap {
		if s.LeaderlessSamplesCount != i {
			t.Errorf("sample[%d]: want count=%d, got %d", i, i, s.LeaderlessSamplesCount)
		}
	}
}

func TestRing_Overwrite_ChronologicalOrder(t *testing.T) {
	var r Ring
	// fill ring, then add one more to trigger wrap-around
	for i := 0; i < ringSize+1; i++ {
		r.Add(GossipSample{LeaderlessSamplesCount: i})
	}
	snap := r.Snapshot()
	if len(snap) != ringSize {
		t.Fatalf("expected %d samples after overwrite, got %d", ringSize, len(snap))
	}
	// oldest surviving entry should be count=1 (count=0 was overwritten)
	if snap[0].LeaderlessSamplesCount != 1 {
		t.Errorf("oldest sample: want count=1, got %d", snap[0].LeaderlessSamplesCount)
	}
	// newest should be count=ringSize
	if snap[ringSize-1].LeaderlessSamplesCount != ringSize {
		t.Errorf("newest sample: want count=%d, got %d", ringSize, snap[ringSize-1].LeaderlessSamplesCount)
	}
}

func TestRing_SnapshotIsIndependentCopy(t *testing.T) {
	var r Ring
	r.Add(GossipSample{LeaderlessSamplesCount: 1})
	snap := r.Snapshot()
	snap[0].LeaderlessSamplesCount = 99
	// ring internals must not be affected
	snap2 := r.Snapshot()
	if snap2[0].LeaderlessSamplesCount != 1 {
		t.Errorf("snapshot mutation affected ring: got %d", snap2[0].LeaderlessSamplesCount)
	}
}

// ── Recorder ────────────────────────────────────────────────────────────────

func makeRecorder() *Recorder {
	node := NodeInfo{
		Name:          "london",
		IP:            "185.26.11.91",
		ActivePubkey:  "ActivePubkey111111111111111111111111111111",
		PassivePubkey: "PassivePubkey11111111111111111111111111111",
	}
	cfg := ConfigSnapshot{
		PollIntervalDuration:               "5s",
		LeaderlessSamplesThreshold:         3,
		LeaderlessConfirmationPollDuration: "1s",
		DelinquencyBypass:                  false,
	}
	return New(node, cfg, time.Date(2026, 3, 31, 14, 30, 22, 0, time.UTC), nil)
}

func TestRecorder_AddEvent(t *testing.T) {
	rec := makeRecorder()
	rec.AddEvent("rank_0_no_delay", "")
	rec.AddEvent("ensure_active_start", "detail=foo")
	if len(rec.event.Timeline) != 2 {
		t.Fatalf("expected 2 timeline entries, got %d", len(rec.event.Timeline))
	}
	if rec.event.Timeline[0].Event != "rank_0_no_delay" {
		t.Errorf("unexpected event name: %s", rec.event.Timeline[0].Event)
	}
	if rec.event.Timeline[1].Detail != "detail=foo" {
		t.Errorf("unexpected detail: %s", rec.event.Timeline[1].Detail)
	}
}

func TestRecorder_AddSample(t *testing.T) {
	rec := makeRecorder()
	rec.AddSample(GossipSample{LeaderlessSamplesCount: 3, RPCError: true})
	if len(rec.event.GossipSamples) != 1 {
		t.Fatalf("expected 1 gossip sample, got %d", len(rec.event.GossipSamples))
	}
	if !rec.event.GossipSamples[0].RPCError {
		t.Error("expected RPCError=true on appended sample")
	}
}

func TestRecorder_WriteAsync_FileCreated(t *testing.T) {
	dir := t.TempDir()
	rec := makeRecorder()
	rec.AddEvent("rank_0_no_delay", "")

	outcome := Outcome{Result: "became_active", FromNode: "chicago", ToNode: "london"}
	rec.WriteAsync(dir, outcome)

	// WriteAsync writes in a goroutine — poll briefly for the file to appear
	var files []string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "-recording.json") {
				files = append(files, e.Name())
			}
		}
		if len(files) > 0 {
			break
		}
		files = nil
		time.Sleep(10 * time.Millisecond)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 recording file, got %v", files)
	}
	name := files[0]
	if !strings.HasSuffix(name, "-recording.json") {
		t.Errorf("filename should end in -recording.json, got %s", name)
	}
	if !strings.HasPrefix(name, "svha-") {
		t.Errorf("filename should start with svha-, got %s", name)
	}
	if !strings.Contains(name, "ActivePubkey") {
		t.Errorf("filename should contain active pubkey, got %s", name)
	}
	if !strings.Contains(name, "20260331T143022.000Z") {
		t.Errorf("filename should contain millisecond timestamp, got %s", name)
	}
	if !strings.Contains(name, "185_26_11_91") {
		t.Errorf("filename should contain producer IP (dots as underscores), got %s", name)
	}
}

func TestRecorder_WriteAsync_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	rec := makeRecorder()
	outcome := Outcome{Result: "became_active", FromNode: "chicago", ToNode: "london"}
	rec.WriteAsync(dir, outcome)

	var path string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), "-recording.json") {
				path = filepath.Join(dir, entry.Name())
				break
			}
		}
		if path != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if path == "" {
		t.Fatal("recording file never appeared")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading recording file: %v", err)
	}
	var event FailoverEvent
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if event.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version: want %s, got %s", SchemaVersion, event.SchemaVersion)
	}
	if event.Node.Name != "london" {
		t.Errorf("node.name: want london, got %s", event.Node.Name)
	}
	if event.Outcome == nil {
		t.Fatal("outcome is nil")
	}
	if event.Outcome.Result != "became_active" {
		t.Errorf("outcome.result: want became_active, got %s", event.Outcome.Result)
	}
	if event.Outcome.FromNode != "chicago" {
		t.Errorf("outcome.from_node: want chicago, got %s", event.Outcome.FromNode)
	}
	if event.Outcome.ToNode != "london" {
		t.Errorf("outcome.to_node: want london, got %s", event.Outcome.ToNode)
	}
}

func TestRecorder_RingSnapshotPreserved(t *testing.T) {
	node := NodeInfo{Name: "london", ActivePubkey: "Pubkey1", PassivePubkey: "Pubkey2"}
	cfg := ConfigSnapshot{PollIntervalDuration: "5s", LeaderlessSamplesThreshold: 3}
	ring := []GossipSample{
		{LeaderlessSamplesCount: 1},
		{LeaderlessSamplesCount: 2},
	}
	rec := New(node, cfg, time.Now().UTC(), ring)
	if len(rec.event.GossipSamples) != 2 {
		t.Fatalf("expected 2 ring samples seeded, got %d", len(rec.event.GossipSamples))
	}
}

func TestRecoverPartials(t *testing.T) {
	dir := t.TempDir()
	rec := makeRecorder()
	rec.AddEvent("incident_started", "rpc_error=true")
	rec.CheckpointAsync(dir, func(err error) { t.Errorf("checkpoint failed: %v", err) })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.partial"))
		if len(matches) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	recovered, err := RecoverPartials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 {
		t.Fatalf("expected one recovered recording, got %v", recovered)
	}
	raw, err := os.ReadFile(recovered[0])
	if err != nil {
		t.Fatal(err)
	}
	var event FailoverEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Outcome == nil || event.Outcome.Result != "interrupted" {
		t.Fatalf("unexpected recovered outcome: %#v", event.Outcome)
	}
}

func TestRecorder_SameMillisecondDoesNotCollide(t *testing.T) {
	dir := t.TempDir()
	first := makeRecorder()
	second := makeRecorder()
	outcome := Outcome{Result: "recovered_no_failover", FromNode: "unknown", ToNode: "unknown"}
	first.WriteAsync(dir, outcome)
	second.WriteAsync(dir, outcome)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*-recording.json"))
		if len(matches) == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*-recording.json"))
	t.Fatalf("expected two distinct recordings, got %v", matches)
}

func TestRecorder_ReportsWriteFailure(t *testing.T) {
	rec := makeRecorder()
	errCh := make(chan error, 1)
	rec.CheckpointAsync(filepath.Join(t.TempDir(), "missing"), func(err error) { errCh <- err })
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected a write error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for asynchronous write failure")
	}
}
