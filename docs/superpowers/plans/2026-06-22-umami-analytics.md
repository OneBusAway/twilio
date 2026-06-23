# Umami Analytics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Umami analytics provider to the existing analytics broker so the Twilio voice/SMS service emits events server-side to Umami's `/api/send` endpoint, per region-agnostic env config.

**Architecture:** The repo already has a pluggable analytics broker (`analytics.Manager` fans one `analytics.Event` out to registered providers on a worker pool + circuit breaker). We add a new `umami` provider implementing `analytics.Analytics`, wire it via env config alongside the existing `plausible` provider, and fill two missing voice-side event emissions. No call-site changes for SMS.

**Tech Stack:** Go, Gin, standard `net/http`, `encoding/json`. Test with `testing` + `net/http/httptest`. The existing project uses `github.com/stretchr/testify` (see `config_test.go`).

## Global Constraints

- Module path is `oba-twilio` (imports are `oba-twilio/...`).
- Provider must implement `analytics.Analytics`: `TrackEvent(ctx, Event) error`, `Flush(ctx) error`, `Close() error`.
- Analytics MUST be fire-and-forget: never block or break a call/SMS; swallow errors; fast timeout (default 5s).
- User-Agent MUST be browser-shaped (`Mozilla/5.0 (...)`) so Umami's `isbot` check does not drop the event.
- Umami returns the bot-drop as HTTP 200 + `{"beep":"boop"}`; a successful ingest body contains `cache`/`sessionId`/`visitId`. Treat `beep/boop` as failure.
- Wire format: `POST <ServerURL>/api/send`, `Content-Type: application/json`, body `{"type":"event","payload":{"website","hostname","url","name","data"}}`.
- Config is env-only: `UMAMI_ENABLED`, `UMAMI_URL`, `UMAMI_WEBSITE_ID`, `UMAMI_HOSTNAME` (optional), `UMAMI_HTTP_TIMEOUT` (optional).
- Before declaring done: `make lint`, `make vet`, `make test`, `make fmt` must all pass.
- Spec: `docs/superpowers/specs/2026-06-22-umami-analytics-design.md`.

---

### Task 1: Sentinel errors + Umami Config

**Files:**
- Modify: `analytics/errors.go`
- Create: `analytics/providers/umami/config.go`
- Test: `analytics/providers/umami/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `umami.Config{ServerURL, WebsiteID, Hostname string; HTTPTimeout time.Duration}`, `umami.DefaultConfig() Config`, `(*Config).Validate() error`, `umami.DefaultHostname` const; `analytics.ErrMissingServerURL`, `analytics.ErrMissingWebsiteID`.

- [ ] **Step 1: Add sentinel errors**

In `analytics/errors.go`, add to the `Configuration errors` block (after `ErrMissingDomain`):

```go
	ErrMissingServerURL = errors.New("analytics server URL is required")
	ErrMissingWebsiteID = errors.New("analytics website ID is required")
```

- [ ] **Step 2: Write the failing test**

Create `analytics/providers/umami/config_test.go`:

```go
package umami

import (
	"testing"
	"time"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
)

func TestConfigValidate(t *testing.T) {
	t.Run("missing server URL", func(t *testing.T) {
		c := Config{WebsiteID: "abc"}
		assert.ErrorIs(t, c.Validate(), analytics.ErrMissingServerURL)
	})

	t.Run("missing website ID", func(t *testing.T) {
		c := Config{ServerURL: "https://umami.example.com"}
		assert.ErrorIs(t, c.Validate(), analytics.ErrMissingWebsiteID)
	})

	t.Run("defaults applied", func(t *testing.T) {
		c := Config{ServerURL: "https://umami.example.com", WebsiteID: "abc"}
		require := assert.New(t)
		require.NoError(c.Validate())
		require.Equal(DefaultHostname, c.Hostname)
		require.Equal(5*time.Second, c.HTTPTimeout)
	})

	t.Run("explicit values preserved", func(t *testing.T) {
		c := Config{ServerURL: "https://umami.example.com", WebsiteID: "abc", Hostname: "api.example.org", HTTPTimeout: 2 * time.Second}
		assert.NoError(t, c.Validate())
		assert.Equal(t, "api.example.org", c.Hostname)
		assert.Equal(t, 2*time.Second, c.HTTPTimeout)
	})
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./analytics/providers/umami/ -run TestConfigValidate -v`
Expected: FAIL (package/`Config` does not exist).

- [ ] **Step 4: Write the implementation**

Create `analytics/providers/umami/config.go`:

```go
// Package umami provides a Umami analytics provider implementation for the
// analytics broker. It emits events server-side by POSTing to Umami's
// unauthenticated /api/send ingestion endpoint.
package umami

import (
	"time"

	"oba-twilio/analytics"
)

// DefaultHostname is the fallback payload hostname when none can be derived.
const DefaultHostname = "twilio.onebusaway.org"

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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./analytics/providers/umami/ -run TestConfigValidate -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add analytics/errors.go analytics/providers/umami/config.go analytics/providers/umami/config_test.go
git commit -m "feat(umami): add provider config and sentinel errors"
```

---

### Task 2: Pure mapping helpers

**Files:**
- Create: `analytics/providers/umami/mapping.go`
- Test: `analytics/providers/umami/mapping_test.go`

**Interfaces:**
- Consumes: `analytics` event-name constants.
- Produces: `pathForEvent(name string) string`, `channelForEvent(name string) string`, `buildUserAgent(event analytics.Event) string`, `sanitizeData(props map[string]interface{}) map[string]interface{}`, `isSuccessfulIngest(statusCode int, body []byte) bool`, const `maxDataValueLen = 256`.

- [ ] **Step 1: Write the failing test**

Create `analytics/providers/umami/mapping_test.go`:

```go
package umami

import (
	"strings"
	"testing"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
)

// emittedEventNames lists every event-name constant the app sends to Umami.
// Adding a new emitted event means adding it here; the test then forces a
// non-default path mapping so events never silently land in "/".
var emittedEventNames = []string{
	analytics.EventSMSRequest,
	analytics.EventSMSResponse,
	analytics.EventSMSDisambiguationPresent,
	analytics.EventSMSDisambiguationSelect,
	analytics.EventVoiceRequest,
	analytics.EventVoiceResponse,
	analytics.EventVoiceDTMFInput,
	analytics.EventVoiceMenuChoice,
	analytics.EventStopLookup,
	analytics.EventStopLookupSuccess,
	analytics.EventStopLookupFailure,
	analytics.EventLanguageDetected,
	analytics.EventErrorOccurred,
	analytics.EventAPILatency,
}

func TestPathForEventNeverDefaultForKnownEvents(t *testing.T) {
	for _, name := range emittedEventNames {
		assert.NotEqualf(t, "/", pathForEvent(name), "event %q must map to a non-default path", name)
	}
}

func TestPathForEvent(t *testing.T) {
	cases := map[string]string{
		analytics.EventSMSRequest:       "/sms",
		analytics.EventVoiceMenuChoice:  "/voice",
		analytics.EventStopLookup:       "/stop",
		analytics.EventErrorOccurred:    "/error",
		analytics.EventLanguageDetected: "/system",
		"unknown_event":                 "/",
	}
	for name, want := range cases {
		assert.Equalf(t, want, pathForEvent(name), "path for %q", name)
	}
}

func TestBuildUserAgentIsBrowserShaped(t *testing.T) {
	ua := buildUserAgent(analytics.Event{Name: analytics.EventSMSRequest, UserID: "0123456789abcdef"})
	assert.True(t, strings.HasPrefix(ua, "Mozilla/5.0 "), "UA must be browser-shaped: %q", ua)
	assert.Contains(t, ua, "SMS")
	assert.Contains(t, ua, "0123456789ab") // first 12 of the hash
	assert.NotContains(t, ua, "0123456789abcdef")

	// Bare product-token style (the rejected design) must NOT be produced.
	assert.NotRegexp(t, `^\w+/[\w()]*$`, ua)

	anon := buildUserAgent(analytics.Event{Name: analytics.EventVoiceRequest})
	assert.Contains(t, anon, "Voice")
	assert.Contains(t, anon, "anon")
}

func TestSanitizeData(t *testing.T) {
	long := strings.Repeat("x", 300)
	out := sanitizeData(map[string]interface{}{
		"keep_str":  "hello",
		"keep_int":  42,
		"keep_bool": true,
		"":          "drop_empty_key",
		"nil_val":   nil,
		"struct":    struct{ A int }{1},
		"long":      long,
	})
	assert.Equal(t, "hello", out["keep_str"])
	assert.Equal(t, 42, out["keep_int"])
	assert.Equal(t, true, out["keep_bool"])
	assert.NotContains(t, out, "")
	assert.NotContains(t, out, "nil_val")
	assert.Equal(t, "{1}", out["struct"]) // stringified
	assert.Len(t, out["long"], maxDataValueLen)
}

func TestIsSuccessfulIngest(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want bool
	}{
		{"beep boop", 200, `{"beep":"boop"}`, false},
		{"success cache", 200, `{"cache":"x","sessionId":"y","visitId":"z"}`, true},
		{"success sessionId only", 200, `{"sessionId":"y"}`, true},
		{"empty body", 200, ``, true},
		{"non-2xx", 500, `{"sessionId":"y"}`, false},
		{"non-json no positive field treated as failure", 200, `OK`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isSuccessfulIngest(tc.code, []byte(tc.body)))
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./analytics/providers/umami/ -run 'TestPath|TestBuild|TestSanitize|TestIsSuccessful' -v`
Expected: FAIL (helpers undefined).

- [ ] **Step 3: Write the implementation**

Create `analytics/providers/umami/mapping.go`:

```go
package umami

import (
	"bytes"
	"fmt"
	"strings"

	"oba-twilio/analytics"
)

// maxDataValueLen caps string values forwarded in the Umami "data" object.
// The SMS query property is uncontrolled user input, so it is truncated.
const maxDataValueLen = 256

// pathForEvent maps an analytics event name to a Umami url path so dashboards
// group sensibly. Known events never return the "/" default.
func pathForEvent(name string) string {
	switch {
	case strings.HasPrefix(name, "sms_"):
		return "/sms"
	case strings.HasPrefix(name, "voice_"):
		return "/voice"
	case strings.HasPrefix(name, "stop_lookup"):
		return "/stop"
	case strings.HasPrefix(name, "error"):
		return "/error"
	case name == analytics.EventLanguageDetected || name == analytics.EventAPILatency:
		return "/system"
	default:
		return "/"
	}
}

// channelForEvent derives the Twilio channel from the event name.
func channelForEvent(name string) string {
	switch {
	case strings.HasPrefix(name, "sms_"):
		return "SMS"
	case strings.HasPrefix(name, "voice_"):
		return "Voice"
	default:
		return "Server"
	}
}

// buildUserAgent returns a browser-shaped, per-caller User-Agent. It must lead
// with "Mozilla/5.0 " so Umami's isbot check does not drop the event.
func buildUserAgent(event analytics.Event) string {
	id := event.UserID
	switch {
	case id == "":
		id = "anon"
	case len(id) > 12:
		id = id[:12]
	}
	return fmt.Sprintf("Mozilla/5.0 (OneBusAway-Twilio; %s; %s) Server/1.0", channelForEvent(event.Name), id)
}

// sanitizeData keeps JSON-friendly types, stringifies the rest, truncates long
// strings, and drops empty keys / nil values.
func sanitizeData(props map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(props))
	for k, v := range props {
		if k == "" || v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			out[k] = truncate(val)
		case bool,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64:
			out[k] = val
		default:
			out[k] = truncate(fmt.Sprintf("%v", val))
		}
	}
	return out
}

func truncate(s string) string {
	if len(s) > maxDataValueLen {
		return s[:maxDataValueLen]
	}
	return s
}

// isSuccessfulIngest reports whether Umami actually ingested the event. Umami
// returns the isbot drop as HTTP 200 + {"beep":"boop"}, so the body must be
// inspected. Parsing is tolerant of non-JSON bodies.
func isSuccessfulIngest(statusCode int, body []byte) bool {
	if statusCode < 200 || statusCode >= 300 {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return true
	}
	if bytes.Contains(trimmed, []byte(`"beep"`)) {
		return false
	}
	return bytes.Contains(trimmed, []byte(`"cache"`)) ||
		bytes.Contains(trimmed, []byte(`"sessionId"`)) ||
		bytes.Contains(trimmed, []byte(`"visitId"`))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./analytics/providers/umami/ -run 'TestPath|TestBuild|TestSanitize|TestIsSuccessful' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add analytics/providers/umami/mapping.go analytics/providers/umami/mapping_test.go
git commit -m "feat(umami): add path/UA/sanitize/ingest helpers"
```

---

### Task 3: Payload conversion

**Files:**
- Create: `analytics/providers/umami/payload.go`
- Test: `analytics/providers/umami/payload_test.go`

**Interfaces:**
- Consumes: `umami.Config`, helpers from Task 2, `analytics.Event`.
- Produces: types `payload{Type string; Payload payloadBody}` and `payloadBody{Website, Hostname, URL, Name string; Data map[string]interface{}}` (JSON tags below), and `convertEvent(cfg Config, event analytics.Event) payload`.

- [ ] **Step 1: Write the failing test**

Create `analytics/providers/umami/payload_test.go`:

```go
package umami

import (
	"encoding/json"
	"testing"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertEvent(t *testing.T) {
	cfg := Config{ServerURL: "https://umami.example.com", WebsiteID: "web-uuid", Hostname: "api.example.org"}
	ev := analytics.Event{
		Name:       analytics.EventSMSRequest,
		UserID:     "hashed-user",
		SessionID:  "sess-1",
		Properties: map[string]interface{}{"language": "en-US", "query": "75403"},
	}

	p := convertEvent(cfg, ev)
	assert.Equal(t, "event", p.Type)
	assert.Equal(t, "web-uuid", p.Payload.Website)
	assert.Equal(t, "api.example.org", p.Payload.Hostname)
	assert.Equal(t, "/sms", p.Payload.URL)
	assert.Equal(t, "sms_request", p.Payload.Name)
	assert.Equal(t, "en-US", p.Payload.Data["language"])
	assert.Equal(t, "75403", p.Payload.Data["query"])
	assert.Equal(t, "hashed-user", p.Payload.Data["user_id"])
	assert.Equal(t, "sess-1", p.Payload.Data["session_id"])
}

func TestConvertEventOmitsEmptyData(t *testing.T) {
	cfg := Config{ServerURL: "https://umami.example.com", WebsiteID: "web-uuid", Hostname: "api.example.org"}
	ev := analytics.Event{Name: analytics.EventVoiceRequest}

	p := convertEvent(cfg, ev)
	b, err := json.Marshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"data"`)
	assert.Contains(t, string(b), `"name":"voice_request"`)
	assert.Contains(t, string(b), `"url":"/voice"`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./analytics/providers/umami/ -run TestConvertEvent -v`
Expected: FAIL (`convertEvent` undefined).

- [ ] **Step 3: Write the implementation**

Create `analytics/providers/umami/payload.go`:

```go
package umami

import "oba-twilio/analytics"

// payload is the top-level Umami /api/send request body.
type payload struct {
	Type    string      `json:"type"`
	Payload payloadBody `json:"payload"`
}

// payloadBody mirrors Umami's event payload. Name is always set (events-only);
// Data is omitted when empty.
type payloadBody struct {
	Website  string                 `json:"website"`
	Hostname string                 `json:"hostname"`
	URL      string                 `json:"url"`
	Name     string                 `json:"name,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// convertEvent maps an analytics.Event to a Umami payload.
func convertEvent(cfg Config, event analytics.Event) payload {
	data := sanitizeData(event.Properties)
	if event.UserID != "" {
		data["user_id"] = event.UserID
	}
	if event.SessionID != "" {
		data["session_id"] = event.SessionID
	}

	body := payloadBody{
		Website:  cfg.WebsiteID,
		Hostname: cfg.Hostname,
		URL:      pathForEvent(event.Name),
		Name:     event.Name,
	}
	if len(data) > 0 {
		body.Data = data
	}

	return payload{Type: "event", Payload: body}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./analytics/providers/umami/ -run TestConvertEvent -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add analytics/providers/umami/payload.go analytics/providers/umami/payload_test.go
git commit -m "feat(umami): add event-to-payload conversion"
```

---

### Task 4: Provider (HTTP emit)

**Files:**
- Create: `analytics/providers/umami/umami.go`
- Test: `analytics/providers/umami/umami_test.go`

**Interfaces:**
- Consumes: `umami.Config`, `convertEvent`, `buildUserAgent`, `isSuccessfulIngest`, `analytics.Event`, `analytics.ErrProviderClosed`.
- Produces: `umami.NewProvider(Config) (*Provider, error)`; `(*Provider)` implements `analytics.Analytics` (`TrackEvent`/`Flush`/`Close`).

- [ ] **Step 1: Write the failing test**

Create `analytics/providers/umami/umami_test.go`:

```go
package umami

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEvent() analytics.Event {
	return analytics.Event{
		ID:         "evt-1",
		Name:       analytics.EventSMSRequest,
		Timestamp:  time.Now().UTC(),
		Version:    1,
		UserID:     "hashed-user-id",
		Properties: map[string]interface{}{"language": "en-US"},
	}
}

func TestTrackEventPostsCorrectRequest(t *testing.T) {
	var gotUA, gotPath string
	var gotBody payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body) // fully consume the request body
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cache":"c","sessionId":"s","visitId":"v"}`))
	}))
	defer srv.Close()

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid", Hostname: "api.example.org"})
	require.NoError(t, err)

	require.NoError(t, p.TrackEvent(context.Background(), newTestEvent()))
	assert.Equal(t, "/api/send", gotPath)
	assert.Contains(t, gotUA, "Mozilla/5.0 ")
	assert.Equal(t, "web-uuid", gotBody.Payload.Website)
	assert.Equal(t, "/sms", gotBody.Payload.URL)
	assert.Equal(t, "sms_request", gotBody.Payload.Name)
}

func TestTrackEventTreatsBeepBoopAsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"beep":"boop"}`))
	}))
	defer srv.Close()

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid"})
	require.NoError(t, err)
	assert.Error(t, p.TrackEvent(context.Background(), newTestEvent()))
}

func TestTrackEventNeverBlocksOnSlowServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hang until released
	}))
	defer srv.Close()
	defer close(release)

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid", HTTPTimeout: 100 * time.Millisecond})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- p.TrackEvent(context.Background(), newTestEvent()) }()

	select {
	case err := <-done:
		assert.Error(t, err) // timed out -> error, but returned promptly
	case <-time.After(2 * time.Second):
		t.Fatal("TrackEvent did not return within timeout")
	}
}

func TestCloseIsIdempotentlyGuarded(t *testing.T) {
	p, err := NewProvider(Config{ServerURL: "https://umami.example.com", WebsiteID: "web-uuid"})
	require.NoError(t, err)
	assert.NoError(t, p.Close())
	assert.ErrorIs(t, p.Close(), analytics.ErrProviderClosed)
	assert.ErrorIs(t, p.TrackEvent(context.Background(), newTestEvent()), analytics.ErrProviderClosed)
}

func TestServerDrainsRequestBody(t *testing.T) {
	var ok int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err == nil {
			atomic.StoreInt32(&ok, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid"})
	require.NoError(t, err)
	_ = p.TrackEvent(context.Background(), newTestEvent())
	assert.Equal(t, int32(1), atomic.LoadInt32(&ok))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./analytics/providers/umami/ -run 'TestTrackEvent|TestClose|TestServerDrains' -v`
Expected: FAIL (`NewProvider` undefined).

- [ ] **Step 3: Write the implementation**

Create `analytics/providers/umami/umami.go`:

```go
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

	endpoint := strings.TrimRight(p.config.ServerURL, "/") + "/api/send"
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./analytics/providers/umami/ -v`
Expected: PASS (all umami tests).

- [ ] **Step 5: Commit**

```bash
git add analytics/providers/umami/umami.go analytics/providers/umami/umami_test.go
git commit -m "feat(umami): add provider HTTP emit with fire-and-forget POST"
```

---

### Task 5: Env config loading

**Files:**
- Modify: `analytics/config_loader.go`
- Test: `analytics/config_loader_test.go` (create if absent; otherwise add cases)

**Interfaces:**
- Consumes: env vars `UMAMI_*`, `ONEBUSAWAY_BASE_URL`.
- Produces: `loadUmamiConfig() (ProviderConfig, error)` and a `ProviderConfig{Name:"umami"}` appended in `loadProviderConfigs()`, with `Config` keys `server_url`, `website_id`, `hostname`, `http_timeout`; helper `resolveUmamiHostname(explicit, baseURL string) string`.

- [ ] **Step 1: Write the failing test**

Create `analytics/config_loader_test.go` (new file in package `analytics`):

```go
package analytics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveUmamiHostname(t *testing.T) {
	assert.Equal(t, "explicit.example.org", resolveUmamiHostname("explicit.example.org", "https://api.pugetsound.onebusaway.org"))
	assert.Equal(t, "api.pugetsound.onebusaway.org", resolveUmamiHostname("", "https://api.pugetsound.onebusaway.org"))
	assert.Equal(t, defaultUmamiHostname, resolveUmamiHostname("", ""))
	assert.Equal(t, defaultUmamiHostname, resolveUmamiHostname("", "::not a url::"))
}

func TestLoadUmamiConfig(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "")
		c, err := loadUmamiConfig()
		require.NoError(t, err)
		assert.False(t, c.Enabled)
	})

	t.Run("missing url errors when enabled", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "true")
		t.Setenv("UMAMI_URL", "")
		t.Setenv("UMAMI_WEBSITE_ID", "web-uuid")
		_, err := loadUmamiConfig()
		assert.Error(t, err)
	})

	t.Run("missing website id errors when enabled", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "true")
		t.Setenv("UMAMI_URL", "https://umami.example.com")
		t.Setenv("UMAMI_WEBSITE_ID", "")
		_, err := loadUmamiConfig()
		assert.Error(t, err)
	})

	t.Run("full config", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "true")
		t.Setenv("UMAMI_URL", "https://umami.example.com")
		t.Setenv("UMAMI_WEBSITE_ID", "web-uuid")
		t.Setenv("UMAMI_HOSTNAME", "")
		t.Setenv("ONEBUSAWAY_BASE_URL", "https://api.pugetsound.onebusaway.org")
		c, err := loadUmamiConfig()
		require.NoError(t, err)
		assert.True(t, c.Enabled)
		assert.Equal(t, "umami", c.Name)
		assert.Equal(t, "https://umami.example.com", c.Config["server_url"])
		assert.Equal(t, "web-uuid", c.Config["website_id"])
		assert.Equal(t, "api.pugetsound.onebusaway.org", c.Config["hostname"])
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./analytics/ -run 'TestResolveUmamiHostname|TestLoadUmamiConfig' -v`
Expected: FAIL (`loadUmamiConfig`/`resolveUmamiHostname` undefined).

- [ ] **Step 3: Add the import and helpers**

In `analytics/config_loader.go`, add `"net/url"` to the import block. Then add these functions at the end of the file:

```go
const defaultUmamiHostname = "twilio.onebusaway.org"

// resolveUmamiHostname picks the payload hostname: explicit override, else the
// host of ONEBUSAWAY_BASE_URL, else a fixed sentinel (never empty).
func resolveUmamiHostname(explicit, baseURL string) string {
	if explicit != "" {
		return explicit
	}
	if baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return defaultUmamiHostname
}

// loadUmamiConfig loads Umami provider configuration from the environment.
func loadUmamiConfig() (ProviderConfig, error) {
	enabled := false
	if enabledStr := os.Getenv("UMAMI_ENABLED"); enabledStr != "" {
		if parsed, err := strconv.ParseBool(enabledStr); err == nil {
			enabled = parsed
		}
	}

	config := ProviderConfig{
		Name:    "umami",
		Enabled: enabled,
		Config:  make(map[string]interface{}),
	}

	if !enabled {
		return config, nil
	}

	serverURL := os.Getenv("UMAMI_URL")
	if serverURL == "" {
		return config, fmt.Errorf("UMAMI_URL is required when Umami is enabled")
	}
	websiteID := os.Getenv("UMAMI_WEBSITE_ID")
	if websiteID == "" {
		return config, fmt.Errorf("UMAMI_WEBSITE_ID is required when Umami is enabled")
	}

	config.Config["server_url"] = serverURL
	config.Config["website_id"] = websiteID
	config.Config["hostname"] = resolveUmamiHostname(os.Getenv("UMAMI_HOSTNAME"), os.Getenv("ONEBUSAWAY_BASE_URL"))

	if httpTimeout := os.Getenv("UMAMI_HTTP_TIMEOUT"); httpTimeout != "" {
		if parsed, err := time.ParseDuration(httpTimeout); err == nil {
			config.Config["http_timeout"] = parsed
		}
	}

	return config, nil
}
```

- [ ] **Step 4: Register umami in `loadProviderConfigs`**

In `analytics/config_loader.go`, in `loadProviderConfigs()`, immediately before `return providers, nil`, add:

```go
	// Umami provider
	umamiConfig, err := loadUmamiConfig()
	if err != nil {
		if os.Getenv("UMAMI_ENABLED") == "true" {
			return nil, fmt.Errorf("umami configuration error: %w", err)
		}
		// If Umami is not enabled, ignore the error and skip it
	} else if umamiConfig.Enabled {
		providers = append(providers, umamiConfig)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./analytics/ -run 'TestResolveUmamiHostname|TestLoadUmamiConfig' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add analytics/config_loader.go analytics/config_loader_test.go
git commit -m "feat(umami): load provider config from environment"
```

---

### Task 6: Wire the provider into main.go

**Files:**
- Modify: `main.go` (import block; the provider-registration `for` loop, ~lines 86-136)

**Interfaces:**
- Consumes: `umami.NewProvider`, `umami.DefaultConfig`, `analyticsManager.RegisterProvider`.
- Produces: a registered `"umami"` provider when `UMAMI_ENABLED=true`.

- [ ] **Step 1: Add the import**

In `main.go`, add to the import block next to the existing plausible import:

```go
	"oba-twilio/analytics/providers/umami"
```

- [ ] **Step 2: Add the registration block**

In `main.go`, inside `for _, providerConfig := range analyticsConfig.Providers {`, after the closing brace of the existing `if providerConfig.Name == "plausible" && providerConfig.Enabled { ... }` block (and before the loop's closing brace), add:

```go
			if providerConfig.Name == "umami" && providerConfig.Enabled {
				serverURL, ok := providerConfig.Config["server_url"].(string)
				if !ok {
					log.Printf("Invalid umami server_url configuration")
					continue
				}
				websiteID, ok := providerConfig.Config["website_id"].(string)
				if !ok {
					log.Printf("Invalid umami website_id configuration")
					continue
				}

				umamiConfig := umami.DefaultConfig()
				umamiConfig.ServerURL = serverURL
				umamiConfig.WebsiteID = websiteID
				if hostname, ok := providerConfig.Config["hostname"].(string); ok {
					umamiConfig.Hostname = hostname
				}
				if httpTimeout, ok := providerConfig.Config["http_timeout"].(time.Duration); ok {
					umamiConfig.HTTPTimeout = httpTimeout
				}

				umamiProvider, err := umami.NewProvider(umamiConfig)
				if err != nil {
					log.Printf("Failed to create umami provider: %v", err)
					continue
				}

				if err := analyticsManager.RegisterProvider("umami", umamiProvider); err != nil {
					log.Printf("Failed to register umami provider: %v", err)
				}
			}
```

- [ ] **Step 3: Build to verify it compiles**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 4: Manual smoke check (optional but recommended)**

Run:
```bash
UMAMI_ENABLED=true UMAMI_URL=https://example.com UMAMI_WEBSITE_ID=test \
ANALYTICS_ENABLED=true ANALYTICS_HASH_SALT=devsalt ONEBUSAWAY_API_KEY=test \
go run . 2>&1 | grep -i "Analytics initialized"
```
Expected: a log line listing `umami` among the providers. (Ctrl-C to stop.)

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat(umami): register provider in main wiring"
```

---

### Task 7: Fill missing voice event emissions

**Files:**
- Modify: `analytics/events.go` (add `VoiceMenuChoiceEvent`)
- Modify: `middleware/analytics.go` (add `TrackVoiceMenuChoice`)
- Modify: `handlers/voice/menu_action.go` (emit menu choice; add `middleware` import)
- Modify: `handlers/voice/find_stop.go` (emit stop lookup in `respondForStopID`; add `time` + `middleware` imports)
- Test: `handlers/voice/analytics_emit_test.go`

**Interfaces:**
- Consumes: existing `middleware.AnalyticsManager`, `middleware.TrackStopLookup`, `analytics.HashPhoneNumber`, `analytics.EventVoiceMenuChoice`, `analytics.PropDTMFDigits`.
- Produces: `analytics.VoiceMenuChoiceEvent(hashedUserID, digits string) Event`; `middleware.TrackVoiceMenuChoice(ctx, manager, phoneNumber, salt, digits string)`.

- [ ] **Step 1: Write the failing test**

Create `handlers/voice/analytics_emit_test.go`:

```go
package voice

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"oba-twilio/analytics"
	"oba-twilio/localization"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForEvents polls the mock provider until it records at least n events or times out.
func waitForEvents(t *testing.T, mock *analytics.MockProvider, n int) []analytics.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mock.GetEventCount() >= n {
			return mock.GetEvents()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return mock.GetEvents()
}

func newTestVoiceHandler(t *testing.T) (*Handler, *analytics.Manager, *analytics.MockProvider) {
	t.Helper()
	locManager, err := localization.NewLocalizationManager([]string{"en-US"})
	require.NoError(t, err)

	cfg := analytics.DefaultConfig()
	cfg.Enabled = true
	cfg.HashSalt = "test-salt"
	mgr := analytics.NewManager(cfg)
	require.NoError(t, mgr.Start())
	mock := analytics.NewMockProvider()
	require.NoError(t, mgr.RegisterProvider("mock", mock))

	h := NewHandler(nil, locManager)
	h.SetAnalytics(mgr, "test-salt")
	return h, mgr, mock
}

func TestMenuActionEmitsMenuChoice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mgr, mock := newTestVoiceHandler(t)
	defer func() { _ = mgr.Close() }()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	form := url.Values{"From": {"+15551234567"}, "Digits": {"2"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/voice/menu_action", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	h.HandleVoiceMenuAction(c)

	events := waitForEvents(t, mock, 1)
	require.GreaterOrEqual(t, len(events), 1)
	var found bool
	for _, e := range events {
		if e.Name == analytics.EventVoiceMenuChoice {
			found = true
			assert.Equal(t, "2", e.Properties[analytics.PropDTMFDigits])
		}
	}
	assert.True(t, found, "expected a voice_menu_choice event")
}
```

Note: this test relies on `localization.NewLocalizationManager` and `analytics.MockProvider` (both exist). If `NewLocalizationManager`'s signature differs in this repo, adjust the call to match — verify with `grep -n "func NewLocalizationManager" localization/manager.go` before writing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./handlers/voice/ -run TestMenuActionEmitsMenuChoice -v`
Expected: FAIL (no `voice_menu_choice` event emitted).

- [ ] **Step 3: Add the event constructor**

In `analytics/events.go`, after `VoiceRequestEvent`, add:

```go
// VoiceMenuChoiceEvent creates an event for a voice menu (DTMF) selection.
func VoiceMenuChoiceEvent(hashedUserID, digits string) Event {
	event := NewEvent(EventVoiceMenuChoice, hashedUserID)
	event.Properties[PropDTMFDigits] = digits
	return event
}
```

- [ ] **Step 4: Add the middleware helper**

In `middleware/analytics.go`, after `TrackVoiceRequest`, add:

```go
// TrackVoiceMenuChoice tracks a voice menu (DTMF) selection.
func TrackVoiceMenuChoice(ctx context.Context, manager AnalyticsManager, phoneNumber, salt, digits string) {
	if manager == nil {
		return
	}

	userID := analytics.HashPhoneNumber(phoneNumber, salt)
	event := analytics.VoiceMenuChoiceEvent(userID, digits)

	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}
```

- [ ] **Step 5: Emit menu choice in the handler**

In `handlers/voice/menu_action.go`, add `"oba-twilio/middleware"` to the import block. Then, in `HandleVoiceMenuAction`, immediately after the line `log.Printf("Received voice menu action from %s: %s", req.From, req.Digits)`, add:

```go
	if h.analyticsManager != nil {
		middleware.TrackVoiceMenuChoice(c.Request.Context(), h.analyticsManager, req.From, h.analyticsHashSalt, req.Digits)
	}
```

- [ ] **Step 6: Run the menu test to verify it passes**

Run: `go test ./handlers/voice/ -run TestMenuActionEmitsMenuChoice -v`
Expected: PASS.

- [ ] **Step 7: Emit stop lookup for the voice path**

In `handlers/voice/find_stop.go`, add `"time"` and `"oba-twilio/middleware"` to the import block. Then replace the body of `respondForStopID` up to and including the `FindAllMatchingStops` call. The current code is:

```go
func (h *Handler) respondForStopID(c *gin.Context, req models.TwilioVoiceRequest, stopID string) {
	matchingStops, err := h.OBAClient.FindAllMatchingStops(stopID)
```

Replace with:

```go
func (h *Handler) respondForStopID(c *gin.Context, req models.TwilioVoiceRequest, stopID string) {
	startTime := time.Now()
	matchingStops, err := h.OBAClient.FindAllMatchingStops(stopID)
	latencyMS := time.Since(startTime).Milliseconds()

	if h.analyticsManager != nil {
		success := err == nil
		agencyName := ""
		if len(matchingStops) > 0 {
			agencyName = matchingStops[0].AgencyName
		}
		middleware.TrackStopLookup(c.Request.Context(), h.analyticsManager, req.From, stopID, agencyName, h.analyticsHashSalt, success, latencyMS)
	}
```

(Leave the rest of `respondForStopID` unchanged — the existing `if err != nil { ... }` handling and disambiguation logic follow.)

- [ ] **Step 8: Build and run the full voice package tests**

Run: `go build ./... && go test ./handlers/voice/ -v`
Expected: build succeeds; voice tests PASS.

- [ ] **Step 9: Commit**

```bash
git add analytics/events.go middleware/analytics.go handlers/voice/menu_action.go handlers/voice/find_stop.go handlers/voice/analytics_emit_test.go
git commit -m "feat(analytics): emit voice menu-choice and stop-lookup events"
```

---

### Task 8: Documentation

**Files:**
- Modify: `README.md` (environment variables / configuration section)
- Modify: `CLAUDE.md` (Environment Configuration section)

**Interfaces:**
- Consumes: nothing.
- Produces: documented `UMAMI_*` env vars.

- [ ] **Step 1: Document in CLAUDE.md**

In `CLAUDE.md`, under `## Environment Configuration` → `Optional:`, add:

```markdown
- `UMAMI_ENABLED` - Enable the Umami analytics provider (default: false)
- `UMAMI_URL` - Umami host; events POST to `<UMAMI_URL>/api/send` (required when enabled)
- `UMAMI_WEBSITE_ID` - Umami website UUID (required when enabled)
- `UMAMI_HOSTNAME` - `hostname` field in emitted events (default: host of `ONEBUSAWAY_BASE_URL`, else `twilio.onebusaway.org`)
- `UMAMI_HTTP_TIMEOUT` - Per-request timeout, Go duration (default: 5s)
```

- [ ] **Step 2: Document in README.md**

In `README.md`, find the environment-variable/configuration section (search for `ONEBUSAWAY_BASE_URL` or `PLAUSIBLE_`). Add a matching set of rows/entries for the five `UMAMI_*` variables above, in the same format the surrounding entries use. Also add a one-line note: "Analytics is API-driven (no JS tracker); the Umami provider POSTs events server-side with a browser-shaped User-Agent."

- [ ] **Step 3: Verify formatting**

Run: `git diff README.md CLAUDE.md`
Expected: clean additions consistent with surrounding style; no unrelated changes.

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs(umami): document UMAMI_* environment variables"
```

---

### Task 9: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Format**

Run: `make fmt`
Expected: no diff afterward (or only formatting normalization on new files).

- [ ] **Step 2: Lint**

Run: `make lint`
Expected: PASS (no issues).

- [ ] **Step 3: Vet**

Run: `make vet`
Expected: PASS.

- [ ] **Step 4: Test**

Run: `make test`
Expected: PASS (all packages, including the new `analytics/providers/umami` and `handlers/voice` tests).

- [ ] **Step 5: Commit any formatting changes**

```bash
git add -A
git commit -m "chore(umami): gofmt and final cleanup" || echo "nothing to commit"
```

---

## Notes for the executor

- The umami provider does NOT batch; `Flush` is intentionally a no-op. The broker's worker pool already runs `TrackEvent` off the request path.
- Do not add region-feed fetching — config is env-only by design (see spec "Out of scope").
- Keep the Plausible provider untouched.
- If a test references a helper signature that differs from this repo (e.g. `localization.NewLocalizationManager`), verify with `grep` and adapt the test call — do not change production signatures to fit a test.
