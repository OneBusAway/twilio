# Internal Metrics Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve Prometheus `/metrics` and the sensitive health endpoints from a separate internal-only HTTP server (default port 9119), leaving only public webhooks and status-only liveness/readiness probes on the internet-routed port.

**Architecture:** The app keeps its public Gin engine on `$PORT` but converts it to an `http.Server`. A second `http.Server` on `:9119` serves a separate Gin engine carrying `/metrics` + detailed/stats/config/cache. The public probes are slimmed to status-only bodies. Both servers shut down gracefully on SIGTERM.

**Tech Stack:** Go, Gin, prometheus/client_golang (`promhttp`), `net/http`.

## Global Constraints

- Before declaring done, these MUST pass: `make lint`, `make vet`, `make test`, `make fmt`.
- Default metrics port: `9119`. Configurable via `METRICS_PORT`.
- Accepted `METRICS_PORT` range: integer `1`–`65535`; anything else falls back to `9119` with a logged warning.
- The internal server listens on `:<port>` (all interfaces) — required for Render's private network. Never loopback.
- `METRICS_PORT` must differ from `PORT`; equal values are a fatal startup error.
- `metrics.Metrics.Handler()` must be called exactly once (it `MustRegister`s scrape counters and panics on a second call against the same registry).
- Follow existing patterns: config helpers mirror `parseEnvInt` in `main.go`; tests use the existing Gin `TestMode` + `httptest` style.

---

### Task 1: METRICS_PORT config helpers

**Files:**
- Modify: `main.go` (add `defaultMetricsPort`, `resolveMetricsPort`, `metricsPortConflicts` near the other `parseEnv*` helpers at the bottom)
- Test: `metrics_port_test.go` (new, `package main`)

**Interfaces:**
- Produces: `resolveMetricsPort(raw string) string` — returns a valid port string or `defaultMetricsPort`. `metricsPortConflicts(metricsPort, mainPort string) bool`. `const defaultMetricsPort = "9119"`.

- [ ] **Step 1: Write the failing test**

Create `metrics_port_test.go`:

```go
package main

import "testing"

func TestResolveMetricsPort(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"", "9119"},
		{"9119", "9119"},
		{"8000", "8000"},
		{"  9200 ", "9200"},
		{"abc", "9119"},
		{"0", "9119"},
		{"-5", "9119"},
		{"70000", "9119"},
	}
	for _, c := range cases {
		if got := resolveMetricsPort(c.raw); got != c.want {
			t.Errorf("resolveMetricsPort(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestMetricsPortConflicts(t *testing.T) {
	if !metricsPortConflicts("8080", "8080") {
		t.Error("expected conflict for equal ports")
	}
	if metricsPortConflicts("9119", "8080") {
		t.Error("did not expect conflict for differing ports")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run 'TestResolveMetricsPort|TestMetricsPortConflicts' -v`
Expected: FAIL — `undefined: resolveMetricsPort`.

- [ ] **Step 3: Write minimal implementation**

In `main.go`, add after `parseEnvInt`:

```go
const defaultMetricsPort = "9119"

// resolveMetricsPort validates a METRICS_PORT value. It accepts an integer in
// [1,65535] and returns it as a canonical string; empty, non-numeric, or
// out-of-range input falls back to defaultMetricsPort with a logged warning.
func resolveMetricsPort(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultMetricsPort
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 || parsed > 65535 {
		log.Printf("Invalid METRICS_PORT=%q, using default %s", raw, defaultMetricsPort)
		return defaultMetricsPort
	}
	return strconv.Itoa(parsed)
}

// metricsPortConflicts reports whether the internal metrics port collides with
// the public server port. The two http.Servers cannot share a port.
func metricsPortConflicts(metricsPort, mainPort string) bool {
	return metricsPort == mainPort
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run 'TestResolveMetricsPort|TestMetricsPortConflicts' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add main.go metrics_port_test.go
git commit -m "feat(metrics): add METRICS_PORT config helpers"
```

---

### Task 2: Slim public probe handlers

**Files:**
- Modify: `health/handlers.go` (add `statusCode`, `PublicLivenessHandler`, `PublicReadinessHandler`)
- Test: `health/handlers_test.go` (add one test)

**Interfaces:**
- Consumes: existing `h.manager.CheckHealthLiveness(ctx)` / `CheckHealthReadiness(ctx)` returning `HealthResponse` with a `.Status` field of type `Status`.
- Produces: `statusCode(s Status) int`; `(*Handler).PublicLivenessHandler(c *gin.Context)`; `(*Handler).PublicReadinessHandler(c *gin.Context)`. Bodies are `{"status": <status>}` only.

- [ ] **Step 1: Write the failing test**

Add to `health/handlers_test.go` (add `"strings"` to its imports):

```go
func TestPublicProbesOmitInternals(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager(WithTimeout(1*time.Second), WithSystemInfo(true))
	manager.AddChecker(&MockHealthChecker{
		name:   "secret-checker",
		result: CheckResult{Status: StatusHealthy, Message: "ok", Metadata: map[string]string{"secret": "x"}},
	})
	h := NewHandler(manager)
	router := gin.New()
	router.GET("/health", h.PublicLivenessHandler)
	// Readiness runs the registered checker, so its full body would include the
	// checker's "secret" metadata — proving the slim handler strips it.
	router.GET("/health/ready", h.PublicReadinessHandler)

	for _, path := range []string{"/health", "/health/ready"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))

		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: expected application/json, got %s", path, ct)
		}
		body := w.Body.String()
		for _, leak := range []string{"system_info", "checks", "goroutines", "go_version", "metadata", "secret"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s leaked %q: %s", path, leak, body)
			}
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s unmarshal: %v", path, err)
		}
		if resp["status"] != "healthy" {
			t.Errorf("%s status = %v, want healthy", path, resp["status"])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./health -run TestPublicProbesOmitInternals -v`
Expected: FAIL — `h.PublicLivenessHandler undefined`.

(`json` is already imported in `handlers_test.go`; only `strings` needs adding.)

- [ ] **Step 3: Write minimal implementation**

In `health/handlers.go`, add (the file already imports `context`, `net/http`, `time`, `gin`):

```go
// statusCode maps a health Status to the probe HTTP status code: healthy and
// degraded are "up" (200); unhealthy is 503.
func statusCode(s Status) int {
	if s == StatusUnhealthy {
		return http.StatusServiceUnavailable
	}
	return http.StatusOK
}

// PublicLivenessHandler is the internet-facing liveness probe. It runs the same
// checks as HealthHandler to derive the status code but returns a status-only
// body, so the public port never exposes SystemInfo, per-check Metadata, or
// error strings.
// GET /health
func (h *Handler) PublicLivenessHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	status := h.manager.CheckHealthLiveness(ctx).Status
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(statusCode(status), gin.H{"status": status})
}

// PublicReadinessHandler is the internet-facing readiness probe — status-only
// body, same rationale as PublicLivenessHandler.
// GET /health/ready
func (h *Handler) PublicReadinessHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	status := h.manager.CheckHealthReadiness(ctx).Status
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(statusCode(status), gin.H{"status": status})
}
```

(The explicit `Content-Type` matches the existing handlers and keeps `TestResponseHeaders` — which asserts an exact `application/json` — green; gin would otherwise append `; charset=utf-8`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./health -run TestPublicLivenessHandlerOmitsInternals -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add health/handlers.go health/handlers_test.go
git commit -m "feat(health): add status-only public probe handlers"
```

---

### Task 3: Split health routes into public and internal

**Files:**
- Modify: `health/handlers.go` (add `SetupPublicRoutes`, `SetupInternalRoutes`; convert `SetupRoutes` into a temporary shim)
- Modify: `health/handlers_test.go` (rework `setupTestHandler`, update probe tests, add leak-guard test, point the metrics-delegation test at the new method)

**Interfaces:**
- Consumes: `h.PublicLivenessHandler`, `h.PublicReadinessHandler` (Task 2); existing `h.DetailedHandler`, `h.StatsHandler`, `h.ConfigHandler`, `h.CacheHandler`, `h.rateLimitMiddleware()`.
- Produces: `(*Handler).SetupPublicRoutes(router *gin.Engine)`; `(*Handler).SetupInternalRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc)`. `SetupRoutes` temporarily delegates to both (removed in Task 4).

- [ ] **Step 1: Write the failing test**

In `health/handlers_test.go`, replace the `setupTestHandler` function with:

```go
func setupTestHandler() (*Handler, *gin.Engine) {
	gin.SetMode(gin.TestMode)

	manager := NewManager(WithTimeout(1*time.Second), WithSystemInfo(true))
	manager.AddChecker(&MockHealthChecker{
		name:   "test-checker",
		result: CheckResult{Status: StatusHealthy, Message: "Test is healthy"},
	})

	handler := NewHandler(manager)
	router := gin.New()
	handler.SetupPublicRoutes(router)
	handler.SetupInternalRoutes(router, func(c *gin.Context) {})

	return handler, router
}
```

Replace `TestHealthHandler` and `TestReadinessHandler` with slim-body versions:

```go
func TestPublicLivenessRouteIsSlim(t *testing.T) {
	_, router := setupTestHandler()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"checks", "system_info", "goroutines", "go_version", "metadata"} {
		if strings.Contains(body, leak) {
			t.Errorf("public /health leaked %q: %s", leak, body)
		}
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache, no-store, must-revalidate" {
		t.Errorf("expected no-cache header, got %s", cc)
	}
}

func TestPublicReadinessRouteIsSlim(t *testing.T) {
	_, router := setupTestHandler()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/health/ready", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "system_info") {
		t.Errorf("public /health/ready leaked system_info: %s", w.Body.String())
	}
}

func TestPublicRouterRejectsInternalRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(NewManager())
	public := gin.New()
	h.SetupPublicRoutes(public)

	for _, path := range []string{"/metrics", "/health/detailed", "/health/config", "/health/stats"} {
		w := httptest.NewRecorder()
		public.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s should be 404 on the public router, got %d", path, w.Code)
		}
	}
}
```

In `TestMetricsRouteDelegatesToProvidedHandler`, change the setup line from `handler.SetupRoutes(...)` to:

```go
	handler.SetupInternalRoutes(router, func(c *gin.Context) { c.String(200, "stubbed") })
```

Re-point the two remaining direct `SetupRoutes` callers so they don't depend on the shim (it is removed in Task 4). In `TestHealthHandler_UnhealthyStatus` (~line 248) and `TestHealthHandler_DegradedStatus` (~line 282), both of which hit the public `GET /health/ready`, replace:

```go
	handler.SetupRoutes(router, func(c *gin.Context) {})
```

with:

```go
	handler.SetupPublicRoutes(router)
```

Their assertions (status code `503`/`200` and `HealthResponse.Status`) still hold: the slim body `{"status":"unhealthy"}` unmarshals into `HealthResponse.Status` correctly.

After these edits, the only remaining `SetupRoutes` caller in the repo is `main.go` (via the shim), which Task 4 removes.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./health -run 'TestPublicLivenessRouteIsSlim|TestPublicRouterRejectsInternalRoutes' -v`
Expected: FAIL — `handler.SetupPublicRoutes undefined`.

- [ ] **Step 3: Write minimal implementation**

In `health/handlers.go`, replace the existing `SetupRoutes` function with these three:

```go
// SetupPublicRoutes registers the internet-facing endpoints: status-only
// liveness and readiness probes, rate-limited. Metrics and sensitive health
// detail are deliberately NOT registered here — see SetupInternalRoutes.
func (h *Handler) SetupPublicRoutes(router *gin.Engine) {
	rateLimited := router.Group("/")
	rateLimited.Use(h.rateLimitMiddleware())

	rateLimited.GET("/health", h.PublicLivenessHandler)
	rateLimited.GET("/health/ready", h.PublicReadinessHandler)
}

// SetupInternalRoutes registers the endpoints that must only be reachable on the
// internal metrics port: Prometheus metrics plus detailed/stats/config/cache.
// No rate limiting — the port is private and the scraper is trusted.
func (h *Handler) SetupInternalRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc) {
	router.GET("/metrics", metricsHandler)

	healthGroup := router.Group("/health")
	{
		healthGroup.GET("/detailed", h.DetailedHandler)
		healthGroup.GET("/stats", h.StatsHandler)
		healthGroup.GET("/config", h.ConfigHandler)
		healthGroup.GET("/cache", h.CacheHandler)
		healthGroup.DELETE("/cache", h.CacheHandler)
	}
}

// SetupRoutes is a temporary shim that keeps main.go compiling during the port
// split. It is removed once main wires the dedicated internal server.
func (h *Handler) SetupRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc) {
	h.SetupPublicRoutes(router)
	h.SetupInternalRoutes(router, metricsHandler)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./health -v`
Expected: PASS (all health tests, including the reworked probe tests and the new leak guard).

- [ ] **Step 5: Commit**

```bash
git add health/handlers.go health/handlers_test.go
git commit -m "feat(health): split routes into public and internal sets"
```

---

### Task 4: Wire the internal metrics server in main.go

**Files:**
- Modify: `main.go` (add `"net/http"` import; add `buildInternalEngine`; replace single-server wiring with dual `http.Server` + graceful shutdown; use `SetupPublicRoutes`; remove the `HealthResponseMiddleware` wiring; call `m.Handler()` once; update startup logging)
- Modify: `health/handlers.go` (delete the temporary `SetupRoutes` shim)
- Test: `main_test.go` (add `buildInternalEngine` test)

**Interfaces:**
- Consumes: `health.(*Handler).SetupInternalRoutes`, `health.(*Handler).SetupPublicRoutes`, `metrics.(*Metrics).Handler()`, `resolveMetricsPort`, `metricsPortConflicts`.
- Produces: `buildInternalEngine(metricsHandler gin.HandlerFunc, healthHandler *health.Handler) *gin.Engine`.

- [ ] **Step 1: Write the failing test**

Add to `main_test.go`. `main_test.go` currently imports `net/http`, `net/http/httptest`, and `github.com/gin-gonic/gin`, but **not** `oba-twilio/health` or `oba-twilio/metrics` — add both, or Task 4 won't compile.

```go
func TestBuildInternalEngineServesMetricsNotWebhooks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := metrics.New()
	// A registered, healthy checker so /health/detailed reports 200 (an empty
	// manager returns 503 because zero checks is treated as unhealthy).
	mgr := health.NewManager()
	mgr.AddChecker(&health.SystemHealthChecker{})
	hh := health.NewHandler(mgr)
	engine := buildInternalEngine(m.Handler(), hh)

	// /metrics is served on the internal engine.
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Errorf("/metrics: got %d, want 200", w.Code)
	}

	// Detailed health is internal-only and served here.
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("GET", "/health/detailed", nil))
	if w.Code != http.StatusOK {
		t.Errorf("/health/detailed: got %d, want 200", w.Code)
	}

	// Public webhooks are NOT on the internal engine.
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("POST", "/sms", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("/sms on internal engine: got %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestBuildInternalEngineServesMetricsNotWebhooks -v`
Expected: FAIL — `undefined: buildInternalEngine`.

- [ ] **Step 3: Add `buildInternalEngine` and resolve config in main**

In `main.go`, add `"net/http"` to the import block. Add this function near `buildInternalEngine`'s siblings (e.g. above `main`):

```go
// buildInternalEngine assembles the gin engine for the internal-only metrics
// server: Prometheus metrics plus the sensitive health endpoints, and none of
// the public webhook routes.
func buildInternalEngine(metricsHandler gin.HandlerFunc, healthHandler *health.Handler) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	healthHandler.SetupInternalRoutes(engine, metricsHandler)
	return engine
}
```

In `main`, just after `port` is resolved (around `main.go:48-50`), add:

```go
	metricsPort := resolveMetricsPort(os.Getenv("METRICS_PORT"))
	if metricsPortConflicts(metricsPort, port) {
		log.Fatalf("METRICS_PORT (%s) must differ from PORT (%s)", metricsPort, port)
	}
```

- [ ] **Step 4: Rewire the servers**

In `main.go`, in the block currently spanning the engine setup and server start (`main.go:248-309`), make these changes:

Replace the middleware/route registration block:

```go
	metricsHandler := m.Handler()

	r := gin.Default()

	// Add analytics middleware
	r.Use(middleware.NewAnalyticsMiddleware(analyticsManager, middleware.AnalyticsConfig{
		Enabled:  analyticsConfig.Enabled,
		HashSalt: analyticsConfig.HashSalt,
	}).Handler())

	// Add Prometheus metrics middleware (public engine only)
	r.Use(m.Middleware())

	// Add health check middleware
	r.Use(healthHandler.HealthMiddleware())

	// Application info endpoint
	r.GET("/", func(c *gin.Context) {
		coverage := obaClient.GetCoverageArea()
		response := gin.H{
			"message": locManager.BrandDisplayName() + " Twilio Integration",
			"status":  "healthy",
			"version": "1.0.0",
		}

		if coverage != nil {
			response["coverage"] = gin.H{
				"center_lat": coverage.CenterLat,
				"center_lon": coverage.CenterLon,
				"radius":     coverage.Radius,
			}
		}

		c.JSON(200, response)
	})

	// Public probes only; metrics + detailed health live on the internal server.
	healthHandler.SetupPublicRoutes(r)

	r.POST("/sms", smsHandler.HandleSMS)
	r.POST("/voice", voiceHandler.HandleVoiceStart)
	r.POST("/voice/find_stop", voiceHandler.HandleFindStop)
	r.POST("/voice/menu_action", voiceHandler.HandleVoiceMenuAction)

	internalEngine := buildInternalEngine(metricsHandler, healthHandler)
```

Notes for this step:
- Delete the old `r.Use(healthHandler.HealthResponseMiddleware())` line.
- Delete the old `healthHandler.SetupRoutes(r, m.Handler())` line.
- Keep `r.Use(healthHandler.HealthMiddleware())`.

Replace the startup log banner and the server-start goroutine (`main.go:291-309`) with:

```go
	publicSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	internalSrv := &http.Server{
		Addr:              ":" + metricsPort,
		Handler:           internalEngine,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("Starting public server on port %s", port)
	log.Printf("Starting internal metrics server on port %s", metricsPort)
	log.Printf("OneBusAway API: %s", obaBaseURL)
	log.Printf("Public endpoints: GET / , POST /sms , POST /voice* , GET /health , GET /health/ready")
	log.Printf("Internal endpoints (:%s): GET /metrics , GET /health/detailed , /health/stats , /health/config , /health/cache", metricsPort)

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := publicSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("Failed to start public server:", err)
		}
	}()
	go func() {
		if err := internalSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("Failed to start metrics server:", err)
		}
	}()
```

Then, in the existing shutdown block (after `<-sigChan` and the creation of `shutdownCtx`, `main.go:311-327`), add server shutdown before the analytics flush:

```go
	log.Println("Shutting down servers...")
	if err := publicSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Public server shutdown error: %v", err)
	}
	if err := internalSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Metrics server shutdown error: %v", err)
	}
```

- [ ] **Step 5: Remove the `SetupRoutes` shim**

In `health/handlers.go`, delete the temporary `SetupRoutes` function added in Task 3 (the one with the "temporary shim" comment).

- [ ] **Step 6: Build and run the full suite**

Run: `go build ./... && go test . ./health -v`
Expected: PASS, including `TestBuildInternalEngineServesMetricsNotWebhooks`. Build succeeds with no unused-import or undefined-symbol errors.

- [ ] **Step 7: Commit**

```bash
git add main.go main_test.go health/handlers.go
git commit -m "feat(metrics): serve metrics + sensitive health on internal :9119 server"
```

---

### Task 5: Remove dead HealthResponseMiddleware

**Files:**
- Modify: `health/handlers.go` (delete `HealthResponseMiddleware` and the `responseWriter` type it uses)
- Modify: `health/handlers_test.go` (delete `TestHealthResponseMiddleware`, the only test caller)

**Interfaces:**
- Consumes: nothing. After Task 4, `main.go` no longer wires `HealthResponseMiddleware`; the only remaining reference is its own test. The `responseWriter` type is used solely by it.

- [ ] **Step 1: Confirm the remaining references**

Run: `grep -rn "HealthResponseMiddleware\|responseWriter" --include=*.go .`
Expected: matches in `health/handlers.go` (the `responseWriter` struct + `Write` method + `HealthResponseMiddleware` function) and exactly one test, `health/handlers_test.go` `TestHealthResponseMiddleware` (~line 376). There must be **no** caller in `main.go`; if one remains, it was missed in Task 4 — remove it before continuing.

- [ ] **Step 2: Delete the dead code and its test**

In `health/handlers.go`, delete the `responseWriter` struct, its `Write` method, and the `HealthResponseMiddleware` function (`health/handlers.go:300-334`).

In `health/handlers_test.go`, delete the `TestHealthResponseMiddleware` function (~lines 376-397).

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./health`
Expected: PASS, no unused-symbol or compile errors.

- [ ] **Step 4: Full verification suite**

Run: `make fmt && make lint && make vet && make test`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add health/handlers.go health/handlers_test.go
git commit -m "refactor(health): remove dead HealthResponseMiddleware"
```

---

## Self-Review Notes

- **Spec coverage:** config helper + range + collision (Task 1); slim public probes / C1 (Task 2); route split + leak-guard test / I4 (Task 3); dual `http.Server` + graceful shutdown both / I3, single `Handler()` / I5 + I7, public-only middleware / I6, startup logging (Task 4); dead-middleware removal / N10 (Task 5); `ReadHeaderTimeout` / N6 (Task 4). N8 (`/` stays public) is a documented decision, no code change. Render port-detection confirmation (I2) is operational verification, handled at deploy time, not a code task.
- **Render docs check (I2):** at deploy time, confirm against current Render docs that an explicitly-set `PORT` remains the sole public port when a second port is bound, and that `9119` is not a Render-reserved port. No `render.yaml` change is expected.
