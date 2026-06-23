// Package umami provides a Umami analytics provider implementation for the
// analytics broker. It emits events server-side by POSTing to Umami's
// unauthenticated /api/send ingestion endpoint.
package umami

import (
	"time"

	"oba-twilio/analytics"
)

// DefaultHostname is the fallback payload hostname when none can be derived.
// It aliases analytics.DefaultUmamiHostname, the single source of truth.
const DefaultHostname = analytics.DefaultUmamiHostname

// Config holds configuration for the Umami provider.
type Config struct {
	// ServerURL is the Umami host; events POST to <ServerURL>/api/send (required).
	ServerURL string

	// WebsiteID is the Umami website UUID events are keyed by (required).
	WebsiteID string

	// Hostname is the payload "hostname" field (defaults to DefaultHostname).
	Hostname string

	// HTTPTimeout bounds each POST (default 5s).
	HTTPTimeout time.Duration
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{HTTPTimeout: 5 * time.Second}
}

// Validate checks required fields and fills defaults.
func (c *Config) Validate() error {
	if c.ServerURL == "" {
		return analytics.ErrMissingServerURL
	}
	if c.WebsiteID == "" {
		return analytics.ErrMissingWebsiteID
	}
	if c.Hostname == "" {
		c.Hostname = DefaultHostname
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 5 * time.Second
	}
	return nil
}
