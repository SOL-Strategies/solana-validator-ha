package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Recording holds configuration for network anomaly and failover incident recording.
type Recording struct {
	// Enabled controls whether incident timelines are written to disk. Default false.
	Enabled bool `koanf:"enabled"`
	// OutputDir is the directory where recording files are written.
	// If empty, defaults to the directory containing the loaded config file.
	OutputDir string `koanf:"output_dir"`
}

// ResolvedOutputDir returns the effective output directory: OutputDir if set, otherwise
// the directory of configFilePath (the loaded config file).
func (r *Recording) ResolvedOutputDir(configFilePath string) string {
	if r.OutputDir != "" {
		return r.OutputDir
	}
	return filepath.Dir(configFilePath)
}

// ValidateOutputDir checks that resolvedDir exists, is a directory, and is writable.
// It writes and removes a probe file to confirm write access.
func (r *Recording) ValidateOutputDir(resolvedDir string) error {
	info, err := os.Stat(resolvedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("failover.recording.output_dir %q does not exist", resolvedDir)
		}
		return fmt.Errorf("failover.recording.output_dir %q: %w", resolvedDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("failover.recording.output_dir %q is not a directory", resolvedDir)
	}
	probe := filepath.Join(resolvedDir, ".svha-write-probe")
	if err := os.WriteFile(probe, []byte{}, 0644); err != nil {
		return fmt.Errorf("failover.recording.output_dir %q is not writable: %w", resolvedDir, err)
	}
	os.Remove(probe) //nolint:errcheck
	return nil
}
