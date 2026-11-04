package ha

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/stretchr/testify/require"
)

func TestInitialize_LogsRecordingRetention(t *testing.T) {
	for _, cleanupEnabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "cleanup enabled", false: "cleanup disabled"}[cleanupEnabled], func(t *testing.T) {
			cfg := createTestConfig()
			cfg.Failover.Recording.Enabled = true
			cfg.Failover.Recording.OutputDir = t.TempDir()
			cfg.Failover.Recording.Retention.Cleanup = config.RecordingCleanup{Enabled: cleanupEnabled, MaxAge: "24h"}
			if !cleanupEnabled {
				keep, discard := true, false
				cfg.Failover.Recording.Retention.Events = config.RecordingEvents{Failover: &discard, Recovery: &keep, Interruption: &discard}
			}
			var output bytes.Buffer
			previous := log.Default()
			log.SetDefault(log.NewWithOptions(&output, log.Options{Level: log.InfoLevel}))
			t.Cleanup(func() { log.SetDefault(previous) })
			m := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
			defer m.cancel()
			require.NoError(t, m.initialize())
			require.Contains(t, output.String(), "recording retention events configured")
			if cleanupEnabled {
				require.Contains(t, output.String(), "failover=true recovery=false interruption=true")
				require.Contains(t, output.String(), "recording cleanup enabled")
				require.Contains(t, output.String(), "interval=1h0m0s max_age=24h0m0s")
			} else {
				require.Contains(t, output.String(), "failover=false recovery=true interruption=false")
				require.NotContains(t, output.String(), "recording cleanup enabled")
			}
		})
	}
}

func TestPruneRecordings_LogsOnlyWhenFilesRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svha-old-recording.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	cfg := createTestConfig()
	cfg.Failover.Recording.Retention.Cleanup.Enabled = true
	cfg.Failover.Recording.Retention.Cleanup.MaxAge = "24h"
	var output bytes.Buffer
	m := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	defer m.cancel()
	m.recordingOutputDir = dir
	m.logger = log.NewWithOptions(&output, log.Options{Level: log.InfoLevel})

	m.pruneRecordings()
	require.NoFileExists(t, path)
	require.Contains(t, output.String(), "pruned expired recordings")
	require.Contains(t, output.String(), "removed=1")
	require.Contains(t, output.String(), "directory="+dir)
	require.Contains(t, output.String(), "max_age=24h0m0s")

	output.Reset()
	m.pruneRecordings()
	require.Empty(t, output.String(), "cleanup with no expired files should stay silent")
}
