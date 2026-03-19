package config

import "time"

const defaultUpdateCheckIntervalDuration = 24 * time.Hour

// Update holds update-check settings
type Update struct {
	// CheckEnabled controls whether update checks run at all (startup + periodic).
	// Defaults to true; set via k.Set before file load so absent key ≠ false.
	CheckEnabled bool `koanf:"check_enabled"`
	// CheckIntervalDuration is how often to check for a new release when running in continuous mode.
	CheckIntervalDuration time.Duration `koanf:"check_interval_duration"`
}

// SetDefaults sets default values for Update
func (u *Update) SetDefaults() {
	if u.CheckIntervalDuration <= 0 {
		u.CheckIntervalDuration = defaultUpdateCheckIntervalDuration
	}
}
