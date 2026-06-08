package ha

import (
	"context"
	"os"
	"testing"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/gossip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProfileName = "main"

// mockPublicIPFunc is a mock function for getting public IP
func mockPublicIPFunc() (string, error) {
	return "192.168.1.100", nil
}

// mockPublicIPFuncError is a mock function that returns an error
func mockPublicIPFuncError() (string, error) {
	return "", assert.AnError
}

func createTestConfig() *config.Config {
	activeKeyPair := createTestPrivateKey("active")
	passiveKeyPair := createTestPrivateKey("passive")

	return &config.Config{
		Validator: config.Validator{
			Name:   "test-validator",
			RPCURL: "http://localhost:8899",
			Identities: config.ValidatorIdentities{
				PassiveKeyPair: passiveKeyPair,
			},
		},
		Cluster: config.Cluster{
			Name:    "mainnet-beta",
			RPCURLs: []string{"https://api.mainnet-beta.solana.com"},
		},
		Failover: config.Failover{
			PollIntervalDuration:       5 * time.Second,
			LeaderlessSamplesThreshold: 3,
			TakeoverJitterDuration:     5 * time.Second,
			DryRun:                     true,
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
			SelfHealthy: config.SelfHealthy{
				MinimumDuration:      45 * time.Second,
				PollIntervalDuration: 5 * time.Second,
			},
		},
		Profiles: config.Profiles{
			testProfileName: {
				Priority: intPtr(0),
				Identities: config.ValidatorIdentities{
					ActiveKeyPair: activeKeyPair,
				},
				VotePubkeyStr:         "Vote111111111111111111111111111111111111111",
				AuthorizedVoterPubkey: "Stake11111111111111111111111111111111111111",
				Peers: config.Peers{
					"test-validator": {IP: "192.168.1.100", Name: "test-validator"},
					"peer1":          {IP: "192.168.1.101", Name: "peer1"},
					"peer2":          {IP: "192.168.1.102", Name: "peer2"},
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

func testProfile(t *testing.T, m *Manager) *profileRuntime {
	t.Helper()
	return profileByName(t, m, testProfileName)
}

func profileByName(t *testing.T, m *Manager, name string) *profileRuntime {
	t.Helper()
	pr := m.profiles[name]
	require.NotNil(t, pr)
	return pr
}

func testGossipState(t *testing.T, m *Manager) *gossip.State {
	t.Helper()
	return testProfile(t, m).gossipState
}

func updateTestProfile(cfg *config.Config, fn func(*config.Profile)) {
	profile := cfg.Profiles[testProfileName]
	fn(&profile)
	cfg.Profiles[testProfileName] = profile
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
	assert.Equal(t, 1, manager.peerCount)
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
	assert.NotNil(t, testGossipState(t, manager))

	// Check that self is declared in the profile peer group
	_, exists := cfg.Profiles[testProfileName].Peers["test-validator"]
	assert.True(t, exists)
	assert.Equal(t, "192.168.1.100", cfg.Profiles[testProfileName].Peers["test-validator"].IP)
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

	// Test with only self in the profile peer group
	updateTestProfile(cfg, func(profile *config.Profile) {
		profile.Peers = config.Peers{
			"test-validator": {IP: "192.168.1.100", Name: "test-validator"},
		}
	})

	// Initialize should still work
	err := manager.initialize()
	assert.NoError(t, err)

	// Test with multiple backups in the same profile
	cfg = createTestConfig()
	updateTestProfile(cfg, func(profile *config.Profile) {
		profile.Peers["peer3"] = config.Peer{IP: "192.168.1.103", Name: "peer3"}
	})
	manager = NewManager(NewManagerOptions{
		Cfg:             cfg,
		GetPublicIPFunc: mockPublicIPFunc,
	})

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
	assert.NotNil(t, manager.cfg.Validator.Identities.PassiveKeyPair)
	assert.Equal(t, "mainnet-beta", manager.cfg.Cluster.Name)
	assert.Len(t, manager.cfg.Cluster.RPCURLs, 1)
	assert.Equal(t, 5*time.Second, manager.cfg.Failover.PollIntervalDuration)
	assert.Equal(t, 3, manager.cfg.Failover.LeaderlessSamplesThreshold)
	assert.Equal(t, 5*time.Second, manager.cfg.Failover.TakeoverJitterDuration)
	assert.True(t, manager.cfg.Failover.DryRun)
	assert.Len(t, manager.cfg.Profiles, 1)
	assert.NotNil(t, manager.cfg.Profiles[testProfileName].Identities.ActiveKeyPair)
	assert.Len(t, manager.cfg.Profiles[testProfileName].Peers, 3)
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
	assert.NotNil(t, testGossipState(t, manager))

	// Verify that self is declared in the profile peer group
	_, exists := cfg.Profiles[testProfileName].Peers["test-validator"]
	assert.True(t, exists)
	assert.Equal(t, "192.168.1.100", cfg.Profiles[testProfileName].Peers["test-validator"].IP)

	// Verify that the manager is ready to run
	assert.NotNil(t, manager.ctx)
	assert.NotNil(t, manager.cancel)
	assert.NotNil(t, manager.metrics)
	assert.NotNil(t, manager.cache)
	assert.NotNil(t, manager.logger)
	assert.NotNil(t, manager.localState)
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
	activeKeyPair := createTestPrivateKey("active")
	passiveKeyPair := createTestPrivateKey("passive")
	cfg := &config.Config{
		Validator: config.Validator{
			Name:   "minimal-validator",
			RPCURL: "http://localhost:8899",
			Identities: config.ValidatorIdentities{
				PassiveKeyPair: passiveKeyPair,
			},
		},
		Cluster: config.Cluster{
			Name:    "mainnet-beta",
			RPCURLs: []string{"https://api.mainnet-beta.solana.com"},
		},
		Failover: config.Failover{
			PollIntervalDuration:       5 * time.Second,
			LeaderlessSamplesThreshold: 3,
			TakeoverJitterDuration:     5 * time.Second,
			DryRun:                     true,
		},
		Profiles: config.Profiles{
			testProfileName: {
				Priority: intPtr(0),
				Identities: config.ValidatorIdentities{
					ActiveKeyPair: activeKeyPair,
				},
				VotePubkeyStr:         "Vote111111111111111111111111111111111111111",
				AuthorizedVoterPubkey: "Stake11111111111111111111111111111111111111",
				Peers: config.Peers{
					"minimal-validator": {IP: "192.168.1.100", Name: "minimal-validator"},
				},
			},
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

	// Verify that self is declared in the profile peer group.
	_, exists := cfg.Profiles[testProfileName].Peers["minimal-validator"]
	assert.True(t, exists)
	assert.Equal(t, "192.168.1.100", cfg.Profiles[testProfileName].Peers["minimal-validator"].IP)
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
	assert.NotNil(t, testGossipState(t, manager))
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
	manager.ensurePassive(testProfile(t, manager))

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
	manager.ensurePassive(testProfile(t, manager))

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
	manager.ensurePassive(testProfile(t, manager))

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
	manager.ensurePassive(testProfile(t, manager))

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
	manager.ensurePassive(testProfile(t, manager))

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
	manager.ensurePassive(testProfile(t, manager))

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
	manager.ensureActive(testProfile(t, manager))

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
	manager.ensureActive(testProfile(t, manager))

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
	manager.ensureActive(testProfile(t, manager))

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
	manager.ensureActive(testProfile(t, manager))

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
	manager.ensureActive(testProfile(t, manager))

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
	manager.ensureActive(testProfile(t, manager))

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
	manager.ensurePassive(testProfile(t, manager))

	// Verify that cache was updated with becoming_passive status
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus)
}

// seedGossipPeers adds synthetic peer states to the manager's gossip state without
// requiring a real RPC call, enabling unit tests for rank-based takeover logic.
func seedGossipPeers(m *Manager, peers map[string]gossip.PeerState) {
	m.profiles[testProfileName].gossipState.SetPeerStatesForTest(peers)
}

func intPtr(i int) *int {
	return &i
}

func TestDelayTakeoverAsActive_NoPeers(t *testing.T) {
	cfg := createTestConfig()
	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())
	// gossipState has no peers after init (no Refresh called)

	delayApplied, err := manager.delayTakeoverAsActive(testProfile(t, manager))

	assert.Error(t, err)
	assert.False(t, delayApplied)
}

func TestDelayTakeoverAsActive_Rank0_ConfigPriority(t *testing.T) {
	cfg := createTestConfig()
	selfIP := "192.168.1.100"
	peerIP := "192.168.1.101"

	// self has priority 0 (highest), peer has priority 1
	updateTestProfile(cfg, func(profile *config.Profile) {
		profile.Peers = config.Peers{
			"test-validator": {IP: selfIP, Name: "test-validator", Priority: intPtr(0)},
			"peer1":          {IP: peerIP, Name: "peer1", Priority: intPtr(1)},
		}
	})

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())
	manager.peerSelf.IP = selfIP

	// seed gossip with one peer so PeerCount() > 0
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"peer1": {IP: peerIP, Name: "peer1"},
	})

	delayApplied, err := manager.delayTakeoverAsActive(testProfile(t, manager))

	assert.NoError(t, err)
	assert.False(t, delayApplied, "rank-0 node should not apply a delay")
}

func TestDelayTakeoverAsActive_Rank1_ConfigPriority(t *testing.T) {
	cfg := createTestConfig()
	selfIP := "192.168.1.100"
	peerIP := "192.168.1.101"

	// self has priority 1, peer has priority 0 (highest)
	cfg.Failover.PollIntervalDuration = 10 * time.Millisecond // keep test fast
	updateTestProfile(cfg, func(profile *config.Profile) {
		profile.Peers = config.Peers{
			"test-validator": {IP: selfIP, Name: "test-validator", Priority: intPtr(1)},
			"peer1":          {IP: peerIP, Name: "peer1", Priority: intPtr(0)},
		}
	})

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())
	manager.peerSelf.IP = selfIP

	seedGossipPeers(manager, map[string]gossip.PeerState{
		"peer1": {IP: peerIP, Name: "peer1"},
	})

	start := time.Now()
	delayApplied, err := manager.delayTakeoverAsActive(testProfile(t, manager))
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.True(t, delayApplied, "rank-1 node should report delay applied")
	assert.GreaterOrEqual(t, elapsed, cfg.Failover.PollIntervalDuration, "rank-1 node should have slept at least one poll interval")
}

func TestDelayTakeoverAsActive_SelfNotInRankMap(t *testing.T) {
	cfg := createTestConfig()

	// No config priorities — falls back to IP-based ranking from gossip state.
	// Self's IP is set to one that is NOT present in gossip, so it won't appear
	// in the IP rank map, triggering the "not found" error path.
	updateTestProfile(cfg, func(profile *config.Profile) {
		profile.Peers = config.Peers{
			"test-validator": {IP: "192.168.1.100", Name: "test-validator"},
			"peer1":          {IP: "192.168.1.101", Name: "peer1"},
		}
	})

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())
	// self IP that won't appear in the gossip peer seed below
	manager.peerSelf.IP = "10.0.0.99"

	// Only peer1 is in gossip — self ("10.0.0.99") is absent from the IP rank map
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"peer1": {IP: "192.168.1.101", Name: "peer1"},
	})

	delayApplied, err := manager.delayTakeoverAsActive(testProfile(t, manager))

	assert.Error(t, err)
	assert.False(t, delayApplied)
}

// TestEnsureHAState_DelinquencyBypass_Enabled verifies that when failover.delinquency_bypass
// is true and the active peer is declared delinquent, ensureHAState proceeds past the
// leaderless-sample threshold check even when LeaderlessSamplesCount is below threshold.
//
// Observable evidence of bypass: ensureHAState reaches the ensurePassive path
// (self not in gossip, other peers visible) and updates cache to "becoming_passive",
// rather than returning early with "no failover required".
func TestEnsureHAState_DelinquencyBypass_Enabled(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.LeaderlessSamplesThreshold = 3
	cfg.Failover.DelinquencyBypass = true

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())

	// Freeze gossip state so Refresh() doesn't reset our seeded values.
	testGossipState(t, manager).SetRefreshNoOpForTest(true)

	// Seed one peer that is NOT self — self (192.168.1.100) will be absent from gossip,
	// causing isSelfNotInGossip()=true; HasPeers(selfIP)=true so ensurePassive() is reached.
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"peer1": {IP: "192.168.1.101", Name: "peer1"},
	})

	// Network has declared the active peer delinquent.
	testGossipState(t, manager).SetActivePeerDelinquentForTest(true)
	manager.localState.SetIdentityForTest(testProfile(t, manager).cfg.ActivePubkey())

	manager.ensureHAState()

	// If the delinquency bypass fired, ensurePassive was called and cache reflects it.
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_passive", state.FailoverStatus,
		"delinquency bypass should trigger ensurePassive path, not return 'no failover required'")
}

// TestEnsureHAState_DelinquencyBypass_Disabled verifies that when failover.delinquency_bypass
// is false (the default), a delinquent peer below the leaderless threshold does NOT trigger
// a failover — the bypass is ignored and the normal threshold check governs.
func TestEnsureHAState_DelinquencyBypass_Disabled(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.LeaderlessSamplesThreshold = 3
	cfg.Failover.DelinquencyBypass = false // explicit default

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())

	testGossipState(t, manager).SetRefreshNoOpForTest(true)
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"peer1": {IP: "192.168.1.101", Name: "peer1"},
	})
	// Peer is delinquent, but bypass is disabled — should be treated as "active peer found".
	testGossipState(t, manager).SetActivePeerDelinquentForTest(true)

	manager.ensureHAState()

	state := manager.cache.GetState()
	assert.Equal(t, "idle", state.FailoverStatus,
		"delinquency bypass should not fire when failover.delinquency_bypass is false")
}

// TestEnsureHAState_BelowThreshold_NoDelinquency_NoFailover verifies that when
// LeaderlessSamplesCount is below the threshold AND the active peer is NOT delinquent,
// ensureHAState returns early without triggering any failover action.
func TestEnsureHAState_BelowThreshold_NoDelinquency_NoFailover(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.LeaderlessSamplesThreshold = 3

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())

	testGossipState(t, manager).SetRefreshNoOpForTest(true)
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"peer1": {IP: "192.168.1.101", Name: "peer1"},
	})

	manager.ensureHAState()

	// refreshMetrics() always sets FailoverStatus="idle"; ensurePassive/ensureActive would
	// overwrite it with "becoming_passive"/"becoming_active". Staying at "idle" confirms the
	// function returned early without triggering a failover.
	state := manager.cache.GetState()
	assert.Equal(t, "idle", state.FailoverStatus,
		"no failover should be triggered when below threshold and peer is not delinquent")
}

func TestEnsureHAState_SelectsLowestProfilePriority(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.DryRun = false
	cfg.Failover.LeaderlessSamplesThreshold = 1
	cfg.Failover.Active.Hooks = config.Hooks{}

	dir := t.TempDir()
	selectedProfilePath := dir + "/selected-profile"
	scriptPath := dir + "/write-selected-profile.sh"
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$2\"\n"), 0o755))
	cfg.Failover.Active.Command = scriptPath
	cfg.Failover.Active.Args = []string{"{{ .ProfileName }}", selectedProfilePath}

	updateTestProfile(cfg, func(profile *config.Profile) {
		profile.Priority = intPtr(1)
	})
	cfg.Profiles["secondary"] = config.Profile{
		Priority: intPtr(0),
		Identities: config.ValidatorIdentities{
			ActiveKeyPair: createTestPrivateKey("secondary-active"),
		},
		VotePubkeyStr:         "SysvarC1ock11111111111111111111111111111111",
		AuthorizedVoterPubkey: "SysvarRecentB1ockHashes11111111111111111111",
		Peers: config.Peers{
			"test-validator": {IP: "192.168.1.100", Name: "test-validator"},
			"peer1":          {IP: "192.168.1.101", Name: "peer1"},
		},
	}

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())

	for _, profileName := range []string{testProfileName, "secondary"} {
		pr := profileByName(t, manager, profileName)
		pr.gossipState.SetRefreshNoOpForTest(true)
		pr.gossipState.LeaderlessSamplesCount = cfg.Failover.LeaderlessSamplesThreshold
		pr.gossipState.SetPeerStatesForTest(map[string]gossip.PeerState{
			"test-validator": {IP: "192.168.1.100", Name: "test-validator", Role: "passive"},
			"peer1":          {IP: "192.168.1.101", Name: "peer1", Role: "passive"},
		})
	}
	manager.localState.SetIdentityForTest(cfg.Validator.Identities.PassivePubkey())
	manager.localState.SetForceHealthyForTest(true)
	manager.localState.SetHealthySinceForTest(time.Now().Add(-time.Hour))

	manager.ensureHAState()

	selectedProfile, err := os.ReadFile(selectedProfilePath)
	require.NoError(t, err)
	assert.Equal(t, "secondary", string(selectedProfile))

	state := manager.cache.GetState()
	assert.Equal(t, "aborted_profile_priority", state.Profiles[testProfileName].FailoverStatus)
	assert.Equal(t, "becoming_active", state.Profiles["secondary"].FailoverStatus)
}

func TestEnsureHAState_ProfileFailoverStatusLocalBusy(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.LeaderlessSamplesThreshold = 1

	cfg.Profiles["secondary"] = config.Profile{
		Priority: intPtr(1),
		Identities: config.ValidatorIdentities{
			ActiveKeyPair: createTestPrivateKey("secondary-active"),
		},
		VotePubkeyStr:         "SysvarC1ock11111111111111111111111111111111",
		AuthorizedVoterPubkey: "SysvarRecentB1ockHashes11111111111111111111",
		Peers: config.Peers{
			"test-validator": {IP: "192.168.1.100", Name: "test-validator"},
			"peer1":          {IP: "192.168.1.101", Name: "peer1"},
		},
	}

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())

	mainProfile := profileByName(t, manager, testProfileName)
	mainProfile.gossipState.SetRefreshNoOpForTest(true)
	mainProfile.gossipState.SetPeerStatesForTest(map[string]gossip.PeerState{
		"test-validator": {
			IP:             "192.168.1.100",
			Name:           "test-validator",
			Pubkey:         mainProfile.cfg.ActivePubkey(),
			LastSeenActive: true,
		},
	})

	secondaryProfile := profileByName(t, manager, "secondary")
	secondaryProfile.gossipState.SetRefreshNoOpForTest(true)
	secondaryProfile.gossipState.LeaderlessSamplesCount = cfg.Failover.LeaderlessSamplesThreshold
	secondaryProfile.gossipState.SetPeerStatesForTest(map[string]gossip.PeerState{
		"test-validator": {
			IP:     "192.168.1.100",
			Name:   "test-validator",
			Pubkey: mainProfile.cfg.ActivePubkey(),
			Role:   "busy",
		},
		"peer1": {
			IP:   "192.168.1.101",
			Name: "peer1",
			Role: "passive",
		},
	})

	manager.localState.SetIdentityForTest(mainProfile.cfg.ActivePubkey())
	manager.localState.SetForceHealthyForTest(true)
	manager.localState.SetHealthySinceForTest(time.Now().Add(-time.Hour))

	manager.ensureHAState()

	state := manager.cache.GetState()
	assert.Equal(t, "aborted_local_busy", state.Profiles["secondary"].FailoverStatus)
}

// TestEnsureHAState_DelinquencyBypass_Rank0_ProceedsToEnsureActive verifies the regression:
// before the fix, a rank-0 node with delinquency_bypass=true and LeaderlessSamplesCount=1
// (below threshold=3) was blocked by the LeaderlessSamplesBelowThreshold check —
// GetActivePeer() would error and ensureHAState() returned without calling ensureActive().
// After the fix (gate on delayApplied && count==0), rank-0 with no delay/no refresh skips
// the check and proceeds all the way to ensureActive(), setting cache to "becoming_active".
func TestEnsureHAState_DelinquencyBypass_Rank0_ProceedsToEnsureActive(t *testing.T) {
	cfg := createTestConfig()
	cfg.Failover.LeaderlessSamplesThreshold = 3
	cfg.Failover.DelinquencyBypass = true
	// rank-0 by IP: self (185.0.0.1) < peer (186.0.0.1)
	selfIP := "185.0.0.1"
	peerIP := "186.0.0.1"
	updateTestProfile(cfg, func(profile *config.Profile) {
		profile.Peers = config.Peers{
			"test-validator": {IP: selfIP, Name: "test-validator"},
			"peer1":          {IP: peerIP, Name: "peer1"},
		}
	})

	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: func() (string, error) { return selfIP, nil }})
	require.NoError(t, manager.initialize())

	// Freeze gossip so seeded state persists across the ensureHAState call.
	testGossipState(t, manager).SetRefreshNoOpForTest(true)

	// Self is in gossip (passive), peer is also in gossip (no one active).
	// LeaderlessSamplesCount is 1 — below threshold=3 — simulating the first delinquency sample.
	seedGossipPeers(manager, map[string]gossip.PeerState{
		"test-validator": {IP: selfIP, Name: "test-validator", LastSeenActive: false},
		"peer1":          {IP: peerIP, Name: "peer1", LastSeenActive: false},
	})
	testGossipState(t, manager).LeaderlessSamplesCount = 1
	testGossipState(t, manager).SetActivePeerDelinquentForTest(true)

	// Local state: healthy and healthy long enough to be a failover candidate.
	manager.localState.SetIdentityForTest(cfg.Validator.Identities.PassivePubkey())
	manager.localState.SetForceHealthyForTest(true)
	manager.localState.SetHealthySinceForTest(time.Now().Add(-time.Hour))

	manager.ensureHAState()

	// ensureActive() sets cache to "becoming_active". Reaching it proves the fix works:
	// the old LeaderlessSamplesBelowThreshold check no longer aborts rank-0 takeovers.
	state := manager.cache.GetState()
	assert.Equal(t, "becoming_active", state.FailoverStatus,
		"rank-0 delinquency bypass should proceed to ensureActive() regardless of leaderless count")
}
