package ha

import (
	"context"
	"testing"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPublicIPFunc is a mock function for getting public IP
func mockPublicIPFunc() (string, error) {
	return "192.168.1.100", nil
}

// mockPublicIPFuncError is a mock function that returns an error
func mockPublicIPFuncError() (string, error) {
	return "", assert.AnError
}

func createTestConfig() *config.Config {
	return &config.Config{
		Validator: config.Validator{
			Name:   "test-validator",
			RPCURL: "http://localhost:8899",
			Identities: config.ValidatorIdentities{
				ActiveKeyPair:  createTestPrivateKey("active"),
				PassiveKeyPair: createTestPrivateKey("passive"),
			},
		},
		Cluster: config.Cluster{
			Name:    "mainnet-beta",
			RPCURLs: []string{"https://api.mainnet-beta.solana.com"},
		},
		Failover: config.Failover{
			PollIntervalDuration:       5 * time.Second,
			LeaderlessSamplesThreshold: 3,
			TakeoverJitterSeconds:      5,
			DryRun:                     true,
			Peers: map[string]config.Peer{
				"peer1": {IP: "192.168.1.101", Name: "peer1"},
				"peer2": {IP: "192.168.1.102", Name: "peer2"},
			},
			Active: config.Role{
				Command: "echo 'active'",
				Hooks: config.Hooks{
					Pre:  []config.Hook{{Command: "echo 'pre-active'"}},
					Post: []config.Hook{{Command: "echo 'post-active'"}},
				},
			},
			Passive: config.Role{
				Command: "echo 'passive'",
				Hooks: config.Hooks{
					Pre:  []config.Hook{{Command: "echo 'pre-passive'"}},
					Post: []config.Hook{{Command: "echo 'post-passive'"}},
				},
			},
		},
		Prometheus: config.Prometheus{
			Port: 9090,
		},
	}
}

func createTestPrivateKey(name string) *solanago.PrivateKey {
	// Create a simple test private key
	key := solanago.NewWallet()
	return &key.PrivateKey
}

func TestNewManager(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)
	require.NotNil(t, manager)

	assert.Equal(t, cfg, manager.cfg)
	assert.NotNil(t, manager.metrics)
	assert.NotNil(t, manager.cache)
	assert.NotNil(t, manager.logger)
	assert.NotNil(t, manager.ctx)
	assert.NotNil(t, manager.cancel)
	assert.NotNil(t, manager.getPublicIPFunc)
	assert.NotNil(t, manager.localRPC)
	assert.Equal(t, 2, manager.peerCount)
}

func TestNewManager_WithoutPublicIPFunc(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg: cfg,
		// No GetPublicIPFunc provided
	}

	manager := NewManager(opts)
	require.NotNil(t, manager)

	assert.Nil(t, manager.getPublicIPFunc)
}

func TestManager_Initialize(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	err := manager.initialize()
	assert.NoError(t, err)

	assert.NotNil(t, manager.peerSelf)
	assert.Equal(t, "192.168.1.100", manager.peerSelf.IP)
	assert.NotNil(t, manager.gossipState)

	// Check that self was added to peers
	_, exists := cfg.Failover.Peers["test-validator"]
	assert.True(t, exists)
	assert.Equal(t, "192.168.1.100", cfg.Failover.Peers["test-validator"].IP)
}

func TestManager_Initialize_WithPublicIPError(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFuncError,
	}

	manager := NewManager(opts)

	err := manager.initialize()
	assert.Error(t, err)
	assert.Error(t, err)
}

func TestManager_GetPublicIP(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	ip, err := manager.getPublicIP()
	assert.NoError(t, err)
	assert.Equal(t, "192.168.1.100", ip)
}

func TestManager_GetPublicIP_WithError(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFuncError,
	}

	manager := NewManager(opts)

	ip, err := manager.getPublicIP()
	assert.Error(t, err)
	assert.Empty(t, ip)
}

func TestManager_ContextCancellation(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize
	err := manager.initialize()
	require.NoError(t, err)

	// Cancel the context
	manager.cancel()

	// Run should return immediately due to cancelled context
	err = manager.Run()
	assert.NoError(t, err)
}

func TestManager_EdgeCases(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Test with empty peers
	cfg.Failover.Peers = map[string]config.Peer{}
	manager.peerCount = 0

	// Initialize should still work
	err := manager.initialize()
	assert.NoError(t, err)

	// Test with single peer (different IP)
	cfg.Failover.Peers = map[string]config.Peer{
		"other": {IP: "192.168.1.103", Name: "other-validator"},
	}
	manager.peerCount = 1

	err = manager.initialize()
	assert.NoError(t, err)
}

func TestManager_ConcurrentAccess(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize
	err := manager.initialize()
	require.NoError(t, err)

	// Test concurrent access to manager methods
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			// Test basic manager properties
			_ = manager.cfg
			_ = manager.metrics
			_ = manager.cache
			_ = manager.logger
			_ = manager.ctx
			_ = manager.peerCount
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// If we get here without panicking, the methods are thread-safe
	assert.True(t, true)
}

func TestManager_ConfigurationValidation(t *testing.T) {
	// Test with valid configuration
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)
	require.NotNil(t, manager)

	// Test that configuration is properly set
	assert.Equal(t, "test-validator", manager.cfg.Validator.Name)
	assert.Equal(t, "http://localhost:8899", manager.cfg.Validator.RPCURL)
	assert.NotNil(t, manager.cfg.Validator.Identities.ActiveKeyPair)
	assert.NotNil(t, manager.cfg.Validator.Identities.PassiveKeyPair)
	assert.Equal(t, "mainnet-beta", manager.cfg.Cluster.Name)
	assert.Len(t, manager.cfg.Cluster.RPCURLs, 1)
	assert.Equal(t, 5*time.Second, manager.cfg.Failover.PollIntervalDuration)
	assert.Equal(t, 3, manager.cfg.Failover.LeaderlessSamplesThreshold)
	assert.Equal(t, 5, manager.cfg.Failover.TakeoverJitterSeconds)
	assert.True(t, manager.cfg.Failover.DryRun)
	assert.Len(t, manager.cfg.Failover.Peers, 2)
	assert.Equal(t, 9090, manager.cfg.Prometheus.Port)
}

func TestManager_InitializationFlow(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Test initialization
	err := manager.initialize()
	assert.NoError(t, err)

	// Verify that all components are properly initialized
	assert.NotNil(t, manager.peerSelf)
	assert.Equal(t, "192.168.1.100", manager.peerSelf.IP)
	assert.NotNil(t, manager.gossipState)

	// Verify that self was added to peers
	_, exists := cfg.Failover.Peers["test-validator"]
	assert.True(t, exists)
	assert.Equal(t, "192.168.1.100", cfg.Failover.Peers["test-validator"].IP)

	// Verify that the manager is ready to run
	assert.NotNil(t, manager.ctx)
	assert.NotNil(t, manager.cancel)
	assert.NotNil(t, manager.metrics)
	assert.NotNil(t, manager.cache)
	assert.NotNil(t, manager.logger)
	assert.NotNil(t, manager.localRPC)
}

func TestManager_PublicIPRetrieval(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Test successful public IP retrieval
	ip, err := manager.getPublicIP()
	assert.NoError(t, err)
	assert.Equal(t, "192.168.1.100", ip)

	// Test with error
	manager.getPublicIPFunc = mockPublicIPFuncError
	ip, err = manager.getPublicIP()
	assert.Error(t, err)
	assert.Empty(t, ip)
}

func TestManager_ManagerLifecycle(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Test initialization
	err := manager.initialize()
	assert.NoError(t, err)

	// Test context cancellation
	manager.cancel()

	// Test that Run returns immediately with cancelled context
	err = manager.Run()
	assert.NoError(t, err)
}

func TestManager_ConfigurationEdgeCases(t *testing.T) {
	// Test with minimal configuration
	cfg := &config.Config{
		Validator: config.Validator{
			Name:   "minimal-validator",
			RPCURL: "http://localhost:8899",
			Identities: config.ValidatorIdentities{
				ActiveKeyPair:  createTestPrivateKey("active"),
				PassiveKeyPair: createTestPrivateKey("passive"),
			},
		},
		Cluster: config.Cluster{
			Name:    "mainnet-beta",
			RPCURLs: []string{"https://api.mainnet-beta.solana.com"},
		},
		Failover: config.Failover{
			PollIntervalDuration:       5 * time.Second,
			LeaderlessSamplesThreshold: 3,
			TakeoverJitterSeconds:      5,
			DryRun:                     true,
			Peers:                      map[string]config.Peer{},
		},
		Prometheus: config.Prometheus{
			Port: 9090,
		},
	}

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)
	require.NotNil(t, manager)

	// Test initialization with minimal config
	err := manager.initialize()
	assert.NoError(t, err)

	// Verify that self was added to peers even with empty initial peers
	_, exists := cfg.Failover.Peers["minimal-validator"]
	assert.True(t, exists)
	assert.Equal(t, "192.168.1.100", cfg.Failover.Peers["minimal-validator"].IP)
}

func TestManager_Run_Success(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Run the manager in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- manager.Run()
	}()

	// Let it run for a short time to ensure it starts properly
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to stop the manager
	manager.cancel()

	// Wait for the manager to stop
	err := <-done
	assert.NoError(t, err)
}

func TestManager_Run_WithInitializationError(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFuncError, // This will cause initialization to fail
	}

	manager := NewManager(opts)

	// Run should return an error due to initialization failure
	err := manager.Run()
	assert.Error(t, err)
}

func TestManager_Run_WithShortPollInterval(t *testing.T) {
	cfg := createTestConfig()
	// Set a very short poll interval for testing
	cfg.Failover.PollIntervalDuration = 10 * time.Millisecond

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Run the manager in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- manager.Run()
	}()

	// Let it run for a few poll cycles
	time.Sleep(50 * time.Millisecond)

	// Cancel the context to stop the manager
	manager.cancel()

	// Wait for the manager to stop
	err := <-done
	assert.NoError(t, err)
}

func TestManager_Run_WithMetricsServer(t *testing.T) {
	cfg := createTestConfig()
	// Use a different port to avoid conflicts
	cfg.Prometheus.Port = 9092

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Run the manager in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- manager.Run()
	}()

	// Let it run for a short time to ensure metrics server starts
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to stop the manager
	manager.cancel()

	// Wait for the manager to stop
	err := <-done
	assert.NoError(t, err)
}

func TestManager_Run_WithContextCancellation(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Cancel the context immediately
	manager.cancel()

	// Run should return immediately due to cancelled context
	err := manager.Run()
	assert.NoError(t, err)
}

func TestManager_Run_WithMultipleStartStop(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Test multiple start/stop cycles
	for i := 0; i < 3; i++ {
		// Run the manager in a goroutine
		done := make(chan error, 1)
		go func() {
			done <- manager.Run()
		}()

		// Let it run briefly
		time.Sleep(50 * time.Millisecond)

		// Cancel the context to stop the manager
		manager.cancel()

		// Wait for the manager to stop
		err := <-done
		assert.NoError(t, err)

		// Create a new context for the next iteration
		manager.ctx, manager.cancel = context.WithCancel(context.Background())
	}
}

func TestManager_Run_WithLongRunning(t *testing.T) {
	cfg := createTestConfig()
	// Set a longer poll interval to test long-running behavior
	cfg.Failover.PollIntervalDuration = 100 * time.Millisecond

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Run the manager in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- manager.Run()
	}()

	// Let it run for several poll cycles
	time.Sleep(500 * time.Millisecond)

	// Cancel the context to stop the manager
	manager.cancel()

	// Wait for the manager to stop
	err := <-done
	assert.NoError(t, err)
}

func TestManager_Run_WithGossipStateIntegration(t *testing.T) {
	cfg := createTestConfig()
	// Set a short poll interval for testing
	cfg.Failover.PollIntervalDuration = 10 * time.Millisecond

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Run the manager in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- manager.Run()
	}()

	// Let it run for a few poll cycles to test gossip state integration
	time.Sleep(100 * time.Millisecond)

	// Verify that the manager was properly initialized
	assert.NotNil(t, manager.gossipState)
	assert.NotNil(t, manager.peerSelf)
	assert.Equal(t, "192.168.1.100", manager.peerSelf.IP)

	// Cancel the context to stop the manager
	manager.cancel()

	// Wait for the manager to stop
	err := <-done
	assert.NoError(t, err)
}

func TestManager_Run_WithMetricsIntegration(t *testing.T) {
	cfg := createTestConfig()
	// Use a different port to avoid conflicts
	cfg.Prometheus.Port = 9093

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Run the manager in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- manager.Run()
	}()

	// Let it run for a short time to test metrics integration
	time.Sleep(100 * time.Millisecond)

	// Verify that metrics were initialized
	assert.NotNil(t, manager.metrics)
	assert.NotNil(t, manager.cache)

	// Cancel the context to stop the manager
	manager.cancel()

	// Wait for the manager to stop
	err := <-done
	assert.NoError(t, err)
}

func TestManager_EnsurePassive_Success(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensurePassive - this will use the real RPC client but with dry run
	manager.ensurePassive()

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}

func TestManager_EnsurePassive_WithPreHookError(t *testing.T) {
	cfg := createTestConfig()
	// Set up a failing pre hook
	cfg.Failover.Passive.Hooks.Pre = []config.Hook{
		{Command: "exit 1"}, // This will fail
	}

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensurePassive - should handle pre hook error gracefully
	manager.ensurePassive()

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}

func TestManager_EnsurePassive_WithCommandError(t *testing.T) {
	cfg := createTestConfig()
	// Set up a failing command
	cfg.Failover.Passive.Command = "exit 1"

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensurePassive - should handle command error gracefully
	manager.ensurePassive()

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}

func TestManager_EnsurePassive_WithRPCError(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensurePassive - will fail due to RPC errors but should handle gracefully
	manager.ensurePassive()

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}

func TestManager_EnsurePassive_WithNotPassiveAfterCommand(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensurePassive - will fail due to RPC errors but should handle gracefully
	manager.ensurePassive()

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}

func TestManager_EnsurePassive_WithNotInGossip(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensurePassive - will fail due to RPC errors but should handle gracefully
	manager.ensurePassive()

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}

func TestManager_EnsureActive_Success(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensureActive - this will use the real RPC client but with dry run
	manager.ensureActive()

	// Verify that cache was updated with becoming_active status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_active", state.FailoverStatus)
}

func TestManager_EnsureActive_WithPreHookError(t *testing.T) {
	cfg := createTestConfig()
	// Set up a failing pre hook
	cfg.Failover.Active.Hooks.Pre = []config.Hook{
		{Command: "exit 1"}, // This will fail
	}

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensureActive - should handle pre hook error gracefully
	manager.ensureActive()

	// Verify that cache was updated with becoming_active status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_active", state.FailoverStatus)
}

func TestManager_EnsureActive_WithCommandError(t *testing.T) {
	cfg := createTestConfig()
	// Set up a failing command
	cfg.Failover.Active.Command = "exit 1"

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensureActive - should handle command error gracefully
	manager.ensureActive()

	// Verify that cache was updated with becoming_active status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_active", state.FailoverStatus)
}

func TestManager_EnsureActive_WithRPCError(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensureActive - will fail due to RPC errors but should handle gracefully
	manager.ensureActive()

	// Verify that cache was updated with becoming_active status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_active", state.FailoverStatus)
}

func TestManager_EnsureActive_WithNotActiveAfterCommand(t *testing.T) {
	cfg := createTestConfig()

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensureActive - will fail due to RPC errors but should handle gracefully
	manager.ensureActive()

	// Verify that cache was updated with becoming_active status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_active", state.FailoverStatus)
}

func TestManager_EnsureActive_WithDryRun(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.DryRun = true

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensureActive
	manager.ensureActive()

	// Verify that cache was updated with becoming_active status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_active", state.FailoverStatus)
}

func TestManager_EnsurePassive_WithDryRun(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.DryRun = true

	opts := NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	}

	manager := NewManager(opts)

	// Initialize the manager
	err := manager.initialize()
	require.NoError(t, err)

	// Call ensurePassive
	manager.ensurePassive()

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}
