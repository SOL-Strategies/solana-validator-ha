package ha

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_FileRPCURL(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, "/path-secret", r.URL.Path)
		assert.Equal(t, "query-secret", r.URL.Query().Get("api-key"))
		var request struct {
			ID any `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": []any{}})
	}))
	t.Cleanup(server.Close)
	endpoint := server.URL + "/path-secret?api-key=query-secret"
	path := filepath.Join(t.TempDir(), "rpc")
	require.NoError(t, os.WriteFile(path, []byte(endpoint), 0600))
	source := (&url.URL{Scheme: "file", Path: path}).String()
	cfg := createTestConfig()
	cfg.Cluster.RPCURLs = []string{source, endpoint}
	cfg.Cluster.SetDefaults()
	require.NoError(t, cfg.Cluster.Validate())
	var logs bytes.Buffer
	previousLogger := log.Default()
	log.SetDefault(log.NewWithOptions(&logs, log.Options{Level: log.DebugLevel}))
	t.Cleanup(func() { log.SetDefault(previousLogger) })
	manager := NewManager(NewManagerOptions{Cfg: cfg, GetPublicIPFunc: mockPublicIPFunc})
	require.NoError(t, manager.initialize())
	assert.Contains(t, logs.String(), source)
	assert.NotContains(t, logs.String(), endpoint)
	assert.NotContains(t, logs.String(), "path-secret")
	assert.NotContains(t, logs.String(), "query-secret")
	manager.gossipState.Refresh()
	assert.Positive(t, requests.Load(), "resolved URL must reach the RPC transport")
	assert.False(t, manager.gossipState.LastRefreshHadRPCError())
}
