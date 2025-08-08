package config

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/charmbracelet/log"
	solanago "github.com/gagliardetto/solana-go"
)

var publicIPServices = []string{
	"https://api.ipify.org",
	"https://checkip.amazonaws.com",
	"https://ipinfo.io/ip",
	"https://4.icanhazip.com",
}

// Validator represents the local validator configuration
type Validator struct {
	Name                string              `koanf:"name"`
	RPCURL              string              `koanf:"rpc_url"`
	PublicIPServiceURLs []string            `koanf:"public_ip_service_urls"`
	Identities          ValidatorIdentities `koanf:"identities"`
}

// ValidatorIdentities represents the identities for the validator
type ValidatorIdentities struct {
	ActiveKeyPairFile  string               `koanf:"active"`
	ActiveKeyPair      *solanago.PrivateKey `koanf:"-"`
	PassiveKeyPairFile string               `koanf:"passive"`
	PassiveKeyPair     *solanago.PrivateKey `koanf:"-"`
}

// Load loads the identities from the key pair files
func (v *ValidatorIdentities) Load() error {
	activeKeyPair, err := solanago.PrivateKeyFromSolanaKeygenFile(v.ActiveKeyPairFile)
	if err != nil {
		return fmt.Errorf("failed to load active identity file: %w", err)
	}
	v.ActiveKeyPair = &activeKeyPair

	passiveKeyPair, err := solanago.PrivateKeyFromSolanaKeygenFile(v.PassiveKeyPairFile)
	if err != nil {
		return fmt.Errorf("failed to load passive identity file: %w", err)
	}
	v.PassiveKeyPair = &passiveKeyPair

	return nil
}

// Validate validates the validator identities, returns an error if the identities are the same
func (v *ValidatorIdentities) Validate() (err error) {
	if v.ActiveKeyPair.PublicKey().String() == v.PassiveKeyPair.PublicKey().String() {
		err = fmt.Errorf("validator.identities.active and validator.identities.passive must be different: %s", v.ActiveKeyPair.PublicKey().String())
	}
	return err
}

// Validate validates the validator configuration
func (v *Validator) Validate() error {
	// validator.name must be defined
	if v.Name == "" {
		return fmt.Errorf("validator.name must be defined")
	}

	// validator.rpc_url must be a valid URL
	if v.RPCURL == "" {
		return fmt.Errorf("validator.rpc_url must be a valid URL")
	}
	parsedURL, err := url.Parse(v.RPCURL)
	if err != nil {
		return fmt.Errorf("validator.rpc_url must be a valid URL: %w", err)
	}
	// Additional validation: must have a scheme and host
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("validator.rpc_url must be a valid URL: invalid URL %s", v.RPCURL)
	}

	// validator.public_ip_service_urls must be a valid URL
	for _, publicIPServiceURL := range v.PublicIPServiceURLs {
		parsedURL, err := url.Parse(publicIPServiceURL)
		if err != nil {
			return fmt.Errorf("validator.public_ip_service_urls must be a valid URL: %w", err)
		}
		if parsedURL.Scheme == "" || parsedURL.Host == "" {
			return fmt.Errorf("validator.public_ip_service_urls must be a valid URL: invalid URL %s", publicIPServiceURL)
		}
	}

	// Only validate identities if they've been loaded
	if v.Identities.ActiveKeyPair != nil && v.Identities.PassiveKeyPair != nil {
		return v.Identities.Validate()
	}

	return nil
}

// SetDefaults sets default values for the validator configuration
func (v *Validator) SetDefaults() {
	// Set default validator RPC URL
	if v.RPCURL == "" {
		v.RPCURL = "http://localhost:8899"
	}

	if len(v.PublicIPServiceURLs) == 0 {
		v.PublicIPServiceURLs = publicIPServices
	}
}

// PublicIP returns the public IP address of the validator using the public IP service URLs
// returns the first successful response
func (v *Validator) PublicIP() (string, error) {
	for _, publicIPServiceURL := range v.PublicIPServiceURLs {
		response, err := http.Get(publicIPServiceURL)
		if err != nil {
			continue
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			log.Warn("failed to read response body from public IP service", "error", err, "service_url", publicIPServiceURL)
			continue
		}

		bodyStr := string(body)
		var sanitizedIP string

		// select the first line of the response
		sanitizedIP = strings.Split(bodyStr, "\n")[0]
		// trim whitespaces
		sanitizedIP = strings.TrimSpace(sanitizedIP)
		// trim leading and trailing single or double quotes
		sanitizedIP = strings.Trim(sanitizedIP, "\"")
		sanitizedIP = strings.Trim(sanitizedIP, "'")

		// validate the IP address is a valid IPv4 address
		ip := net.ParseIP(sanitizedIP)
		if ip == nil || ip.To4() == nil {
			log.Warn("invalid IPv4 address returned from public IP service", "ip", sanitizedIP, "service_url", publicIPServiceURL)
			continue
		}
		return sanitizedIP, nil
	}
	return "", fmt.Errorf("failed to get public IP from any public IP service URLs: %v", v.PublicIPServiceURLs)
}
