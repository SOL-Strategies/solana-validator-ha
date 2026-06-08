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

// ValidatorIdentities represents the identities for the validator.
//
// Each identity (active and passive) can be provided as either:
//   - a keypair file path (identities.active / identities.passive)
//   - a base58 public key string (identities.active_pubkey / identities.passive_pubkey)
//
// When a keypair file is provided, the public key is derived from it. When only
// a public key is supplied, the daemon operates in pubkey-only mode for that
// identity. The keypair file takes precedence if both are set.
type ValidatorIdentities struct {
	ActiveKeyPairFile  string               `koanf:"active"`
	ActiveKeyPair      *solanago.PrivateKey `koanf:"-"`
	ActivePubkeyStr    string               `koanf:"active_pubkey"`
	PassiveKeyPairFile string               `koanf:"passive"`
	PassiveKeyPair     *solanago.PrivateKey `koanf:"-"`
	PassivePubkeyStr   string               `koanf:"passive_pubkey"`
}

// ActivePubkey returns the active identity public key string.
func (v *ValidatorIdentities) ActivePubkey() string {
	if v.ActiveKeyPair != nil {
		return v.ActiveKeyPair.PublicKey().String()
	}
	return v.ActivePubkeyStr
}

// PassivePubkey returns the passive identity public key string.
func (v *ValidatorIdentities) PassivePubkey() string {
	if v.PassiveKeyPair != nil {
		return v.PassiveKeyPair.PublicKey().String()
	}
	return v.PassivePubkeyStr
}

// Load loads the identities from keypair files or validates pubkey strings.
func (v *ValidatorIdentities) Load() error {
	// Load active identity
	if v.ActiveKeyPairFile != "" {
		activeKeyPair, err := solanago.PrivateKeyFromSolanaKeygenFile(v.ActiveKeyPairFile)
		if err != nil {
			return fmt.Errorf("failed to load active identity file: %w", err)
		}
		v.ActiveKeyPair = &activeKeyPair
		v.ActivePubkeyStr = activeKeyPair.PublicKey().String()
	} else if v.ActivePubkeyStr != "" {
		if _, err := solanago.PublicKeyFromBase58(v.ActivePubkeyStr); err != nil {
			return fmt.Errorf("failed to parse active_pubkey as base58 public key: %w", err)
		}
	} else {
		return fmt.Errorf("either validator.identities.active (keypair file) or validator.identities.active_pubkey (base58 pubkey) must be set")
	}

	// Load passive identity
	if v.PassiveKeyPairFile != "" {
		passiveKeyPair, err := solanago.PrivateKeyFromSolanaKeygenFile(v.PassiveKeyPairFile)
		if err != nil {
			return fmt.Errorf("failed to load passive identity file: %w", err)
		}
		v.PassiveKeyPair = &passiveKeyPair
		v.PassivePubkeyStr = passiveKeyPair.PublicKey().String()
	} else if v.PassivePubkeyStr != "" {
		if _, err := solanago.PublicKeyFromBase58(v.PassivePubkeyStr); err != nil {
			return fmt.Errorf("failed to parse passive_pubkey as base58 public key: %w", err)
		}
	} else {
		return fmt.Errorf("either validator.identities.passive (keypair file) or validator.identities.passive_pubkey (base58 pubkey) must be set")
	}

	return nil
}

// LoadPassive loads only the passive identity from a keypair file or validates
// passive_pubkey. Multi-profile configs keep active identities under profiles.
func (v *ValidatorIdentities) LoadPassive() error {
	if v.PassiveKeyPairFile != "" {
		passiveKeyPair, err := solanago.PrivateKeyFromSolanaKeygenFile(v.PassiveKeyPairFile)
		if err != nil {
			return fmt.Errorf("failed to load passive identity file: %w", err)
		}
		v.PassiveKeyPair = &passiveKeyPair
		v.PassivePubkeyStr = passiveKeyPair.PublicKey().String()
	} else if v.PassivePubkeyStr != "" {
		if _, err := solanago.PublicKeyFromBase58(v.PassivePubkeyStr); err != nil {
			return fmt.Errorf("failed to parse passive_pubkey as base58 public key: %w", err)
		}
	} else {
		return fmt.Errorf("either validator.identities.passive (keypair file) or validator.identities.passive_pubkey (base58 pubkey) must be set")
	}
	return nil
}

// LoadActive loads only the active identity from a keypair file or validates
// active_pubkey. Used by profiles.
func (v *ValidatorIdentities) LoadActive() error {
	if v.ActiveKeyPairFile != "" {
		activeKeyPair, err := solanago.PrivateKeyFromSolanaKeygenFile(v.ActiveKeyPairFile)
		if err != nil {
			return fmt.Errorf("failed to load active identity file: %w", err)
		}
		v.ActiveKeyPair = &activeKeyPair
		v.ActivePubkeyStr = activeKeyPair.PublicKey().String()
	} else if v.ActivePubkeyStr != "" {
		if _, err := solanago.PublicKeyFromBase58(v.ActivePubkeyStr); err != nil {
			return fmt.Errorf("failed to parse active_pubkey as base58 public key: %w", err)
		}
	} else {
		return fmt.Errorf("either identities.active (keypair file) or identities.active_pubkey (base58 pubkey) must be set")
	}
	return nil
}

// Validate validates the validator identities, returns an error if the identities are the same
func (v *ValidatorIdentities) Validate() (err error) {
	if v.ActivePubkey() == v.PassivePubkey() {
		err = fmt.Errorf("validator.identities.active and validator.identities.passive must be different: %s", v.ActivePubkey())
	}
	return err
}

// ValidatePassive validates the global passive identity in multi-profile mode.
func (v *ValidatorIdentities) ValidatePassive() error {
	if v.PassivePubkey() == "" {
		return fmt.Errorf("validator.identities.passive or validator.identities.passive_pubkey must be defined")
	}
	if v.ActiveKeyPairFile != "" || v.ActivePubkeyStr != "" || v.ActiveKeyPair != nil {
		return fmt.Errorf("validator.identities.active is no longer supported; move active identities under profiles.<name>.identities")
	}
	return nil
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

	if err := v.Identities.ValidatePassive(); err != nil {
		return err
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
