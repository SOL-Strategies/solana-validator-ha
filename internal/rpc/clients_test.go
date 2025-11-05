package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSolanaRPCServer creates a mock HTTP server that responds to Solana RPC requests
func mockSolanaRPCServer(t *testing.T, responses map[string]interface{}) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse the JSON-RPC request
		var request struct {
			Method string      `json:"method"`
			Params interface{} `json:"params"`
			ID     int         `json:"id"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Check if we have a response for this method
		response, exists := responses[request.Method]
		if !exists {
			// Return a default error response
			errorResponse := map[string]interface{}{
				"jsonrpc": "2.0",
				"error": map[string]interface{}{
					"code":    -32601,
					"message": "Method not found",
				},
				"id": request.ID,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(errorResponse)
			return
		}

		// Return the mock response
		successResponse := map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  response,
			"id":      request.ID,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(successResponse)
	}))

	t.Cleanup(func() {
		server.Close()
	})

	return server
}

// mockFailingServer creates a server that always returns errors
func mockFailingServer(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse the JSON-RPC request to get the ID
		var request struct {
			ID int `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&request)

		// Return an error response
		errorResponse := map[string]interface{}{
			"jsonrpc": "2.0",
			"error": map[string]interface{}{
				"code":    -32000,
				"message": "Server error",
			},
			"id": request.ID,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(errorResponse)
	}))

	t.Cleanup(func() {
		server.Close()
	})

	return server
}

// mockSlowServer creates a server that responds slowly
func mockSlowServer(t *testing.T, delay time.Duration) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)

		var request struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&request)

		// Return a simple success response
		successResponse := map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  "ok",
			"id":      request.ID,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(successResponse)
	}))

	t.Cleanup(func() {
		server.Close()
	})

	return server
}

func TestNewClient(t *testing.T) {
	// Test creating client with multiple URLs
	client := NewClient("test", "http://localhost:8899", "https://api.testnet.solana.com")

	assert.NotNil(t, client)
	assert.Len(t, client.clients, 2)
	assert.Contains(t, client.clients, "http://localhost:8899")
	assert.Contains(t, client.clients, "https://api.testnet.solana.com")
	assert.Equal(t, 5*time.Second, client.timeout)
}

func TestGetClusterNodes(t *testing.T) {
	// Mock response for GetClusterNodes
	mockResponse := []map[string]interface{}{
		{
			"pubkey":  "11111111111111111111111111111111",
			"gossip":  "127.0.0.1:8001",
			"tpu":     "127.0.0.1:8002",
			"rpc":     "127.0.0.1:8003",
			"version": "1.16.0",
		},
		{
			"pubkey":  "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
			"gossip":  "127.0.0.1:8004",
			"tpu":     "127.0.0.1:8005",
			"rpc":     "127.0.0.1:8006",
			"version": "1.16.0",
		},
	}

	server := mockSolanaRPCServer(t, map[string]interface{}{
		"getClusterNodes": mockResponse,
	})

	client := NewClient("test", server.URL)
	ctx := context.Background()

	result, err := client.GetClusterNodes(ctx)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Check first node
	assert.Equal(t, "11111111111111111111111111111111", result[0].Pubkey.String())
	assert.Equal(t, "127.0.0.1:8001", *result[0].Gossip)
	assert.Equal(t, "127.0.0.1:8002", *result[0].TPU)
	assert.Equal(t, "127.0.0.1:8003", *result[0].RPC)
	assert.Equal(t, "1.16.0", *result[0].Version)

	// Check second node
	assert.Equal(t, "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", result[1].Pubkey.String())
	assert.Equal(t, "127.0.0.1:8004", *result[1].Gossip)
	assert.Equal(t, "127.0.0.1:8005", *result[1].TPU)
	assert.Equal(t, "127.0.0.1:8006", *result[1].RPC)
	assert.Equal(t, "1.16.0", *result[1].Version)
}

func TestGetIdentity(t *testing.T) {
	// Mock response for GetIdentity
	mockResponse := map[string]interface{}{
		"identity": "11111111111111111111111111111111",
	}

	server := mockSolanaRPCServer(t, map[string]interface{}{
		"getIdentity": mockResponse,
	})

	client := NewClient("test", server.URL)
	ctx := context.Background()

	result, err := client.GetIdentity(ctx)
	require.NoError(t, err)
	assert.Equal(t, "11111111111111111111111111111111", result.Identity.String())
}

func TestGetHealth(t *testing.T) {
	// Mock response for GetHealth
	mockResponse := "ok"

	server := mockSolanaRPCServer(t, map[string]interface{}{
		"getHealth": mockResponse,
	})

	client := NewClient("test", server.URL)
	ctx := context.Background()

	result, err := client.GetHealth(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
}

func TestRetryLogic(t *testing.T) {
	// Create a failing server and a working server
	failingServer := mockFailingServer(t)

	mockResponse := map[string]interface{}{
		"identity": "11111111111111111111111111111111",
	}
	workingServer := mockSolanaRPCServer(t, map[string]interface{}{
		"getIdentity": mockResponse,
	})

	// Create client with failing server first, then working server
	client := NewClient("test", failingServer.URL, workingServer.URL)
	ctx := context.Background()

	// Should succeed by retrying to the working server
	result, err := client.GetIdentity(ctx)
	require.NoError(t, err)
	assert.Equal(t, "11111111111111111111111111111111", result.Identity.String())
}

func TestAllEndpointsFail(t *testing.T) {
	// Create two failing servers
	failingServer1 := mockFailingServer(t)
	failingServer2 := mockFailingServer(t)

	client := NewClient("test", failingServer1.URL, failingServer2.URL)
	ctx := context.Background()

	// Should fail when all endpoints fail
	result, err := client.GetIdentity(ctx)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "method call failed on all RPC endpoints")
	assert.Contains(t, err.Error(), "GetIdentity")
}

func TestTimeout(t *testing.T) {
	// Create a slow server that takes longer than the timeout
	slowServer := mockSlowServer(t, 10*time.Second)

	client := NewClient("test", slowServer.URL)
	ctx := context.Background()

	// Should timeout
	result, err := client.GetHealth(ctx)
	assert.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "method call failed on all RPC endpoints")
}

func TestCustomTimeout(t *testing.T) {
	// Create a slow server
	slowServer := mockSlowServer(t, 2*time.Second)

	client := NewClient("test", slowServer.URL)
	client.timeout = 1 * time.Second // Set custom timeout
	ctx := context.Background()

	// Should timeout
	result, err := client.GetHealth(ctx)
	assert.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "method call failed on all RPC endpoints")
}

func TestMultipleWorkingEndpoints(t *testing.T) {
	// Create two working servers
	mockResponse1 := map[string]interface{}{
		"identity": "11111111111111111111111111111111",
	}
	mockResponse2 := map[string]interface{}{
		"identity": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
	}

	server1 := mockSolanaRPCServer(t, map[string]interface{}{
		"getIdentity": mockResponse1,
	})
	server2 := mockSolanaRPCServer(t, map[string]interface{}{
		"getIdentity": mockResponse2,
	})

	client := NewClient("test", server1.URL, server2.URL)
	ctx := context.Background()

	// Should succeed with the first working server
	result, err := client.GetIdentity(ctx)
	require.NoError(t, err)
	assert.Equal(t, "11111111111111111111111111111111", result.Identity.String())
}

func TestEmptyURLs(t *testing.T) {
	// Test creating client with no URLs
	client := NewClient("test")
	assert.NotNil(t, client)
	assert.Len(t, client.clients, 0)
}

func TestInvalidURL(t *testing.T) {
	// Test with invalid URL - this should still create the client
	// but the actual RPC calls will fail
	client := NewClient("test", "invalid-url")
	assert.NotNil(t, client)
	assert.Len(t, client.clients, 1)
	assert.Contains(t, client.clients, "invalid-url")

	ctx := context.Background()
	result, err := client.GetHealth(ctx)
	assert.Error(t, err)
	assert.Empty(t, result)
}

func TestContextCancellation(t *testing.T) {
	// Create a slow server
	slowServer := mockSlowServer(t, 5*time.Second)

	client := NewClient("test", slowServer.URL)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context immediately
	cancel()

	// Should fail due to context cancellation
	result, err := client.GetHealth(ctx)
	assert.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "method call failed on all RPC endpoints")
}

func TestComplexClusterNodesResponse(t *testing.T) {
	// Test with a more complex cluster nodes response
	mockResponse := []map[string]interface{}{
		{
			"pubkey":  "11111111111111111111111111111111",
			"gossip":  "192.168.1.100:8001",
			"tpu":     "192.168.1.100:8002",
			"rpc":     "192.168.1.100:8003",
			"version": "1.17.0",
		},
		{
			"pubkey":  "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
			"gossip":  "192.168.1.101:8001",
			"tpu":     "192.168.1.101:8002",
			"rpc":     "192.168.1.101:8003",
			"version": "1.17.0",
		},
		{
			"pubkey":  "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL",
			"gossip":  "192.168.1.102:8001",
			"tpu":     "192.168.1.102:8002",
			"rpc":     "192.168.1.102:8003",
			"version": "1.17.0",
		},
	}

	server := mockSolanaRPCServer(t, map[string]interface{}{
		"getClusterNodes": mockResponse,
	})

	client := NewClient("test", server.URL)
	ctx := context.Background()

	result, err := client.GetClusterNodes(ctx)
	require.NoError(t, err)
	require.Len(t, result, 3)

	// Verify all nodes
	expectedPubkeys := []string{
		"11111111111111111111111111111111",
		"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
		"ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL",
	}

	for i, expectedPubkey := range expectedPubkeys {
		assert.Equal(t, expectedPubkey, result[i].Pubkey.String())
		assert.Equal(t, fmt.Sprintf("192.168.1.10%d:8001", i), *result[i].Gossip)
		assert.Equal(t, fmt.Sprintf("192.168.1.10%d:8002", i), *result[i].TPU)
		assert.Equal(t, fmt.Sprintf("192.168.1.10%d:8003", i), *result[i].RPC)
		assert.Equal(t, "1.17.0", *result[i].Version)
	}
}

func TestGetURLsToTry(t *testing.T) {
	tests := []struct {
		name              string
		urls              []string
		lastSuccessfulURL string
		expected          []string
	}{
		{
			name:              "single URL",
			urls:              []string{"url1"},
			lastSuccessfulURL: "",
			expected:          []string{"url1"},
		},
		{
			name:              "no last successful URL",
			urls:              []string{"url1", "url2", "url3"},
			lastSuccessfulURL: "",
			expected:          []string{"url1", "url2", "url3"},
		},
		{
			name:              "with last successful URL",
			urls:              []string{"url1", "url2", "url3"},
			lastSuccessfulURL: "url2",
			expected:          []string{"url1", "url3", "url2"},
		},
		{
			name:              "last successful URL is first",
			urls:              []string{"url1", "url2", "url3"},
			lastSuccessfulURL: "url1",
			expected:          []string{"url2", "url3", "url1"},
		},
		{
			name:              "last successful URL is last",
			urls:              []string{"url1", "url2", "url3"},
			lastSuccessfulURL: "url3",
			expected:          []string{"url1", "url2", "url3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient("test", tt.urls...)
			client.lastSuccessfulURL = tt.lastSuccessfulURL

			result := client.getURLsToTry()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLastSuccessfulURLAvoidance(t *testing.T) {
	// Create multiple mock servers that track which one was called
	var callCounts = make(map[string]int)

	// Create 3 mock servers
	servers := make([]*httptest.Server, 3)
	urls := make([]string, 3)

	for i := 0; i < 3; i++ {
		serverIndex := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverURL := servers[serverIndex].URL
			callCounts[serverURL]++

			// Return a simple identity response
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"result": map[string]interface{}{
					"identity": "11111111111111111111111111111111",
				},
				"id": 1,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		urls[i] = servers[i].URL
	}

	// Clean up servers when test completes
	for _, server := range servers {
		defer server.Close()
	}

	// Create client with multiple URLs
	client := NewClient("test", urls...)

	// Make several calls and verify it avoids the last successful URL initially
	ctx := context.Background()

	// First call - should succeed on first URL
	_, err := client.GetIdentity(ctx)
	require.NoError(t, err, "GetIdentity should succeed")
	firstSuccessfulURL := client.lastSuccessfulURL

	// Reset call counts to track subsequent calls
	callCounts = make(map[string]int)

	// Make 3 more calls - should avoid the first successful URL initially
	for i := 0; i < 3; i++ {
		_, err := client.GetIdentity(ctx)
		require.NoError(t, err, "GetIdentity should succeed")
	}

	// Verify that other URLs were tried first (throttling protection)
	// The first successful URL should only be used as the last option
	totalCalls := 0
	for _, count := range callCounts {
		totalCalls += count
	}

	assert.Equal(t, 3, totalCalls, "Should have made 3 additional calls")

	// With 3 URLs and first successful URL being avoided initially,
	// the pattern should try the other 2 URLs first, then fallback to the first
	if count, exists := callCounts[firstSuccessfulURL]; exists {
		assert.True(t, count <= 1, "First successful URL should be used minimally for throttling protection")
	}
}

func TestLastSuccessfulURLWithFailures(t *testing.T) {
	// Create servers where first one fails, others succeed
	var callCounts = make(map[string]int)

	servers := make([]*httptest.Server, 3)
	urls := make([]string, 3)

	for i := 0; i < 3; i++ {
		serverIndex := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverURL := servers[serverIndex].URL
			callCounts[serverURL]++

			// First server always fails
			if serverIndex == 0 {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}

			// Other servers succeed
			response := map[string]interface{}{
				"jsonrpc": "2.0",
				"result": map[string]interface{}{
					"identity": "11111111111111111111111111111111",
				},
				"id": 1,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		urls[i] = servers[i].URL
	}

	// Clean up servers when test completes
	for _, server := range servers {
		defer server.Close()
	}

	// Create client with multiple URLs
	client := NewClient("test", urls...)

	// Make 6 calls - should avoid lastSuccessfulURL but server 0 always fails
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		_, err := client.GetIdentity(ctx)
		require.NoError(t, err, "GetIdentity should eventually succeed despite server 0 failures")
	}

	// Verify behavior: failing server should be tried when it's not the last successful URL
	assert.True(t, callCounts[urls[0]] >= 1, "Failing server should have been tried at least once")
	assert.True(t, callCounts[urls[1]] > 0, "Server 1 should have handled some requests")
	assert.True(t, callCounts[urls[2]] > 0, "Server 2 should have handled some requests")

	// Total successful calls should equal the working servers' call counts
	successfulCalls := callCounts[urls[1]] + callCounts[urls[2]]
	assert.Equal(t, 6, successfulCalls, "All 6 calls should eventually succeed")

	// The client should remember the last successful URL and avoid it for throttling protection
	assert.True(t, client.lastSuccessfulURL == urls[1] || client.lastSuccessfulURL == urls[2],
		"Last successful URL should be one of the working servers")
}
