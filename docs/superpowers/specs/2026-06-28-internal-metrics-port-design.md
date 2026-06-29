# Internal Metrics Port — Design

**Date:** 2026-06-28
**Status:** Approved (incorporates architect review), pending implementation plan

## Problem

The Prometheus `/metrics` endpoint and the sensitive health endpoints
(`/health/detailed`, `/health/stats`, `/health/config`, `/health/cache`) are
currently registered on the **public** Gin server (`$PORT`) via
`health.SetupRoutes`. They are only rate-limited, so anyone who can reach the
Twilio webhook port can scrape internal metrics and read configuration/cache
state. This leaks sensitive operational information.

Compounding this: the probes the spec originally intended to keep public
(`/health`, `/health/ready`) are **not bare** — their JSON bodies include
`SystemInfo` (Go version, live goroutine count, heap/GC memory stats) and
per-checker `Metadata`/`Error`/`Message` strings (`health/manager.go:122-129`,
`health/checkers.go:40-45`). So `/health/ready` is effectively `/health/detailed`
minus dependency info. Hiding `/health/detailed` while leaving `/health/ready`
public would defeat the goal.

## Goal

Serve metrics and the sensitive health endpoints from a **separate, internal-only
HTTP server** on a dedicated port (default `9119`, configurable), leaving only
the public webhooks and **status-only** liveness/readiness probes on the
internet-routed port. This mirrors the existing OBA pattern of exposing JMX on an
internal port (`1234`) that Prometheus scrapes over the private network.

## Routing Split

**Public server** — `http.Server` on `$PORT` (Render-routed to the internet):

- `/` (app info — see N8 decision below)
- `POST /sms`
- `POST /voice`, `POST /voice/find_stop`, `POST /voice/menu_action`
- `GET /health` (liveness probe) — **status-only body**
- `GET /health/ready` (readiness probe) — **status-only body**

The public probes run the same underlying checks (so the HTTP status code still
reflects real health: 200 healthy/degraded, 503 unhealthy), but emit a minimal
body `{"status":"healthy|degraded|unhealthy"}` with **no** `Checks`,
`SystemInfo`, or `Metadata`. Render's health check and standard uptime probes
inspect only the status code, not the body.

**Internal server** — `http.Server` on `:9119` (all interfaces; private network only):

- `GET /metrics`
- `GET /health/detailed` (full liveness/readiness detail lives here)
- `GET /health/stats`
- `GET /health/config`
- `GET /health/cache`, `DELETE /health/cache` (cache inspection + destructive clear)

## Components / Changes

### 1. Configuration

- New `METRICS_PORT` env var, default `9119`.
- Parsed by a small, testable helper (mirrors the existing `parseEnvBool`
  pattern in `main.go`). Accepted range: integer `1`–`65535`. Anything else —
  non-numeric, `0`, negative, `> 65535` — logs a warning and falls back to
  `9119`.
- The server listens on `:<port>`, which binds all interfaces — **required** for
  Render's private network to reach it. Loopback would make it unreachable to
  Prometheus.
- **Fail-fast guard:** if `METRICS_PORT == PORT`, `log.Fatal` at startup with a
  clear message. The two servers cannot share a port, and an accidental
  collision would otherwise surface as an opaque "address already in use".

### 2. Slim public probe handlers (closes the C1 leak)

Add status-only public variants in the `health` package (e.g.
`PublicLivenessHandler` / `PublicReadinessHandler`, or a shared helper that runs
the existing `CheckHealthLiveness` / `CheckHealthReadiness` for the status code
but writes only `{"status": <status>}`). The existing full-detail
`HealthHandler` / `ReadinessHandler` bodies are no longer served on the public
port; equivalent detail remains available via `/health/detailed` on `:9119`.

### 3. `health.Handler` route split

Replace the single `SetupRoutes(router, metricsHandler)` with two methods:

- `SetupPublicRoutes(router *gin.Engine)` — registers `/health` and
  `/health/ready` using the **slim** handlers, keeping the existing rate-limiter
  group.
- `SetupInternalRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc)` —
  registers `/metrics` plus the detailed/stats/config/cache endpoints. **No rate
  limiting** here: the port is private and the scraper is trusted, and
  rate-limiting `/metrics` would be a scrape footgun.

### 4. Engine construction extracted for testability

Extract engine wiring into testable functions, e.g.
`buildPublicEngine(...) *gin.Engine` and
`buildInternalEngine(metricsHandler, healthHandler) *gin.Engine`, so the
**composed** engines can be asserted in tests (not just the per-method route
registration). This is what guards the real wiring in `main.go`.

### 5. Two `http.Server` instances + graceful shutdown

Convert **both** servers to `http.Server` (the public one moves off gin's
`r.Run()`):

- Each gets `Handler` = its gin engine and `ReadHeaderTimeout` set (cheap
  slowloris hygiene; the current `r.Run` has no timeouts).
- Launch each in its own goroutine via `ListenAndServe()`; treat any error other
  than `http.ErrServerClosed` as `log.Fatal`.
- On SIGINT/SIGTERM, call `Shutdown(shutdownCtx)` on **both** servers so
  in-flight Twilio webhooks drain too — not just scrapes. (The original spec left
  the public server on `r.Run`, which would abruptly drop in-flight webhook
  requests while gracefully draining idempotent scrapes — a backwards priority.
  Fixed here.)

### 6. Metrics middleware stays public-only

`m.Middleware()` is registered only on the public engine, so scrape traffic
doesn't pollute the `http_requests_total` / `http_request_duration_seconds`
series. (It is also a no-op on the internal engine anyway, since every internal
path is `/metrics` or `/health*`, which `Middleware()` already skips.)

### 7. Metrics handler registered exactly once

`m.Handler()` calls `promhttp.InstrumentMetricHandler`, which `MustRegister`s
`promhttp_metric_handler_requests_total` / `_in_flight` on the registry and
**panics** on a second call against the same registry. Call `m.Handler()` once
and pass it only to the internal routes. Tests that build internal engines across
multiple cases must use a fresh `metrics.New()` per case (or call `Handler()`
once).

### 8. Remove dead `HealthResponseMiddleware`

`HealthResponseMiddleware` (`health/handlers.go:311`) wraps every response in a
buffering writer that appends the full body into memory and then never reads it
(the `>= 500` branch is a placeholder no-op). It runs on every public request,
including the hot SMS/voice webhook path. Remove it and its `r.Use(...)` wiring.

### 9. Startup logging

Update the endpoint banner to show which routes are public vs. internal, and log
the `:9119` bind address.

## Testing

- `health` package:
  - **Slim public probes:** the public `/health` and `/health/ready` responses
    return the right status code but the body does **not** contain `goroutines`,
    `go_version`, `memory`, `Checks`, or `Metadata`. This is the core C1 guard.
  - `SetupPublicRoutes` returns **404 for `/metrics` and `/health/detailed`**.
  - `SetupInternalRoutes` serves `/metrics` plus detailed/stats/config/cache.
- **Composed-engine guard:** assert against the engine produced by
  `buildPublicEngine` (not just `SetupPublicRoutes`) so a future re-registration
  of internal routes on the public engine fails the test.
- `METRICS_PORT` parsing helper: default, valid override, and each invalid case
  (non-numeric, `0`, negative, `> 65535`) → fallback to `9119`.
- `METRICS_PORT == PORT` collision guard (test the validation function, not the
  fatal path).
- Use a fresh `metrics.New()` per internal-engine test case (see component 7).
- Update existing metrics/health tests for the split method signatures and the
  removed middleware.

## Error Handling

- Invalid `METRICS_PORT` → log warning, use `9119`.
- `METRICS_PORT == PORT` → `log.Fatal` at startup.
- Either `ListenAndServe` error (except `http.ErrServerClosed`) → `log.Fatal`.

## Render Notes

- No `render.yaml` change is strictly required for scraping. Render's private
  network reaches any port a service binds, addressed as
  `<service>-xxxx:9119/metrics` over `render-internal.com`, mirroring the
  JMX-on-`1234` setup.
- **Public-port routing:** Render routes the port named by the `PORT` env var (set
  by default on Render web services) to the internet; additional bound ports stay
  private. The public server binds exactly `$PORT`, and the `METRICS_PORT != PORT`
  guard ensures `:9119` is always the *other* port. The implementation plan should
  confirm against **current** Render docs (a) that an explicitly-set `PORT` is
  honored as the sole public port when a second port is bound, and (b) the current
  reserved-port list, since `9119` must not collide with a Render-reserved port.

## Decisions

- **N8 — `/` info endpoint stays public.** It exposes coverage center lat/lon
  (derivable from public OBA data) and a static `version: "1.0.0"`. Low
  sensitivity; left public intentionally rather than moved internal. Revisit only
  if version disclosure becomes a concern.

## Out of Scope (YAGNI)

- On/off switch or `METRICS_ENABLED` flag — the internal server is always on.
- Configurable bind address — fixed to all-interfaces for Render compatibility.
- Touching `HealthMiddleware` (slow-request logger) — only the dead
  `HealthResponseMiddleware` is removed.
