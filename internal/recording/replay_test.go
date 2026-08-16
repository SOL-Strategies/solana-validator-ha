package recording

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplayTwoNodeGolden(t *testing.T) {
	paths, err := filepath.Glob("testdata/two-node/*.json")
	if err != nil {
		t.Fatal(err)
	}
	data, warnings, err := LoadReplayData(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	got, err := RenderReplay(data)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/two-node.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("replay output changed; review the display and update testdata/two-node.golden if intentional\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestReplayEntryTTFLAndRoleTransitions(t *testing.T) {
	paths, err := filepath.Glob("testdata/two-node/*.json")
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := LoadReplayData(paths)
	if err != nil {
		t.Fatal(err)
	}

	var sawPreIncident, sawChicagoDemotion, sawLondonPromotion bool
	for _, entry := range data.Entries {
		switch {
		case entry.Kind == "sample" && entry.NodeName == "chicago" && entry.At.Equal(time.Date(2026, 3, 31, 14, 30, 10, 0, time.UTC)):
			sawPreIncident = true
			if entry.TTFL != -5*time.Second || entry.Role != "active" {
				t.Fatalf("pre-incident entry: want TTFL=-5s role=active, got TTFL=%s role=%s", entry.TTFL, entry.Role)
			}
		case entry.Event == "passive_identity_confirmed":
			sawChicagoDemotion = true
			if entry.Role != "passive" {
				t.Fatalf("passive confirmation: want role=passive, got %s", entry.Role)
			}
		case entry.Event == "active_identity_confirmed":
			sawLondonPromotion = true
			if entry.Role != "active" {
				t.Fatalf("active confirmation: want role=active, got %s", entry.Role)
			}
		}
	}
	if !sawPreIncident || !sawChicagoDemotion || !sawLondonPromotion {
		t.Fatalf("missing expected entries: pre_incident=%t chicago_demotion=%t london_promotion=%t", sawPreIncident, sawChicagoDemotion, sawLondonPromotion)
	}
}

func TestReplayThreeNodeProductionInputs(t *testing.T) {
	paths, err := filepath.Glob("testdata/three-node/*.json")
	if err != nil {
		t.Fatal(err)
	}
	data, warnings, err := LoadReplayData(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	out, err := RenderReplay(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"london", "chicago", "frankfurt", "became_active", "aborted_peer_took_over", "demoted_passive"} {
		if !strings.Contains(out, expected) {
			t.Errorf("output missing %q", expected)
		}
	}
}

func TestReplayPreviewScenarios(t *testing.T) {
	tests := map[string]string{
		"partition-recovery": "recovered_no_failover",
		"last-node-standing": "last_node_standing_retained_active",
		"delinquency-bypass": "delinquency_bypass_triggered",
		"command-failure":    "promotion_failed",
	}
	for scenario, expected := range tests {
		t.Run(scenario, func(t *testing.T) {
			paths, err := filepath.Glob(filepath.Join("testdata", scenario, "*.json"))
			if err != nil || len(paths) < 2 {
				t.Fatalf("expected at least two fixture inputs: paths=%v err=%v", paths, err)
			}
			data, warnings, err := LoadReplayData(paths)
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			out, err := RenderReplay(data)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, expected) {
				t.Fatalf("preview missing %q:\n%s", expected, out)
			}
		})
	}
}

func TestReplayV1DoesNotInventV2LocalState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.json")
	raw := `{"schema_version":"1","node":{"name":"legacy","ip":"127.0.0.1","active_pubkey":"active","passive_pubkey":"passive"},"config":{"poll_interval_duration":"5s"},"detected_at":"2026-03-31T14:30:15Z","gossip_samples":[{"sampled_at":"2026-03-31T14:30:15Z","peers":[],"leaderless_samples_count":1,"active_peer_delinquent":false,"rpc_error":false}],"timeline":[],"outcome":{"result":"aborted_other","from_node":"unknown","to_node":"unknown"}}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	data, _, err := LoadReplayData([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderReplay(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "local=") || strings.Contains(out, "healthy=") {
		t.Fatalf("v1 replay invented unavailable local state:\n%s", out)
	}
	if !strings.Contains(out, "schema=1 binary=unknown") {
		t.Fatalf("v1 provenance missing:\n%s", out)
	}
	if !strings.Contains(out, "legacy  unknown  gossip") {
		t.Fatalf("v1 replay should show an unknown role:\n%s", out)
	}
}
