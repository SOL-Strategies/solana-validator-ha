package rpc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
)

// defaultTimeout is the default per-call RPC timeout.
const defaultTimeout = 5 * time.Second

// defaultURLCooldown is the default duration a URL that returned a permanent HTTP error
// (403/429/503) is deprioritised before being retried again.
const defaultURLCooldown = 60 * time.Second

// Client represents an RPC client that can handle multiple URLs
type Client struct {
	// urls is a slice of URLs for load balancing
	urls []string
	// clients is a map of RPC clients, keyed by the rpc URL
	clients map[string]*rpc.Client
	// lastSuccessfulURL tracks the last URL that succeeded to avoid it for throttling protection
	lastSuccessfulURL string
	// urlCooldowns tracks when rate-limited / access-forbidden URLs may be retried again
	urlCooldowns map[string]time.Time
	timeout      time.Duration
	urlCooldown  time.Duration
	logger       *log.Logger
}

// NewClient creates a new RPC client with one or more URLs
func NewClient(logPrefix string, urls ...string) *Client {
	clients := make(map[string]*rpc.Client)
	for _, url := range urls {
		clients[url] = rpc.New(url)
	}
	return &Client{
		logger:            logging.New(logPrefix, "rpc_client"),
		urls:              urls,
		clients:           clients,
		lastSuccessfulURL: "",
		urlCooldowns:      make(map[string]time.Time),
		timeout:           defaultTimeout,
		urlCooldown:       defaultURLCooldown,
	}
}

// WithTimeout sets the per-call RPC timeout and returns the client for chaining.
func (c *Client) WithTimeout(d time.Duration) *Client {
	c.timeout = d
	return c
}

// WithCooldown sets how long a throttled/forbidden URL is deprioritised and returns the client for chaining.
func (c *Client) WithCooldown(d time.Duration) *Client {
	c.urlCooldown = d
	return c
}

// withTimeout executes a function with the client's timeout
func (c *Client) withTimeout(ctx context.Context, fn func(context.Context) error) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return fn(timeoutCtx)
}

// rpcOperation represents a generic RPC operation
type rpcOperation[T any] struct {
	name    string
	execute func(*rpc.Client, context.Context) (T, error)
}

// getURLsToTry returns URLs ordered for optimal reliability:
//
//  1. Active URLs (not lastSuccessfulURL, not in cooldown) — tried first to spread load
//  2. lastSuccessfulURL (if not in cooldown) — known-good, used as fallback
//  3. Cooling-down URLs (403/429/503) — tried last, after all healthy options are exhausted
func (c *Client) getURLsToTry() []string {
	if len(c.urls) <= 1 {
		return c.urls
	}

	now := time.Now()
	activeURLs := make([]string, 0, len(c.urls))
	coolingURLs := make([]string, 0)
	lastSuccessfulInCooldown := false

	for _, url := range c.urls {
		inCooldown := c.urlCooldowns[url].After(now)
		if url == c.lastSuccessfulURL {
			lastSuccessfulInCooldown = inCooldown
			continue // placed explicitly below
		}
		if inCooldown {
			coolingURLs = append(coolingURLs, url)
		} else {
			activeURLs = append(activeURLs, url)
		}
	}

	result := activeURLs
	if c.lastSuccessfulURL != "" {
		if lastSuccessfulInCooldown {
			coolingURLs = append(coolingURLs, c.lastSuccessfulURL)
		} else {
			result = append(result, c.lastSuccessfulURL)
		}
	}
	return append(result, coolingURLs...)
}

// executeWithRetry executes an RPC method, trying URLs in throttling-optimized order
func executeWithRetry[T any](c *Client, ctx context.Context, op rpcOperation[T]) (T, error) {
	attemptedURLs := []string{}
	errors := []error{}

	// try each URL in order, with lastSuccessfulURL at the end for throttling protection
	for _, url := range c.getURLsToTry() {
		client, exists := c.clients[url]
		if !exists {
			continue
		}

		attemptedURLs = append(attemptedURLs, url)

		var result T
		err := c.withTimeout(ctx, func(timeoutCtx context.Context) error {
			var err error
			result, err = op.execute(client, timeoutCtx)
			return err
		})

		if err != nil {
			if isPermanentHTTPError(err) {
				now := time.Now()
				alreadyCooling := c.urlCooldowns[url].After(now)
				c.urlCooldowns[url] = now.Add(c.urlCooldown)
				if !alreadyCooling {
					c.logger.Warn("RPC endpoint rate-limited or access forbidden, cooling down",
						"method", op.name,
						"url", url,
						"cooldown", c.urlCooldown,
					)
				}
			}
			c.logger.Debug("method call failed", "method", op.name, "error", err, "rpc_url", url)
			errors = append(errors, err)
			continue
		}

		// Success! Update the last successful URL
		c.lastSuccessfulURL = url
		return result, nil
	}

	var zero T
	if len(errors) > 0 && allMethodNotFound(errors) {
		return zero, fmt.Errorf("%s on %v: %w", op.name, attemptedURLs, ErrMethodNotFound)
	}
	return zero, fmt.Errorf("method call failed on all RPC endpoints method: %s, attempted_urls: %v, errors: %v", op.name, attemptedURLs, errors)
}

// GetVoteAccounts gets the vote accounts from the first working RPC client

func (c *Client) GetVoteAccounts(ctx context.Context, opts *rpc.GetVoteAccountsOpts) (*rpc.GetVoteAccountsResult, error) {
	return executeWithRetry(c, ctx, rpcOperation[*rpc.GetVoteAccountsResult]{
		name: "GetVoteAccounts",
		execute: func(client *rpc.Client, ctx context.Context) (*rpc.GetVoteAccountsResult, error) {
			return client.GetVoteAccounts(ctx, opts)
		},
	})
}

// GetBalance gets the balance from the first working RPC client
func (c *Client) GetBalance(ctx context.Context, pubkey solana.PublicKey) (*rpc.GetBalanceResult, error) {
	return executeWithRetry(c, ctx, rpcOperation[*rpc.GetBalanceResult]{
		name: "GetBalance",
		execute: func(client *rpc.Client, ctx context.Context) (*rpc.GetBalanceResult, error) {
			result, err := client.GetBalance(ctx, pubkey, rpc.CommitmentProcessed)
			if err != nil {
				return nil, err
			}
			return result, nil
		},
	})
}

// GetSlot gets the current slot from the first working RPC client
func (c *Client) GetSlot(ctx context.Context) (uint64, error) {
	return executeWithRetry(c, ctx, rpcOperation[uint64]{
		name: "GetSlot",
		execute: func(client *rpc.Client, ctx context.Context) (uint64, error) {
			return client.GetSlot(ctx, rpc.CommitmentProcessed)
		},
	})
}

// GetClusterNodes tries each RPC client in order and returns the first successful response
func (c *Client) GetClusterNodes(ctx context.Context) ([]*rpc.GetClusterNodesResult, error) {
	return executeWithRetry(c, ctx, rpcOperation[[]*rpc.GetClusterNodesResult]{
		name: "GetClusterNodes",
		execute: func(client *rpc.Client, ctx context.Context) ([]*rpc.GetClusterNodesResult, error) {
			return client.GetClusterNodes(ctx)
		},
	})
}

// GetIdentity gets the identity from the first working RPC client
func (c *Client) GetIdentity(ctx context.Context) (*rpc.GetIdentityResult, error) {
	return executeWithRetry(c, ctx, rpcOperation[*rpc.GetIdentityResult]{
		name: "GetIdentity",
		execute: func(client *rpc.Client, ctx context.Context) (*rpc.GetIdentityResult, error) {
			return client.GetIdentity(ctx)
		},
	})
}

// GetHealth gets the health from the first working RPC client
func (c *Client) GetHealth(ctx context.Context) (string, error) {
	result, err := executeWithRetry(c, ctx, rpcOperation[string]{
		name: "GetHealth",
		execute: func(client *rpc.Client, ctx context.Context) (string, error) {
			return client.GetHealth(ctx)
		},
	})

	if err != nil {
		// Return just the error message, not the full error
		return "", errors.New(extractErrorMessage(err))
	}

	return result, nil
}

// GetSlotWithCommitment gets the slot at the given commitment from the first working RPC client.
func (c *Client) GetSlotWithCommitment(ctx context.Context, commitment rpc.CommitmentType) (uint64, error) {
	return executeWithRetry(c, ctx, rpcOperation[uint64]{
		name: "GetSlotWithCommitment",
		execute: func(client *rpc.Client, ctx context.Context) (uint64, error) {
			return client.GetSlot(ctx, commitment)
		},
	})
}

// AgGenesisCert is the part of an Alpenglow genesis certificate this client needs.
// Blocks after Block.Slot are produced under Alpenglow consensus.
type AgGenesisCert struct {
	Block struct {
		Slot uint64 `json:"slot"`
	} `json:"block"`
}

// GetAgGenesisCert calls getAgGenesisCert. It returns nil while the cluster has not yet
// switched to Alpenglow. When no endpoint implements the method, the error wraps
// ErrMethodNotFound.
func (c *Client) GetAgGenesisCert(ctx context.Context) (*AgGenesisCert, error) {
	return executeWithRetry(c, ctx, rpcOperation[*AgGenesisCert]{
		name: "GetAgGenesisCert",
		execute: func(client *rpc.Client, ctx context.Context) (*AgGenesisCert, error) {
			var cert *AgGenesisCert
			if err := client.RPCCallForInto(ctx, &cert, "getAgGenesisCert", nil); err != nil {
				return nil, err
			}
			// Slot 0 is the cluster's genesis block, never an Alpenglow genesis. Seeing it means the
			// endpoint answered with something other than a certificate.
			if cert != nil && cert.Block.Slot == 0 {
				return nil, fmt.Errorf("getAgGenesisCert returned a certificate without a block slot")
			}
			return cert, nil
		},
	})
}

// FeatureStatus is the activation state of a feature gate account.
type FeatureStatus struct {
	Activated bool
	// ActivatedAt is the slot the feature activated at. It is only set when Activated is true.
	ActivatedAt uint64
}

// GetFeatureStatus reads a feature gate account at finalized commitment. A feature whose
// account does not exist yet is reported as not activated.
func (c *Client) GetFeatureStatus(ctx context.Context, feature solana.PublicKey) (FeatureStatus, error) {
	return executeWithRetry(c, ctx, rpcOperation[FeatureStatus]{
		name: "GetFeatureStatus",
		execute: func(client *rpc.Client, ctx context.Context) (FeatureStatus, error) {
			result, err := client.GetAccountInfoWithOpts(ctx, feature, &rpc.GetAccountInfoOpts{
				Encoding:   solana.EncodingBase64,
				Commitment: rpc.CommitmentFinalized,
			})
			if errors.Is(err, rpc.ErrNotFound) {
				return FeatureStatus{}, nil
			}
			if err != nil {
				return FeatureStatus{}, err
			}
			return decodeFeatureStatus(result.Value.Data.GetBinary())
		},
	})
}

// decodeFeatureStatus decodes the bincode layout of a feature account,
// Feature { activated_at: Option<u64> }: a 0 byte for None, or a 1 byte followed by a
// little-endian u64 slot.
func decodeFeatureStatus(data []byte) (FeatureStatus, error) {
	const someLen = 1 + 8
	if len(data) == 0 {
		return FeatureStatus{}, fmt.Errorf("feature account data is empty")
	}
	switch data[0] {
	case 0:
		return FeatureStatus{}, nil
	case 1:
		if len(data) < someLen {
			return FeatureStatus{}, fmt.Errorf("feature account data is %d bytes, want at least %d", len(data), someLen)
		}
		return FeatureStatus{Activated: true, ActivatedAt: binary.LittleEndian.Uint64(data[1:someLen])}, nil
	default:
		return FeatureStatus{}, fmt.Errorf("feature account data has invalid option tag %d", data[0])
	}
}

// VoteLag is a vote account's last vote together with the processed slot, both read from the
// same RPC node so the difference between them is meaningful.
type VoteLag struct {
	// Found is false when the node returned no vote account for the requested pubkey.
	Found          bool
	LastVote       uint64
	ActivatedStake uint64
	ProcessedSlot  uint64
}

// Slots returns how many slots the last vote is behind the processed slot.
func (v VoteLag) Slots() uint64 {
	if v.LastVote >= v.ProcessedSlot {
		return 0
	}
	return v.ProcessedSlot - v.LastVote
}

// GetVoteLag reads a vote account and the processed slot from one RPC node. Unstaked and
// delinquent accounts are included, so Found is false only when the node knows no such account.
func (c *Client) GetVoteLag(ctx context.Context, votePubkey solana.PublicKey) (VoteLag, error) {
	return executeWithRetry(c, ctx, rpcOperation[VoteLag]{
		name: "GetVoteLag",
		execute: func(client *rpc.Client, ctx context.Context) (VoteLag, error) {
			keepUnstakedDelinquents := true
			accounts, err := client.GetVoteAccounts(ctx, &rpc.GetVoteAccountsOpts{
				Commitment:              rpc.CommitmentProcessed,
				VotePubkey:              &votePubkey,
				KeepUnstakedDelinquents: &keepUnstakedDelinquents,
			})
			if err != nil {
				return VoteLag{}, err
			}
			// Read the slot after the vote accounts so it is never older than the bank lastVote came from.
			processedSlot, err := client.GetSlot(ctx, rpc.CommitmentProcessed)
			if err != nil {
				return VoteLag{}, err
			}
			lag := VoteLag{ProcessedSlot: processedSlot}
			for _, account := range slices.Concat(accounts.Current, accounts.Delinquent) {
				if account.VotePubkey.Equals(votePubkey) {
					lag.Found = true
					lag.LastVote = account.LastVote
					lag.ActivatedStake = account.ActivatedStake
					break
				}
			}
			return lag, nil
		},
	})
}

// ErrMethodNotFound is wrapped by errors from methods that no configured endpoint implements.
var ErrMethodNotFound = errors.New("RPC method not implemented by any endpoint")

// methodNotFoundCode is the JSON-RPC 2.0 error code for an unknown method.
const methodNotFoundCode = -32601

// allMethodNotFound reports whether every error is a JSON-RPC "method not found" response.
func allMethodNotFound(errs []error) bool {
	for _, err := range errs {
		var rpcErr *jsonrpc.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != methodNotFoundCode {
			return false
		}
	}
	return true
}

// isPermanentHTTPError returns true when the error signals that the endpoint actively
// refused the request (403 Forbidden, 429 Too Many Requests, 503 Service Unavailable).
// These are distinct from transient network errors: they won't resolve by immediately
// retrying the same URL, so the caller should impose a cooldown before trying it again.
//
// The library surfaces two concrete error types for this:
//   - *jsonrpc.RPCError  — server responded with a JSON-RPC error body (Code is the RPC code)
//   - *jsonrpc.HTTPError — server returned a non-JSON 4xx/5xx (Code is the HTTP status)
func isPermanentHTTPError(err error) bool {
	var rpcErr *jsonrpc.RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code == 403 || rpcErr.Code == 429 || rpcErr.Code == 503
	}
	var httpErr *jsonrpc.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Code == 403 || httpErr.Code == 429 || httpErr.Code == 503
	}
	return false
}

// extractErrorMessage extracts just the message from an RPC error
func extractErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	// First, try to use reflection to find the Message field directly
	// This works if the error is an RPCError or directly contains it
	v := reflect.ValueOf(err)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() == reflect.Struct {
		messageField := v.FieldByName("Message")
		if messageField.IsValid() && messageField.Kind() == reflect.String {
			message := messageField.String()
			if message != "" {
				return message
			}
		}
	}

	// If reflection didn't work, the error might be wrapped by fmt.Errorf
	// Parse the error string to extract the message from RPCError formatted by spew
	// Format: Message: (string) (len=17) "Node is unhealthy",
	errStr := err.Error()

	// Look for "Message:" followed by a quoted string
	msgIdx := strings.Index(errStr, "Message:")
	if msgIdx != -1 {
		// Find the quoted string after "Message:"
		// Skip past "Message:" and any type information like "(string) (len=17)"
		afterMsg := errStr[msgIdx+len("Message:"):]
		// Find the first quote
		quoteStart := strings.Index(afterMsg, `"`)
		if quoteStart != -1 {
			// Find the closing quote
			quoteEnd := strings.Index(afterMsg[quoteStart+1:], `"`)
			if quoteEnd != -1 {
				return afterMsg[quoteStart+1 : quoteStart+1+quoteEnd]
			}
		}
	}

	// Fall back to error string if we can't extract the message
	return errStr
}
