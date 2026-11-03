package config

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rpcSecretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rpc secret")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestCluster_FileRPCURLs(t *testing.T) {
	endpoint := "https://user:password@rpc.example.com/path-key?api-key=query-key#fragment"
	for _, localhost := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "localhost"}[localhost], func(t *testing.T) {
			source := rpcSecretFile(t, " \r\n  # comment\r\n\t"+endpoint+"  \r\ninvalid later line\n")
			if localhost {
				source = strings.Replace(source, "file:///", "file://localhost/", 1)
			}
			cluster := validCluster("custom")
			cluster.RPCURLs = []string{endpoint, source, "http://localhost:8899", source}
			require.NoError(t, cluster.Validate())
			assert.Equal(t, []string{endpoint, endpoint, "http://localhost:8899", endpoint}, cluster.RPCURLs)
			assert.Equal(t, map[string]string{endpoint: source}, cluster.RPCURLLabels())
			labels := cluster.RPCURLLabels()
			labels[endpoint] = "modified"
			assert.Equal(t, source, cluster.RPCURLLabels()[endpoint])
			parsed, err := url.Parse(source)
			require.NoError(t, err)
			require.NoError(t, os.Remove(parsed.Path))
			require.NoError(t, cluster.Validate(), "validation must not reread resolved files")
			assert.Equal(t, source, cluster.RPCURLLabels()[endpoint])
		})
	}
}

func TestCluster_FileRPCURLFailures(t *testing.T) {
	cases := []struct{ name, content, message string }{
		{"empty", "", "no non-blank"},
		{"comments only", " \n # comment\n\t# another\n", "no non-blank"},
		{"invalid first eligible line", "bad-secret\nhttps://valid.example.com\n", "HTTP/HTTPS"},
		{"nested file", "file:///run/secrets/other", "HTTP/HTTPS"},
		{"unsupported scheme", "ftp://rpc.example.com/secret", "HTTP/HTTPS"},
		{"missing hostname", "https:///secret", "HTTP/HTTPS"},
		{"malformed URL", "https://rpc.example.com/%bad-secret%", "HTTP/HTTPS"},
		{"oversized URL", "https://rpc.example.com/" + strings.Repeat("x", 65536), "64 KiB"},
		{"oversized comment", "#" + strings.Repeat("x", 65536) + "\nhttps://valid.example.com", "64 KiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := rpcSecretFile(t, tc.content)
			cluster := validCluster("custom")
			cluster.RPCURLs = []string{source}
			err := cluster.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cluster.rpc_urls[0]")
			assert.Contains(t, err.Error(), source)
			assert.Contains(t, err.Error(), tc.message)
			assert.NotContains(t, err.Error(), "bad-secret")
			assert.Equal(t, []string{source}, cluster.RPCURLs)
			assert.Empty(t, cluster.RPCURLLabels())
		})
	}
}

func TestCluster_RPCURLValidation(t *testing.T) {
	for _, source := range []string{
		"file:relative", "file://relative", "file:///tmp/rpc?secret=value",
		"file:///tmp/rpc?", "file:///tmp/rpc#fragment", "file:///tmp/rpc#",
		"file://user:password@localhost/tmp/rpc", "file://remote/tmp/rpc",
		"file:///tmp/%00rpc", "file:///tmp/%XX", "ftp://rpc.example.com",
		"https:///no-host", "https://:8899", "https://rpc.example.com/%XX",
	} {
		t.Run(source, func(t *testing.T) {
			cluster := validCluster("custom")
			cluster.RPCURLs = []string{source}
			err := cluster.Validate()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), source)
		})
	}
}

func TestCluster_FileRPCURLAtomicResolution(t *testing.T) {
	source := rpcSecretFile(t, "https://rpc.example.com/?key=secret")
	cluster := validCluster("custom")
	cluster.RPCURLs = []string{source, "invalid"}
	require.Error(t, cluster.Validate())
	assert.Equal(t, []string{source, "invalid"}, cluster.RPCURLs)
	assert.Empty(t, cluster.RPCURLLabels())
	cluster.RPCURLs = []string{source}
	cluster.RPCTimeoutDuration = -1
	require.Error(t, cluster.Validate())
	assert.Equal(t, []string{source}, cluster.RPCURLs)
	assert.Empty(t, cluster.RPCURLLabels())
}

func TestCluster_RPCSecretFilesystem(t *testing.T) {
	cluster := validCluster("custom")
	path := filepath.Join(t.TempDir(), "missing")
	cluster.RPCURLs = []string{(&url.URL{Scheme: "file", Path: path}).String()}
	require.ErrorContains(t, cluster.Validate(), "cannot stat")
	cluster.RPCURLs = []string{(&url.URL{Scheme: "file", Path: filepath.Dir(path)}).String()}
	require.ErrorContains(t, cluster.Validate(), "regular file")

	source := rpcSecretFile(t, "https://rpc.example.com?key=secret")
	parsed, err := url.Parse(source)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(parsed.Path, path))
	cluster.RPCURLs = []string{(&url.URL{Scheme: "file", Path: path}).String()}
	require.NoError(t, cluster.Validate())

	if os.Geteuid() != 0 {
		require.NoError(t, os.Chmod(parsed.Path, 0000))
		cluster.RPCURLs = []string{source}
		require.ErrorContains(t, cluster.Validate(), "cannot open")
	}
}

func TestConfig_InitializesFileRPCURL(t *testing.T) {
	endpoint := "http://user:password@localhost:8899/path-key?api-key=query-key"
	source := rpcSecretFile(t, endpoint)
	configPath := createTempConfigFile(t)
	t.Cleanup(func() { os.Remove(configPath) })
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	content = bytes.ReplaceAll(content, []byte("https://api.testnet.solana.com"), []byte(source))
	require.NoError(t, os.WriteFile(configPath, content, 0600))
	cfg, err := New(NewConfigParams{})
	require.NoError(t, err)
	var logs bytes.Buffer
	cfg.logger = log.New(&logs)
	require.NoError(t, cfg.LoadFromFile(configPath))
	require.NoError(t, cfg.Initialize())
	assert.Equal(t, []string{endpoint}, cfg.Cluster.RPCURLs)
	assert.Contains(t, logs.String(), source)
	for _, secret := range []string{endpoint, "password", "path-key", "query-key"} {
		assert.NotContains(t, logs.String(), secret)
	}
}
