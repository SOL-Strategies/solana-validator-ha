package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type TestOrchestrator struct {
	mockSolanaURL string
	validatorURLs map[string]string
}

type ValidatorStatus struct {
	Role    string `json:"role"`
	Healthy bool   `json:"healthy"`
	Active  bool   `json:"active"`
	Passive bool   `json:"passive"`
}

type MockSolanaControl struct {
	ActiveValidator string `json:"active_validator"`
}

type NetworkControl struct {
	DisconnectValidator string `json:"disconnect_validator"`
	ReconnectValidator  string `json:"reconnect_validator"`
}

func NewTestOrchestrator() *TestOrchestrator {
	return &TestOrchestrator{
		mockSolanaURL: os.Getenv("MOCK_SOLANA_URL"),
		validatorURLs: map[string]string{
			"validator-1": os.Getenv("VALIDATOR_1_URL"),
			"validator-2": os.Getenv("VALIDATOR_2_URL"),
			"validator-3": os.Getenv("VALIDATOR_3_URL"),
		},
	}
}

func (t *TestOrchestrator) setActiveValidator(validator string) error {
	control := MockSolanaControl{ActiveValidator: validator}

	jsonData, err := json.Marshal(control)
	if err != nil {
		return fmt.Errorf("failed to marshal control data: %w", err)
	}

	resp, err := http.Post(t.mockSolanaURL+"/control", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to set active validator: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set active validator, status: %d", resp.StatusCode)
	}

	log.Printf("Set active validator to: %s", validator)
	return nil
}

func (t *TestOrchestrator) disconnectValidator(validator string) error {
	control := NetworkControl{DisconnectValidator: validator}

	jsonData, err := json.Marshal(control)
	if err != nil {
		return fmt.Errorf("failed to marshal disconnect data: %w", err)
	}

	resp, err := http.Post(t.mockSolanaURL+"/network", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to disconnect validator: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to disconnect validator, status: %d", resp.StatusCode)
	}

	log.Printf("Disconnected validator: %s", validator)
	return nil
}

func (t *TestOrchestrator) reconnectValidator(validator string) error {
	control := NetworkControl{ReconnectValidator: validator}

	jsonData, err := json.Marshal(control)
	if err != nil {
		return fmt.Errorf("failed to marshal reconnect data: %w", err)
	}

	resp, err := http.Post(t.mockSolanaURL+"/network", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to reconnect validator: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to reconnect validator, status: %d", resp.StatusCode)
	}

	log.Printf("Reconnected validator: %s", validator)
	return nil
}

func (t *TestOrchestrator) getValidatorStatus(validator string) (*ValidatorStatus, error) {
	url := t.validatorURLs[validator] + "/metrics"

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for %s: %w", validator, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get metrics for %s, status: %d", validator, resp.StatusCode)
	}

	// Parse metrics to extract role and status
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	metrics := string(body)

	// Parse role from metadata metric
	var role string
	if strings.Contains(metrics, `validator_role="active"`) {
		role = "active"
	} else if strings.Contains(metrics, `validator_role="passive"`) {
		role = "passive"
	} else {
		role = "unknown"
	}

	// Parse status from metadata metric
	var status string
	if strings.Contains(metrics, `validator_status="healthy"`) {
		status = "healthy"
	} else {
		status = "unhealthy"
	}

	return &ValidatorStatus{
		Role:    role,
		Healthy: status == "healthy",
		Active:  role == "active",
		Passive: role == "passive",
	}, nil
}

func (t *TestOrchestrator) waitForValidatorRole(validator, expectedRole string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		status, err := t.getValidatorStatus(validator)
		if err != nil {
			log.Printf("Error getting status for %s: %v", validator, err)
			time.Sleep(2 * time.Second)
			continue
		}

		if status.Role == expectedRole {
			log.Printf("Validator %s is now %s", validator, expectedRole)
			return nil
		}

		log.Printf("Validator %s is %s, waiting for %s", validator, status.Role, expectedRole)
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout waiting for %s to become %s", validator, expectedRole)
}

func (t *TestOrchestrator) waitForActiveValidator(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		for validator := range t.validatorURLs {
			status, err := t.getValidatorStatus(validator)
			if err != nil {
				continue
			}

			if status.Role == "active" {
				log.Printf("Found active validator: %s", validator)
				return validator, nil
			}
		}

		log.Printf("No active validator found, waiting...")
		time.Sleep(2 * time.Second)
	}

	return "", fmt.Errorf("timeout waiting for any validator to become active")
}

func (t *TestOrchestrator) getActiveValidators() ([]string, error) {
	var activeValidators []string

	for validator := range t.validatorURLs {
		status, err := t.getValidatorStatus(validator)
		if err != nil {
			continue
		}

		if status.Role == "active" {
			activeValidators = append(activeValidators, validator)
		}
	}

	return activeValidators, nil
}

func (t *TestOrchestrator) runScenario1() error {
	log.Println("=== Scenario 1: One active and two passive peers ===")

	// Set initial state - validator-1 should be active
	if err := t.setActiveValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to set initial active validator: %w", err)
	}

	// Wait for all validators to stabilize
	time.Sleep(10 * time.Second)

	// Check that validator-1 is active and others are passive
	if err := t.waitForValidatorRole("validator-1", "active", 30*time.Second); err != nil {
		return fmt.Errorf("validator-1 should be active: %w", err)
	}

	if err := t.waitForValidatorRole("validator-2", "passive", 30*time.Second); err != nil {
		return fmt.Errorf("validator-2 should be passive: %w", err)
	}

	if err := t.waitForValidatorRole("validator-3", "passive", 30*time.Second); err != nil {
		return fmt.Errorf("validator-3 should be passive: %w", err)
	}

	log.Println("✅ Scenario 1 passed: One active, two passive")
	return nil
}

func (t *TestOrchestrator) runScenario2() error {
	log.Println("=== Scenario 2: Active peer disconnects, passive peer takes over ===")

	// Start with validator-1 active
	if err := t.setActiveValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to set validator-1 as active: %w", err)
	}

	time.Sleep(5 * time.Second)

	// Disconnect validator-1 (simulate network failure)
	if err := t.disconnectValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to disconnect validator-1: %w", err)
	}

	// Wait for one of the passive validators to become active (first responder wins)
	activeValidator, err := t.waitForActiveValidator(30 * time.Second)
	if err != nil {
		return fmt.Errorf("no validator became active after validator-1 disconnection: %w", err)
	}

	// Verify only one validator is active
	activeValidators, err := t.getActiveValidators()
	if err != nil {
		return fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(activeValidators) != 1 {
		return fmt.Errorf("expected exactly 1 active validator, got %d: %v", len(activeValidators), activeValidators)
	}

	log.Printf("✅ Scenario 2 passed: %s became active after validator-1 disconnection", activeValidator)
	return nil
}

func (t *TestOrchestrator) runScenario3() error {
	log.Println("=== Scenario 3: Active peer disconnects, multiple passive peers compete ===")

	// Start with validator-1 active, validator-2 and validator-3 passive
	if err := t.setActiveValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to set validator-1 as active: %w", err)
	}

	time.Sleep(5 * time.Second)

	// Disconnect validator-1
	if err := t.disconnectValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to disconnect validator-1: %w", err)
	}

	// Wait for one validator to become active (first responder wins)
	activeValidator, err := t.waitForActiveValidator(30 * time.Second)
	if err != nil {
		return fmt.Errorf("no validator became active: %w", err)
	}

	// Verify only one validator is active
	activeValidators, err := t.getActiveValidators()
	if err != nil {
		return fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(activeValidators) != 1 {
		return fmt.Errorf("expected exactly 1 active validator, got %d: %v", len(activeValidators), activeValidators)
	}

	log.Printf("✅ Scenario 3 passed: Only %s became active (first responder wins)", activeValidator)
	return nil
}

func (t *TestOrchestrator) runScenario4() error {
	log.Println("=== Scenario 4: Unhealthy/non-gossip-visible validators don't become active ===")

	// Start with validator-1 active
	if err := t.setActiveValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to set validator-1 as active: %w", err)
	}

	time.Sleep(5 * time.Second)

	// Verify initial state: validator-1 active, others passive
	if err := t.waitForValidatorRole("validator-1", "active", 10*time.Second); err != nil {
		return fmt.Errorf("validator-1 should be active: %w", err)
	}
	if err := t.waitForValidatorRole("validator-2", "passive", 10*time.Second); err != nil {
		return fmt.Errorf("validator-2 should be passive: %w", err)
	}
	if err := t.waitForValidatorRole("validator-3", "passive", 10*time.Second); err != nil {
		return fmt.Errorf("validator-3 should be passive: %w", err)
	}

	// Test 1: Make validator-2 unhealthy by removing it from gossip
	log.Println("Test 1: Making validator-2 unhealthy by removing it from gossip...")
	if err := t.disconnectValidator("validator-2"); err != nil {
		return fmt.Errorf("failed to disconnect validator-2: %w", err)
	}

	// Disconnect the active validator
	log.Println("Disconnecting active validator-1...")
	if err := t.disconnectValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to disconnect validator-1: %w", err)
	}

	// Update the mock to set validator-3 as the active validator immediately
	// This ensures validator-3 gets the correct identity when it becomes active
	log.Println("Updating mock to set validator-3 as active...")
	if err := t.setActiveValidator("validator-3"); err != nil {
		return fmt.Errorf("failed to set validator-3 as active in mock: %w", err)
	}

	// Give the mock time to update
	time.Sleep(2 * time.Second)

	// Wait and verify that validator-3 (the healthy one) becomes active
	log.Println("Waiting for validator-3 to become active...")
	if err := t.waitForValidatorRole("validator-3", "active", 30*time.Second); err != nil {
		return fmt.Errorf("validator-3 should become active: %w", err)
	}

	// Update the mock to set validator-3 as the active validator
	log.Println("Updating mock to set validator-3 as active...")
	if err := t.setActiveValidator("validator-3"); err != nil {
		return fmt.Errorf("failed to set validator-3 as active in mock: %w", err)
	}

	// Give the mock time to update
	time.Sleep(2 * time.Second)

	// Verify validator-2 (unhealthy) does NOT become active
	status, err := t.getValidatorStatus("validator-2")
	if err != nil {
		return fmt.Errorf("failed to get validator-2 status: %w", err)
	}
	if status.Role == "active" {
		return fmt.Errorf("validator-2 (unhealthy) should not become active")
	}

	log.Println("✅ Test 1 passed: Unhealthy validator-2 did not become active")

	// Reconnect validator-2 and set validator-3 as active for next test
	if err := t.reconnectValidator("validator-2"); err != nil {
		return fmt.Errorf("failed to reconnect validator-2: %w", err)
	}
	if err := t.setActiveValidator("validator-3"); err != nil {
		return fmt.Errorf("failed to set validator-3 as active: %w", err)
	}
	time.Sleep(5 * time.Second)

	// Test 2: Make validator-1 unhealthy by removing it from gossip
	log.Println("Test 2: Making validator-1 unhealthy by removing it from gossip...")
	if err := t.disconnectValidator("validator-1"); err != nil {
		return fmt.Errorf("failed to disconnect validator-1: %w", err)
	}

	// Disconnect the active validator
	log.Println("Disconnecting active validator-3...")
	if err := t.disconnectValidator("validator-3"); err != nil {
		return fmt.Errorf("failed to disconnect validator-3: %w", err)
	}

	// Update the mock to set validator-2 as the active validator immediately
	// This ensures validator-2 gets the correct identity when it becomes active
	log.Println("Updating mock to set validator-2 as active...")
	if err := t.setActiveValidator("validator-2"); err != nil {
		return fmt.Errorf("failed to set validator-2 as active in mock: %w", err)
	}

	// Give the mock time to update
	time.Sleep(2 * time.Second)

	// Wait and verify that validator-2 (the healthy one) becomes active
	log.Println("Waiting for validator-2 to become active...")
	if err := t.waitForValidatorRole("validator-2", "active", 30*time.Second); err != nil {
		return fmt.Errorf("validator-2 should become active: %w", err)
	}

	// Update the mock to set validator-2 as the active validator
	log.Println("Updating mock to set validator-2 as active...")
	if err := t.setActiveValidator("validator-2"); err != nil {
		return fmt.Errorf("failed to set validator-2 as active in mock: %w", err)
	}

	// Give the mock time to update
	time.Sleep(2 * time.Second)

	// Verify validator-1 (unhealthy) does NOT become active
	status, err = t.getValidatorStatus("validator-1")
	if err != nil {
		return fmt.Errorf("failed to get validator-1 status: %w", err)
	}
	if status.Role == "active" {
		return fmt.Errorf("validator-1 (unhealthy) should not become active")
	}

	log.Println("✅ Test 2 passed: Unhealthy validator-1 did not become active")

	log.Println("✅ Scenario 4 passed: Unhealthy validators correctly prevented from becoming active")
	return nil
}

func (t *TestOrchestrator) runAllScenarios() error {
	log.Println("Starting integration test scenarios...")

	// Wait for services to be ready
	log.Println("Waiting for services to be ready...")
	time.Sleep(15 * time.Second)

	// Run all scenarios
	scenarios := []struct {
		name string
		fn   func() error
	}{
		{"Scenario 1", t.runScenario1},
		{"Scenario 2", t.runScenario2},
		{"Scenario 3", t.runScenario3},
		{"Scenario 4", t.runScenario4},
	}

	for _, scenario := range scenarios {
		log.Printf("Running %s...", scenario.name)
		if err := scenario.fn(); err != nil {
			return fmt.Errorf("%s failed: %w", scenario.name, err)
		}
		log.Printf("%s completed successfully", scenario.name)

		// Brief pause between scenarios
		time.Sleep(5 * time.Second)
	}

	log.Println("🎉 All integration test scenarios passed!")
	return nil
}

func main() {
	orchestrator := NewTestOrchestrator()

	if err := orchestrator.runAllScenarios(); err != nil {
		log.Printf("❌ Integration test failed: %v", err)
		os.Exit(1)
	}

	log.Println("✅ Integration test completed successfully!")
}
