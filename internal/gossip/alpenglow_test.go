package gossip

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/consensus"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	solanagorpc "github.com/solana-foundation/solana-go/v2/rpc"
)

const (
	testProcessedSlot = 1000
	testVoteLagLimit  = 32
	// testOtherNodePubkey holds the rest of the network's stake in the mock vote accounts.
	testOtherNodePubkey = "Vote111111111111111111111111111111111111111"
)

var testNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func testAlpenglowConfig() config.Alpenglow {
	return config.Alpenglow{
		VoteLagSlotsThreshold:             testVoteLagLimit,
		WarmupSlots:                       64,
		FinalizationStallDuration:         15 * time.Second,
		NetworkStakeCheckIntervalDuration: time.Minute,
	}
}

// alpenglowVoteAccounts returns a getVoteAccounts result with the active identity current at
// lastVote with activeStake, and the rest of the network holding otherStake, delinquent if
// otherDelinquent is set.
func alpenglowVoteAccounts(lastVote, activeStake, otherStake uint64, otherDelinquent bool) map[string]interface{} {
	account := func(nodePubkey string, stake uint64) map[string]interface{} {
		return map[string]interface{}{
			"nodePubkey":       nodePubkey,
			"votePubkey":       nodePubkey,
			"activatedStake":   stake,
			"epochVoteAccount": true,
			"epochCredits":     []interface{}{},
			"commission":       0,
			"lastVote":         lastVote,
			"rootSlot":         0,
		}
	}
	current := []interface{}{account(testActivePubkey, activeStake)}
	delinquent := []interface{}{}
	if otherDelinquent {
		delinquent = append(delinquent, account(testOtherNodePubkey, otherStake))
	} else {
		current = append(current, account(testOtherNodePubkey, otherStake))
	}
	return map[string]interface{}{"current": current, "delinquent": delinquent}
}

// blsKeyAccounts returns a getMultipleAccounts result with one VoteStateV4 account per key, in
// order. Each key is filled with its byte; 0 means the account has no BLS pubkey.
func blsKeyAccounts(keys ...byte) map[string]interface{} {
	const optionOffset = 4 + 4*32 + 2 + 2 + 8
	values := []interface{}{}
	for _, key := range keys {
		data := make([]byte, optionOffset+1+48)
		binary.LittleEndian.PutUint32(data, 3)
		if key != 0 {
			data[optionOffset] = 1
			for i := optionOffset + 1; i < len(data); i++ {
				data[i] = key
			}
		}
		values = append(values, map[string]interface{}{
			"data":       []string{base64.StdEncoding.EncodeToString(data), "base64"},
			"executable": false,
			"lamports":   1,
			"owner":      "Vote111111111111111111111111111111111111111",
			"rentEpoch":  0,
		})
	}
	return map[string]interface{}{"context": map[string]interface{}{"slot": 1}, "value": values}
}

// newAlpenglowTestState returns a State in the Alpenglow phase (genesis slot 100, long past
// warm-up) whose cluster RPC answers with responses, at the fixed time testNow.
func newAlpenglowTestState(t *testing.T, responses map[string]interface{}) *State {
	t.Helper()
	server := newGossipMockRPCServer(t, responses)
	state := NewState(Options{
		ClusterRPC:   rpc.NewClient("test", server.URL),
		ActivePubkey: testActivePubkey,
		SelfIP:       testSelfIP,
		ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
		Alpenglow:    testAlpenglowConfig(),
	})
	state.SetConsensusView(consensus.View{Phase: consensus.PhaseAlpenglow, GenesisSlot: 100})
	state.now = func() time.Time { return testNow }
	return state
}

// liveFinalization makes the next observed finalized slot count as an advance.
func liveFinalization() finalizationTracker {
	return finalizationTracker{maxSlot: testProcessedSlot - 10, advancedAt: testNow.Add(-time.Second)}
}

// stalledFinalization has seen the test slot already, a minute ago.
func stalledFinalization() finalizationTracker {
	return finalizationTracker{maxSlot: testProcessedSlot, advancedAt: testNow.Add(-time.Minute)}
}

func TestRefresh_AlpenglowVoteEvidence(t *testing.T) {
	tests := []struct {
		name            string
		view            *consensus.View // nil keeps the Alpenglow phase, long past warm-up
		voteAccounts    interface{}     // nil leaves getVoteAccounts unanswered
		finalization    finalizationTracker
		stakeRatioMin   float64
		wantLeaderless  int
		wantReason      string
		wantVeto        string
		wantDelinquent  bool
		wantLagSlots    uint64
		wantLagMeasured bool
		wantVerdict     string
	}{
		{
			name:            "votes landing",
			wantVerdict:     VerdictVoting,
			voteAccounts:    alpenglowVoteAccounts(testProcessedSlot-10, 1000, 9000, false),
			finalization:    liveFinalization(),
			wantLagSlots:    10,
			wantLagMeasured: true,
		},
		{
			name:            "lag at the threshold still counts as voting",
			wantVerdict:     VerdictVoting,
			voteAccounts:    alpenglowVoteAccounts(testProcessedSlot-testVoteLagLimit, 1000, 9000, false),
			finalization:    liveFinalization(),
			wantLagSlots:    testVoteLagLimit,
			wantLagMeasured: true,
		},
		{
			name:            "votes not landing while the cluster finalizes",
			wantVerdict:     VerdictNotVoting,
			voteAccounts:    alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
			finalization:    liveFinalization(),
			wantLeaderless:  1,
			wantReason:      LeaderlessReasonVoteLag,
			wantDelinquent:  true,
			wantLagSlots:    100,
			wantLagMeasured: true,
		},
		{
			name:            "votes not landing while the cluster is stalled",
			wantVerdict:     VetoClusterStalled,
			voteAccounts:    alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
			finalization:    stalledFinalization(),
			wantVeto:        VetoClusterStalled,
			wantLagSlots:    100,
			wantLagMeasured: true,
		},
		{
			name:            "votes not landing while too little stake is current",
			wantVerdict:     VetoClusterStalled,
			voteAccounts:    alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, true),
			finalization:    liveFinalization(),
			stakeRatioMin:   0.85,
			wantVeto:        VetoClusterStalled,
			wantLagSlots:    100,
			wantLagMeasured: true,
		},
		{
			name:            "votes not landing with enough current stake",
			wantVerdict:     VerdictNotVoting,
			voteAccounts:    alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
			finalization:    liveFinalization(),
			stakeRatioMin:   0.85,
			wantLeaderless:  1,
			wantReason:      LeaderlessReasonVoteLag,
			wantDelinquent:  true,
			wantLagSlots:    100,
			wantLagMeasured: true,
		},
		{
			name:         "no vote account is vetoed as excluded",
			wantVerdict:  VetoVoteAccountExcluded,
			voteAccounts: map[string]interface{}{"current": []interface{}{}, "delinquent": []interface{}{}},
			finalization: liveFinalization(),
			wantVeto:     VetoVoteAccountExcluded,
		},
		{
			name:         "unstaked vote account is vetoed as excluded",
			wantVerdict:  VetoVoteAccountExcluded,
			voteAccounts: alpenglowVoteAccounts(testProcessedSlot-100, 0, 9000, false),
			finalization: liveFinalization(),
			wantVeto:     VetoVoteAccountExcluded,
		},
		{
			name:         "vote account RPC error assumes the active is voting",
			wantVerdict:  VerdictUnknown,
			voteAccounts: nil,
			finalization: liveFinalization(),
		},
		{
			name:         "warm-up ignores vote evidence",
			view:         &consensus.View{Phase: consensus.PhaseAlpenglow, GenesisSlot: testProcessedSlot - 10},
			voteAccounts: alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
			finalization: liveFinalization(),
		},
		{
			name:         "migration window ignores vote evidence",
			view:         &consensus.View{Phase: consensus.PhaseMigrating},
			voteAccounts: alpenglowVoteAccounts(testProcessedSlot-500, 1000, 9000, false),
			finalization: liveFinalization(),
		},
		{
			name:         "unknown phase ignores vote evidence",
			view:         &consensus.View{Phase: consensus.PhaseUnknown},
			voteAccounts: alpenglowVoteAccounts(testProcessedSlot-500, 1000, 9000, false),
			finalization: liveFinalization(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := map[string]interface{}{
				"getClusterNodes":     []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
				"getSlot":             testProcessedSlot,
				"getMultipleAccounts": blsKeyAccounts(1, 2),
			}
			if tt.voteAccounts != nil {
				responses["getVoteAccounts"] = tt.voteAccounts
			}
			state := newAlpenglowTestState(t, responses)
			if tt.view != nil {
				state.SetConsensusView(*tt.view)
			}
			state.finalization = tt.finalization
			state.alpenglowCfg.NetworkCurrentStakeRatioMin = tt.stakeRatioMin

			state.Refresh()

			if state.LeaderlessSamplesCount != tt.wantLeaderless {
				t.Errorf("LeaderlessSamplesCount = %d, want %d", state.LeaderlessSamplesCount, tt.wantLeaderless)
			}
			if got := state.LeaderlessReason(); got != tt.wantReason {
				t.Errorf("LeaderlessReason() = %q, want %q", got, tt.wantReason)
			}
			if got := state.VetoReason(); got != tt.wantVeto {
				t.Errorf("VetoReason() = %q, want %q", got, tt.wantVeto)
			}
			if got := state.ActivePeerIsDelinquent(); got != tt.wantDelinquent {
				t.Errorf("ActivePeerIsDelinquent() = %t, want %t", got, tt.wantDelinquent)
			}
			if got := state.AlpenglowVerdict(); got != tt.wantVerdict {
				t.Errorf("AlpenglowVerdict() = %q, want %q", got, tt.wantVerdict)
			}
			if lag, ok := state.ActiveVoteLag(); ok != tt.wantLagMeasured || lag != tt.wantLagSlots {
				t.Errorf("ActiveVoteLag() = %d, %t; want %d, %t", lag, ok, tt.wantLagSlots, tt.wantLagMeasured)
			}
		})
	}
}

func TestRefresh_AlpenglowVoteLagRecordsDelinquencyDetail(t *testing.T) {
	state := newAlpenglowTestState(t, map[string]interface{}{
		"getClusterNodes":     []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
		"getSlot":             testProcessedSlot,
		"getVoteAccounts":     alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
		"getMultipleAccounts": blsKeyAccounts(1, 2),
	})
	state.finalization = liveFinalization()

	state.Refresh()

	want := DelinquencyDetail{LastVoteSlot: testProcessedSlot - 100, CurrentSlot: testProcessedSlot, SlotDistance: 100, AllowedDistance: testVoteLagLimit}
	if got := state.GetDelinquencyDetail(); got == nil || *got != want {
		t.Errorf("GetDelinquencyDetail() = %+v, want %+v", got, want)
	}
}

func TestRefresh_AlpenglowFinalizationLagVetoes(t *testing.T) {
	const finalizedSlot = testProcessedSlot - 100
	// answers getSlot by commitment, so finalization can trail the processed slot
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string           `json:"method"`
			Params []map[string]any `json:"params"`
			ID     any              `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		results := map[string]interface{}{
			"getClusterNodes": []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
			"getVoteAccounts": alpenglowVoteAccounts(finalizedSlot, 1000, 9000, false),
			"getSlot":         testProcessedSlot,
		}
		if req.Method == "getSlot" && len(req.Params) > 0 && req.Params[0]["commitment"] == "finalized" {
			results["getSlot"] = finalizedSlot
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": results[req.Method]}) //nolint:errcheck
	}))
	t.Cleanup(server.Close)
	state := newAlpenglowTestState(t, map[string]interface{}{})
	state.clusterRPC = rpc.NewClient("test", server.URL)
	// finalization is still advancing, just far behind the tip
	state.finalization = finalizationTracker{maxSlot: finalizedSlot - 1, advancedAt: testNow.Add(-time.Second)}

	state.Refresh()

	if got := state.VetoReason(); got != VetoClusterStalled {
		t.Errorf("VetoReason() = %q, want %q", got, VetoClusterStalled)
	}
	if state.LeaderlessSamplesCount != 0 {
		t.Errorf("LeaderlessSamplesCount = %d, want 0", state.LeaderlessSamplesCount)
	}
}

func TestRefresh_AlpenglowVoterSet(t *testing.T) {
	// the active and the rest of the network, with the given BLS keys, identities and epoch flag
	voteAccounts := func(otherNodePubkey string, activeInEpoch bool) map[string]interface{} {
		accounts := alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false)
		current := accounts["current"].([]interface{})
		current[0].(map[string]interface{})["epochVoteAccount"] = activeInEpoch
		current[1].(map[string]interface{})["nodePubkey"] = otherNodePubkey
		return accounts
	}
	tests := []struct {
		name         string
		voteAccounts map[string]interface{}
		blsAccounts  interface{} // nil leaves getMultipleAccounts unanswered
		wantVerdict  string
	}{
		{
			name:         "member is judged by its votes",
			voteAccounts: voteAccounts(testOtherNodePubkey, true),
			blsAccounts:  blsKeyAccounts(1, 2),
			wantVerdict:  VerdictNotVoting,
		},
		{
			name:         "no BLS pubkey",
			voteAccounts: voteAccounts(testOtherNodePubkey, true),
			blsAccounts:  blsKeyAccounts(0, 2),
			wantVerdict:  VetoVoteAccountExcluded,
		},
		{
			name:         "BLS pubkey shared with another staked account",
			voteAccounts: voteAccounts(testOtherNodePubkey, true),
			blsAccounts:  blsKeyAccounts(1, 1),
			wantVerdict:  VetoVoteAccountExcluded,
		},
		{
			name:         "identity shared with another staked account",
			voteAccounts: voteAccounts(testActivePubkey, true),
			blsAccounts:  blsKeyAccounts(1, 2),
			wantVerdict:  VetoVoteAccountExcluded,
		},
		{
			name:         "no stake in the current epoch",
			voteAccounts: voteAccounts(testOtherNodePubkey, false),
			blsAccounts:  blsKeyAccounts(2),
			wantVerdict:  VetoVoteAccountExcluded,
		},
		{
			name:         "BLS pubkeys unavailable falls back to the votes",
			voteAccounts: voteAccounts(testOtherNodePubkey, true),
			wantVerdict:  VerdictNotVoting,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := map[string]interface{}{
				"getClusterNodes": []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
				"getSlot":         testProcessedSlot,
				"getVoteAccounts": tt.voteAccounts,
			}
			if tt.blsAccounts != nil {
				responses["getMultipleAccounts"] = tt.blsAccounts
			}
			state := newAlpenglowTestState(t, responses)
			state.finalization = liveFinalization()

			state.Refresh()

			if got := state.AlpenglowVerdict(); got != tt.wantVerdict {
				t.Errorf("AlpenglowVerdict() = %q, want %q", got, tt.wantVerdict)
			}
			wantLeaderless := 0
			if tt.wantVerdict == VerdictNotVoting {
				wantLeaderless = 1
			}
			if state.LeaderlessSamplesCount != wantLeaderless {
				t.Errorf("LeaderlessSamplesCount = %d, want %d", state.LeaderlessSamplesCount, wantLeaderless)
			}
		})
	}
}

func TestRefresh_AlpenglowVoterSetIsCached(t *testing.T) {
	state := newAlpenglowTestState(t, map[string]interface{}{
		"getClusterNodes":     []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
		"getSlot":             testProcessedSlot,
		"getVoteAccounts":     alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
		"getMultipleAccounts": blsKeyAccounts(0, 2),
	})
	state.finalization = liveFinalization()
	state.Refresh()
	if got := state.AlpenglowVerdict(); got != VetoVoteAccountExcluded {
		t.Fatalf("first AlpenglowVerdict() = %q, want %q", got, VetoVoteAccountExcluded)
	}

	// the key is set now, but the cached answer is reused until the recheck interval passes
	member := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes":     []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
		"getSlot":             testProcessedSlot,
		"getVoteAccounts":     alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
		"getMultipleAccounts": blsKeyAccounts(1, 2),
	})
	state.clusterRPC = rpc.NewClient("test", member.URL)
	state.Refresh()
	if got := state.AlpenglowVerdict(); got != VetoVoteAccountExcluded {
		t.Errorf("cached AlpenglowVerdict() = %q, want %q", got, VetoVoteAccountExcluded)
	}

	later := testNow.Add(voterSetRecheckInterval)
	state.now = func() time.Time { return later }
	state.finalization = finalizationTracker{maxSlot: testProcessedSlot - 10, advancedAt: later.Add(-time.Second)}
	state.Refresh()
	if got := state.AlpenglowVerdict(); got != VerdictNotVoting {
		t.Errorf("rechecked AlpenglowVerdict() = %q, want %q", got, VerdictNotVoting)
	}
}

func TestRefresh_AlpenglowVoterSetRecheckedAfterVotesLand(t *testing.T) {
	server := func(lastVote uint64, blsAccounts map[string]interface{}) *rpc.Client {
		return rpc.NewClient("test", newGossipMockRPCServer(t, map[string]interface{}{
			"getClusterNodes":     []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
			"getSlot":             testProcessedSlot,
			"getVoteAccounts":     alpenglowVoteAccounts(lastVote, 1000, 9000, false),
			"getMultipleAccounts": blsAccounts,
		}).URL)
	}
	state := newAlpenglowTestState(t, map[string]interface{}{})
	state.finalization = liveFinalization()

	steps := []struct {
		name        string
		client      *rpc.Client
		wantVerdict string
	}{
		{name: "member, not voting", client: server(testProcessedSlot-100, blsKeyAccounts(1, 2)), wantVerdict: VerdictNotVoting},
		{name: "votes land", client: server(testProcessedSlot-10, blsKeyAccounts(1, 2)), wantVerdict: VerdictVoting},
		{name: "key removed, not voting", client: server(testProcessedSlot-100, blsKeyAccounts(0, 2)), wantVerdict: VetoVoteAccountExcluded},
	}
	for _, step := range steps {
		state.clusterRPC = step.client
		state.Refresh()
		if got := state.AlpenglowVerdict(); got != step.wantVerdict {
			t.Errorf("%s: AlpenglowVerdict() = %q, want %q", step.name, got, step.wantVerdict)
		}
	}
}

func TestRefresh_AlpenglowFailoverIneffective(t *testing.T) {
	const otherDeclaredIP = "192.168.1.102"
	server := func(ip string, lastVote uint64) *rpc.Client {
		return rpc.NewClient("test", newGossipMockRPCServer(t, map[string]interface{}{
			"getClusterNodes":     []interface{}{gossipClusterNode(testActivePubkey, ip)},
			"getSlot":             testProcessedSlot,
			"getVoteAccounts":     alpenglowVoteAccounts(lastVote, 1000, 9000, false),
			"getMultipleAccounts": blsKeyAccounts(1, 2),
		}).URL)
	}
	state := newAlpenglowTestState(t, map[string]interface{}{})
	state.configPeers = config.Peers{
		"peer1": {IP: testDeclaredIP, Name: "peer1"},
		"peer2": {IP: otherDeclaredIP, Name: "peer2"},
	}
	state.finalization = liveFinalization()

	steps := []struct {
		name        string
		client      *rpc.Client
		wantVerdict string
	}{
		{name: "first holder not voting", client: server(testDeclaredIP, testProcessedSlot-100), wantVerdict: VerdictNotVoting},
		{name: "first holder still not voting", client: server(testDeclaredIP, testProcessedSlot-100), wantVerdict: VerdictNotVoting},
		{name: "identity moved and still not voting", client: server(otherDeclaredIP, testProcessedSlot-100), wantVerdict: VetoFailoverIneffective},
		{name: "back on the first holder, still not voting", client: server(testDeclaredIP, testProcessedSlot-100), wantVerdict: VetoFailoverIneffective},
		{name: "votes land again", client: server(testDeclaredIP, testProcessedSlot-10), wantVerdict: VerdictVoting},
		{name: "a new spell starts afresh", client: server(otherDeclaredIP, testProcessedSlot-100), wantVerdict: VerdictNotVoting},
	}
	for _, step := range steps {
		state.clusterRPC = step.client
		state.Refresh()
		if got := state.AlpenglowVerdict(); got != step.wantVerdict {
			t.Errorf("%s: AlpenglowVerdict() = %q, want %q", step.name, got, step.wantVerdict)
		}
		wantVeto := ""
		if step.wantVerdict == VetoFailoverIneffective {
			wantVeto = VetoFailoverIneffective
		}
		if got := state.VetoReason(); got != wantVeto {
			t.Errorf("%s: VetoReason() = %q, want %q", step.name, got, wantVeto)
		}
	}
}

func TestRefresh_LeaderlessStreakIsVoteOnly(t *testing.T) {
	notVoting := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes":     []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
		"getSlot":             testProcessedSlot,
		"getVoteAccounts":     alpenglowVoteAccounts(testProcessedSlot-100, 1000, 9000, false),
		"getMultipleAccounts": blsKeyAccounts(1, 2),
	})
	activeGone := newGossipMockRPCServer(t, map[string]interface{}{
		"getClusterNodes": []interface{}{},
		"getSlot":         testProcessedSlot,
	})
	state := newAlpenglowTestState(t, map[string]interface{}{})
	state.finalization = liveFinalization()

	state.clusterRPC = rpc.NewClient("test", notVoting.URL)
	state.Refresh()
	state.Refresh()
	if !state.LeaderlessStreakIsVoteOnly() {
		t.Fatalf("after two vote-lag samples, LeaderlessStreakIsVoteOnly() = false, want true (count %d)", state.LeaderlessSamplesCount)
	}

	state.clusterRPC = rpc.NewClient("test", activeGone.URL)
	state.Refresh()
	if state.LeaderlessStreakIsVoteOnly() {
		t.Errorf("after a gossip-absent sample, LeaderlessStreakIsVoteOnly() = true, want false")
	}
	if got := state.LeaderlessReason(); got != LeaderlessReasonGossipAbsent {
		t.Errorf("LeaderlessReason() = %q, want %q", got, LeaderlessReasonGossipAbsent)
	}
	if state.LeaderlessSamplesCount != 3 {
		t.Errorf("LeaderlessSamplesCount = %d, want 3", state.LeaderlessSamplesCount)
	}
}

func TestRefresh_TowerLeaderlessReasons(t *testing.T) {
	tests := []struct {
		name         string
		voteAccounts interface{}
		wantReason   string
	}{
		{
			name:         "delinquent",
			voteAccounts: delinquentVoteAccountsResult([]string{testActivePubkey}, 100),
			wantReason:   LeaderlessReasonDelinquent,
		},
		{
			name:         "no vote account",
			voteAccounts: votingVoteAccountsResult(nil),
			wantReason:   LeaderlessReasonNoVoteAccount,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newGossipMockRPCServer(t, map[string]interface{}{
				"getClusterNodes": []interface{}{gossipClusterNode(testActivePubkey, testDeclaredIP)},
				"getVoteAccounts": tt.voteAccounts,
				"getBalance":      balanceResult(10_000_000),
				"getSlot":         500,
			})
			state := NewState(Options{
				ClusterRPC:   rpc.NewClient("test", server.URL),
				ActivePubkey: testActivePubkey,
				SelfIP:       testSelfIP,
				ConfigPeers:  config.Peers{"peer1": {IP: testDeclaredIP, Name: "peer1"}},
			})

			state.Refresh()

			if got := state.LeaderlessReason(); got != tt.wantReason {
				t.Errorf("LeaderlessReason() = %q, want %q", got, tt.wantReason)
			}
			if !state.LeaderlessStreakIsVoteOnly() {
				t.Error("LeaderlessStreakIsVoteOnly() = false, want true")
			}
		})
	}
}

func TestFinalizationTracker(t *testing.T) {
	const stallAfter = 15 * time.Second
	var tracker finalizationTracker

	tracker.observe(100, testNow)
	if tracker.isLive(testNow, stallAfter) {
		t.Fatal("isLive() after the first observation = true, want false: one sample shows no advance")
	}

	tracker.observe(90, testNow.Add(time.Second))
	if tracker.maxSlot != 100 {
		t.Fatalf("maxSlot after a lagging RPC = %d, want 100", tracker.maxSlot)
	}

	tracker.observe(110, testNow.Add(2*time.Second))
	if !tracker.isLive(testNow.Add(2*time.Second+stallAfter), stallAfter) {
		t.Error("isLive() at exactly the stall duration after an advance = false, want true")
	}
	if tracker.isLive(testNow.Add(3*time.Second+stallAfter), stallAfter) {
		t.Error("isLive() past the stall duration = true, want false")
	}
}

func TestCurrentStakeRatio(t *testing.T) {
	accounts := func(current, delinquent []uint64) *solanagorpc.GetVoteAccountsResult {
		result := &solanagorpc.GetVoteAccountsResult{}
		for _, stake := range current {
			result.Current = append(result.Current, solanagorpc.VoteAccountsResult{ActivatedStake: stake})
		}
		for _, stake := range delinquent {
			result.Delinquent = append(result.Delinquent, solanagorpc.VoteAccountsResult{ActivatedStake: stake})
		}
		return result
	}
	tests := []struct {
		name       string
		current    []uint64
		delinquent []uint64
		wantRatio  float64
		wantOK     bool
	}{
		{name: "all current", current: []uint64{30, 70}, wantRatio: 1, wantOK: true},
		{name: "mixed", current: []uint64{90}, delinquent: []uint64{10}, wantRatio: 0.9, wantOK: true},
		{name: "no stake", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio, ok := currentStakeRatio(accounts(tt.current, tt.delinquent))
			if ratio != tt.wantRatio || ok != tt.wantOK {
				t.Errorf("currentStakeRatio(current=%v delinquent=%v) = %g, %t; want %g, %t", tt.current, tt.delinquent, ratio, ok, tt.wantRatio, tt.wantOK)
			}
		})
	}
}

func TestNetworkStakeGateClosesOnStaleRatio(t *testing.T) {
	state := newAlpenglowTestState(t, map[string]interface{}{})
	state.alpenglowCfg.NetworkCurrentStakeRatioMin = 0.85
	state.stakeRatio = stakeRatioSample{value: 0.99, computedAt: testNow.Add(-stakeRatioStaleAfterIntervals*time.Minute - time.Second)}

	if _, open := state.networkStakeGateOpen(testNow); open {
		t.Error("networkStakeGateOpen() with a stale ratio = open, want closed")
	}
}
