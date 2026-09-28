package config

import (
	"strings"
	"testing"
	"time"
)

func TestConsensus_SetDefaults(t *testing.T) {
	var consensus Consensus
	consensus.SetDefaults()
	if consensus.Mode != ConsensusModeAuto {
		t.Errorf("Mode = %q, want %q", consensus.Mode, ConsensusModeAuto)
	}
	if consensus.DetectionIntervalDuration != 60*time.Second {
		t.Errorf("DetectionIntervalDuration = %s, want 60s", consensus.DetectionIntervalDuration)
	}

	pinned := Consensus{Mode: ConsensusModeTower, DetectionIntervalDuration: 5 * time.Second}
	pinned.SetDefaults()
	if pinned.Mode != ConsensusModeTower || pinned.DetectionIntervalDuration != 5*time.Second {
		t.Errorf("SetDefaults() overrode explicit values: %+v", pinned)
	}
}

func TestConsensus_Validate(t *testing.T) {
	tests := []struct {
		name      string
		consensus Consensus
		wantErr   string // empty when valid
	}{
		{name: "auto", consensus: Consensus{Mode: ConsensusModeAuto, DetectionIntervalDuration: time.Minute}},
		{name: "tower", consensus: Consensus{Mode: ConsensusModeTower, DetectionIntervalDuration: time.Minute}},
		{name: "alpenglow", consensus: Consensus{Mode: ConsensusModeAlpenglow, DetectionIntervalDuration: time.Minute}},
		{name: "unknown mode", consensus: Consensus{Mode: "votor", DetectionIntervalDuration: time.Minute}, wantErr: "cluster.consensus.mode"},
		{name: "zero interval", consensus: Consensus{Mode: ConsensusModeAuto}, wantErr: "cluster.consensus.detection_interval_duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidationError(t, tt.consensus.Validate(), tt.wantErr)
		})
	}
}

func assertValidationError(t *testing.T, err error, wantSubstring string) {
	t.Helper()
	if wantSubstring == "" {
		if err != nil {
			t.Errorf("Validate() error = %v, want nil", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), wantSubstring) {
		t.Errorf("Validate() error = %v, want one mentioning %q", err, wantSubstring)
	}
}
