package config

import (
	"os"
	"path/filepath"
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

func TestAlpenglow_SetDefaults(t *testing.T) {
	var alpenglow Alpenglow
	alpenglow.SetDefaults(5 * time.Second)
	want := Alpenglow{
		VoteLagSlotsThreshold:             32,
		WarmupSlots:                       64,
		FinalizationStallDuration:         15 * time.Second,
		NetworkStakeCheckIntervalDuration: 60 * time.Second,
	}
	if alpenglow != want {
		t.Errorf("SetDefaults(5s) = %+v, want %+v", alpenglow, want)
	}
}

func TestAlpenglow_SetDefaults_StallDurationCoversSlowPoll(t *testing.T) {
	var alpenglow Alpenglow
	alpenglow.SetDefaults(30 * time.Second)
	if alpenglow.FinalizationStallDuration != 30*time.Second {
		t.Errorf("FinalizationStallDuration = %s, want 30s", alpenglow.FinalizationStallDuration)
	}
	if err := alpenglow.Validate(30 * time.Second); err != nil {
		t.Errorf("defaults with a 30s poll interval fail validation: %v", err)
	}
}

func TestAlpenglow_Validate(t *testing.T) {
	const pollInterval = 5 * time.Second
	valid := func() Alpenglow {
		return Alpenglow{
			VoteLagSlotsThreshold:             32,
			WarmupSlots:                       64,
			FinalizationStallDuration:         15 * time.Second,
			NetworkCurrentStakeRatioMin:       0.85,
			NetworkStakeCheckIntervalDuration: time.Minute,
		}
	}
	tests := []struct {
		name    string
		modify  func(*Alpenglow)
		wantErr string // empty when valid
	}{
		{name: "defaults", modify: func(*Alpenglow) {}},
		{name: "stake gate disabled", modify: func(a *Alpenglow) { a.NetworkCurrentStakeRatioMin = 0 }},
		{name: "lag threshold at minimum", modify: func(a *Alpenglow) { a.VoteLagSlotsThreshold = 16 }},
		{name: "lag threshold below minimum", modify: func(a *Alpenglow) { a.VoteLagSlotsThreshold = 15 }, wantErr: "vote_lag_slots_threshold"},
		{name: "stall duration equal to poll interval", modify: func(a *Alpenglow) { a.FinalizationStallDuration = pollInterval }},
		{name: "stall duration below poll interval", modify: func(a *Alpenglow) { a.FinalizationStallDuration = pollInterval - time.Second }, wantErr: "finalization_stall_duration"},
		{name: "ratio above one", modify: func(a *Alpenglow) { a.NetworkCurrentStakeRatioMin = 1.1 }, wantErr: "network_current_stake_ratio_min"},
		{name: "negative ratio", modify: func(a *Alpenglow) { a.NetworkCurrentStakeRatioMin = -0.1 }, wantErr: "network_current_stake_ratio_min"},
		{name: "zero stake check interval", modify: func(a *Alpenglow) { a.NetworkStakeCheckIntervalDuration = 0 }, wantErr: "network_stake_check_interval_duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alpenglow := valid()
			tt.modify(&alpenglow)
			assertValidationError(t, alpenglow.Validate(pollInterval), tt.wantErr)
		})
	}
}

func TestLoadFromFile_NetworkCurrentStakeRatioMin(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want float64
	}{
		{name: "defaults when absent", yaml: "failover:\n  alpenglow: {}\n", want: 0.85},
		{name: "zero disables the check", yaml: "failover:\n  alpenglow:\n    network_current_stake_ratio_min: 0\n", want: 0},
		{name: "explicit value", yaml: "failover:\n  alpenglow:\n    network_current_stake_ratio_min: 0.7\n", want: 0.7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := New(NewConfigParams{})
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.LoadFromFile(path); err != nil {
				t.Fatalf("LoadFromFile() error = %v", err)
			}
			if got := cfg.Failover.Alpenglow.NetworkCurrentStakeRatioMin; got != tt.want {
				t.Errorf("NetworkCurrentStakeRatioMin = %g, want %g", got, tt.want)
			}
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
