package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
)

// MockConfig represents the configuration for the mock server
type MockConfig struct {
	Validators map[string]ValidatorConfig `koanf:"validators"`
}

// ValidatorConfig represents a validator's configuration
type ValidatorConfig struct {
	PublicIP          string `koanf:"public_ip"`
	IsOffline         bool   `koanf:"is_offline"`
	OnStartupIdentity string `koanf:"on_startup_identity"` // "active" or "passive"
	PassivePubkey     string `koanf:"passive_pubkey"`
	ActivePubkey      string `koanf:"active_pubkey"`
	Healthy           bool   `koanf:"healthy"`
	IsActive          bool   `koanf:"-"` // Runtime state, not in config
}

// MockServer represents the mock server
type MockServer struct {
	configPath string
	logger     *log.Logger
	mu         sync.RWMutex
	config     *MockConfig
}

// NewMockServer creates a new mock server
func NewMockServer(configPath string) *MockServer {
	return &MockServer{
		configPath: configPath,
		logger:     log.New(os.Stderr, "[mock-server] ", log.LstdFlags),
	}
}

// loadConfig loads the configuration from the YAML file
func (m *MockServer) loadConfig() (*MockConfig, error) {
	k := koanf.New(".")

	if err := k.Load(file.Provider(m.configPath), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("failed to load config file: %w", err)
	}

	var config MockConfig
	if err := k.Unmarshal("", &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Set initial active state based on on_startup_identity
	for name, validator := range config.Validators {
		validator.IsActive = (validator.OnStartupIdentity == "active")
		config.Validators[name] = validator
	}

	m.logger.Printf("loaded config: validators=%d", len(config.Validators))
	for name, validator := range config.Validators {
		m.logger.Printf("validator config: name=%s, public_ip=%s, is_active=%v, is_offline=%v, startup_identity=%s", name, validator.PublicIP, validator.IsActive, validator.IsOffline, validator.OnStartupIdentity)
	}

	return &config, nil
}

// getValidatorIdentity returns the appropriate identity for a validator
func (m *MockServer) getValidatorIdentity(validator ValidatorConfig) string {
	if validator.IsActive {
		return validator.ActivePubkey
	}
	return validator.PassivePubkey
}

// handlePublicIP handles requests for validator public IP
func (m *MockServer) handlePublicIP(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Extract validator name from URL path
	// URL format: /validator-1/public-ip
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	validatorName := pathParts[1]
	validator, exists := m.config.Validators[validatorName]
	if !exists {
		http.Error(w, "Validator not found", http.StatusNotFound)
		return
	}

	m.logger.Printf("returning public IP: validator=%s, ip=%s", validatorName, validator.PublicIP)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(validator.PublicIP))
}

// handleSetIdentity handles requests to set validator identity
func (m *MockServer) handleSetIdentity(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Extract validator name and identity type from URL path
	// URL format: /validator-1/set-identity/active
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	validatorName := pathParts[1]
	identityType := pathParts[3]

	validator, exists := m.config.Validators[validatorName]
	if !exists {
		http.Error(w, "Validator not found", http.StatusNotFound)
		return
	}

	// Set the active state based on the identity type
	switch identityType {
	case "active":
		validator.IsActive = true
		m.logger.Printf("set validator to active: validator=%s", validatorName)
	case "passive":
		validator.IsActive = false
		m.logger.Printf("set validator to passive: validator=%s", validatorName)
	default:
		http.Error(w, "Invalid identity type", http.StatusBadRequest)
		return
	}

	m.config.Validators[validatorName] = validator

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("OK"))
}

// handleValidatorRPC handles validator RPC requests
func (m *MockServer) handleValidatorRPC(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Extract validator name from URL path
	// URL format: /validator-1-rpc
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	validatorName := strings.TrimSuffix(pathParts[1], "-rpc")
	validator, exists := m.config.Validators[validatorName]
	if !exists {
		http.Error(w, "Validator not found", http.StatusNotFound)
		return
	}

	// Parse the request body to determine which RPC method is being called
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	method, ok := request["method"].(string)
	if !ok {
		http.Error(w, "Method not specified", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch method {
	case "getIdentity":
		identity := m.getValidatorIdentity(validator)
		m.logger.Printf("returning identity: validator=%s, identity=%s, is_active=%v", validatorName, identity, validator.IsActive)
		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"result": map[string]interface{}{
				"identity": identity,
			},
			"id": request["id"],
		}
		json.NewEncoder(w).Encode(response)

	case "getHealth":
		healthStatus := "ok"
		if !validator.Healthy {
			healthStatus = "unhealthy"
		}
		// Return JSON RPC response for health endpoint
		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  healthStatus,
			"id":      request["id"],
		}
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Unsupported method", http.StatusBadRequest)
	}
}

// handleSolanaNetworkRPC handles Solana network RPC requests
func (m *MockServer) handleSolanaNetworkRPC(w http.ResponseWriter, r *http.Request) {
	// Check if this is a set-gossip-state request
	if strings.HasPrefix(r.URL.Path, "/solana-network-rpc/set-gossip-state") {
		m.handleSetGossipState(w, r)
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Parse the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	method, ok := request["method"].(string)
	if !ok {
		http.Error(w, "Method not specified", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch method {
	case "getClusterNodes":
		// Build cluster nodes response from validators config
		var nodes []map[string]interface{}
		for _, validator := range m.config.Validators {
			// Only include validators that are not offline
			if !validator.IsOffline && validator.Healthy {
				identity := m.getValidatorIdentity(validator)
				nodes = append(nodes, map[string]interface{}{
					"pubkey": identity,
					"gossip": fmt.Sprintf("%s:8001", validator.PublicIP),
					"rpc":    fmt.Sprintf("http://%s:8899", validator.PublicIP),
					"tpu":    fmt.Sprintf("%s:8003", validator.PublicIP),
				})
			}
		}

		response := map[string]interface{}{
			"jsonrpc": "2.0",
			"result":  nodes,
			"id":      request["id"],
		}
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Unsupported method", http.StatusBadRequest)
	}
}

// handleSetGossipState handles requests to set validator online/offline state
func (m *MockServer) handleSetGossipState(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Parse query parameters
	validatorName := r.URL.Query().Get("validator")
	onlineStr := r.URL.Query().Get("online")

	if validatorName == "" {
		http.Error(w, "validator parameter is required", http.StatusBadRequest)
		return
	}

	if onlineStr == "" {
		http.Error(w, "online parameter is required", http.StatusBadRequest)
		return
	}

	// Parse online parameter
	var online bool
	switch onlineStr {
	case "true":
		online = true
	case "false":
		online = false
	default:
		http.Error(w, "online parameter must be 'true' or 'false'", http.StatusBadRequest)
		return
	}

	// Find and update the validator
	validator, exists := m.config.Validators[validatorName]
	if !exists {
		http.Error(w, "Validator not found", http.StatusNotFound)
		return
	}

	// Update the offline state (inverse of online)
	validator.IsOffline = !online
	m.config.Validators[validatorName] = validator

	m.logger.Printf("set validator gossip state: validator=%s, online=%v, is_offline=%v", validatorName, online, validator.IsOffline)

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("OK"))
}

// setupRoutes sets up the HTTP routes
func (m *MockServer) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Public IP endpoints
	mux.HandleFunc("/validator-1/public-ip", m.handlePublicIP)
	mux.HandleFunc("/validator-2/public-ip", m.handlePublicIP)
	mux.HandleFunc("/validator-3/public-ip", m.handlePublicIP)

	// Set identity endpoints
	mux.HandleFunc("/validator-1/set-identity/active", m.handleSetIdentity)
	mux.HandleFunc("/validator-1/set-identity/passive", m.handleSetIdentity)
	mux.HandleFunc("/validator-2/set-identity/active", m.handleSetIdentity)
	mux.HandleFunc("/validator-2/set-identity/passive", m.handleSetIdentity)
	mux.HandleFunc("/validator-3/set-identity/active", m.handleSetIdentity)
	mux.HandleFunc("/validator-3/set-identity/passive", m.handleSetIdentity)

	// Validator RPC endpoints
	mux.HandleFunc("/validator-1-rpc", m.handleValidatorRPC)
	mux.HandleFunc("/validator-2-rpc", m.handleValidatorRPC)
	mux.HandleFunc("/validator-3-rpc", m.handleValidatorRPC)

	// Solana network RPC endpoints (including sub-paths)
	mux.HandleFunc("/solana-network-rpc/", m.handleSolanaNetworkRPC)
	mux.HandleFunc("/solana-network-rpc", m.handleSolanaNetworkRPC)

	return mux
}

// Start starts the mock server
func (m *MockServer) Start(port int) error {
	m.logger.Printf("starting mock server: port=%d, config_path=%s", port, m.configPath)

	// Load initial config
	config, err := m.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load initial config: %w", err)
	}
	m.config = config

	mux := m.setupRoutes()

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return server.ListenAndServe()
}

func main() {
	// Default config path
	configPath := "/config/mock-config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("config file not found: path=%s", configPath)
		os.Exit(1)
	}

	server := NewMockServer(configPath)

	port := 8989
	if err := server.Start(port); err != nil {
		log.Printf("failed to start server: error=%v", err)
		os.Exit(1)
	}
}
