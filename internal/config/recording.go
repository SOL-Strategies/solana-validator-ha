package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Recording holds configuration for network anomaly and failover incident recording.
type Recording struct {
	// Enabled controls whether incident timelines are written to disk. Default false.
	Enabled bool `koanf:"enabled"`
	// OutputDir is the destination, defaulting to recordings beside the config.
	OutputDir string             `koanf:"output_dir"`
	Retention RecordingRetention `koanf:"retention"`
}

// RecordingRetention controls expiration and the incident categories saved to disk.
type RecordingRetention struct {
	Cleanup RecordingCleanup `koanf:"cleanup"`
	Events  RecordingEvents  `koanf:"events"`
}

// RecordingCleanup controls optional expiration and the background cleanup interval.
type RecordingCleanup struct {
	Enabled  bool   `koanf:"enabled"`
	Interval string `koanf:"interval"`
	MaxAge   string `koanf:"max_age"`
}

// IntervalDuration returns the validated interval, defaulting to one hour.
func (r RecordingCleanup) IntervalDuration() (time.Duration, error) {
	if r.Interval == "" {
		return time.Hour, nil
	}
	interval, err := time.ParseDuration(r.Interval)
	if err != nil || interval <= 0 {
		return 0, fmt.Errorf("failover.recording.retention.cleanup.interval must be a positive Go duration: %q", r.Interval)
	}
	return interval, nil
}

// MaxAgeDuration returns the validated age, required when cleanup is enabled.
// When cleanup is disabled, an omitted age returns zero for indefinite retention.
func (r RecordingCleanup) MaxAgeDuration() (time.Duration, error) {
	if r.MaxAge == "" {
		if r.Enabled {
			return 0, fmt.Errorf("failover.recording.retention.cleanup.max_age is required when cleanup.enabled is true")
		}
		return 0, nil
	}
	age, err := time.ParseDuration(r.MaxAge)
	if err != nil || age <= 0 {
		return 0, fmt.Errorf("failover.recording.retention.cleanup.max_age must be a positive Go duration: %q", r.MaxAge)
	}
	return age, nil
}

// RecordingEvents selects which completed incidents are retained. Pointers distinguish
// omitted settings from explicit false values, including in programmatic configurations.
type RecordingEvents struct {
	Failover     *bool `koanf:"failover"`
	Recovery     *bool `koanf:"recovery"`
	Interruption *bool `koanf:"interruption"`
}

// Keeps reports whether an outcome should be saved. Failover includes aborted attempts,
// retained-active decisions and failed actions, and defaults to true for new outcomes.
func (k RecordingEvents) Keeps(outcome string) bool {
	setting, fallback := k.Failover, true
	switch outcome {
	case "recovered_no_failover":
		setting, fallback = k.Recovery, false
	case "interrupted":
		setting = k.Interruption
	}
	if setting == nil {
		return fallback
	}
	return *setting
}

// ResolvedOutputDir returns OutputDir or a recordings directory beside the loaded config.
func (r *Recording) ResolvedOutputDir(configFilePath string) string {
	if r.OutputDir != "" {
		return r.OutputDir
	}
	return filepath.Join(filepath.Dir(configFilePath), "recordings")
}

// Prepare validates retention and creates the recording directory when enabled.
func (r *Recording) Prepare(configFilePath string) error {
	if _, err := r.Retention.Cleanup.MaxAgeDuration(); err != nil {
		return err
	}
	if _, err := r.Retention.Cleanup.IntervalDuration(); err != nil {
		return err
	}
	if !r.Enabled {
		return nil
	}
	resolvedDir := r.ResolvedOutputDir(configFilePath)
	if err := os.MkdirAll(resolvedDir, 0755); err != nil {
		return fmt.Errorf("create failover.recording.output_dir %q: %w", resolvedDir, err)
	}
	probe, err := os.CreateTemp(resolvedDir, ".svha-write-probe-*")
	if err != nil {
		return fmt.Errorf("failover.recording.output_dir %q is not writable: %w", resolvedDir, err)
	}
	path := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
