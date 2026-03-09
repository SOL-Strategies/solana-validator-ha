package main

import (
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
	currentSlot      = uint64(1000)
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
}

func NewMockSolanaServer() *MockSolanaServer {
	return &MockSolanaServer{
		activeValidator: os.Getenv("ACTIVE_VALIDATOR"),
		disconnected:    make(map[string]bool),
		unhealthy:       make(map[string]bool),
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
	Current   []VoteAccount `json:"current"`
	Delinquent []VoteAccount `json:"delinquent"`
}

type BalanceResult struct {
	Context struct {
		Slot uint64 `json:"slot"`
	} `json:"context"`
	Value uint64 `json:"value"`
}

// ── Control types ─────────────────────────────────────────────────────────────

// ControlAction is the unified control request accepted by the /action endpoint.
// Actions: set_active, set_passive, disconnect, reconnect, set_unhealthy, set_healthy, reset.
type ControlAction struct {
	Action string `json:"action"`
	Target string `json:"target"` // validator name; empty for reset/set_active with no target
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
		result = currentSlot
	case "getVoteAccounts":
		result = s.getVoteAccounts()
	case "getBalance":
		result = s.getBalance()
	default:
		result = map[string]any{
			"error": map[string]any{
				"code":    -32601,
				"message": fmt.Sprintf("method not found: %s", method),
			},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      req["id"],
		"result":  result,
	})
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
		// Reconnect all validators, clear all unhealthy state, set initial active.
		s.disconnected = make(map[string]bool)
		s.unhealthy = make(map[string]bool)
		s.activeValidator = action.Target
		log.Printf("[control] reset: active=%q", action.Target)

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

// getVoteAccounts returns the active validator's pubkey in Current[].
// This confirms to the HA manager that the active node is genuinely voting.
func (s *MockSolanaServer) getVoteAccounts() VoteAccountsResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.activeValidator == "" || s.disconnected[s.activeValidator] {
		return VoteAccountsResult{
			Current:   []VoteAccount{},
			Delinquent: []VoteAccount{},
		}
	}

	return VoteAccountsResult{
		Current: []VoteAccount{
			{
				VotePubkey:       activeVotePubkey,
				NodePubkey:       activePubkey,
				ActivatedStake:   1_000_000_000,
				EpochVoteAccount: true,
				Commission:       0,
				LastVote:         currentSlot - 2, // recent vote, well within delinquency threshold
				EpochCredits:     [][]uint64{},
				RootSlot:         currentSlot - 32,
			},
		},
		Delinquent: []VoteAccount{},
	}
}

// getBalance returns a high lamport balance — well above the rent-exempt minimum (890,880).
// This prevents the delinquency-due-to-low-balance code path from triggering.
func (s *MockSolanaServer) getBalance() BalanceResult {
	var result BalanceResult
	result.Context.Slot = currentSlot
	result.Value = 10_000_000_000
	return result
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
