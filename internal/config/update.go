package config

import "time"

const defaultUpdateCheckIntervalDuration = 24 * time.Hour

// Update holds update-check settings
type Update struct {
	// CheckIntervalDuration is how often to check for a new release when running in continuous mode.
	CheckIntervalDuration time.Duration `koanf:"check_interval_duration"`
}

// SetDefaults sets default values for Update
func (u *Update) SetDefaults() {
	if u.CheckIntervalDuration <= 0 {
		u.CheckIntervalDuration = defaultUpdateCheckIntervalDuration
	}
}
