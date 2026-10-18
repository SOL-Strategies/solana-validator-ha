package ha

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
	"github.com/sol-strategies/solana-validator-ha/internal/recording"
	"github.com/stretchr/testify/require"
)

// newNotVotingActive returns a manager that holds the active identity in gossip while its votes
// are not landing, with the leaderless threshold reached. peers are the other nodes visible in
// gossip. Recordings go to recordingDir; empty disables recording.
func newNotVotingActive(t *testing.T, peers map[string]gossip.PeerState, recordingDir string) *Manager {
	t.Helper()
	local := newLocalValidatorStub(t, 1000, "")
	manager := newDemotionCandidate(t, local.url, recordingDir)
	local.mu.Lock()
	local.identity = manager.cfg.Validator.Identities.ActivePubkey()
	local.mu.Unlock()
	seedGossipPeers(manager, peers)
	manager.gossipState.SetNotVotingActivePeerForTest(gossip.PeerState{
		Name:   manager.cfg.Validator.Name,
		IP:     manager.peerSelf.IP,
		Pubkey: manager.cfg.Validator.Identities.ActivePubkey(),
	})
	return manager
}

// waitForRecordings waits until dir holds want finished recordings and no checkpoint, and
// returns their outcomes. It fails if more than want are written.
func waitForRecordings(t *testing.T, dir string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		matches, _ := filepath.Glob(filepath.Join(dir, "*-recording.json"))
		partials, _ := filepath.Glob(filepath.Join(dir, "*.partial"))
		if len(matches) > want {
			t.Fatalf("%d recordings written, want %d", len(matches), want)
		}
		if len(matches) == want && len(partials) == 0 {
			outcomes := make([]string, 0, len(matches))
			for _, path := range matches {
				outcomes = append(outcomes, readRecordingOutcome(t, path))
			}
			return outcomes
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d recordings and %d checkpoints, want %d recordings and no checkpoint", len(matches), len(partials), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readRecordingOutcome(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading recording: %v", err)
	}
	var event recording.FailoverEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("parsing recording: %v", err)
	}
	if event.Outcome == nil {
		t.Fatal("recording has no outcome")
	}
	return event.Outcome.Result
}

func TestEnsureHAState_NotVotingActiveCountsAsInGossip(t *testing.T) {
	manager := newNotVotingActive(t, map[string]gossip.PeerState{}, "")

	if !manager.isSelfInGossip() {
		t.Error("isSelfInGossip() = false, want true: we hold the active identity in gossip")
	}
	if !manager.buildGossipSample().SelfInGossip {
		t.Error("recording sample SelfInGossip = false, want true")
	}
}

func TestEnsureHAState_NotVotingActiveWithoutPeerStaysActive(t *testing.T) {
	dir := t.TempDir()
	manager := newNotVotingActive(t, map[string]gossip.PeerState{}, dir)

	manager.ensureHAState()

	if got := manager.cache.GetState().FailoverStatus; got != "idle" {
		t.Errorf("FailoverStatus = %q, want idle: with no peer to take over we must stay active", got)
	}
	if got := waitForRecordingOutcome(t, dir); got != outcomeNotVotingRetainedActive {
		t.Errorf("recording outcome = %q, want %q", got, outcomeNotVotingRetainedActive)
	}
}

func TestEnsureHAState_NotVotingActiveWithPeerDemotes(t *testing.T) {
	dir := t.TempDir()
	manager := newNotVotingActive(t, map[string]gossip.PeerState{"peer1": {IP: "192.168.1.101", Name: "peer1"}}, dir)

	manager.ensureHAState()

	if got := manager.cache.GetState().FailoverStatus; got != "becoming_passive" {
		t.Errorf("FailoverStatus = %q, want becoming_passive: a visible peer can take over", got)
	}
	// the stub keeps answering with the active identity, so the dry-run demotion is not confirmed
	if got := waitForRecordingOutcome(t, dir); got != outcomeDemotionFailed {
		t.Errorf("recording outcome = %q, want %q", got, outcomeDemotionFailed)
	}
}

func TestEnsureHAState_RepeatedNoActionOutcomeIsRecordedOnce(t *testing.T) {
	dir := t.TempDir()
	manager := newNotVotingActive(t, map[string]gossip.PeerState{}, dir)

	for range 3 {
		manager.ensureHAState()
	}
	waitForRecordings(t, dir, 1)

	// the votes land again: the condition clears, so the next occurrence is a new incident
	manager.gossipState.SetNotVotingActivePeerForTest(gossip.PeerState{})
	seedGossipPeers(manager, map[string]gossip.PeerState{
		manager.cfg.Validator.Name: {IP: manager.peerSelf.IP, Name: manager.cfg.Validator.Name, Pubkey: manager.cfg.Validator.Identities.ActivePubkey(), LastSeenActive: true},
	})
	manager.gossipState.LeaderlessSamplesCount = 0
	manager.ensureHAState()

	seedGossipPeers(manager, map[string]gossip.PeerState{})
	manager.gossipState.SetNotVotingActivePeerForTest(gossip.PeerState{
		Name: manager.cfg.Validator.Name, IP: manager.peerSelf.IP, Pubkey: manager.cfg.Validator.Identities.ActivePubkey(),
	})
	manager.gossipState.LeaderlessSamplesCount = manager.cfg.Failover.LeaderlessSamplesThreshold
	manager.ensureHAState()

	for _, got := range waitForRecordings(t, dir, 2) {
		if got != outcomeNotVotingRetainedActive {
			t.Errorf("recording outcome = %q, want %q", got, outcomeNotVotingRetainedActive)
		}
	}
}

func TestEnsureHAState_DifferentOutcomeAfterRepeatIsRecorded(t *testing.T) {
	dir := t.TempDir()
	manager := newNotVotingActive(t, map[string]gossip.PeerState{}, dir)
	manager.ensureHAState()
	manager.ensureHAState()
	waitForRecordings(t, dir, 1)

	// a peer comes up while our votes are still not landing: the demotion must be recorded
	seedGossipPeers(manager, map[string]gossip.PeerState{"peer1": {IP: "192.168.1.101", Name: "peer1"}})
	manager.ensureHAState()

	outcomes := waitForRecordings(t, dir, 2)
	found := false
	for _, got := range outcomes {
		found = found || got == outcomeDemotionFailed
	}
	if !found {
		t.Errorf("recording outcomes = %v, want one %q", outcomes, outcomeDemotionFailed)
	}
}

// waitForRecordingWrites puts a separate recording behind the pending jobs in the
// shared write queue, so assertions also wait for failed writes and discards.
func waitForRecordingWrites(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	barrier := recording.New(recording.NodeInfo{ActivePubkey: "barrier"}, recording.ConfigSnapshot{}, time.Now(), nil)
	barrier.FinishAsync(dir, recording.Outcome{Result: "barrier"}, nil)
	require.Equal(t, "barrier", waitForRecordingOutcome(t, dir))
}

func TestEnsureHAState_NoActionRecordingRetriesAfterWriteFailure(t *testing.T) {
	dir := t.TempDir()
	manager := newNotVotingActive(t, map[string]gossip.PeerState{}, dir)

	// The directory passed initialization, then became unavailable while the
	// not-voting condition persisted. Neither checkpoints nor the final write succeed.
	require.NoError(t, os.Remove(dir))
	manager.ensureHAState()
	waitForRecordingWrites(t)

	// Storage recovers without any change in gossip or the no-action outcome.
	require.NoError(t, os.Mkdir(dir, 0755))
	manager.ensureHAState()
	waitForRecordingWrites(t)
	outcomes := waitForRecordings(t, dir, 1)
	require.Equal(t, []string{outcomeNotVotingRetainedActive}, outcomes)

	// Once the retry is persisted, further identical outcomes are suppressed again.
	manager.ensureHAState()
	waitForRecordingWrites(t)
	require.Equal(t, outcomes, waitForRecordings(t, dir, 1))
}
