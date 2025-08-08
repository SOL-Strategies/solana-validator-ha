package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrometheus_SetDefaults(t *testing.T) {
	prometheus := &Prometheus{}
	prometheus.SetDefaults()

	assert.Equal(t, 9090, prometheus.Port)
}

func TestPrometheus_Validate(t *testing.T) {
	// Test with valid port
	prometheus := &Prometheus{
		Port: 9090,
		StaticLabels: map[string]string{
			"environment": "test",
		},
	}

	err := prometheus.Validate()
	assert.NoError(t, err)

	// Test with zero port
	prometheus.Port = 0
	err = prometheus.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prometheus.port must be positive and non-zero")

	// Test with negative port
	prometheus.Port = -1
	err = prometheus.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prometheus.port must be positive and non-zero")

	// Test with valid port again
	prometheus.Port = 8080
	err = prometheus.Validate()
	assert.NoError(t, err)
}
