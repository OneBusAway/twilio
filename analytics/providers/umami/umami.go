package umami

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"oba-twilio/analytics"
)

// Provider implements analytics.Analytics for Umami. Each TrackEvent does a
// single synchronous POST; the broker's worker pool runs this off the request
// path, so no internal batching/goroutine is needed.
type Provider struct {
	config Config
	client *http.Client

	mu     sync.RWMutex
	closed bool
}

// NewProvider creates a Umami provider, validating config and applying defaults.
func NewProvider(config Config) (*Provider, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid umami config: %w", err)
	}
	return &Provider{
		config: config,
		client: &http.Client{Timeout: config.HTTPTimeout},
	}, nil
}

// TrackEvent POSTs a single event to <ServerURL>/api/send.
func (p *Provider) TrackEvent(ctx context.Context, event analytics.Event) error {
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return analytics.ErrProviderClosed
	}

	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid event for umami: %w", err)
	}

	body, err := json.Marshal(convertEvent(p.config, event))
	if err != nil {
		return fmt.Errorf("failed to marshal umami payload: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.config.HTTPTimeout)
	defer cancel()

	endpoint := strings.TrimSuffix(p.config.ServerURL, "/") + "/api/send"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create umami request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildUserAgent(event))

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("umami request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
		_ = resp.Body.Close()
	}()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if !isSuccessfulIngest(resp.StatusCode, respBody) {
		return fmt.Errorf("umami dropped event (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// Flush is a no-op; the provider buffers nothing.
func (p *Provider) Flush(ctx context.Context) error { return nil }

// Close marks the provider closed.
func (p *Provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return analytics.ErrProviderClosed
	}
	p.closed = true
	return nil
}
