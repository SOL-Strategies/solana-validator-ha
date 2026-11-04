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
func Prune(baseDir string, maxAge time.Duration, now time.Time) error {
	if maxAge <= 0 {
		return fmt.Errorf("recording retention max age must be positive")
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return err
	}
	cutoff := now.Add(-maxAge)
	var errs []error
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
		if err := os.Remove(filepath.Join(baseDir, entry.Name())); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
