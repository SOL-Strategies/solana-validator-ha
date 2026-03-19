package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

const (
	appName          = "solana-validator-ha"
	githubRepoURL    = "https://github.com/SOL-Strategies/solana-validator-ha"
	githubReleaseURL = "https://api.github.com/repos/SOL-Strategies/solana-validator-ha/releases/latest"
	checkTimeout     = 3 * time.Second
	waitTimeout      = 1500 * time.Millisecond
)

// StartBackgroundCheck fires a goroutine and returns a channel that will receive
// the latest version string if a newer one is available, or "" otherwise.
// Returns a closed channel if the check is skipped (dev build).
func StartBackgroundCheck(currentVersion string) chan string {
	ch := make(chan string, 1)

	if currentVersion == "dev" {
		close(ch)
		return ch
	}

	go func() {
		defer close(ch)
		latest, err := fetchLatestVersion(currentVersion)
		if err == nil && latest != "" {
			ch <- latest
		}
	}()

	return ch
}

// PrintWarningIfAvailable waits up to waitTimeout for the channel and logs a
// warning if a newer version is available.
func PrintWarningIfAvailable(ch chan string) {
	if ch == nil {
		return
	}

	select {
	case latest, ok := <-ch:
		if ok && latest != "" {
			log.Warnf("update available: v%s → %s/releases/latest", latest, githubRepoURL)
		}
	case <-time.After(waitTimeout):
		// timed out — skip silently
	}
}

// StartPeriodicCheck starts a goroutine that checks for updates on the given interval.
// onResult is called after each check with the latest version string (non-empty when an
// update is available, empty string when up to date). Logs a warning whenever a newer
// version is found. Stops when ctx is cancelled. No-op for dev builds or zero interval.
func StartPeriodicCheck(ctx context.Context, currentVersion string, interval time.Duration, onResult func(latestVersion string)) {
	if currentVersion == "dev" || interval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				latest, err := fetchLatestVersion(currentVersion)
				if err == nil {
					if latest != "" {
						log.Warnf("update available: v%s → %s/releases/latest", latest, githubRepoURL)
					}
					if onResult != nil {
						onResult(latest)
					}
				}
			}
		}
	}()
}

// fetchLatestVersion calls the GitHub releases API, decodes the tag_name,
// and returns the version string (without "v" prefix) if it is newer than
// currentVersion. Returns an empty string and no error if up to date.
func fetchLatestVersion(currentVersion string) (string, error) {
	client := &http.Client{Timeout: checkTimeout}
	req, err := http.NewRequest(http.MethodGet, githubReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", appName, currentVersion))
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	if isNewerVersion(currentVersion, latest) {
		return latest, nil
	}
	return "", nil
}

// isNewerVersion returns true if candidate is strictly greater than current
// using semver major.minor.patch ordering.
func isNewerVersion(current, candidate string) bool {
	c := parseSemver(current)
	n := parseSemver(candidate)
	for i := range c {
		if n[i] > c[i] {
			return true
		}
		if n[i] < c[i] {
			return false
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		p, _, _ = strings.Cut(p, "-")
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
