package rpc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"maps"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
)

const testVotePubkey = "Vote111111111111111111111111111111111111111"

func TestDecodeFeatureStatus(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    FeatureStatus
		wantErr bool
	}{
		{name: "pending", data: []byte{0}, want: FeatureStatus{}},
		{name: "activated", data: []byte{1, 0x10, 0x27, 0, 0, 0, 0, 0, 0}, want: FeatureStatus{Activated: true, ActivatedAt: 10000}},
		{name: "empty", data: nil, wantErr: true},
		{name: "truncated slot", data: []byte{1, 0x10, 0x27}, wantErr: true},
		{name: "invalid tag", data: []byte{2, 0, 0, 0, 0, 0, 0, 0, 0}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeFeatureStatus(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeFeatureStatus(%v) error = %v, wantErr %t", tt.data, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("decodeFeatureStatus(%v) = %+v, want %+v", tt.data, got, tt.want)
			}
		})
	}
}

func featureAccountInfo(data []byte) map[string]interface{} {
	return map[string]interface{}{
		"context": map[string]interface{}{"slot": 1},
		"value": map[string]interface{}{
			"data":       []string{base64.StdEncoding.EncodeToString(data), "base64"},
			"executable": false,
			"lamports":   1,
			"owner":      "Feature111111111111111111111111111111111111",
			"rentEpoch":  0,
		},
	}
}

func TestGetFeatureStatus(t *testing.T) {
	feature := solana.MustPublicKeyFromBase58("A1pengvuM6JEcyNuTnMqepBKhwHE3N6PmUrdATGawhJS")
	tests := []struct {
		name        string
		accountInfo interface{}
		want        FeatureStatus
	}{
		{
			name:        "account missing means not activated",
			accountInfo: map[string]interface{}{"context": map[string]interface{}{"slot": 1}, "value": nil},
			want:        FeatureStatus{},
		},
		{
			name:        "activated account",
			accountInfo: featureAccountInfo([]byte{1, 0xe8, 0x03, 0, 0, 0, 0, 0, 0}),
			want:        FeatureStatus{Activated: true, ActivatedAt: 1000},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mockSolanaRPCServer(t, map[string]interface{}{"getAccountInfo": tt.accountInfo})
			got, err := NewClient("test", server.URL).GetFeatureStatus(context.Background(), feature)
			if err != nil {
				t.Fatalf("GetFeatureStatus() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("GetFeatureStatus() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetAgGenesisCert(t *testing.T) {
	t.Run("null before alpenglow", func(t *testing.T) {
		server := mockSolanaRPCServer(t, map[string]interface{}{"getAgGenesisCert": nil})
		cert, err := NewClient("test", server.URL).GetAgGenesisCert(context.Background())
		if err != nil || cert != nil {
			t.Fatalf("GetAgGenesisCert() = %v, %v; want nil, nil", cert, err)
		}
	})

	t.Run("certificate after alpenglow", func(t *testing.T) {
		server := mockSolanaRPCServer(t, map[string]interface{}{
			"getAgGenesisCert": map[string]interface{}{"block": map[string]interface{}{"slot": 4242}},
		})
		cert, err := NewClient("test", server.URL).GetAgGenesisCert(context.Background())
		if err != nil {
			t.Fatalf("GetAgGenesisCert() error = %v", err)
		}
		if cert == nil || cert.Block.Slot != 4242 {
			t.Errorf("GetAgGenesisCert() = %+v, want block slot 4242", cert)
		}
	})

	t.Run("certificate without slot is rejected", func(t *testing.T) {
		server := mockSolanaRPCServer(t, map[string]interface{}{"getAgGenesisCert": map[string]interface{}{}})
		if _, err := NewClient("test", server.URL).GetAgGenesisCert(context.Background()); err == nil {
			t.Error("GetAgGenesisCert() error = nil, want an error for a certificate without a slot")
		}
	})

	t.Run("method not found on every endpoint", func(t *testing.T) {
		first := mockSolanaRPCServer(t, map[string]interface{}{})
		second := mockSolanaRPCServer(t, map[string]interface{}{})
		_, err := NewClient("test", first.URL, second.URL).GetAgGenesisCert(context.Background())
		if !errors.Is(err, ErrMethodNotFound) {
			t.Errorf("GetAgGenesisCert() error = %v, want ErrMethodNotFound", err)
		}
	})

	t.Run("method found on a later endpoint", func(t *testing.T) {
		unsupported := mockSolanaRPCServer(t, map[string]interface{}{})
		supported := mockSolanaRPCServer(t, map[string]interface{}{
			"getAgGenesisCert": map[string]interface{}{"block": map[string]interface{}{"slot": 7}},
		})
		cert, err := NewClient("test", unsupported.URL, supported.URL).GetAgGenesisCert(context.Background())
		if err != nil || cert == nil || cert.Block.Slot != 7 {
			t.Errorf("GetAgGenesisCert() = %+v, %v; want block slot 7", cert, err)
		}
	})

	t.Run("mixed failures are not reported as method not found", func(t *testing.T) {
		unsupported := mockSolanaRPCServer(t, map[string]interface{}{})
		failing := mockFailingServer(t)
		_, err := NewClient("test", unsupported.URL, failing.URL).GetAgGenesisCert(context.Background())
		if err == nil || errors.Is(err, ErrMethodNotFound) {
			t.Errorf("GetAgGenesisCert() error = %v, want a non-ErrMethodNotFound error", err)
		}
	})
}

func voteAccountsWith(votePubkey string, lastVote, stake uint64) map[string]interface{} {
	return map[string]interface{}{
		"current": []map[string]interface{}{{
			"nodePubkey":       "11111111111111111111111111111111",
			"votePubkey":       votePubkey,
			"activatedStake":   stake,
			"epochVoteAccount": true,
			"epochCredits":     []interface{}{},
			"commission":       0,
			"lastVote":         lastVote,
			"rootSlot":         0,
		}},
		"delinquent": []interface{}{},
	}
}

func TestGetVoteLag(t *testing.T) {
	votePubkey := solana.MustPublicKeyFromBase58(testVotePubkey)
	tests := []struct {
		name         string
		voteAccounts interface{}
		slot         uint64
		want         VoteLag
		wantSlots    uint64
	}{
		{
			name:         "found",
			voteAccounts: voteAccountsWith(testVotePubkey, 960, 5000),
			slot:         1000,
			want:         VoteLag{Found: true, LastVote: 960, ActivatedStake: 5000, ProcessedSlot: 1000},
			wantSlots:    40,
		},
		{
			name:         "last vote ahead of slot is zero lag",
			voteAccounts: voteAccountsWith(testVotePubkey, 1001, 5000),
			slot:         1000,
			want:         VoteLag{Found: true, LastVote: 1001, ActivatedStake: 5000, ProcessedSlot: 1000},
			wantSlots:    0,
		},
		{
			name:         "not found",
			voteAccounts: map[string]interface{}{"current": []interface{}{}, "delinquent": []interface{}{}},
			slot:         1000,
			want:         VoteLag{ProcessedSlot: 1000},
			wantSlots:    1000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mockSolanaRPCServer(t, map[string]interface{}{
				"getVoteAccounts": tt.voteAccounts,
				"getSlot":         tt.slot,
			})
			got, err := NewClient("test", server.URL).GetVoteLag(context.Background(), votePubkey)
			if err != nil {
				t.Fatalf("GetVoteLag() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("GetVoteLag() = %+v, want %+v", got, tt.want)
			}
			if got.Slots() != tt.wantSlots {
				t.Errorf("GetVoteLag().Slots() = %d, want %d", got.Slots(), tt.wantSlots)
			}
		})
	}
}

// voteStateData returns the start of a vote account's data in the given version, with the BLS
// pubkey set to key when key is not nil.
func voteStateData(version uint32, key *BLSPubkey) []byte {
	data := make([]byte, blsPubkeySliceLength)
	binary.LittleEndian.PutUint32(data, version)
	if key != nil {
		data[blsPubkeyOptionOffset] = 1
		copy(data[blsPubkeyOptionOffset+1:], key[:])
	}
	return data
}

func TestDecodeVoteBLSPubkey(t *testing.T) {
	key := BLSPubkey{1, 2, 3}
	tests := []struct {
		name   string
		data   []byte
		want   BLSPubkey
		wantOK bool
	}{
		{name: "v4 with key", data: voteStateData(voteStateVersionV4, &key), want: key, wantOK: true},
		{name: "v4 without key", data: voteStateData(voteStateVersionV4, nil)},
		{name: "v3", data: voteStateData(2, &key)},
		{name: "invalid option tag", data: func() []byte {
			data := voteStateData(voteStateVersionV4, &key)
			data[blsPubkeyOptionOffset] = 2
			return data
		}()},
		{name: "truncated", data: voteStateData(voteStateVersionV4, &key)[:blsPubkeyOptionOffset+1]},
		{name: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decodeVoteBLSPubkey(tt.data)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("decodeVoteBLSPubkey() = %x, %t; want %x, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestGetVoteAccountBLSPubkeys(t *testing.T) {
	withKey := solana.MustPublicKeyFromBase58(testVotePubkey)
	withoutKey := solana.MustPublicKeyFromBase58("11111111111111111111111111111111")
	missing := solana.MustPublicKeyFromBase58("Stake11111111111111111111111111111111111111")
	key := BLSPubkey{7}
	account := func(data []byte) map[string]interface{} {
		return map[string]interface{}{
			"data":       []string{base64.StdEncoding.EncodeToString(data), "base64"},
			"executable": false,
			"lamports":   1,
			"owner":      "Vote111111111111111111111111111111111111111",
			"rentEpoch":  0,
		}
	}
	server := mockSolanaRPCServer(t, map[string]interface{}{
		"getMultipleAccounts": map[string]interface{}{
			"context": map[string]interface{}{"slot": 1},
			"value": []interface{}{
				account(voteStateData(voteStateVersionV4, &key)),
				account(voteStateData(voteStateVersionV4, nil)),
				nil,
			},
		},
	})

	got, err := NewClient("test", server.URL).GetVoteAccountBLSPubkeys(context.Background(), []solana.PublicKey{withKey, withoutKey, missing})
	if err != nil {
		t.Fatalf("GetVoteAccountBLSPubkeys() error = %v", err)
	}
	want := map[solana.PublicKey]BLSPubkey{withKey: key}
	if !maps.Equal(got, want) {
		t.Errorf("GetVoteAccountBLSPubkeys() = %v, want %v", got, want)
	}
}

func TestGetVoteAccountBLSPubkeysRejectsShortAnswer(t *testing.T) {
	server := mockSolanaRPCServer(t, map[string]interface{}{
		"getMultipleAccounts": map[string]interface{}{
			"context": map[string]interface{}{"slot": 1},
			"value":   []interface{}{nil},
		},
	})
	pubkeys := []solana.PublicKey{
		solana.MustPublicKeyFromBase58(testVotePubkey),
		solana.MustPublicKeyFromBase58("11111111111111111111111111111111"),
	}

	if _, err := NewClient("test", server.URL).GetVoteAccountBLSPubkeys(context.Background(), pubkeys); err == nil {
		t.Error("GetVoteAccountBLSPubkeys() error = nil, want an error for one account returned for two pubkeys")
	}
}
