package gossip

import (
	"testing"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	solana "github.com/solana-foundation/solana-go/v2"
	solanarpc "github.com/solana-foundation/solana-go/v2/rpc"
)

func TestBusyPeerWinsOverStalePassiveGossip(t *testing.T) {
	busy := solana.NewWallet().PublicKey()
	passive := solana.NewWallet().PublicKey()
	const selfIP = "192.0.2.1"
	const backupIP = "192.0.2.2"
	backupGossip := backupIP + ":8001"
	occupied := &solanarpc.GetClusterNodesResult{Gossip: &backupGossip, Pubkey: busy}
	stale := &solanarpc.GetClusterNodesResult{Gossip: &backupGossip, Pubkey: passive}

	for _, nodes := range [][]*solanarpc.GetClusterNodesResult{{occupied, stale}, {stale, occupied}} {
		state := NewState(Options{
			ActivePubkey: solana.NewWallet().PublicKey().String(),
			SelfIP:       selfIP,
			ConfigPeers: config.Peers{
				"self":   {IP: selfIP},
				"backup": {IP: backupIP},
			},
			KnownActiveIdentities: map[string]string{busy.String(): "other"},
		})
		state.RefreshFromClusterNodes(nodes, nil)
		if peer := state.GetPeerStates()["backup"]; peer.BusyProfile != "other" || peer.Pubkey != busy.String() {
			t.Fatalf("backup from gossip = %+v, want busy with other", peer)
		}
		if state.HasPeers(selfIP) {
			t.Fatal("busy peer was counted as an available backup")
		}
	}
}
