package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testActivePubkey  = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	testPassivePubkey = "11111111111111111111111111111112"
)

// newMockRPCServer creates a mock Solana JSON-RPC HTTP server.
// responses maps method names to their JSON result values (success path).
// To simulate an error for a method, omit it from responses — the server returns
// -32601 Method Not Found, which the rpc.Client surfaces as an error.
func newMockRPCServer(t *testing.T, responses map[string]interface{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		result, ok := responses[req.Method]
		if !ok {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
				"id":      req.ID,
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  result,
			"id":      req.ID,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

// identityResult returns a mock getIdentity result for the given pubkey.
func identityResult(pubkey string) map[string]interface{} {
	return map[string]interface{}{"identity": pubkey}
}

// newTestState creates a State wired to the given rpc.Client with sensible test defaults.
func newTestState(rpcClient *rpc.Client, minDuration time.Duration) *State {
	return NewState(Options{
		RPC:          rpcClient,
		ActivePubkey: testActivePubkey,
		Cfg: config.SelfHealthy{
			MinimumDuration:      minDuration,
			PollIntervalDuration: time.Second,
		},
		Ctx: context.Background(),
	})
}

// --- constructor ---

func TestNewState(t *testing.T) {
	rpcClient := rpc.NewClient("test", "http://localhost:8899")
	cfg := config.SelfHealthy{
		MinimumDuration:      45 * time.Second,
		PollIntervalDuration: 5 * time.Second,
	}

	s := NewState(Options{
		RPC:          rpcClient,
		Cfg:          cfg,
		ActivePubkey: testActivePubkey,
		Ctx:          context.Background(),
	})

	require.NotNil(t, s)
	assert.Equal(t, testActivePubkey, s.activePubkey)
	assert.Equal(t, cfg.MinimumDuration, s.cfg.MinimumDuration)
	assert.True(t, s.healthySince.IsZero())
	assert.False(t, s.sampledOnce)
	assert.False(t, s.minDurationReached)
	assert.False(t, s.unhealthyLogged)
}

// --- IsSelfHealthyLongEnough / SelfHealthyDuration (pure state, no RPC) ---

func TestIsSelfHealthyLongEnough_NoStreak(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "http://localhost:8899"), 45*time.Second)
	assert.False(t, s.IsSelfHealthyLongEnough())
}

func TestIsSelfHealthyLongEnough_StreakBelowMinimum(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "http://localhost:8899"), 45*time.Second)
	s.healthySince = time.Now().Add(-10 * time.Second) // healthy for 10s, min is 45s
	assert.False(t, s.IsSelfHealthyLongEnough())
}

func TestIsSelfHealthyLongEnough_StreakExceedsMinimum(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "http://localhost:8899"), 45*time.Second)
	s.healthySince = time.Now().Add(-60 * time.Second) // healthy for 60s, min is 45s
	assert.True(t, s.IsSelfHealthyLongEnough())
}

func TestSelfHealthyDuration_NoStreak(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "http://localhost:8899"), 45*time.Second)
	assert.Equal(t, time.Duration(0), s.SelfHealthyDuration())
}

func TestSelfHealthyDuration_WithStreak(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "http://localhost:8899"), 45*time.Second)
	s.healthySince = time.Now().Add(-30 * time.Second)
	dur := s.SelfHealthyDuration()
	assert.True(t, dur >= 29*time.Second && dur <= 31*time.Second, "expected ~30s, got %s", dur)
}

// --- IsSelfHealthy ---

func TestIsSelfHealthy_Healthy(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getHealth": "ok",
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)
	assert.True(t, s.IsSelfHealthy())
}

func TestIsSelfHealthy_Unhealthy(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		// getHealth omitted — server returns error
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)
	assert.False(t, s.IsSelfHealthy())
}

func TestIsSelfHealthy_RPCError(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "https://invalid-url-that-will-fail.local"), 45*time.Second)
	assert.False(t, s.IsSelfHealthy())
}

// --- IsSelfActive ---

func TestIsSelfActive_IdentityMatchesActivePubkey(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getIdentity": identityResult(testActivePubkey),
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)
	assert.True(t, s.IsSelfActive())
}

func TestIsSelfActive_IdentityDoesNotMatchActivePubkey(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getIdentity": identityResult(testPassivePubkey),
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)
	assert.False(t, s.IsSelfActive())
}

func TestIsSelfActive_RPCError(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "https://invalid-url-that-will-fail.local"), 45*time.Second)
	assert.False(t, s.IsSelfActive())
}

// --- IsSelfPassive ---

func TestIsSelfPassive_IdentityIsPassive(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getIdentity": identityResult(testPassivePubkey),
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)
	assert.True(t, s.IsSelfPassive())
}

func TestIsSelfPassive_IdentityIsActive(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getIdentity": identityResult(testActivePubkey),
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)
	assert.False(t, s.IsSelfPassive())
}

func TestIsSelfPassive_RPCError(t *testing.T) {
	s := newTestState(rpc.NewClient("test", "https://invalid-url-that-will-fail.local"), 45*time.Second)
	assert.False(t, s.IsSelfPassive())
}

// --- SampleSelf streak logic ---

func TestSampleSelf_FirstSampleHealthy_StartsStreak(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getHealth": "ok",
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)

	s.SampleSelf()

	assert.True(t, s.sampledOnce)
	assert.False(t, s.healthySince.IsZero(), "healthy streak should have started")
	assert.False(t, s.unhealthyLogged)
}

func TestSampleSelf_FirstSampleUnhealthy_LogsOnce(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		// getHealth omitted — error
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)

	s.SampleSelf()
	assert.True(t, s.sampledOnce)
	assert.True(t, s.healthySince.IsZero(), "no streak should start when unhealthy")
	assert.True(t, s.unhealthyLogged, "should log unhealthy once")

	// second sample should NOT log again
	s.SampleSelf()
	assert.True(t, s.unhealthyLogged) // flag stays true, no second log
}

func TestSampleSelf_HealthyStreakCrossesMinDuration(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getHealth":   "ok",
		"getIdentity": identityResult(testPassivePubkey), // passive, so "eligible for failover" appended
	})
	s := newTestState(rpc.NewClient("test", server.URL), 30*time.Second)

	// First sample: starts streak
	s.SampleSelf()
	require.False(t, s.healthySince.IsZero())
	assert.False(t, s.minDurationReached)

	// Backdate healthySince to simulate the streak having been going for longer than min_duration
	s.healthySince = time.Now().Add(-60 * time.Second)
	s.SampleSelf()

	assert.True(t, s.minDurationReached, "min duration flag should be set after streak crosses threshold")
}

func TestSampleSelf_MinDurationLoggedOnlyOnce(t *testing.T) {
	server := newMockRPCServer(t, map[string]interface{}{
		"getHealth":   "ok",
		"getIdentity": identityResult(testPassivePubkey),
	})
	s := newTestState(rpc.NewClient("test", server.URL), 30*time.Second)

	s.SampleSelf()
	s.healthySince = time.Now().Add(-60 * time.Second) // backdate to cross threshold
	s.SampleSelf()                                     // crosses threshold, sets minDurationReached
	assert.True(t, s.minDurationReached)

	// Subsequent samples should not reset the flag
	s.SampleSelf()
	s.SampleSelf()
	assert.True(t, s.minDurationReached)
}

func TestSampleSelf_HealthyToUnhealthy_ResetsStreak(t *testing.T) {
	healthyServer := newMockRPCServer(t, map[string]interface{}{
		"getHealth": "ok",
	})
	s := newTestState(rpc.NewClient("test", healthyServer.URL), 45*time.Second)

	// Establish a healthy streak
	s.SampleSelf()
	require.False(t, s.healthySince.IsZero())

	// Swap to an unhealthy server
	unhealthyServer := newMockRPCServer(t, map[string]interface{}{})
	s.rpc = rpc.NewClient("test", unhealthyServer.URL)

	s.SampleSelf()

	assert.True(t, s.healthySince.IsZero(), "streak should reset on unhealthy sample")
	assert.False(t, s.minDurationReached)
	assert.True(t, s.unhealthyLogged)
}

func TestSampleSelf_UnhealthyToHealthy_ResetsUnhealthyLogged(t *testing.T) {
	unhealthyServer := newMockRPCServer(t, map[string]interface{}{})
	s := newTestState(rpc.NewClient("test", unhealthyServer.URL), 45*time.Second)

	// Sample unhealthy to set the flag
	s.SampleSelf()
	require.True(t, s.unhealthyLogged)

	// Recover to healthy
	healthyServer := newMockRPCServer(t, map[string]interface{}{
		"getHealth": "ok",
	})
	s.rpc = rpc.NewClient("test", healthyServer.URL)
	s.SampleSelf()

	assert.False(t, s.healthySince.IsZero(), "streak should start after recovery")
	assert.False(t, s.unhealthyLogged, "unhealthyLogged flag should reset on recovery")
}

func TestSampleSelf_ConcurrentAccess(t *testing.T) {
	// Simulates the real-world usage: one writer goroutine (the health ticker calling SampleSelf)
	// and multiple reader goroutines (the main HA loop calling IsSelfHealthyLongEnough /
	// SelfHealthyDuration). rpc.Client is intentionally not goroutine-safe — SampleSelf is
	// always called from a single goroutine in production.
	server := newMockRPCServer(t, map[string]interface{}{
		"getHealth":   "ok",
		"getIdentity": identityResult(testPassivePubkey),
	})
	s := newTestState(rpc.NewClient("test", server.URL), 45*time.Second)

	stop := make(chan struct{})
	readerDone := make(chan struct{}, 10)

	// Single writer goroutine — mirrors real health tracker
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				s.SampleSelf()
			}
		}
	}()

	// Multiple reader goroutines — mirrors main HA loop
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = s.IsSelfHealthyLongEnough()
				_ = s.SelfHealthyDuration()
			}
			readerDone <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-readerDone
	}
	close(stop)
}
