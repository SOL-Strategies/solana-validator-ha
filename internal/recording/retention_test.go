package recording

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	age := 24 * time.Hour
	for _, tc := range []struct {
		name    string
		mtime   time.Time
		removed bool
	}{
		{"svha-old-recording.json", now.Add(-age - time.Second), true},
		{"svha-fresh-recording.json", now.Add(-time.Hour), false},
		{"svha-boundary-recording.json", now.Add(-age), false},
		{"svha-future-recording.json", now.Add(time.Hour), false},
		{"svha-old-recording.json.partial", now.Add(-2 * age), false},
		{"unrelated.json", now.Add(-2 * age), false},
		{"other-recording.json", now.Add(-2 * age), false},
	} {
		path := filepath.Join(dir, tc.name)
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
		require.NoError(t, os.Chtimes(path, tc.mtime, tc.mtime))
		t.Cleanup(func() {
			if tc.removed {
				require.NoFileExists(t, path)
			} else {
				require.FileExists(t, path)
			}
		})
	}
	subdir := filepath.Join(dir, "svha-dir-recording.json")
	require.NoError(t, os.Mkdir(subdir, 0755))
	child := filepath.Join(subdir, "svha-old-recording.json")
	require.NoError(t, os.WriteFile(child, []byte("{}"), 0600))
	require.NoError(t, os.Chtimes(child, now.Add(-2*age), now.Add(-2*age)))
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(target, []byte("preserve"), 0600))
	require.NoError(t, os.Chtimes(target, now.Add(-2*age), now.Add(-2*age)))
	link := filepath.Join(dir, "svha-link-recording.json")
	require.NoError(t, os.Symlink(target, link))
	require.NoError(t, Prune(dir, age, now))
	require.FileExists(t, child)
	require.FileExists(t, target)
	_, err := os.Lstat(link)
	require.NoError(t, err)
	require.Error(t, Prune(dir, 0, now))
	require.Error(t, Prune(filepath.Join(dir, "missing"), age, now))
}
