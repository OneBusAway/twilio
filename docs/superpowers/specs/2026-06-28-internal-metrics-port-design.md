# Internal Metrics Port — Design

**Date:** 2026-06-28
**Status:** Approved, pending implementation

## Problem

The Prometheus `/metrics` endpoint and the sensitive health endpoints
(`/health/detailed`, `/health/stats`, `/health/config`, `/health/cache`) are
currently registered on the **public** Gin server (`$PORT`) via
`health.SetupRoutes`. They are only rate-limited, so anyone who can reach the
Twilio webhook port can scrape internal metrics and read configuration/cache
state. This leaks sensitive operational information.

## Goal

Serve metrics and the sensitive health endpoints from a **separate, internal-only
HTTP server** on a dedicated port (default `9119`, configurable), leaving only
the public webhooks and bare liveness/readiness probes on the internet-routed
port. This mirrors the existing OBA pattern of exposing JMX on an internal port
(`1234`) that Prometheus scrapes over the private network.

## Routing Split

**Public server** — existing Gin engine on `$PORT` (Render-routed to the internet):

- `/` (app info)
- `POST /sms`
- `POST /voice`, `POST /voice/find_stop`, `POST /voice/menu_action`
- `GET /health` (liveness probe)
- `GET /health/ready` (readiness probe)

Liveness/readiness stay public so Render health checks and external uptime
probes keep working.

**Internal server** — new engine on `0.0.0.0:9119` (private network only):

- `GET /metrics`
- `GET /health/detailed`
- `GET /health/stats`
- `GET /health/config`
- `GET /health/cache`, `DELETE /health/cache` (cache inspection + destructive clear)

## Components / Changes

### 1. Configuration

- New `METRICS_PORT` env var, default `9119`.
- Parsed by a small, testable helper (mirrors the existing `parseEnvBool`
  pattern in `main.go`): invalid input logs a warning and falls back to `9119`.
- The server listens on `:<port>`, which binds all interfaces — **required** for
  Render's private network to reach it. Loopback would make it unreachable to
  Prometheus.

### 2. `health.Handler` route split

Replace the single `SetupRoutes(router, metricsHandler)` with two methods:

- `SetupPublicRoutes(router *gin.Engine)` — registers `/health` and
  `/health/ready`, keeping the existing rate-limiter group.
- `SetupInternalRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc)` —
  registers `/metrics` plus the detailed/stats/config/cache endpoints. **No rate
  limiting** here: the port is private and the scraper is trusted, and
  rate-limiting `/metrics` would be a scrape footgun.

### 3. Internal server

- Built with `gin.New()` + `gin.Recovery()` (not `gin.Default()`, so 15-second
  scrapes don't flood the logs).
- Wrapped in `http.Server{Addr: ":" + metricsPort, Handler: internalEngine}`.
- The Prometheus HTTP-request middleware (`m.Middleware()`) stays **only** on the
  public engine, so scrape traffic doesn't pollute the `http_requests_total` /
  `http_request_duration_seconds` series — consistent with how `Middleware()`
  already skips `/metrics`.

### 4. Startup

- Launch the internal server in its own goroutine via `ListenAndServe()`.
- Treat any error other than `http.ErrServerClosed` as `log.Fatal` — a port
  conflict on `:9119` should fail the deploy loudly, same as the main server
  does today.
- The public `$PORT` server is unchanged (still `r.Run(":" + port)`).

### 5. Graceful shutdown

- Add `internalSrv.Shutdown(shutdownCtx)` to the existing SIGINT/SIGTERM block so
  the metrics listener drains alongside the analytics flush.

### 6. Startup logging

- Update the endpoint banner to show which routes are public vs. internal, and
  log the `:9119` bind address.

## Testing

- `health` package:
  - `SetupPublicRoutes` serves `/health` and `/health/ready` but returns **404
    for `/metrics` and `/health/detailed`** — the core security regression guard.
  - `SetupInternalRoutes` serves `/metrics` plus detailed/stats/config/cache.
- `METRICS_PORT` parsing helper: default, valid override, invalid-fallback.
- Update existing metrics/health tests for the split method signatures.

## Error Handling

- Invalid `METRICS_PORT` → log warning, use `9119`.
- Internal `ListenAndServe` error (except `http.ErrServerClosed`) → `log.Fatal`.

## Render Notes

- No `render.yaml` change is strictly required for scraping. Render's private
  network reaches any port a service binds (max 75 ports; `9119` is allowed —
  only `10000`, `18012`, `18013`, `19099` are reserved). Prometheus addresses the
  service as `<service>-xxxx:9119/metrics` over `render-internal.com`, mirroring
  the JMX-on-`1234` setup.
- Render routes only the one detected public `$PORT` to the internet, so `:9119`
  stays private automatically.

## Out of Scope (YAGNI)

- On/off switch or `METRICS_ENABLED` flag — the internal server is always on.
- Configurable bind address — fixed to all-interfaces for Render compatibility.
- Converting the main public server from `r.Run` to `http.Server`.
