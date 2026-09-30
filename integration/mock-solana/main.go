package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	activePubkey     = "ArkzFExXXHaA6izkNhTJJ5zpXdQpynffjfRMJu4Yq6H"
	activeVotePubkey = "ArkzFExXXHaA6izkNhTJJ5zpXdQpynffjfRMJu4Yq6H"
	// networkPubkey is a vote account holding the rest of the network's stake, so the HA
	// client's network stake ratio stays high unless a scenario says otherwise.
	networkPubkey = "Vote111111111111111111111111111111111111111"

	// The slot clock starts at startSlot and advances one slot per slotDuration.
	startSlot    = uint64(1000)
	slotDuration = 400 * time.Millisecond
	// healthyVoteLag is how far a voting validator's lastVote trails the processed slot.
	healthyVoteLag = uint64(2)
	// delinquentSlotDistance matches Agave's getVoteAccounts default.
	delinquentSlotDistance = uint64(128)
	// towerFinalizedLag and alpenglowFinalizedLag are how far the finalized slot trails processed.
	towerFinalizedLag     = uint64(32)
	alpenglowFinalizedLag = uint64(2)

	phaseTower     = "tower"
	phaseMigrating = "migrating"
	phaseAlpenglow = "alpenglow"
)

// validatorMeta holds the fixed metadata for each known validator in the test network.
var validatorMeta = map[string]struct {
	dockerIP      string
	publicIP      string
	passivePubkey string
}{
	"validator-1": {"172.20.0.10", "10.0.0.100", "AP4JyZq2vuN4u64FGFHTwdG11xHu1vZWVYQj21MPLrnw"},
	"validator-2": {"172.20.0.11", "10.0.0.101", "DJ7w4p8Ve7qdSAmkpA3sviSbsd1HPUxd43x7MTH72JHT"},
	"validator-3": {"172.20.0.12", "10.0.0.102", "5dXttfrjFEEExmZhVmVAdw2LzepNAhFYJTUgPCDk8CYD"},
}

// MockSolanaServer simulates a Solana RPC node and exposes a control API for test scenarios.
type MockSolanaServer struct {
	mu               sync.RWMutex
	activeValidator  string          // which validator currently holds the active identity
	disconnected     map[string]bool // validators removed from gossip
	unhealthy        map[string]bool // validators whose local health check returns unhealthy
	callingValidator string          // populated from ?validator= query param per request
	startedAt        time.Time       // origin of the slot clock

	// Consensus state. The phase survives reset, like a real cluster's does. The activation and
	// genesis slots are kept once set, so re-entering a phase reuses them.
	phase              string
	featureActivatedAt uint64 // 0 until the phase first leaves tower
	genesisSlot        uint64 // 0 until the phase first becomes alpenglow
	// stalledAt is the processed slot at which finalization stopped; 0 while finalizing.
	stalledAt uint64
	// voteLag is extra lag added to a validator's lastVote while it holds the active identity.
	voteLag map[string]uint64
	// voteAccountExcluded hides the active vote account, as if it were left out of the voter set.
	voteAccountExcluded bool
	// localGenesis overrides the genesis slot a validator's local RPC reports; 0 means none.
	localGenesis map[string]uint64
}

func NewMockSolanaServer() *MockSolanaServer {
	return &MockSolanaServer{
		activeValidator: os.Getenv("ACTIVE_VALIDATOR"),
		disconnected:    make(map[string]bool),
		unhealthy:       make(map[string]bool),
		startedAt:       time.Now(),
		phase:           phaseTower,
		voteLag:         make(map[string]uint64),
		localGenesis:    make(map[string]uint64),
	}
}

// ── RPC types ────────────────────────────────────────────────────────────────

type ClusterNode struct {
	Pubkey       string `json:"pubkey"`
	Gossip       string `json:"gossip"`
	TPU          string `json:"tpu"`
	RPC          string `json:"rpc"`
	Version      string `json:"version"`
	FeatureSet   int    `json:"featureSet"`
	ShredVersion int    `json:"shredVersion"`
}

type VoteAccount struct {
	VotePubkey       string     `json:"votePubkey"`
	NodePubkey       string     `json:"nodePubkey"`
	ActivatedStake   uint64     `json:"activatedStake"`
	EpochVoteAccount bool       `json:"epochVoteAccount"`
	Commission       uint8      `json:"commission"`
	LastVote         uint64     `json:"lastVote"`
	EpochCredits     [][]uint64 `json:"epochCredits"`
	RootSlot         uint64     `json:"rootSlot"`
}

type VoteAccountsResult struct {
	Current    []VoteAccount `json:"current"`
	Delinquent []VoteAccount `json:"delinquent"`
}

type AccountInfoResult struct {
	Context struct {
		Slot uint64 `json:"slot"`
	} `json:"context"`
	Value *AccountInfo `json:"value"`
}

type AccountInfo struct {
	Data       []string `json:"data"`
	Executable bool     `json:"executable"`
	Lamports   uint64   `json:"lamports"`
	Owner      string   `json:"owner"`
	RentEpoch  uint64   `json:"rentEpoch"`
}

type BalanceResult struct {
	Context struct {
		Slot uint64 `json:"slot"`
	} `json:"context"`
	Value uint64 `json:"value"`
}

// ── Control types ─────────────────────────────────────────────────────────────

// ControlAction is the unified control request accepted by the /action endpoint.
// Actions: set_active, set_passive, disconnect, reconnect, set_unhealthy, set_healthy, reset,
// set_phase, set_vote_lag, stall_finalization, resume_finalization, set_local_genesis,
// exclude_vote_account, include_vote_account.
type ControlAction struct {
	Action string `json:"action"`
	Target string `json:"target"` // validator name; empty for reset/set_active with no target
	// Phase is used by set_phase: tower, migrating or alpenglow.
	Phase string `json:"phase,omitempty"`
	// Value is used by set_vote_lag (slots) and set_local_genesis (slot, 0 for none).
	Value uint64 `json:"value,omitempty"`
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

func (s *MockSolanaServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	method, _ := req["method"].(string)

	// Track which validator is making the call via ?validator= query param.
	// Used for per-validator responses (getIdentity, getHealth).
	if v := r.URL.Query().Get("validator"); v != "" {
		s.mu.Lock()
		s.callingValidator = v
		s.mu.Unlock()
	}

	var result any
	switch method {
	case "getClusterNodes":
		result = s.getClusterNodes()
	case "getIdentity":
		result = s.getIdentity()
	case "getHealth":
		result = s.getHealth()
	case "getSlot":
		result = s.getSlot(commitmentParam(req["params"]))
	case "getVoteAccounts":
		result = s.getVoteAccounts()
	case "getBalance":
		result = s.getBalance()
	case "getAccountInfo":
		result = s.getFeatureAccountInfo()
	case "getAgGenesisCert":
		// Local validator RPC URLs carry ?validator=<name>; cluster RPC URLs do not.
		result = s.getAgGenesisCert(r.URL.Query().Get("validator"))
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"error": map[string]any{
				"code":    -32601,
				"message": fmt.Sprintf("method not found: %s", method),
			},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      req["id"],
		"result":  result,
	})
}

// commitmentParam returns the commitment of a request whose first param is a config object,
// e.g. getSlot [{"commitment":"finalized"}]. It defaults to processed.
func commitmentParam(params any) string {
	list, _ := params.([]any)
	if len(list) == 0 {
		return "processed"
	}
	config, _ := list[0].(map[string]any)
	if commitment, ok := config["commitment"].(string); ok {
		return commitment
	}
	return "processed"
}

// handleAction is the unified control endpoint used by test scenarios and validator commands.
func (s *MockSolanaServer) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var action ControlAction
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	switch action.Action {
	case "set_active":
		s.activeValidator = action.Target
		log.Printf("[control] set_active: %q", action.Target)

	case "set_passive":
		// Only clear the active validator if this specific validator is currently active.
		// Idempotent: if it was already passive, this is a no-op.
		if s.activeValidator == action.Target {
			s.activeValidator = ""
			log.Printf("[control] set_passive: %q (was active, cleared)", action.Target)
		} else {
			log.Printf("[control] set_passive: %q (already passive, no-op)", action.Target)
		}

	case "disconnect":
		s.disconnected[action.Target] = true
		// If the disconnected validator was active, clear the active slot —
		// simulating the reality that an offline node is no longer serving blocks.
		if s.activeValidator == action.Target {
			s.activeValidator = ""
			log.Printf("[control] disconnect: %q (was active, cleared)", action.Target)
		} else {
			log.Printf("[control] disconnect: %q", action.Target)
		}

	case "reconnect":
		delete(s.disconnected, action.Target)
		log.Printf("[control] reconnect: %q", action.Target)

	case "set_unhealthy":
		s.unhealthy[action.Target] = true
		log.Printf("[control] set_unhealthy: %q", action.Target)

	case "set_healthy":
		delete(s.unhealthy, action.Target)
		log.Printf("[control] set_healthy: %q", action.Target)

	case "reset":
		// Reconnect all validators, clear all faults, set initial active. The consensus phase is
		// kept: the HA clients treat Alpenglow as final, so scenarios must not rely on going back.
		s.disconnected = make(map[string]bool)
		s.unhealthy = make(map[string]bool)
		s.voteLag = make(map[string]uint64)
		s.localGenesis = make(map[string]uint64)
		s.voteAccountExcluded = false
		s.stalledAt = 0
		s.activeValidator = action.Target
		log.Printf("[control] reset: active=%q phase=%q", action.Target, s.phase)

	case "set_phase":
		if err := s.setPhase(action.Phase); err != nil {
			s.mu.Unlock()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[control] set_phase: %q feature_activated_at=%d genesis_slot=%d", s.phase, s.featureActivatedAt, s.genesisSlot)

	case "set_vote_lag":
		s.voteLag[action.Target] = action.Value
		log.Printf("[control] set_vote_lag: %q lag=%d", action.Target, action.Value)

	case "stall_finalization":
		s.stalledAt = s.processedSlot()
		log.Printf("[control] stall_finalization: at slot %d", s.stalledAt)

	case "resume_finalization":
		s.stalledAt = 0
		log.Printf("[control] resume_finalization")

	case "set_local_genesis":
		s.localGenesis[action.Target] = action.Value
		log.Printf("[control] set_local_genesis: %q slot=%d", action.Target, action.Value)

	case "exclude_vote_account":
		s.voteAccountExcluded = true
		log.Printf("[control] exclude_vote_account")

	case "include_vote_account":
		s.voteAccountExcluded = false
		log.Printf("[control] include_vote_account")

	default:
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("unknown action: %s", action.Action), http.StatusBadRequest)
		return
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handlePublicIP returns a stable public IP for each validator based on their Docker network IP.
// This lets HA managers discover their own public IP during initialisation.
// A ?validator=<name> query param overrides IP-based detection — used for local/demo runs.
func (s *MockSolanaServer) handlePublicIP(w http.ResponseWriter, r *http.Request) {
	// Allow demo/local runs to identify themselves by name instead of Docker IP.
	if v := r.URL.Query().Get("validator"); v != "" {
		if meta, ok := validatorMeta[v]; ok {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(meta.publicIP))
			return
		}
	}

	clientIP := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		clientIP = fwd
	}
	if i := strings.LastIndex(clientIP, ":"); i != -1 {
		clientIP = clientIP[:i]
	}

	for _, meta := range validatorMeta {
		if meta.dockerIP == clientIP {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(meta.publicIP))
			return
		}
	}

	// Fallback for unknown callers (e.g. the orchestrator running health checks)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("10.0.0.199"))
}

// ── RPC method implementations ────────────────────────────────────────────────

// getClusterNodes returns gossip entries for all connected validators.
// Gossip addresses use public IPs so that the HA manager's peer-IP matching works correctly.
func (s *MockSolanaServer) getClusterNodes() []ClusterNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var nodes []ClusterNode
	for name, meta := range validatorMeta {
		if s.disconnected[name] {
			continue
		}

		pubkey := meta.passivePubkey
		if name == s.activeValidator {
			pubkey = activePubkey
		}

		nodes = append(nodes, ClusterNode{
			Pubkey:       pubkey,
			Gossip:       fmt.Sprintf("%s:8001", meta.publicIP),
			TPU:          fmt.Sprintf("%s:8003", meta.publicIP),
			RPC:          fmt.Sprintf("%s:8899", meta.publicIP),
			Version:      "2.0.0",
			FeatureSet:   123456789,
			ShredVersion: 12345,
		})
	}
	return nodes
}

// getIdentity returns the identity pubkey for the calling validator.
// The ?validator= query param identifies the caller.
func (s *MockSolanaServer) getIdentity() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.callingValidator == s.activeValidator && s.activeValidator != "" {
		return map[string]any{"identity": activePubkey}
	}

	// Return the passive pubkey for this validator
	if meta, ok := validatorMeta[s.callingValidator]; ok {
		return map[string]any{"identity": meta.passivePubkey}
	}

	// Fallback
	return map[string]any{"identity": validatorMeta["validator-1"].passivePubkey}
}

// getHealth returns "ok" unless the calling validator has been marked unhealthy.
func (s *MockSolanaServer) getHealth() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.unhealthy[s.callingValidator] {
		return "behind"
	}
	return "ok"
}

// getVoteAccounts returns the active identity's vote account, current or delinquent depending on
// its vote lag, plus a vote account holding the rest of the network's stake. The active account
// is omitted when no validator is active or the account is excluded from the voter set.
func (s *MockSolanaServer) getVoteAccounts() VoteAccountsResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	processed := s.processedSlot()
	result := VoteAccountsResult{
		Current:    []VoteAccount{s.voteAccount(networkPubkey, 9_000_000_000, s.lastVote(processed, 0))},
		Delinquent: []VoteAccount{},
	}
	if s.activeValidator == "" || s.disconnected[s.activeValidator] || s.voteAccountExcluded {
		return result
	}

	lastVote := s.lastVote(processed, s.voteLag[s.activeValidator])
	active := s.voteAccount(activeVotePubkey, 1_000_000_000, lastVote)
	active.NodePubkey = activePubkey
	if processed-lastVote >= delinquentSlotDistance {
		result.Delinquent = append(result.Delinquent, active)
	} else {
		result.Current = append(result.Current, active)
	}
	return result
}

func (s *MockSolanaServer) voteAccount(pubkey string, stake, lastVote uint64) VoteAccount {
	return VoteAccount{
		VotePubkey:       pubkey,
		NodePubkey:       pubkey,
		ActivatedStake:   stake,
		EpochVoteAccount: true,
		LastVote:         lastVote,
		EpochCredits:     [][]uint64{},
		RootSlot:         lastVote - min(lastVote, towerFinalizedLag),
	}
}

// getBalance returns a high lamport balance — well above the rent-exempt minimum (890,880).
// This prevents the delinquency-due-to-low-balance code path from triggering.
func (s *MockSolanaServer) getBalance() BalanceResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result BalanceResult
	result.Context.Slot = s.processedSlot()
	result.Value = 10_000_000_000
	return result
}

// ── Consensus simulation ──────────────────────────────────────────────────────
// Callers hold s.mu.

// processedSlot is the slot clock: it advances one slot per slotDuration.
func (s *MockSolanaServer) processedSlot() uint64 {
	return startSlot + uint64(time.Since(s.startedAt)/slotDuration)
}

// voteSlot is the slot votes and finalization refer to: frozen at the stall point while the
// cluster is stalled, otherwise the processed slot.
func (s *MockSolanaServer) voteSlot(processed uint64) uint64 {
	if s.stalledAt != 0 {
		return s.stalledAt
	}
	return processed
}

// lastVote is a validator's last vote given the extra lag it was configured with.
func (s *MockSolanaServer) lastVote(processed, extraLag uint64) uint64 {
	behind := healthyVoteLag + extraLag
	voteSlot := s.voteSlot(processed)
	return voteSlot - min(voteSlot, behind)
}

func (s *MockSolanaServer) getSlot(commitment string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	processed := s.processedSlot()
	if commitment != "finalized" {
		return processed
	}
	finalizedLag := towerFinalizedLag
	if s.phase == phaseAlpenglow {
		finalizedLag = alpenglowFinalizedLag
	}
	voteSlot := s.voteSlot(processed)
	return voteSlot - min(voteSlot, finalizedLag)
}

// setPhase moves the cluster to phase. Setting tower after alpenglow makes the RPC answer as a
// TowerBFT cluster would, which lets scenarios check that the HA clients never leave alpenglow.
func (s *MockSolanaServer) setPhase(phase string) error {
	processed := s.processedSlot()
	switch phase {
	case phaseTower:
	case phaseMigrating:
		if s.featureActivatedAt == 0 {
			s.featureActivatedAt = processed
		}
	case phaseAlpenglow:
		if s.featureActivatedAt == 0 {
			s.featureActivatedAt = processed
		}
		if s.genesisSlot == 0 {
			s.genesisSlot = processed
		}
	default:
		return fmt.Errorf("unknown phase: %q", phase)
	}
	s.phase = phase
	return nil
}

// getFeatureAccountInfo answers getAccountInfo for the Alpenglow feature gate, the only account
// the HA client reads: a bincode Feature { activated_at: Some(slot) }, or no account.
func (s *MockSolanaServer) getFeatureAccountInfo() AccountInfoResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result AccountInfoResult
	result.Context.Slot = s.processedSlot()
	if s.phase == phaseTower {
		return result
	}
	data := make([]byte, 9)
	data[0] = 1
	binary.LittleEndian.PutUint64(data[1:], s.featureActivatedAt)
	result.Value = &AccountInfo{
		Data:     []string{base64.StdEncoding.EncodeToString(data), "base64"},
		Lamports: 1_000_000,
		Owner:    "Feature111111111111111111111111111111111111",
	}
	return result
}

// getAgGenesisCert returns the Alpenglow genesis certificate, or nil before Alpenglow. A
// validator's local RPC reports the cluster's certificate unless set_local_genesis overrode it.
func (s *MockSolanaServer) getAgGenesisCert(validator string) any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var slot uint64
	if s.phase == phaseAlpenglow {
		slot = s.genesisSlot
	}
	if override, ok := s.localGenesis[validator]; ok && validator != "" {
		slot = override
	}
	if slot == 0 {
		return nil
	}
	return map[string]any{"block": map[string]any{"slot": slot}}
}

// ── Backward-compatible legacy endpoints ──────────────────────────────────────

// handleControl keeps the old /control endpoint working for any existing tooling.
func (s *MockSolanaServer) handleControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ActiveValidator string `json:"active_validator"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.activeValidator = body.ActiveValidator
	s.mu.Unlock()
	log.Printf("[control/legacy] set active_validator=%q", body.ActiveValidator)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleNetwork keeps the old /network endpoint working for any existing tooling.
func (s *MockSolanaServer) handleNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		DisconnectValidator string `json:"disconnect_validator"`
		ReconnectValidator  string `json:"reconnect_validator"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if body.DisconnectValidator != "" {
		s.disconnected[body.DisconnectValidator] = true
		if s.activeValidator == body.DisconnectValidator {
			s.activeValidator = ""
		}
		log.Printf("[network/legacy] disconnected %q", body.DisconnectValidator)
	}
	if body.ReconnectValidator != "" {
		delete(s.disconnected, body.ReconnectValidator)
		log.Printf("[network/legacy] reconnected %q", body.ReconnectValidator)
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func main() {
	server := NewMockSolanaServer()

	http.HandleFunc("/", server.handleRPC)
	http.HandleFunc("/action", server.handleAction)
	http.HandleFunc("/public-ip", server.handlePublicIP)
	// Legacy endpoints kept for backward compatibility
	http.HandleFunc("/control", server.handleControl)
	http.HandleFunc("/network", server.handleNetwork)

	port := ":8899"
	log.Printf("mock-solana starting on %s", port)
	log.Printf("initial active validator: %q", server.activeValidator)
	log.Printf("started at %s", time.Now().Format(time.RFC3339))

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}
