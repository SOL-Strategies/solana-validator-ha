package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	solanarpc "github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretURLDiagnostics(t *testing.T) {
	for _, mode := range []string{"http forbidden", "rpc failure", "method unavailable", "transport"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify the real secret-bearing URL is used for the request.
				assert.Equal(t, "/path-secret", r.URL.Path)
				assert.Equal(t, "query-secret", r.URL.Query().Get("api-key"))
				user, password, ok := r.BasicAuth()
				assert.True(t, ok)
				assert.Equal(t, "user-secret", user)
				assert.Equal(t, "password-secret", password)
				message := "user-secret password-secret path-secret query-secret response-secret"
				if mode == "http forbidden" {
					http.Error(w, message, http.StatusForbidden)
					return
				}
				var req struct {
					ID any `json:"id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				code := -32000
				if mode == "method unavailable" {
					code = methodNotFoundCode
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": code, "message": message},
				})
			}))
			t.Cleanup(server.Close)
			endpoint := "http://user-secret:password-secret@" + server.Listener.Addr().String() + "/path-secret?api-key=query-secret"
			if mode == "transport" {
				server.Close()
			}
			const label = "file:///run/secrets/rpc_url"
			labels := map[string]string{endpoint: label}
			client := NewClient("test", endpoint).WithURLLabels(labels)
			labels[endpoint] = "modified"
			var logs bytes.Buffer
			client.logger = log.NewWithOptions(&logs, log.Options{Level: log.DebugLevel})
			_, err := client.GetSlot(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), label)
			assert.Contains(t, logs.String(), label)
			for _, secret := range []string{endpoint, "user-secret", "password-secret", "path-secret", "query-secret", "response-secret"} {
				assert.NotContains(t, err.Error(), secret)
				assert.NotContains(t, logs.String(), secret)
			}
			if mode == "method unavailable" {
				assert.ErrorIs(t, err, ErrMethodNotFound)
			}
			if mode == "http forbidden" || mode == "transport" {
				assert.True(t, client.urlCooldowns[endpoint].After(time.Now()))
			}
		})
	}
}

func TestSecretURLFallback(t *testing.T) {
	failing := mockFailingServer(t)
	healthy := mockSolanaRPCServer(t, map[string]interface{}{"getSlot": 42})
	endpoint := failing.URL + "/path-secret?api-key=query-secret"
	client := NewClient("test", endpoint, healthy.URL).WithURLLabels(map[string]string{endpoint: "file:///run/secrets/rpc"})
	slot, err := client.GetSlot(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 42, slot)
	assert.Equal(t, healthy.URL, client.lastSuccessfulURL)
}

func TestSecretURLDiagnosticCategories(t *testing.T) {
	const endpoint = "https://secret.example.com/key"
	client := NewClient("test", endpoint).WithURLLabels(map[string]string{endpoint: "file:///secret"})
	for _, tc := range []struct {
		err      error
		category string
	}{
		{context.Canceled, "context canceled"},
		{context.DeadlineExceeded, "context deadline exceeded"},
		{&jsonrpc.RPCError{Code: methodNotFoundCode, Message: "secret"}, ErrMethodNotFound.Error()},
		{fmt.Errorf("secret response"), "RPC failure"},
	} {
		assert.Equal(t, tc.category, client.diagnosticError(endpoint, tc.err).Error())
		assert.Equal(t, tc.err, client.diagnosticError("https://inline.example.com", tc.err))
	}

	// Raw errors still drive method-not-found detection before sanitizing diagnostics.
	_, err := executeWithRetry(client, context.Background(), rpcOperation[int]{
		name: "test",
		execute: func(*solanarpc.Client, context.Context) (int, error) {
			return 0, &jsonrpc.RPCError{Code: methodNotFoundCode, Message: "secret response"}
		},
	})
	assert.True(t, errors.Is(err, ErrMethodNotFound))
	assert.NotContains(t, err.Error(), "secret response")
}
