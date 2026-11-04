package recording

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Prune removes completed recordings with modification times strictly older than maxAge.
// It never traverses directories or follows symlinks, and leaves checkpoints untouched.
// The returned count includes only files successfully removed, even if other removals fail.
func Prune(baseDir string, maxAge time.Duration, now time.Time) (int, error) {
	if maxAge <= 0 {
		return 0, fmt.Errorf("recording retention max age must be positive")
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-maxAge)
	var errs []error
	removed := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "svha-") || !strings.HasSuffix(entry.Name(), "-recording.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, err)
			}
			continue
		}
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(baseDir, entry.Name())); err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		} else {
			removed++
		}
	}
	return removed, errors.Join(errs...)
}
