package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecordingEventsLoad(t *testing.T) {
	for _, tc := range []struct {
		name, yaml                       string
		failover, recovery, interruption bool
	}{
		{"omitted", "", true, false, true},
		{"empty", "    retention: {events: {}}\n", true, false, true},
		{"partial", "    retention:\n      events:\n        recovery: true\n", true, true, true},
		{"explicit false", "    retention:\n      events:\n        failover: false\n        interruption: false\n", false, false, false},
		{"all enabled", "    retention:\n      events:\n        failover: true\n        recovery: true\n        interruption: true\n", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte("failover:\n  recording:\n    enabled: true\n"+tc.yaml), 0600))
			cfg, err := New(NewConfigParams{})
			require.NoError(t, err)
			require.NoError(t, cfg.LoadFromFile(path))
			cfg.setDefaults()
			keep := cfg.Failover.Recording.Retention.Events
			require.Equal(t, tc.failover, keep.Keeps("became_active"))
			require.Equal(t, tc.recovery, keep.Keeps("recovered_no_failover"))
			require.Equal(t, tc.interruption, keep.Keeps("interrupted"))
		})
	}
	var cfg Recording
	require.False(t, cfg.Enabled)
	require.True(t, cfg.Retention.Events.Keeps("became_active"))
	require.False(t, cfg.Retention.Events.Keeps("recovered_no_failover"))
	require.True(t, cfg.Retention.Events.Keeps("interrupted"))
}

func TestRecordingRetentionMaxAge(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", 0, false},
		{"1680h", 1680 * time.Hour, false},
		{"1h30m", 90 * time.Minute, false},
		{"10w", 0, true},
		{"nonsense", 0, true},
		{"0", 0, true},
		{"-1h", 0, true},
		{"999999999999999999h", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			r := RecordingCleanup{MaxAge: tc.value}
			age, err := r.MaxAgeDuration()
			if tc.invalid {
				require.ErrorContains(t, err, "failover.recording.retention.cleanup.max_age")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, age)
		})
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("failover:\n  recording:\n    retention:\n      cleanup:\n        enabled: true\n        interval: 2h\n        max_age: 48h\n"), 0600))
	cfg, err := New(NewConfigParams{})
	require.NoError(t, err)
	require.NoError(t, cfg.LoadFromFile(path))
	age, err := cfg.Failover.Recording.Retention.Cleanup.MaxAgeDuration()
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, age)
	require.True(t, cfg.Failover.Recording.Retention.Cleanup.Enabled)
	interval, err := cfg.Failover.Recording.Retention.Cleanup.IntervalDuration()
	require.NoError(t, err)
	require.Equal(t, 2*time.Hour, interval)
}

func TestRecordingCleanupInterval(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"0", 0, true},
		{"-1h", 0, true},
		{"10w", 0, true},
		{"invalid", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cleanup := RecordingCleanup{Interval: tc.value}
			interval, err := cleanup.IntervalDuration()
			if tc.invalid {
				require.ErrorContains(t, err, "failover.recording.retention.cleanup.interval")
				r := Recording{Retention: RecordingRetention{Cleanup: cleanup}}
				require.ErrorContains(t, r.Prepare("config.yaml"), "cleanup.interval")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, interval)
		})
	}
}

func TestRecordingPrepare(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.yaml")
	r := Recording{}
	require.NoError(t, r.Prepare(cfgPath))
	require.NoDirExists(t, filepath.Join(root, "recordings"))
	r.Enabled = true
	require.NoError(t, r.Prepare(cfgPath))
	require.DirExists(t, filepath.Join(root, "recordings"))
	require.Equal(t, filepath.Join(root, "recordings"), r.ResolvedOutputDir(cfgPath))
	require.NoError(t, r.Prepare(cfgPath)) // existing directories are reusable
	entries, err := os.ReadDir(r.ResolvedOutputDir(cfgPath))
	require.NoError(t, err)
	require.Empty(t, entries) // no probe files left behind
	r.OutputDir = filepath.Join(root, "custom", "nested")
	require.NoError(t, r.Prepare(cfgPath))
	require.DirExists(t, r.OutputDir)
	require.Equal(t, r.OutputDir, r.ResolvedOutputDir(cfgPath))
	r.OutputDir = "relative"
	require.Equal(t, "relative", r.ResolvedOutputDir(cfgPath))
	r.OutputDir = filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(r.OutputDir, []byte("occupied"), 0600))
	require.Error(t, r.Prepare(cfgPath))
	r.OutputDir = ""
	r.Retention.Cleanup.MaxAge = "bad"
	require.ErrorContains(t, r.Prepare(cfgPath), "max_age")
}

func TestRecordingCleanupEnabledRequiresAge(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cleanup RecordingCleanup
		invalid bool
	}{
		{"default disabled", RecordingCleanup{}, false},
		{"disabled with age", RecordingCleanup{MaxAge: "24h"}, false},
		{"enabled without age", RecordingCleanup{Enabled: true}, true},
		{"enabled with age", RecordingCleanup{Enabled: true, MaxAge: "24h"}, false},
		{"enabled with invalid age", RecordingCleanup{Enabled: true, MaxAge: "bad"}, true},
		{"enabled with zero age", RecordingCleanup{Enabled: true, MaxAge: "0"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Recording{Retention: RecordingRetention{Cleanup: tc.cleanup}}
			err := r.Prepare("config.yaml")
			if tc.invalid {
				require.ErrorContains(t, err, "cleanup.max_age")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
