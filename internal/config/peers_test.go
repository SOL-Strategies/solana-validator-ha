package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPeers_Add(t *testing.T) {
	peers := &Peers{}

	peer1 := Peer{Name: "validator-1", IP: "192.168.1.10"}
	peer2 := Peer{Name: "validator-2", IP: "192.168.1.11"}

	peers.Add(peer1)
	peers.Add(peer2)

	assert.Equal(t, peer1, (*peers)["validator-1"])
	assert.Equal(t, peer2, (*peers)["validator-2"])
	assert.Len(t, *peers, 2)
}

func TestPeers_String(t *testing.T) {
	peers := &Peers{
		"validator-1": {Name: "validator-1", IP: "192.168.1.10"},
		"validator-2": {Name: "validator-2", IP: "192.168.1.11"},
	}

	result := peers.String()
	// Map iteration order is not guaranteed, so we need to check that both entries are present
	assert.Contains(t, result, "validator-1:192.168.1.10")
	assert.Contains(t, result, "validator-2:192.168.1.11")
	assert.True(t, strings.HasPrefix(result, "["))
	assert.True(t, strings.HasSuffix(result, "]"))

	// Test with empty peers
	emptyPeers := &Peers{}
	result = emptyPeers.String()
	assert.Equal(t, "[]", result)
}

func TestPeers_GetIPs(t *testing.T) {
	peers := &Peers{
		"validator-1": {Name: "validator-1", IP: "192.168.1.10"},
		"validator-2": {Name: "validator-2", IP: "192.168.1.11"},
		"validator-3": {Name: "validator-3", IP: "192.168.1.12"},
	}

	ips := peers.GetIPs()
	expected := []string{"192.168.1.10", "192.168.1.11", "192.168.1.12"}

	// Note: map iteration order is not guaranteed, so we need to check that all IPs are present
	assert.Len(t, ips, 3)
	for _, expectedIP := range expected {
		assert.Contains(t, ips, expectedIP)
	}

	// Test with empty peers
	emptyPeers := &Peers{}
	ips = emptyPeers.GetIPs()
	assert.Len(t, ips, 0)
}
