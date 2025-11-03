package rpc

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gagliardetto/solana-go/rpc"
)

// Client represents an RPC client that can handle multiple URLs
type Client struct {
	// urls is a slice of URLs for load balancing
	urls []string
	// clients is a map of RPC clients, keyed by the rpc URL
	clients map[string]*rpc.Client
	// lastSuccessfulURL tracks the last URL that succeeded to avoid it for throttling protection
	lastSuccessfulURL string
	timeout           time.Duration
	logger            *log.Logger
}

// NewClient creates a new RPC client with one or more URLs
func NewClient(urls ...string) *Client {
	clients := make(map[string]*rpc.Client)
	for _, url := range urls {
		clients[url] = rpc.New(url)
	}
	return &Client{
		logger:            log.WithPrefix("rpc_client"),
		urls:              urls,
		clients:           clients,
		lastSuccessfulURL: "",
		timeout:           5 * time.Second, // Default timeout
	}
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

// getURLsToTry returns URLs to try with lastSuccessfulURL at the end for throttling protection
func (c *Client) getURLsToTry() []string {
	if len(c.urls) <= 1 || c.lastSuccessfulURL == "" {
		return c.urls
	}

	// Build list with lastSuccessfulURL at the end
	urlsToTry := make([]string, 0, len(c.urls))

	// Add all URLs except lastSuccessfulURL first
	for _, url := range c.urls {
		if url != c.lastSuccessfulURL {
			urlsToTry = append(urlsToTry, url)
		}
	}

	// Add lastSuccessfulURL at the end (as fallback)
	urlsToTry = append(urlsToTry, c.lastSuccessfulURL)

	return urlsToTry
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
			c.logger.Debug("method call failed", "method", op.name, "error", err, "rpc_url", url)
			errors = append(errors, err)
			continue
		}

		// Success! Update the last successful URL
		c.lastSuccessfulURL = url
		return result, nil
	}

	var zero T
	return zero, fmt.Errorf("method call failed on all RPC endpoints method: %s, attempted_urls: %v, errors: %v", op.name, attemptedURLs, errors)
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

// GetVoteAccounts gets the vote accounts from the first working RPC client

func (c *Client) GetVoteAccounts(ctx context.Context) (*rpc.GetVoteAccountsResult, error) {
	return executeWithRetry(c, ctx, rpcOperation[*rpc.GetVoteAccountsResult]{
		name: "GetVoteAccounts",
		execute: func(client *rpc.Client, ctx context.Context) (*rpc.GetVoteAccountsResult, error) {
			return client.GetVoteAccounts(ctx, &rpc.GetVoteAccountsOpts{
				Commitment: rpc.CommitmentProcessed,
			})
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
	return executeWithRetry(c, ctx, rpcOperation[string]{
		name: "GetHealth",
		execute: func(client *rpc.Client, ctx context.Context) (string, error) {
			return client.GetHealth(ctx)
		},
	})
}
