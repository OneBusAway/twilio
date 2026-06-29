# Prometheus Support — Design Spec

**Date:** 2026-06-28
**Branch:** `prometheus`
**Status:** Approved design, pending implementation

## Summary

The OneBusAway Twilio integration currently exposes a `/metrics` endpoint backed
by a hand-rolled Prometheus text-format builder (`health/metrics.go`,
`formatPrometheusMetrics`). It produces technically valid but incomplete output
and has one genuine correctness bug. This project replaces that with the official
`github.com/prometheus/client_golang` library, ports the existing metrics to it,
gains the conventional `go_*`/`process_*` runtime metrics for free, and adds
HTTP-level and domain-level instrumentation.

### Problems with the current implementation

1. **Conditional series (correctness bug).** Several metrics
   (`dependency_requests_total`, `session_store_*`) are emitted only when their
   value is `> 0`. Prometheus best practice is to always expose a series
   initialized to 0; omission breaks `rate()`, shows "no data" instead of zero,
   and can be misread as a counter reset.
2. **Non-standard runtime metric names.** It hand-reimplements
   `system_goroutines_total`, `system_memory_alloc_bytes`, etc., instead of the
   conventional `go_goroutines`, `go_memstats_*`, `process_*` that every
   off-the-shelf Grafana dashboard expects.
3. **No HTTP request metrics** — request rate, latency, and status by route are
   not captured at all.
4. **Fragility.** Manual string concatenation with no label escaping will emit
   invalid output the first time a label contains a quote or newline.

## Decisions (locked)

| Decision | Choice |
| --- | --- |
| Scope | Replace hand-rolled with `client_golang` **and** add HTTP + app metrics |
| Migration | **Clean break** — standard metric names; drop the `Accept: application/json` branch on `/metrics`. JSON health data remains at `/health/detailed` and `/health/stats`. |
| App metrics | OBA API client, SMS/voice interactions, stop-lookup results, cache & session |
| Exposure | **Public, rate-limited** (keep current behavior); no auth, no separate port. Metric data carries no PII. |
| Structure | New `metrics/` package with a **custom registry** + dependency injection (Approach A) |

## Architecture

A new `metrics/` package owns a single `*prometheus.Registry` (custom, not the
global default). Two integration styles feed it:

1. **Bridge collectors (pull).** Custom `prometheus.Collector`s whose `Collect()`
   reads the *existing* in-memory counters — `client.GetMetrics()` and
   `sessionStore.GetMetrics()` — at scrape time and emits them as proper
   Prometheus series. No double counting; the OBA client needs no new dependency
   injected into it because it already maintains these counters.
2. **Direct instrumentation (push).** Metrics the app does not yet track:
   - HTTP requests via a Gin middleware.
   - SMS/voice interaction outcomes and stop-lookup results, incremented inside
     the handlers (at the same call sites that already emit analytics events).

### Why a custom registry

Using `prometheus.NewRegistry()` instead of the global default means each test
constructs a fresh registry, avoiding `MustRegister` duplicate-registration
panics and cross-test state bleed — the most common source of flakiness in Go
Prometheus code.

### Why bridge (not re-instrument) the client/session counters

The OBA client already maintains `CacheHits`, `CacheMisses`, `APICallCount`,
`APIErrorCount`, `TotalResponseTime`, `CircuitBreakerOpen`, `ValidationErrors`
via `client.GetMetrics()`. The session store maintains `TotalSessions`,
`CacheHits`, `CacheMisses`, `Evictions`, `ExpiredSessions`, `CreatedSessions`
via `GetMetrics()`. Reading these at scrape time avoids double-counting and
keeps client/handler call sites untouched for those metrics.

## Components

| File | Responsibility |
| --- | --- |
| `metrics/metrics.go` | Construct the registry; register `collectors.NewGoCollector()` + `collectors.NewProcessCollector()`; build and register HTTP + interaction collectors; expose a `*Metrics` holder with typed record methods. |
| `metrics/http.go` | Gin middleware recording HTTP request count + duration. |
| `metrics/bridge.go` | Custom `prometheus.Collector`s wrapping the OBA client and session store; map their existing fields to standard series at scrape time. |
| `metrics/handler.go` | `promhttp.HandlerFor(reg, promhttp.HandlerOpts{})` adapted to a `gin.HandlerFunc`. |
| `metrics/*_test.go` | Unit tests using `prometheus/testutil`. |

## Metrics catalog

### HTTP (direct, via middleware)
- `http_requests_total{method,route,status}` — counter.
- `http_request_duration_seconds{method,route}` — histogram (default buckets).

`route` is `c.FullPath()` (the route *template*, e.g. `/voice/find_stop`), never
the raw URL — this bounds label cardinality. Requests that match no route (404)
use the fixed label value `"unmatched"`.

`method` **must be sanitized to a fixed allow-list** (GET, POST, PUT, DELETE,
PATCH, HEAD, OPTIONS, CONNECT, TRACE); any other value maps to `"unknown"`.
Without this, an attacker sending arbitrary method strings to an unmatched path
(`route="unmatched"` is constant) drives unbounded `method` series — a
memory-exhaustion vector (cf. CVE-2022-21698). This mirrors `client_golang`'s own
`sanitizeMethod`. `status` is the numeric HTTP code rendered as a string (already
bounded).

### OBA client (bridged)
- `oba_api_requests_total` — counter (from `APICallCount`).
- `oba_api_errors_total` — counter (from `APIErrorCount`).
- `oba_api_request_duration_seconds` — histogram, see note below.
- `oba_validation_errors_total` — counter (from `ValidationErrors`).
- `oba_circuit_breaker_state` — gauge (0=closed, 1=open, 2=half-open).
- `oba_circuit_breaker_open_total` — counter (from `CircuitBreakerOpen`).

**Note on API duration:** the client only keeps `TotalResponseTime` and
`APICallCount`, not a distribution. The bridge emits this via
`prometheus.MustNewConstHistogram(desc, uint64(APICallCount),
TotalResponseTime.Seconds(), nil /* no buckets */)`. With a nil bucket map this
produces a valid `histogram` family — `_sum`, `_count`, and an implicit
`_bucket{le="+Inf"}` — so `rate(_sum)/rate(_count)` yields average latency. Do
**not** register two standalone metrics literally named `..._sum`/`..._count`:
those suffixes are reserved for histogram/summary families and standalone use is
an anti-pattern that breaks tooling and would collide with a future real
histogram of the same base name. Both inputs are monotonic, mapping cleanly onto
count/sum (which Prometheus treats as counters).

**Circuit-breaker state accessor:** the current circuit-breaker `state` field is
private and guarded by `cb.mutex`; the client exposes only the
`CircuitBreakerOpen` *counter*, not the current state. Implementation adds a
small read accessor (e.g. `client.CircuitBreakerState() int`) that takes
`cb.mutex.RLock()` (the suite runs under `-race`) and maps the enum via an
explicit `switch` (decoupled from iota order, though `CircuitClosed=0 /
CircuitOpen=1 / CircuitHalfOpen=2` already matches the gauge values).

### Cache & session (bridged)
- `oba_cache_hits_total` — counter (client `CacheHits`; `oba_`-prefixed to
  distinguish from the session cache).
- `oba_cache_misses_total` — counter (client `CacheMisses`).
- `session_store_active_sessions{store}` — gauge (`TotalSessions`).
- `session_store_cache_hits_total{store}` — counter (session `CacheHits`).
- `session_store_cache_misses_total{store}` — counter (session `CacheMisses`).
- `session_store_evictions_total{store}` — counter (session `Evictions`).
- `session_store_expired_total{store}` — counter (session `ExpiredSessions`).
- `session_store_created_total{store}` — counter (session `CreatedSessions`).

**Multiple session stores (important).** There is no single shared session store.
`main.go` constructs one store wired *only* into the health checker, while the
SMS handler and the voice handler each create their own internal store. Bridging
the health-checker's store would report an idle, orphaned store. Therefore the
session bridge observes the **stores actually in use** — the SMS handler's store
and the voice handler's store — distinguished by a low-cardinality `store` label
(`sms` | `voice`). Each handler exposes a read accessor for its store. The
health-checker store is left unchanged and is **not** bridged. (Consolidating to
a single shared store is a sound future cleanup but is out of scope here: it
would change `Close()` ownership and cross-flow session sharing, a behavioral
change with its own risk.)

### Interactions (direct, instrumented in handlers)
- `interactions_total{channel,outcome}` — counter.
  - `channel`: `sms` | `voice`.
  - `outcome`: `resolved` | `ambiguous` | `not_found` | `error`.
- `stop_lookups_total{result,agency}` — counter.
  - `result`: `resolved` | `ambiguous` | `not_found` | `error`. `error` (an
    upstream/transport failure) is kept distinct from `not_found` (a genuinely
    empty result) so the two can't be conflated on a dashboard.
  - `agency`: matched agency prefix (e.g. `1`, `40`, `29`) or `none`.

The `agency` label is low-cardinality (a fixed set of known prefixes). If lookup
fails, `agency="none"`.

### Runtime (free, via default collectors)
- `go_*` (goroutines, GC, memstats), `process_*` (CPU, FDs, resident memory),
  and Go build info.

## Wiring & data flow

In `main.go`:
1. `m := metrics.New()` constructs the registry and collectors once.
2. Register bridge collectors:
   - `m.RegisterClientBridge(obaClient)` — reads `client.GetMetrics()` +
     `CircuitBreakerState()`.
   - `m.RegisterSessionBridge("sms", smsHandler.SessionStore)` and
     `m.RegisterSessionBridge("voice", voiceHandler.SessionStore)` — the
     stores actually in use (see "Multiple session stores" above). The
     health-checker store is not bridged.
3. Inject `m` into handlers via a **setter**, mirroring the existing
   `SetAnalytics` pattern, so existing 2-arg constructor call sites (and all
   handler tests) keep compiling: `smsHandler.SetMetrics(m)` /
   `voiceHandler.SetMetrics(m)`. Handlers call `m.RecordInteraction(channel,
   outcome)` / `m.RecordStopLookup(result, agency)` at the points where they
   already emit analytics events. `RecordInteraction`/`RecordStopLookup` are
   **nil-safe** (no-op when metrics is nil) so tests that don't set metrics never
   panic.
4. Add the HTTP middleware: `r.Use(m.Middleware())` alongside the existing
   analytics and health middleware. The middleware **skips** `/metrics` and the
   `/health*` paths (matching the existing `HealthMiddleware` behavior) so scrape
   and probe traffic don't dominate `http_requests_total`.
5. `/metrics` stays on the existing rate-limited route group. Because that group
   and its limiter are private to `health.Handler.SetupRoutes`, `SetupRoutes`
   gains a `metricsHandler gin.HandlerFunc` parameter (just a func value — no
   import of the `metrics` package) and registers it on the rate-limited group.
   `main.go` passes `m.Handler()` (a `gin.WrapH` of `promhttp.HandlerFor(reg,
   …)`). Optionally wrap with `promhttp.InstrumentMetricHandler` for the standard
   `promhttp_metric_handler_requests_total` scrape counters.

### Removals from the `health` package
- Delete `formatPrometheusMetrics`, `formatMetricLine`, and
  `GetPrometheusMetrics`.
- Delete the `MetricsCollector` type and its wiring in `health/manager.go`
  (`metricsCollector` field, `UpdateMetrics`, `GetMetrics` metrics path), **or**
  reduce `MetricsInfo` to only what the JSON `/health/*` endpoints still need.
- Remove the `Accept: application/json` branch and the hand-rolled Prometheus
  path from `MetricsHandler` (the handler is removed; the route is served by the
  injected `metricsHandler`).
- **Update the affected tests** (these reference deleted symbols and will not
  compile otherwise):
  - `health/metrics_test.go` is built entirely on `MetricsCollector` /
    `formatPrometheusMetrics` / `MetricsInfo` / the `Increment*`/`Update*`
    methods — delete or rewrite it. (Verified: those `Increment*`/`Update*`
    methods are called only from tests, never production, so deletion is safe.)
  - `health/handlers_test.go` (~line 126) exercises the `MetricsInfo` JSON /
    `Accept: application/json` branch being removed — update it to assert the new
    delegated behavior (or drop that assertion).
- `Manager.GetMetrics()` / `MetricsInfo` are consumed only by the removed
  `/metrics` handler; the JSON `/health/*` endpoints use the independent
  `collectDependencyInfo` path, so their deletion does not affect health output.
- `/health`, `/health/ready`, `/health/detailed`, `/health/stats`,
  `/health/config`, `/health/cache` endpoints and the health *check* system are
  untouched. Only the metrics-formatting responsibility leaves the package.

## Error handling & edge cases

- **Cardinality:** `route` from `FullPath()` only; 404 → `"unmatched"`.
  `method` sanitized to a fixed allow-list; unknown → `"unknown"`. `agency` from
  a known fixed set; unknown/none → `"none"`. `status` is the numeric HTTP code
  as a string. `store` label is the fixed set `{sms, voice}`.
- **Custom registry:** prevents global-state panics; tests get fresh registries.
- **Scrape safety:** bridge `Collect()` only reads snapshots
  (`GetMetrics()`), never mutates; safe under concurrent scrapes.
- **Counter monotonicity:** bridged counters read from monotonic in-memory
  counters; they reset only on process restart (expected Prometheus semantics).

## Testing

- `metrics/http_test.go`: middleware records correct `method`/`route`/`status`
  labels and observes duration; 404 → `route="unmatched"`; an unknown HTTP method
  → `method="unknown"`; `/metrics` and `/health*` are skipped.
- `metrics/bridge_test.go`: bridge collectors emit expected series for fake
  client/session metric snapshots, verified with
  `prometheus/testutil.CollectAndCompare`.
- `metrics/metrics_test.go`: `/metrics` output is valid exposition format and
  contains `go_*` plus the registered app series.
- Handler tests: `interactions_total` and `stop_lookups_total` increment with the
  correct labels for each outcome path (resolved / ambiguous / not_found /
  error), extending existing handler test suites. Handlers with `nil` metrics do
  not panic.
- `health` package tests updated for the removals: `health/metrics_test.go`
  deleted/rewritten, `health/handlers_test.go` metrics-JSON assertion updated.
- All gates pass: `make lint`, `make vet`, `make test`, `make fmt`.

## Dependencies & docs

- Add `github.com/prometheus/client_golang` (the standard, well-maintained Go
  client).
- Update `README.md`, `.env.example`, and `CLAUDE.md` to document the
  now-standard `/metrics` endpoint and the metric catalog. No new environment
  variables are introduced (exposure stays public + rate-limited).

## Out of scope

- Authentication or a separate metrics port for `/metrics`.
- A true *bucketed* latency histogram for OBA API calls (requires instrumenting
  the client call path; the const-histogram bridge exposes sum/count/+Inf only).
- Consolidating the three session stores into one shared store.
- Alerting rules, Grafana dashboards, or scrape configuration (consumer-side).
