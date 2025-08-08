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

	"github.com/gagliardetto/solana-go"
)

type MockSolanaServer struct {
	activeValidator  string
	validators       map[string]string
	disconnected     map[string]bool
	mu               sync.RWMutex
	callingValidator string // Added to track which validator is calling the RPC endpoint
}

type RPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      interface{}   `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type MockSolanaControl struct {
	ActiveValidator string `json:"active_validator"`
}

type NetworkControl struct {
	DisconnectValidator string `json:"disconnect_validator"`
	ReconnectValidator  string `json:"reconnect_validator"`
}

type ClusterNode struct {
	Pubkey       string `json:"pubkey"`
	Gossip       string `json:"gossip"`
	TPU          string `json:"tpu"`
	RPC          string `json:"rpc"`
	Version      string `json:"version"`
	FeatureSet   int    `json:"featureSet"`
	ShredVersion int    `json:"shredVersion"`
}

type BlockInfo struct {
	Blockhash    string        `json:"blockhash"`
	ParentSlot   int64         `json:"parentSlot"`
	Transactions []interface{} `json:"transactions"`
	Rewards      []interface{} `json:"rewards"`
	BlockTime    int64         `json:"blockTime"`
	BlockHeight  int64         `json:"blockHeight"`
}

func NewMockSolanaServer() *MockSolanaServer {
	return &MockSolanaServer{
		activeValidator: os.Getenv("ACTIVE_VALIDATOR"),
		validators: map[string]string{
			"validator-1": os.Getenv("VALIDATOR_1_IP"),
			"validator-2": os.Getenv("VALIDATOR_2_IP"),
			"validator-3": os.Getenv("VALIDATOR_3_IP"),
		},
		disconnected: make(map[string]bool),
	}
}

func (s *MockSolanaServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	// Parse the request body
	var request map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	method, ok := request["method"].(string)
	if !ok {
		http.Error(w, "Missing method", http.StatusBadRequest)
		return
	}

	// Track which validator is calling based on query parameter
	validatorName := r.URL.Query().Get("validator")
	if validatorName != "" {
		s.mu.Lock()
		s.callingValidator = validatorName
		s.mu.Unlock()
	}

	var response interface{}
	switch method {
	case "getClusterNodes":
		response = s.getClusterNodes()
	case "getIdentity":
		response = s.getIdentity()
	case "getHealth":
		response = s.getHealth()
	default:
		response = map[string]interface{}{
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "Method not found",
			},
		}
	}

	// Create the JSON-RPC response
	jsonResponse := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      request["id"],
		"result":  response,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonResponse)
}

func (s *MockSolanaServer) handleControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var control MockSolanaControl
	if err := json.NewDecoder(r.Body).Decode(&control); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	s.SetActiveValidator(control.ActiveValidator)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *MockSolanaServer) handleNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var control NetworkControl
	if err := json.NewDecoder(r.Body).Decode(&control); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if control.DisconnectValidator != "" {
		s.DisconnectValidator(control.DisconnectValidator)
	} else if control.ReconnectValidator != "" {
		s.ReconnectValidator(control.ReconnectValidator)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *MockSolanaServer) handlePublicIP(w http.ResponseWriter, r *http.Request) {
	// Get the client's IP address
	clientIP := r.RemoteAddr
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		clientIP = forwardedFor
	}

	// Remove port if present
	if colonIndex := strings.Index(clientIP, ":"); colonIndex != -1 {
		clientIP = clientIP[:colonIndex]
	}

	// Map the client IP to a different public IP for testing
	// This prevents the "must not reference ourselves" validation error
	// Return IPs that are NOT in the peers configuration
	var validatorIP string
	switch clientIP {
	case "172.20.0.10":
		validatorIP = "10.0.0.100" // Different from peers configuration
	case "172.20.0.11":
		validatorIP = "10.0.0.101" // Different from peers configuration
	case "172.20.0.12":
		validatorIP = "10.0.0.102" // Different from peers configuration
	default:
		// Fallback to a different IP
		validatorIP = "10.0.0.103"
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(validatorIP))
}

func (s *MockSolanaServer) getClusterNodes() []ClusterNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var nodes []ClusterNode
	for name, ip := range s.validators {
		// Skip disconnected validators
		if s.disconnected[name] {
			continue
		}

		// Use the actual active pubkey for the active validator
		var pubkey string
		if name == s.activeValidator {
			pubkey = "ArkzFExXXHaA6izkNhTJJ5zpXdQpynffjfRMJu4Yq6H"
		} else {
			// Use valid Solana public keys for passive validators
			switch name {
			case "validator-2":
				pubkey = "AP4JyZq2vuN4u64FGFHTwdG11xHu1vZWVYQj21MPLrnw"
			case "validator-3":
				pubkey = "DJ7w4p8Ve7qdSAmkpA3sviSbsd1HPUxd43x7MTH72JHT"
			default:
				pubkey = "11111111111111111111111111111111"
			}
		}

		node := ClusterNode{
			Pubkey:       pubkey,
			Gossip:       fmt.Sprintf("%s:8001", ip),
			TPU:          fmt.Sprintf("%s:8003", ip),
			RPC:          fmt.Sprintf("%s:8899", ip),
			Version:      "1.17.0",
			FeatureSet:   123456789,
			ShredVersion: 12345,
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func (s *MockSolanaServer) getBlocks() []int64 {
	return []int64{1000, 1001, 1002, 1003, 1004}
}

func (s *MockSolanaServer) getBlock() *BlockInfo {
	return &BlockInfo{
		Blockhash:    solana.NewWallet().PublicKey().String(),
		ParentSlot:   999,
		Transactions: []interface{}{},
		Rewards:      []interface{}{},
		BlockTime:    time.Now().Unix(),
		BlockHeight:  1000,
	}
}

func (s *MockSolanaServer) getSlot() int64 {
	return 1000
}

func (s *MockSolanaServer) getIdentity() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return the appropriate identity based on which validator is calling
	// Only the active validator should return the active pubkey
	if s.callingValidator == s.activeValidator {
		// This validator is the active one, return active pubkey
		return map[string]interface{}{
			"identity": "ArkzFExXXHaA6izkNhTJJ5zpXdQpynffjfRMJu4Yq6H",
		}
	} else {
		// This validator is passive, return passive pubkey
		// Use different passive pubkeys for different validators
		switch s.callingValidator {
		case "validator-1":
			return map[string]interface{}{
				"identity": "AP4JyZq2vuN4u64FGFHTwdG11xHu1vZWVYQj21MPLrnw",
			}
		case "validator-2":
			return map[string]interface{}{
				"identity": "DJ7w4p8Ve7qdSAmkpA3sviSbsd1HPUxd43x7MTH72JHT",
			}
		case "validator-3":
			return map[string]interface{}{
				"identity": "5dXttfrjFEEExmZhVmVAdw2LzepNAhFYJTUgPCDk8CYD",
			}
		default:
			// Fallback - return passive pubkey
			return map[string]interface{}{
				"identity": "AP4JyZq2vuN4u64FGFHTwdG11xHu1vZWVYQj21MPLrnw",
			}
		}
	}
}

func (s *MockSolanaServer) getHealth() string {
	// Return a healthy status for all validators
	return "ok"
}

func (s *MockSolanaServer) SetActiveValidator(validator string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeValidator = validator
	log.Printf("Active validator changed to: %s", validator)
}

func (s *MockSolanaServer) DisconnectValidator(validator string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disconnected[validator] = true
	log.Printf("Validator disconnected: %s", validator)
}

func (s *MockSolanaServer) ReconnectValidator(validator string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.disconnected, validator)
	log.Printf("Validator reconnected: %s", validator)
}

func main() {
	server := NewMockSolanaServer()

	http.HandleFunc("/", server.handleRPC)
	http.HandleFunc("/control", server.handleControl)
	http.HandleFunc("/network", server.handleNetwork)
	http.HandleFunc("/public-ip", server.handlePublicIP)

	port := ":8899"
	log.Printf("Mock Solana RPC server starting on port %s", port)
	log.Printf("Active validator: %s", server.activeValidator)
	log.Printf("Public IP service available at: http://localhost%s/public-ip", port)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}
