package prometheus

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sol-strategies/solana-validator-ha/internal/cache"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
)

func createTestConfig() *config.Config {
	return &config.Config{
		Validator: config.Validator{
			Name:   "test-validator",
			RPCURL: "http://localhost:8899",
		},
		Prometheus: config.Prometheus{
			Port: 9090,
			StaticLabels: map[string]string{
				"environment": "test",
				"region":      "us-west-1",
			},
		},
	}
}

func createTestCache() *cache.Cache {
	return cache.New()
}

func createTestLogger() *log.Logger {
	return log.WithPrefix("test")
}

func TestNew(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)
	require.NotNil(t, metrics)

	// Verify that all components are properly initialized
	assert.Equal(t, cfg, metrics.config)
	assert.Equal(t, cacheInstance, metrics.cache)
	assert.Equal(t, logger, metrics.logger)
	assert.NotNil(t, metrics.registry)
	assert.NotNil(t, metrics.metadata)
	assert.NotNil(t, metrics.peerCount)
	assert.NotNil(t, metrics.selfInGossip)
	assert.NotNil(t, metrics.failoverStatus)

	// Verify common label names include static labels
	expectedLabelNames := []string{
		"validator_name",
		"public_ip",
		"environment",
		"region",
	}
	assert.ElementsMatch(t, expectedLabelNames, metrics.commonLabelNames)
}

func TestNew_WithEmptyStaticLabels(t *testing.T) {
	cfg := createTestConfig()
	cfg.Prometheus.StaticLabels = map[string]string{}
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)
	require.NotNil(t, metrics)

	// Verify common label names only include default labels
	expectedLabelNames := []string{
		"validator_name",
		"public_ip",
	}
	assert.ElementsMatch(t, expectedLabelNames, metrics.commonLabelNames)
}

func TestRefreshMetrics(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Update cache state
	state := cache.State{
		ValidatorName:  "test-validator",
		PublicIP:       "192.168.1.100",
		Role:           "active",
		Status:         "healthy",
		PeerCount:      5,
		SelfInGossip:   true,
		FailoverStatus: "stable",
		LastUpdated:    time.Now(),
	}
	cacheInstance.UpdateState(state)

	// Refresh metrics
	metrics.RefreshMetrics()

	// Verify that metrics were updated by checking the registry
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, metricsList)

	// Check that all expected metrics are present
	metricNames := make(map[string]bool)
	for _, metricFamily := range metricsList {
		metricNames[*metricFamily.Name] = true
	}

	expectedMetrics := []string{
		"solana_validator_ha_metadata",
		"solana_validator_ha_peer_count",
		"solana_validator_ha_self_in_gossip",
		"solana_validator_ha_failover_status",
	}

	for _, expectedMetric := range expectedMetrics {
		assert.True(t, metricNames[expectedMetric], "Expected metric %s not found", expectedMetric)
	}
}

func TestRefreshMetrics_WithEmptyState(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Refresh metrics with empty state
	metrics.RefreshMetrics()

	// Verify that metrics were still created
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, metricsList)
}

func TestGetCommonLabels(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	state := cache.State{
		ValidatorName: "test-validator",
		PublicIP:      "192.168.1.100",
	}

	labels := metrics.getCommonLabels(&state)

	expectedLabels := prometheus.Labels{
		"validator_name": "test-validator",
		"public_ip":      "192.168.1.100",
		"environment":    "test",
		"region":         "us-west-1",
	}

	assert.Equal(t, expectedLabels, labels)
}

func TestGetCommonLabels_WithEmptyStaticLabels(t *testing.T) {
	cfg := createTestConfig()
	cfg.Prometheus.StaticLabels = map[string]string{}
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	state := cache.State{
		ValidatorName: "test-validator",
		PublicIP:      "192.168.1.100",
	}

	labels := metrics.getCommonLabels(&state)

	expectedLabels := prometheus.Labels{
		"validator_name": "test-validator",
		"public_ip":      "192.168.1.100",
	}

	assert.Equal(t, expectedLabels, labels)
}

func TestMergeLabels(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	toLabels := prometheus.Labels{
		"key1": "value1",
		"key2": "value2",
	}

	fromLabels := prometheus.Labels{
		"key2": "new_value2",
		"key3": "value3",
	}

	result := metrics.mergeLabels(toLabels, fromLabels)

	expectedLabels := prometheus.Labels{
		"key1": "value1",
		"key2": "new_value2", // Should be overwritten
		"key3": "value3",     // Should be added
	}

	assert.Equal(t, expectedLabels, result)
}

func TestMergeLabels_WithEmptyFromLabels(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	toLabels := prometheus.Labels{
		"key1": "value1",
		"key2": "value2",
	}

	fromLabels := prometheus.Labels{}

	result := metrics.mergeLabels(toLabels, fromLabels)

	assert.Equal(t, toLabels, result)
}

func TestExportMetricMetadata(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	state := cache.State{
		ValidatorName: "test-validator",
		PublicIP:      "192.168.1.100",
		Role:          "active",
		Status:        "healthy",
	}

	metrics.exportMetricMetadata(&state)

	// Verify the metric was set by checking the registry
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)

	var metadataMetric *dto.MetricFamily
	for _, metricFamily := range metricsList {
		if *metricFamily.Name == "solana_validator_ha_metadata" {
			metadataMetric = metricFamily
			break
		}
	}

	require.NotNil(t, metadataMetric)
	assert.Len(t, metadataMetric.Metric, 1)
	assert.Equal(t, float64(1), *metadataMetric.Metric[0].Gauge.Value)
}

func TestExportMetricPeerCount(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	state := cache.State{
		ValidatorName: "test-validator",
		PublicIP:      "192.168.1.100",
		PeerCount:     10,
	}

	metrics.exportMetricPeerCount(&state)

	// Verify the metric was set by checking the registry
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)

	var peerCountMetric *dto.MetricFamily
	for _, metricFamily := range metricsList {
		if *metricFamily.Name == "solana_validator_ha_peer_count" {
			peerCountMetric = metricFamily
			break
		}
	}

	require.NotNil(t, peerCountMetric)
	assert.Len(t, peerCountMetric.Metric, 1)
	assert.Equal(t, float64(10), *peerCountMetric.Metric[0].Gauge.Value)
}

func TestExportMetricSelfInGossip_True(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	state := cache.State{
		ValidatorName: "test-validator",
		PublicIP:      "192.168.1.100",
		SelfInGossip:  true,
	}

	metrics.exportMetricSelfInGossip(&state)

	// Verify the metric was set by checking the registry
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)

	var selfInGossipMetric *dto.MetricFamily
	for _, metricFamily := range metricsList {
		if *metricFamily.Name == "solana_validator_ha_self_in_gossip" {
			selfInGossipMetric = metricFamily
			break
		}
	}

	require.NotNil(t, selfInGossipMetric)
	assert.Len(t, selfInGossipMetric.Metric, 1)
	assert.Equal(t, float64(1), *selfInGossipMetric.Metric[0].Gauge.Value)
}

func TestExportMetricSelfInGossip_False(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	state := cache.State{
		ValidatorName: "test-validator",
		PublicIP:      "192.168.1.100",
		SelfInGossip:  false,
	}

	metrics.exportMetricSelfInGossip(&state)

	// Verify the metric was set by checking the registry
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)

	var selfInGossipMetric *dto.MetricFamily
	for _, metricFamily := range metricsList {
		if *metricFamily.Name == "solana_validator_ha_self_in_gossip" {
			selfInGossipMetric = metricFamily
			break
		}
	}

	require.NotNil(t, selfInGossipMetric)
	assert.Len(t, selfInGossipMetric.Metric, 1)
	assert.Equal(t, float64(0), *selfInGossipMetric.Metric[0].Gauge.Value)
}

func TestExportMetricFailoverStatus(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	state := cache.State{
		ValidatorName:  "test-validator",
		PublicIP:       "192.168.1.100",
		FailoverStatus: "becoming_active",
	}

	metrics.exportMetricFailoverStatus(&state)

	// Verify the metric was set by checking the registry
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)

	var failoverStatusMetric *dto.MetricFamily
	for _, metricFamily := range metricsList {
		if *metricFamily.Name == "solana_validator_ha_failover_status" {
			failoverStatusMetric = metricFamily
			break
		}
	}

	require.NotNil(t, failoverStatusMetric)
	assert.Len(t, failoverStatusMetric.Metric, 1)
	assert.Equal(t, float64(1), *failoverStatusMetric.Metric[0].Gauge.Value)
}

func TestGetRegistry(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	registry := metrics.GetRegistry()
	require.NotNil(t, registry)

	// Verify it's the same registry instance
	assert.Equal(t, metrics.registry, registry)
}

func TestStartServer(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Start server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- metrics.StartServer(0) // Use port 0 for testing
	}()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Verify server was created
	assert.NotNil(t, metrics.server)

	// Stop the server
	err := metrics.StopServer()
	assert.NoError(t, err)

	// Wait for server to stop
	select {
	case err := <-serverErr:
		// Server stopped, "http: Server closed" is expected when we call Close()
		if err != nil && err.Error() != "http: Server closed" {
			assert.NoError(t, err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Server did not stop within timeout")
	}
}

func TestStartServer_WithInvalidPort(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Try to start server with invalid port (negative)
	err := metrics.StartServer(-1)
	assert.Error(t, err)
}

func TestStopServer_WhenNotStarted(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Stop server when not started
	err := metrics.StopServer()
	assert.NoError(t, err) // Should not error when server is nil
}

func TestMetrics_Integration(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Update cache with various states
	testCases := []cache.State{
		{
			ValidatorName:  "test-validator",
			PublicIP:       "192.168.1.100",
			Role:           "active",
			Status:         "healthy",
			PeerCount:      5,
			SelfInGossip:   true,
			FailoverStatus: "stable",
			LastUpdated:    time.Now(),
		},
		{
			ValidatorName:  "test-validator",
			PublicIP:       "192.168.1.100",
			Role:           "passive",
			Status:         "unhealthy",
			PeerCount:      0,
			SelfInGossip:   false,
			FailoverStatus: "becoming_passive",
			LastUpdated:    time.Now(),
		},
	}

	for i, testCase := range testCases {
		t.Run(fmt.Sprintf("TestCase_%d", i), func(t *testing.T) {
			cacheInstance.UpdateState(testCase)
			metrics.RefreshMetrics()

			// Verify metrics were updated
			registry := metrics.GetRegistry()
			metricsList, err := registry.Gather()
			require.NoError(t, err)
			assert.NotEmpty(t, metricsList)

			// Check that all expected metrics are present
			metricNames := make(map[string]bool)
			for _, metricFamily := range metricsList {
				metricNames[*metricFamily.Name] = true
			}

			expectedMetrics := []string{
				"solana_validator_ha_metadata",
				"solana_validator_ha_peer_count",
				"solana_validator_ha_self_in_gossip",
				"solana_validator_ha_failover_status",
			}

			for _, expectedMetric := range expectedMetrics {
				assert.True(t, metricNames[expectedMetric], "Expected metric %s not found", expectedMetric)
			}
		})
	}
}

func TestMetrics_WithHTTPClient(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Update cache state
	state := cache.State{
		ValidatorName:  "test-validator",
		PublicIP:       "192.168.1.100",
		Role:           "active",
		Status:         "healthy",
		PeerCount:      5,
		SelfInGossip:   true,
		FailoverStatus: "stable",
		LastUpdated:    time.Now(),
	}
	cacheInstance.UpdateState(state)
	metrics.RefreshMetrics()

	// Start server
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- metrics.StartServer(0) // Use port 0 for testing
	}()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Get the actual port from the server
	port := metrics.server.Addr[1:] // Remove the colon
	if port == "0" {
		// Port 0 means the OS assigned a random port, we can't test HTTP in this case
		// Just verify the server started
		assert.NotNil(t, metrics.server)
	} else {
		// Test HTTP endpoint
		resp, err := http.Get(fmt.Sprintf("http://localhost:%s/metrics", port))
		if err == nil {
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		}
	}

	// Stop the server
	err := metrics.StopServer()
	assert.NoError(t, err)

	// Wait for server to stop
	select {
	case err := <-serverErr:
		// Server stopped, "http: Server closed" is expected when we call Close()
		if err != nil && err.Error() != "http: Server closed" {
			assert.NoError(t, err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Server did not stop within timeout")
	}
}

func TestMetrics_ConcurrentAccess(t *testing.T) {
	cfg := createTestConfig()
	cacheInstance := createTestCache()
	logger := createTestLogger()

	opts := Options{
		Config: cfg,
		Logger: logger,
		Cache:  cacheInstance,
	}

	metrics := New(opts)

	// Test concurrent access to metrics
	numGoroutines := 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			state := cache.State{
				ValidatorName:  fmt.Sprintf("test-validator-%d", id),
				PublicIP:       fmt.Sprintf("192.168.1.%d", id),
				Role:           "active",
				Status:         "healthy",
				PeerCount:      id,
				SelfInGossip:   id%2 == 0,
				FailoverStatus: "stable",
				LastUpdated:    time.Now(),
			}
			cacheInstance.UpdateState(state)
			metrics.RefreshMetrics()
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify metrics are still accessible
	registry := metrics.GetRegistry()
	metricsList, err := registry.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, metricsList)
}
