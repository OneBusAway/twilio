# Prometheus Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hand-rolled `/metrics` text builder with the official `prometheus/client_golang`, add HTTP + domain instrumentation, and expose standard `go_*`/`process_*` runtime metrics.

**Architecture:** A new `metrics/` package owns a custom `*prometheus.Registry`. Already-tracked client/session counters are surfaced via **bridge collectors** (custom `prometheus.Collector`s read at scrape time); new HTTP and interaction/stop-lookup metrics are instrumented directly (Gin middleware + handler call sites). The hand-rolled formatter leaves the `health` package; health *checks* stay.

**Tech Stack:** Go 1.24, Gin, `github.com/prometheus/client_golang`.

**Spec:** `docs/superpowers/specs/2026-06-28-prometheus-support-design.md`

## Global Constraints

- Module path: `oba-twilio`; Go version `1.24.2` (do not lower).
- Custom registry only (`prometheus.NewRegistry()`) — never the global default registry — to keep tests isolated.
- Label cardinality is bounded: `route` = `c.FullPath()` or `"unmatched"`; `method` = fixed HTTP-verb allow-list or `"unknown"`; `status` = numeric code string; `agency` = stop-ID prefix or `"none"`; `store` = `{sms, voice}`; `channel` = `{sms, voice}`; `outcome` = `{resolved, ambiguous, not_found, error}`; `result` = `{resolved, ambiguous, not_found}`.
- `RecordInteraction` / `RecordStopLookup` and any handler metrics hook must be **nil-safe** (no-op when metrics is nil).
- Metrics injected into handlers via a **setter** (`SetMetrics`), mirroring the existing `SetAnalytics` pattern — do not change constructor arity.
- `/metrics` stays public + rate-limited. No new env vars.
- All gates must pass before any task is "done": `make fmt`, `make vet`, `make lint`, `make test`.
- Commit after every task.

---

### Task 1: Add dependency + `metrics` package skeleton (registry, handler, runtime collectors)

**Files:**
- Modify: `go.mod`, `go.sum` (via `go get`)
- Create: `metrics/metrics.go`
- Create: `metrics/handler.go`
- Test: `metrics/metrics_test.go`

**Interfaces:**
- Produces:
  - `type Metrics struct { ... }` holding `reg *prometheus.Registry`.
  - `func New() *Metrics` — constructs registry, registers Go + process collectors.
  - `func (m *Metrics) Registry() *prometheus.Registry`
  - `func (m *Metrics) Handler() gin.HandlerFunc` — promhttp handler wrapped for Gin.

- [ ] **Step 1: Add the dependency**

Run:
```bash
go get github.com/prometheus/client_golang@latest
```
Expected: `go.mod` now lists `github.com/prometheus/client_golang` as a direct require.

- [ ] **Step 2: Write the failing test**

Create `metrics/metrics_test.go`:
```go
package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandlerExposesRuntimeMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()

	r := gin.New()
	r.GET("/metrics", m.Handler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"go_goroutines", "process_", "# TYPE"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./metrics/ -run TestHandlerExposesRuntimeMetrics -v`
Expected: FAIL (package/New undefined — build error).

- [ ] **Step 4: Write `metrics/metrics.go`**

```go
// Package metrics provides Prometheus instrumentation for the application:
// a custom registry, HTTP middleware, and bridge collectors that surface
// already-tracked client/session counters at scrape time.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics owns the Prometheus registry and all registered collectors.
type Metrics struct {
	reg *prometheus.Registry
}

// New creates a Metrics with a private registry and the standard Go runtime
// and process collectors registered.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &Metrics{reg: reg}
}

// Registry exposes the underlying registry (for tests and registration).
func (m *Metrics) Registry() *prometheus.Registry {
	return m.reg
}
```

- [ ] **Step 5: Write `metrics/handler.go`**

```go
package metrics

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns a gin.HandlerFunc that serves the registry in Prometheus
// exposition format. It also registers the standard promhttp scrape counters
// (promhttp_metric_handler_requests_total) on the same registry.
func (m *Metrics) Handler() gin.HandlerFunc {
	h := promhttp.InstrumentMetricHandler(
		m.reg,
		promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}),
	)
	return gin.WrapH(h)
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./metrics/ -run TestHandlerExposesRuntimeMetrics -v`
Expected: PASS.

- [ ] **Step 7: Gates + commit**

```bash
make fmt && go vet ./metrics/ && go test ./metrics/...
git add go.mod go.sum metrics/metrics.go metrics/handler.go metrics/metrics_test.go
git commit -m "feat(metrics): add client_golang registry, promhttp handler, runtime collectors"
```

---

### Task 2: HTTP request middleware

**Files:**
- Create: `metrics/http.go`
- Test: `metrics/http_test.go`

**Interfaces:**
- Consumes: `*Metrics` from Task 1.
- Produces:
  - `func (m *Metrics) Middleware() gin.HandlerFunc`
  - Registered series: `http_requests_total{method,route,status}` (counter), `http_request_duration_seconds{method,route}` (histogram).
  - Internal helper `sanitizeMethod(string) string`.

- [ ] **Step 1: Write the failing test**

Create `metrics/http_test.go`:
```go
package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func scrape(m *Metrics) string {
	r := gin.New()
	r.GET("/metrics", m.Handler())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	return w.Body.String()
}

func TestMiddlewareRecordsRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/voice/find_stop", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/voice/find_stop", nil))

	body := scrape(m)
	if !strings.Contains(body, `http_requests_total{method="GET",route="/voice/find_stop",status="200"} 1`) {
		t.Errorf("missing/incorrect http_requests_total series:\n%s", body)
	}
	if !strings.Contains(body, "http_request_duration_seconds_bucket") {
		t.Errorf("missing duration histogram")
	}
}

func TestMiddlewareUnmatchedAndUnknownMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PROPFIND", "/nope", nil))

	body := scrape(m)
	if !strings.Contains(body, `route="unmatched"`) {
		t.Errorf("expected route=unmatched, got:\n%s", body)
	}
	if !strings.Contains(body, `method="unknown"`) {
		t.Errorf("expected method=unknown, got:\n%s", body)
	}
}

func TestMiddlewareSkipsMetricsAndHealth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/metrics", m.Handler())
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })

	for _, p := range []string{"/metrics", "/health"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	}
	body := scrape(m)
	if strings.Contains(body, `route="/health"`) || strings.Contains(body, `route="/metrics"`) {
		t.Errorf("scrape/health traffic should be skipped:\n%s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./metrics/ -run TestMiddleware -v`
Expected: FAIL (`Middleware` undefined).

- [ ] **Step 3: Write `metrics/http.go`**

```go
package metrics

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

var httpMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {},
	"HEAD": {}, "OPTIONS": {}, "CONNECT": {}, "TRACE": {},
}

// sanitizeMethod bounds the method label to a fixed allow-list; anything else
// (including arbitrary attacker-supplied verbs) collapses to "unknown".
func sanitizeMethod(method string) string {
	m := strings.ToUpper(method)
	if _, ok := httpMethods[m]; ok {
		return m
	}
	return "unknown"
}

// Middleware records HTTP request counts and durations. It skips /metrics and
// /health* so scrape and probe traffic don't dominate the series.
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/metrics" || strings.HasPrefix(path, "/health") {
			c.Next()
			return
		}

		timer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			m.httpDuration.WithLabelValues(sanitizeMethod(c.Request.Method), route).Observe(v)
		}))
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		m.httpRequests.WithLabelValues(
			sanitizeMethod(c.Request.Method),
			route,
			strconv.Itoa(c.Writer.Status()),
		).Inc()
		timer.ObserveDuration()
	}
}
```

- [ ] **Step 4: Add the metric fields and registration to `metrics/metrics.go`**

Add fields to the `Metrics` struct:
```go
type Metrics struct {
	reg          *prometheus.Registry
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
}
```

In `New()`, after creating `reg` and before `return`, construct and register the HTTP metrics:
```go
	m := &Metrics{reg: reg}
	m.httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, route template, and status code.",
		},
		[]string{"method", "route", "status"},
	)
	m.httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method and route template.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
	reg.MustRegister(m.httpRequests, m.httpDuration)
	return m
```
Replace the old `return &Metrics{reg: reg}` line accordingly.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./metrics/ -v`
Expected: PASS (all middleware + Task 1 tests).

- [ ] **Step 6: Gates + commit**

```bash
make fmt && go vet ./metrics/ && go test ./metrics/...
git add metrics/http.go metrics/http_test.go metrics/metrics.go
git commit -m "feat(metrics): add HTTP request middleware with bounded labels"
```

---

### Task 3: OBA client bridge collector (+ circuit-breaker state accessor)

**Files:**
- Modify: `client/onebusaway.go` (add `CircuitBreakerState()` accessor)
- Create: `metrics/bridge_client.go`
- Test: `client/onebusaway_test.go` (add accessor test) — or existing client test file
- Test: `metrics/bridge_client_test.go`

**Interfaces:**
- Consumes: `client.OneBusAwayClient.GetMetrics() client.Metrics` (existing), new `client.OneBusAwayClient.CircuitBreakerState() int`.
- Produces:
  - `type clientSource interface { GetMetrics() client.Metrics; CircuitBreakerState() int }`
  - `func (m *Metrics) RegisterClientBridge(src clientSource)`
  - Series: `oba_api_requests_total`, `oba_api_errors_total`, `oba_validation_errors_total`, `oba_circuit_breaker_open_total` (counters), `oba_circuit_breaker_state` (gauge), `oba_cache_hits_total`, `oba_cache_misses_total` (counters), `oba_api_request_duration_seconds` (const histogram from sum/count).

- [ ] **Step 1: Write the failing accessor test**

Add to `client/onebusaway_test.go`:
```go
func TestCircuitBreakerStateAccessor(t *testing.T) {
	c := NewOneBusAwayClient("https://example.com", "test")
	if got := c.CircuitBreakerState(); got != int(CircuitClosed) {
		t.Errorf("expected closed (%d), got %d", CircuitClosed, got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./client/ -run TestCircuitBreakerStateAccessor -v`
Expected: FAIL (`CircuitBreakerState` undefined).

- [ ] **Step 3: Add the accessor to `client/onebusaway.go`**

On the `CircuitBreaker` type:
```go
// State returns the current circuit-breaker state under a read lock.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}
```

On `OneBusAwayClient` (use the existing field name for its circuit breaker — confirm via grep `circuitBreaker` in the struct):
```go
// CircuitBreakerState reports the live circuit-breaker state as an int
// (0=closed, 1=open, 2=half-open) for metrics export.
func (c *OneBusAwayClient) CircuitBreakerState() int {
	switch c.circuitBreaker.State() {
	case CircuitOpen:
		return 1
	case CircuitHalfOpen:
		return 2
	default:
		return 0
	}
}
```
> Before writing, run `grep -n "circuitBreaker\|CircuitBreaker" client/onebusaway.go` to confirm the field name on `OneBusAwayClient`; adjust `c.circuitBreaker` to match.

- [ ] **Step 4: Run to verify the accessor passes**

Run: `go test ./client/ -run TestCircuitBreakerStateAccessor -v`
Expected: PASS.

- [ ] **Step 5: Write the failing bridge test**

Create `metrics/bridge_client_test.go`:
```go
package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"oba-twilio/client"
)

type fakeClientSource struct {
	m     client.Metrics
	state int
}

func (f fakeClientSource) GetMetrics() client.Metrics { return f.m }
func (f fakeClientSource) CircuitBreakerState() int   { return f.state }

func TestClientBridgeEmitsSeries(t *testing.T) {
	src := fakeClientSource{
		m: client.Metrics{
			CacheHits: 7, CacheMisses: 3,
			APICallCount: 10, APIErrorCount: 2,
			ValidationErrors: 1, CircuitBreakerOpen: 4,
		},
		state: 1,
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(newClientCollector(src))

	expected := `
# HELP oba_api_requests_total Total OneBusAway API calls.
# TYPE oba_api_requests_total counter
oba_api_requests_total 10
# HELP oba_circuit_breaker_state Current circuit-breaker state (0=closed,1=open,2=half-open).
# TYPE oba_circuit_breaker_state gauge
oba_circuit_breaker_state 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"oba_api_requests_total", "oba_circuit_breaker_state"); err != nil {
		t.Error(err)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./metrics/ -run TestClientBridgeEmitsSeries -v`
Expected: FAIL (`newClientCollector` undefined).

- [ ] **Step 7: Write `metrics/bridge_client.go`**

```go
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"oba-twilio/client"
)

// clientSource is the read interface the client bridge needs.
type clientSource interface {
	GetMetrics() client.Metrics
	CircuitBreakerState() int
}

type clientCollector struct {
	src clientSource

	apiRequests   *prometheus.Desc
	apiErrors     *prometheus.Desc
	validationErr *prometheus.Desc
	cbOpen        *prometheus.Desc
	cbState       *prometheus.Desc
	cacheHits     *prometheus.Desc
	cacheMisses   *prometheus.Desc
	apiDuration   *prometheus.Desc
}

func newClientCollector(src clientSource) *clientCollector {
	return &clientCollector{
		src:           src,
		apiRequests:   prometheus.NewDesc("oba_api_requests_total", "Total OneBusAway API calls.", nil, nil),
		apiErrors:     prometheus.NewDesc("oba_api_errors_total", "Total OneBusAway API errors.", nil, nil),
		validationErr: prometheus.NewDesc("oba_validation_errors_total", "Total OneBusAway response validation errors.", nil, nil),
		cbOpen:        prometheus.NewDesc("oba_circuit_breaker_open_total", "Total times the circuit breaker opened.", nil, nil),
		cbState:       prometheus.NewDesc("oba_circuit_breaker_state", "Current circuit-breaker state (0=closed,1=open,2=half-open).", nil, nil),
		cacheHits:     prometheus.NewDesc("oba_cache_hits_total", "Total OneBusAway API cache hits.", nil, nil),
		cacheMisses:   prometheus.NewDesc("oba_cache_misses_total", "Total OneBusAway API cache misses.", nil, nil),
		apiDuration:   prometheus.NewDesc("oba_api_request_duration_seconds", "OneBusAway API request latency (sum/count derived from running totals).", nil, nil),
	}
}

func (c *clientCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.apiRequests
	ch <- c.apiErrors
	ch <- c.validationErr
	ch <- c.cbOpen
	ch <- c.cbState
	ch <- c.cacheHits
	ch <- c.cacheMisses
	ch <- c.apiDuration
}

func (c *clientCollector) Collect(ch chan<- prometheus.Metric) {
	m := c.src.GetMetrics()
	ch <- prometheus.MustNewConstMetric(c.apiRequests, prometheus.CounterValue, float64(m.APICallCount))
	ch <- prometheus.MustNewConstMetric(c.apiErrors, prometheus.CounterValue, float64(m.APIErrorCount))
	ch <- prometheus.MustNewConstMetric(c.validationErr, prometheus.CounterValue, float64(m.ValidationErrors))
	ch <- prometheus.MustNewConstMetric(c.cbOpen, prometheus.CounterValue, float64(m.CircuitBreakerOpen))
	ch <- prometheus.MustNewConstMetric(c.cbState, prometheus.GaugeValue, float64(c.src.CircuitBreakerState()))
	ch <- prometheus.MustNewConstMetric(c.cacheHits, prometheus.CounterValue, float64(m.CacheHits))
	ch <- prometheus.MustNewConstMetric(c.cacheMisses, prometheus.CounterValue, float64(m.CacheMisses))
	ch <- prometheus.MustNewConstHistogram(
		c.apiDuration,
		uint64(m.APICallCount),
		m.TotalResponseTime.Seconds(),
		nil, // no buckets: emits _sum, _count, and _bucket{le="+Inf"}
	)
}

// RegisterClientBridge registers a bridge collector that reads the OBA client's
// in-memory counters at scrape time.
func (m *Metrics) RegisterClientBridge(src clientSource) {
	m.reg.MustRegister(newClientCollector(src))
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test ./metrics/ ./client/ -v`
Expected: PASS.

- [ ] **Step 9: Gates + commit**

```bash
make fmt && go vet ./metrics/ ./client/ && go test ./metrics/... ./client/...
git add client/onebusaway.go client/onebusaway_test.go metrics/bridge_client.go metrics/bridge_client_test.go
git commit -m "feat(metrics): bridge OBA client counters + circuit-breaker state accessor"
```

---

### Task 4: Session-store bridge collector (+ handler store accessors)

**Files:**
- Modify: `handlers/voice.go` (add `SessionStore()` accessor on `VoiceHandler`)
- Create: `metrics/bridge_session.go`
- Test: `metrics/bridge_session_test.go`

**Interfaces:**
- Consumes: `common.ImprovedSessionStore.GetMetrics() *common.SessionMetrics` (existing); `smsHandler.SessionStore` (exported field, existing); new `voiceHandler.SessionStore()`.
- Produces:
  - `type sessionSource interface { GetMetrics() *common.SessionMetrics }`
  - `func (m *Metrics) RegisterSessionBridge(store string, src sessionSource)`
  - Series (all `{store}`-labelled): `session_store_active_sessions` (gauge), `session_store_cache_hits_total`, `session_store_cache_misses_total`, `session_store_evictions_total`, `session_store_expired_total`, `session_store_created_total` (counters).

- [ ] **Step 1: Add the voice store accessor (no test needed in isolation; covered by Task 7 wiring)**

In `handlers/voice.go`, add:
```go
// SessionStore exposes the underlying session store for metrics bridging.
func (h *VoiceHandler) SessionStore() *common.SessionStore {
	if h.Handler == nil {
		return nil
	}
	return h.Handler.SessionStore
}
```
> Confirm `common` is already imported in `handlers/voice.go`; if not, add `"oba-twilio/handlers/common"`.

- [ ] **Step 2: Write the failing bridge test**

Create `metrics/bridge_session_test.go`:
```go
package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"oba-twilio/handlers/common"
)

type fakeSessionSource struct{ sm common.SessionMetrics }

func (f fakeSessionSource) GetMetrics() *common.SessionMetrics { return &f.sm }

func TestSessionBridgeEmitsLabelledSeries(t *testing.T) {
	src := fakeSessionSource{sm: common.SessionMetrics{
		TotalSessions: 5, CacheHits: 9, CacheMisses: 1,
		Evictions: 2, ExpiredSessions: 3, CreatedSessions: 8,
	}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(newSessionCollector("sms", src))

	expected := `
# HELP session_store_active_sessions Active sessions in the store.
# TYPE session_store_active_sessions gauge
session_store_active_sessions{store="sms"} 5
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"session_store_active_sessions"); err != nil {
		t.Error(err)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./metrics/ -run TestSessionBridge -v`
Expected: FAIL (`newSessionCollector` undefined).

- [ ] **Step 4: Write `metrics/bridge_session.go`**

```go
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"oba-twilio/handlers/common"
)

type sessionSource interface {
	GetMetrics() *common.SessionMetrics
}

type sessionCollector struct {
	store string
	src   sessionSource

	active   *prometheus.Desc
	hits     *prometheus.Desc
	misses   *prometheus.Desc
	evicted  *prometheus.Desc
	expired  *prometheus.Desc
	created  *prometheus.Desc
}

func newSessionCollector(store string, src sessionSource) *sessionCollector {
	labels := []string{"store"}
	return &sessionCollector{
		store:   store,
		src:     src,
		active:  prometheus.NewDesc("session_store_active_sessions", "Active sessions in the store.", labels, nil),
		hits:    prometheus.NewDesc("session_store_cache_hits_total", "Session-store cache hits.", labels, nil),
		misses:  prometheus.NewDesc("session_store_cache_misses_total", "Session-store cache misses.", labels, nil),
		evicted: prometheus.NewDesc("session_store_evictions_total", "Sessions evicted.", labels, nil),
		expired: prometheus.NewDesc("session_store_expired_total", "Sessions expired.", labels, nil),
		created: prometheus.NewDesc("session_store_created_total", "Sessions created.", labels, nil),
	}
}

func (c *sessionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.active
	ch <- c.hits
	ch <- c.misses
	ch <- c.evicted
	ch <- c.expired
	ch <- c.created
}

func (c *sessionCollector) Collect(ch chan<- prometheus.Metric) {
	m := c.src.GetMetrics()
	if m == nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(m.TotalSessions), c.store)
	ch <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(m.CacheHits), c.store)
	ch <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(m.CacheMisses), c.store)
	ch <- prometheus.MustNewConstMetric(c.evicted, prometheus.CounterValue, float64(m.Evictions), c.store)
	ch <- prometheus.MustNewConstMetric(c.expired, prometheus.CounterValue, float64(m.ExpiredSessions), c.store)
	ch <- prometheus.MustNewConstMetric(c.created, prometheus.CounterValue, float64(m.CreatedSessions), c.store)
}

// RegisterSessionBridge registers a bridge for one session store, tagged with a
// store label ("sms" or "voice").
func (m *Metrics) RegisterSessionBridge(store string, src sessionSource) {
	m.reg.MustRegister(newSessionCollector(store, src))
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./metrics/ -v`
Expected: PASS.

- [ ] **Step 6: Gates + commit**

```bash
make fmt && go vet ./metrics/ ./handlers/ && go test ./metrics/... ./handlers/...
git add handlers/voice.go metrics/bridge_session.go metrics/bridge_session_test.go
git commit -m "feat(metrics): bridge session-store counters with store label"
```

---

### Task 5: Interaction + stop-lookup record methods, handler `SetMetrics`, instrument SMS handler

**Files:**
- Create: `metrics/interactions.go`
- Modify: `handlers/sms.go` (add `metrics` field, `SetMetrics`, record calls)
- Test: `metrics/interactions_test.go`
- Test: `handlers/sms_test.go` (add instrumentation assertions)

**Interfaces:**
- Consumes: `*Metrics`.
- Produces:
  - `func (m *Metrics) RecordInteraction(channel, outcome string)` — nil-safe.
  - `func (m *Metrics) RecordStopLookup(result, agency string)` — nil-safe.
  - Series: `interactions_total{channel,outcome}`, `stop_lookups_total{result,agency}` (counters).
  - SMS handler gains `metrics *metrics.Metrics` field + `func (h *SMSHandler) SetMetrics(m *metrics.Metrics)`.

- [ ] **Step 1: Write the failing record-method test**

Create `metrics/interactions_test.go`:
```go
package metrics

import (
	"strings"
	"testing"
)

func TestRecordInteractionAndStopLookup(t *testing.T) {
	m := New()
	m.RecordInteraction("sms", "resolved")
	m.RecordInteraction("sms", "resolved")
	m.RecordStopLookup("ambiguous", "1")

	body := scrape(m)
	if !strings.Contains(body, `interactions_total{channel="sms",outcome="resolved"} 2`) {
		t.Errorf("missing interactions_total:\n%s", body)
	}
	if !strings.Contains(body, `stop_lookups_total{agency="1",result="ambiguous"} 1`) {
		t.Errorf("missing stop_lookups_total:\n%s", body)
	}
}

func TestRecordNilSafe(t *testing.T) {
	var m *Metrics
	m.RecordInteraction("sms", "error") // must not panic
	m.RecordStopLookup("not_found", "none")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./metrics/ -run "TestRecord" -v`
Expected: FAIL (`RecordInteraction` undefined).

- [ ] **Step 3: Write `metrics/interactions.go`**

```go
package metrics

import "github.com/prometheus/client_golang/prometheus"

// (fields below are added to the Metrics struct in metrics.go — see Step 4.)

// RecordInteraction increments interactions_total. Nil-safe.
func (m *Metrics) RecordInteraction(channel, outcome string) {
	if m == nil || m.interactions == nil {
		return
	}
	m.interactions.WithLabelValues(channel, outcome).Inc()
}

// RecordStopLookup increments stop_lookups_total. Nil-safe.
func (m *Metrics) RecordStopLookup(result, agency string) {
	if m == nil || m.stopLookups == nil {
		return
	}
	m.stopLookups.WithLabelValues(result, agency).Inc()
}

func newInteractionMetrics(reg *prometheus.Registry) (*prometheus.CounterVec, *prometheus.CounterVec) {
	interactions := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "interactions_total",
			Help: "Total user interactions by channel and outcome.",
		},
		[]string{"channel", "outcome"},
	)
	stopLookups := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stop_lookups_total",
			Help: "Total stop lookups by result and matched agency prefix.",
		},
		[]string{"result", "agency"},
	)
	reg.MustRegister(interactions, stopLookups)
	return interactions, stopLookups
}
```

- [ ] **Step 4: Add the fields + wiring to `metrics/metrics.go`**

Add to the struct:
```go
	interactions *prometheus.CounterVec
	stopLookups  *prometheus.CounterVec
```
In `New()`, before `return m`:
```go
	m.interactions, m.stopLookups = newInteractionMetrics(reg)
```

- [ ] **Step 5: Run to verify record tests pass**

Run: `go test ./metrics/ -run "TestRecord" -v`
Expected: PASS.

- [ ] **Step 6: Wire metrics into the SMS handler**

In `handlers/sms.go`:
1. Add import `"oba-twilio/metrics"`.
2. Add field to `SMSHandler` struct: `metrics *metrics.Metrics`.
3. Add setter after the constructor:
```go
// SetMetrics attaches the Prometheus metrics holder. Safe to leave unset (the
// record calls are nil-safe).
func (h *SMSHandler) SetMetrics(m *metrics.Metrics) {
	h.metrics = m
}
```
4. Instrument the outcome branches in the stop-lookup flow (the block at `handlers/sms.go:146-204`). Add `agency := agencyPrefix(stopID, matchingStops)` helper usage and record calls. Insert immediately after the existing analytics `TrackStopLookup` block and within each outcome branch:

After the `if err != nil {` block's analytics tracking, before `return`:
```go
		h.metrics.RecordInteraction("sms", "error")
		h.metrics.RecordStopLookup("not_found", "none")
```
In the `if len(matchingStops) == 0 {` branch, before `return`:
```go
		h.metrics.RecordInteraction("sms", "not_found")
		h.metrics.RecordStopLookup("not_found", "none")
```
In the `if len(matchingStops) > 1 {` branch, before its `return` (after the disambiguation tracking):
```go
		h.metrics.RecordInteraction("sms", "ambiguous")
		h.metrics.RecordStopLookup("ambiguous", agencyPrefix(matchingStops[0].FullStopID))
```
At the single-stop path (after the `if len(matchingStops) > 1` block, before calling `getAndFormatArrivals...`):
```go
	h.metrics.RecordInteraction("sms", "resolved")
	h.metrics.RecordStopLookup("resolved", agencyPrefix(matchingStops[0].FullStopID))
```
5. Add the helper (in `handlers/sms.go` or a shared `handlers/common` file — keep it in `sms.go` for now):
```go
// agencyPrefix extracts the agency prefix from a full stop ID like "1_75403"
// → "1". Returns "none" when no prefix is present.
func agencyPrefix(fullStopID string) string {
	if i := strings.Index(fullStopID, "_"); i > 0 {
		return fullStopID[:i]
	}
	return "none"
}
```
> Confirm `strings` is imported in `handlers/sms.go`; it is used elsewhere, but verify.

- [ ] **Step 7: Write the failing handler instrumentation test**

Add to `handlers/sms_test.go` a test that drives a known stop ID through the handler with a mock OBA client returning multiple stops, sets metrics via `SetMetrics(metrics.New())`, and asserts the counter incremented. Use the existing mock-client test scaffolding in that file as the template (match its mock type and request-construction helpers):
```go
func TestSMSHandlerRecordsResolvedInteraction(t *testing.T) {
	m := metrics.New()
	h := newTestSMSHandler(t) // existing helper; adjust to match the file
	h.SetMetrics(m)
	// ... drive a single-match stop lookup through h.HandleSMS using the
	// file's existing mock OBA client returning exactly one matching stop ...

	if got := testutil.ToFloat64(m /* interactions vec via a small exported getter or scrape */); got != 1 {
		t.Errorf("expected one resolved interaction, got %v", got)
	}
}
```
> If the metrics vectors are unexported, assert by scraping: build a gin router with `m.Handler()` at `/metrics` and check the body contains `interactions_total{channel="sms",outcome="resolved"} 1`. Prefer the scrape approach to avoid exporting internals. Mirror whatever mock/request helpers `handlers/sms_test.go` already defines; do not invent new ones.

- [ ] **Step 8: Run to verify it fails, then passes**

Run: `go test ./handlers/ -run TestSMSHandlerRecords -v`
Expected: FAIL first (no record call / wiring), then PASS after Step 6 is complete.

- [ ] **Step 9: Gates + commit**

```bash
make fmt && go vet ./... && go test ./metrics/... ./handlers/...
git add metrics/interactions.go metrics/metrics.go metrics/interactions_test.go handlers/sms.go handlers/sms_test.go
git commit -m "feat(metrics): record SMS interactions and stop lookups"
```

---

### Task 6: Instrument the voice handler

**Files:**
- Modify: `handlers/voice.go` (add `SetMetrics` delegating to inner handler)
- Modify: `handlers/voice/handler.go` (add `metrics` field + `SetMetrics`)
- Modify: `handlers/voice/find_stop.go` (record calls at outcome branches)
- Test: `handlers/voice/find_stop_test.go` (add instrumentation assertion)

**Interfaces:**
- Consumes: `metrics.RecordInteraction`, `metrics.RecordStopLookup`, `agencyPrefix` (exported from `common` if shared — see note).
- Produces: voice handler records `interactions_total{channel="voice",...}` and `stop_lookups_total`.

- [ ] **Step 1: Add `metrics` field + setter to the inner voice handler**

In `handlers/voice/handler.go`:
1. Add import `"oba-twilio/metrics"`.
2. Add field `metrics *metrics.Metrics` to the inner `Handler` struct.
3. Add:
```go
func (h *Handler) SetMetrics(m *metrics.Metrics) { h.metrics = m }
```

- [ ] **Step 2: Delegate from the wrapper in `handlers/voice.go`**

```go
// SetMetrics attaches the Prometheus metrics holder to the inner handler.
func (h *VoiceHandler) SetMetrics(m *metrics.Metrics) {
	if h.Handler != nil {
		h.Handler.SetMetrics(m)
	}
}
```
> Add import `"oba-twilio/metrics"` to `handlers/voice.go`.

- [ ] **Step 3: Share `agencyPrefix`**

Move `agencyPrefix` from `handlers/sms.go` to `handlers/common/` as an exported helper to avoid duplication:
- Create `handlers/common/agency.go`:
```go
package common

import "strings"

// AgencyPrefix extracts the agency prefix from a full stop ID like "1_75403"
// → "1". Returns "none" when no prefix is present.
func AgencyPrefix(fullStopID string) string {
	if i := strings.Index(fullStopID, "_"); i > 0 {
		return fullStopID[:i]
	}
	return "none"
}
```
- In `handlers/sms.go`, delete the local `agencyPrefix` func and replace its call sites with `common.AgencyPrefix(...)`.

- [ ] **Step 4: Record at voice outcome branches in `handlers/voice/find_stop.go`**

Locate the lookup-result branches (mirror SMS: error / zero / multiple / single — the existing analytics `Track*` calls mark these sites). Add beside each:
- error path: `h.metrics.RecordInteraction("voice", "error")` + `h.metrics.RecordStopLookup("not_found", "none")`
- no matches: `h.metrics.RecordInteraction("voice", "not_found")` + `h.metrics.RecordStopLookup("not_found", "none")`
- multiple (disambiguation): `h.metrics.RecordInteraction("voice", "ambiguous")` + `h.metrics.RecordStopLookup("ambiguous", common.AgencyPrefix(stops[0].FullStopID))`
- single: `h.metrics.RecordInteraction("voice", "resolved")` + `h.metrics.RecordStopLookup("resolved", common.AgencyPrefix(stops[0].FullStopID))`
> Confirm the slice variable name in `find_stop.go` (grep `FindAllMatchingStops`) and the `common` import.

- [ ] **Step 5: Write the failing instrumentation test**

In `handlers/voice/find_stop_test.go`, mirror the file's existing mock setup; set `h.SetMetrics(metrics.New())`, drive a single-match lookup, and assert via scrape (router with `m.Handler()`) that the body contains `interactions_total{channel="voice",outcome="resolved"} 1`.

- [ ] **Step 6: Run to verify fail → pass**

Run: `go test ./handlers/voice/ -run Record -v`
Expected: FAIL first, PASS after wiring.

- [ ] **Step 7: Gates + commit**

```bash
make fmt && go vet ./... && go test ./handlers/...
git add handlers/voice.go handlers/voice/handler.go handlers/voice/find_stop.go handlers/voice/find_stop_test.go handlers/common/agency.go handlers/sms.go
git commit -m "feat(metrics): record voice interactions and stop lookups"
```

---

### Task 7: Wire into `main.go`; remove hand-rolled health metrics; update health route + tests

**Files:**
- Modify: `main.go` (construct `metrics.New()`, middleware, bridges, `SetMetrics`, pass handler to `SetupRoutes`)
- Modify: `health/handlers.go` (drop `MetricsHandler` body; `SetupRoutes` takes a `gin.HandlerFunc`; serve it on the rate-limited group)
- Modify: `health/metrics.go` (delete `formatPrometheusMetrics`, `formatMetricLine`, `GetPrometheusMetrics`, `MetricsCollector` if no longer referenced)
- Modify: `health/manager.go` (remove `metricsCollector` field/usage if unused after removal; keep `MetricsInfo`/`GetMetrics` only if still consumed)
- Modify/Delete: `health/metrics_test.go` (delete or rewrite)
- Modify: `health/handlers_test.go` (update the `Accept: application/json` / `MetricsInfo` assertion at ~line 126)

**Interfaces:**
- Consumes: everything produced in Tasks 1–6.
- Produces: a running server whose `/metrics` is served by `metrics.Handler()`, on the existing rate-limited route.

- [ ] **Step 1: Change `health.SetupRoutes` to accept the metrics handler**

In `health/handlers.go`, change the signature:
```go
func (h *Handler) SetupRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc) {
```
Replace the existing `rateLimited.GET("/metrics", h.MetricsHandler)` with:
```go
	rateLimited.GET("/metrics", metricsHandler)
```
Delete the `MetricsHandler` method (lines ~186-203).

- [ ] **Step 2: Remove the hand-rolled formatter from `health/metrics.go`**

Delete `GetPrometheusMetrics`, `formatPrometheusMetrics`, and `formatMetricLine`. Then run `grep -rn "MetricsCollector\|MetricsInfo\|GetPrometheusMetrics\|formatPrometheusMetrics\|UpdateMetrics\|metricsCollector" health/` to find remaining references. Delete `MetricsCollector` and its methods, and the `metricsCollector` field + calls in `health/manager.go`, **only if** nothing outside the deleted code (and outside tests) references them. Keep `MetricsInfo`/`Manager.GetMetrics` only if another non-test caller remains (per spec, they are consumed only by the removed handler — expect to delete them).

- [ ] **Step 3: Update `main.go` wiring**

1. Add import `"oba-twilio/metrics"`.
2. After handlers are constructed and configured (after the `SetAnalytics`/`SetArrivalFilterConfig` calls, ~line 210):
```go
	// Prometheus metrics
	m := metrics.New()
	m.RegisterClientBridge(obaClient)
	m.RegisterSessionBridge("sms", smsHandler.SessionStore)
	m.RegisterSessionBridge("voice", voiceHandler.SessionStore())
	smsHandler.SetMetrics(m)
	voiceHandler.SetMetrics(m)
```
3. Add the middleware near the other `r.Use(...)` calls:
```go
	r.Use(m.Middleware())
```
4. Update the `SetupRoutes` call to pass the handler:
```go
	healthHandler.SetupRoutes(r, m.Handler())
```
> `obaClient` already satisfies `clientSource` (has `GetMetrics()` and the new `CircuitBreakerState()`). `smsHandler.SessionStore` is the exported `*common.SessionStore` field; `voiceHandler.SessionStore()` is the accessor from Task 4.

- [ ] **Step 4: Fix the health tests**

- `health/metrics_test.go`: delete the file if all its tests target removed symbols; otherwise rewrite to cover only surviving behavior.
- `health/handlers_test.go` (~line 126): the test exercised `Accept: application/json` returning `MetricsInfo` JSON from `/metrics`. Replace it with a test that registers a stub `gin.HandlerFunc` via `SetupRoutes(r, stub)` and asserts the stub is served at `/metrics`:
```go
func TestMetricsRouteDelegatesToProvidedHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(/* existing manager constructor used elsewhere in this file */)
	h.SetupRoutes(r, func(c *gin.Context) { c.String(200, "stubbed") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 200 || w.Body.String() != "stubbed" {
		t.Fatalf("expected delegated handler, got %d %q", w.Code, w.Body.String())
	}
}
```
> Match `NewHandler`/manager construction to how the rest of `health/handlers_test.go` builds them.

- [ ] **Step 5: Build, run full suite**

Run: `go build ./... && go test ./...`
Expected: PASS (no references to deleted symbols; `/metrics` served by promhttp).

- [ ] **Step 6: Smoke-test the endpoint manually**

Run:
```bash
ONEBUSAWAY_API_KEY=test go run . &
sleep 2
curl -s localhost:8080/metrics | grep -E "go_goroutines|http_requests_total|oba_api_requests_total|session_store_active_sessions" | head
kill %1
```
Expected: standard exposition lines for runtime + app metrics present.

- [ ] **Step 7: Gates + commit**

```bash
make fmt && go vet ./... && golangci-lint run && go test ./...
git add main.go health/handlers.go health/metrics.go health/manager.go health/handlers_test.go health/metrics_test.go
git commit -m "feat(metrics): wire metrics package into server; remove hand-rolled formatter"
```

---

### Task 8: Documentation + final verification

**Files:**
- Modify: `README.md`, `.env.example`, `CLAUDE.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Update `README.md`**

Document the `/metrics` endpoint and the metric catalog (HTTP, OBA client, session, interactions, runtime). Note it is public + rate-limited, standard Prometheus exposition format, no new env vars. Replace any prior description of the hand-rolled metrics.

- [ ] **Step 2: Update `.env.example`**

No new variables are required; add a short comment noting `/metrics` exposes Prometheus metrics (no configuration needed). Only edit if `.env.example` references the old metrics behavior.

- [ ] **Step 3: Update `CLAUDE.md`**

Under "API Endpoints", confirm `GET /metrics` is described as "Prometheus metrics (client_golang)". Under Architecture, add a one-line note that `metrics/` owns the Prometheus registry, HTTP middleware, and bridge collectors.

- [ ] **Step 4: Final full verification**

Run:
```bash
make fmt && make vet && make lint && make test
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add README.md .env.example CLAUDE.md
git commit -m "docs(metrics): document Prometheus /metrics endpoint and catalog"
```

---

## Self-Review

**Spec coverage:**
- Replace hand-rolled with client_golang → Tasks 1, 7.
- HTTP metrics (bounded labels, method sanitize, skip scrape/health) → Task 2.
- OBA client bridge + const histogram + CB state accessor (locked) → Task 3.
- Session bridge (multiple stores, `store` label, accessors) → Task 4.
- Interactions + stop-lookups (nil-safe, setter injection) → Tasks 5, 6.
- Runtime `go_*`/`process_*` → Task 1.
- Clean break: drop JSON branch, delete formatter, update health tests → Task 7.
- Docs → Task 8.
- All four `make` gates → every task + Task 8.

**Placeholder scan:** Test bodies in Tasks 5/6 intentionally defer to the existing handler-test mock helpers (which the implementer can see in-file) rather than inventing a parallel mock; every other step ships complete code. No "TODO"/"TBD" remain.

**Type consistency:** `Metrics` fields (`reg`, `httpRequests`, `httpDuration`, `interactions`, `stopLookups`) defined in Task 1/2/5 and used consistently. `clientSource`/`sessionSource` interfaces match the bridge constructors. `RegisterClientBridge`/`RegisterSessionBridge`/`SetMetrics`/`RecordInteraction`/`RecordStopLookup`/`Handler`/`Middleware`/`CircuitBreakerState`/`AgencyPrefix` names are used identically across tasks.
