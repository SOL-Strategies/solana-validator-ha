package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsolation_SetDefaults(t *testing.T) {
	tests := []struct {
		name         string
		pollInterval time.Duration
		want         time.Duration
	}{
		{name: "fast poll", pollInterval: 5 * time.Second, want: 15 * time.Second},
		{name: "slow poll", pollInterval: 30 * time.Second, want: 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var isolation Isolation
			isolation.SetDefaults(tt.pollInterval)
			if isolation.LocalSlotStallDuration != tt.want {
				t.Errorf("SetDefaults(%s): LocalSlotStallDuration = %s, want %s", tt.pollInterval, isolation.LocalSlotStallDuration, tt.want)
			}
		})
	}
}

func TestIsolation_Validate(t *testing.T) {
	const pollInterval = 5 * time.Second
	tests := []struct {
		name      string
		isolation Isolation
		wantErr   string // empty when valid
	}{
		{name: "defaults", isolation: Isolation{Enabled: true, LocalSlotStallDuration: 15 * time.Second}},
		{name: "stall equal to poll interval", isolation: Isolation{Enabled: true, LocalSlotStallDuration: pollInterval}},
		{name: "stall below poll interval", isolation: Isolation{Enabled: true, LocalSlotStallDuration: time.Second}, wantErr: "local_slot_stall_duration"},
		{name: "disabled skips validation", isolation: Isolation{Enabled: false, LocalSlotStallDuration: time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidationError(t, tt.isolation.Validate(pollInterval), tt.wantErr)
		})
	}
}

func TestLoadFromFile_IsolationEnabled(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{name: "enabled when absent", yaml: "failover:\n  isolation: {}\n", want: true},
		{name: "explicitly disabled", yaml: "failover:\n  isolation:\n    enabled: false\n", want: false},
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
			if got := cfg.Failover.Isolation.Enabled; got != tt.want {
				t.Errorf("Isolation.Enabled = %t, want %t", got, tt.want)
			}
		})
	}
}
