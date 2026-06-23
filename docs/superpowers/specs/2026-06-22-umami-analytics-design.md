# Umami Analytics — Design

**Ticket:** [OneBusAway/twilio#14](https://github.com/OneBusAway/twilio/issues/14) — Add Umami Analytics support (server-side event emission)
**Date:** 2026-06-22
**Status:** Approved (brainstorming → ready for implementation plan)

## Summary

Add [Umami](https://umami.is) analytics emission to the Twilio voice/SMS service by
registering a new **Umami provider** in the analytics broker that already exists in
this repository. Umami events are emitted server-side by POSTing directly to Umami's
unauthenticated `/api/send` ingestion endpoint. No new broker is built — the existing
`analytics.Manager` is already the single call-site abstraction the ticket's spirit
asks for.

## Decisions (resolved during brainstorming)

| Question | Decision |
| --- | --- |
| How to obtain `umamiAnalytics` `{url, id}`? | **Env vars only** (`UMAMI_URL`, `UMAMI_WEBSITE_ID`). The app does not fetch the region feed today; we do not add region-feed fetching. |
| Plausible provider? | **Keep it.** Add Umami alongside Plausible; the `Manager` fans out each event to both when both are enabled. |
| Session attribution / User-Agent? | **Per-caller UA.** A browser/device-like UA embedding channel + the salted caller hash, so Umami's `isbot` check passes and we get per-caller/per-device session rollups. |

## Background: the existing broker

The `analytics` package already implements a pluggable broker:

- `analytics.Analytics` interface: `TrackEvent` / `Flush` / `Close`.
- `analytics.Manager`: fans one generic `analytics.Event` out to every registered
  provider on a worker pool, each wrapped in a circuit breaker. Enqueue is
  non-blocking (drops on full queue).
- Existing providers: `plausible`, `mock`, `noop`.
- Call-sites already route through one place: `middleware.TrackSMSRequest(...)`,
  `TrackVoiceRequest(...)`, `TrackStopLookup(...)`, `TrackError(...)`,
  `TrackDisambiguation*(...)`. Each dispatches on a goroutine.

```text
handler → middleware.Track*  →  Manager (worker pool, circuit breaker)
                                   ├── plausible.Provider   (existing)
                                   └── umami.Provider        (NEW)
```

Nothing at the call-sites changes. The work is: a new provider, config loading, and
`main.go` wiring.

## Component: `analytics/providers/umami/`

A `Provider` implementing `analytics.Analytics`.

### Config

```go
type Config struct {
    ServerURL   string        // e.g. https://analytics.onebusawaycloud.com (required)
    WebsiteID   string        // Umami website UUID (required)
    Hostname    string        // e.g. api.pugetsound.onebusaway.org
    HTTPTimeout time.Duration // default 5s
}
```

`Validate()` returns new sentinel errors added to `analytics/errors.go`:
`ErrMissingServerURL`, `ErrMissingWebsiteID`. Defaults: `HTTPTimeout = 5s`.

### Behavior

- **`TrackEvent(ctx, event)`**: build the Umami payload (below) and do a
  **synchronous POST** to `<ServerURL>/api/send`. No internal batching or goroutine —
  the `Manager`'s worker pool already runs this off the request path, so this provider
  stays simpler than the batching Plausible provider. Returns an error on failure so
  the `Manager` logs it and the circuit breaker can trip; the error never reaches a
  user-facing caller.
- **HTTP specifics** (per architect review):
  - Use **one shared `http.Client`** stored on the provider (not per-call) so the
    keep-alive connection pool works. Set `Client.Timeout = HTTPTimeout` as a
    belt-and-suspenders cap.
  - Build the request with `http.NewRequestWithContext` deriving a per-request
    deadline via `context.WithTimeout(ctx, HTTPTimeout)` from the `ctx` the `Manager`
    passes in, so a `Manager` shutdown/cancel propagates to the in-flight POST.
  - On the response, **`defer resp.Body.Close()` and fully drain with
    `io.Copy(io.Discard, resp.Body)`** before close, so the connection can be reused.
- **`Flush(ctx)`**: no-op (nothing buffered).
- **`Close()`**: marks the provider closed; returns `ErrProviderClosed` on reuse,
  matching the existing provider convention.

## Wire format & event mapping

Mirrors the iOS (`UmamiAnalytics.swift`) and Android (`UmamiAnalytics.java`) prior art.

```json
{
  "type": "event",
  "payload": {
    "website": "<WebsiteID>",
    "hostname": "<Hostname>",
    "url": "/sms",
    "name": "sms_request",
    "data": { "language": "en-US", "query": "75403" }
  }
}
```

Mapping from `analytics.Event`:

- `website` ← `Config.WebsiteID`.
- `hostname` ← `Config.Hostname` (see Configuration for the default + fallback).
- `name` ← `Event.Name`, **always set**. We emit events-only — every
  `analytics.Event` becomes a Umami **custom event**. (Umami records a *pageview*
  only when `name` is omitted; the `analytics.Event` model has no pageview concept, so
  we never omit it. The `type` field stays `"event"`; Umami also accepts
  `identify`/`performance`, which we don't use.)
- `url` ← synthetic path from an event-name-prefix map so Umami dashboards group
  sensibly:
  - `sms_*` → `/sms`
  - `voice_*` → `/voice`
  - `stop_lookup*` → `/stop`
  - `error_*` → `/error`
  - default → `/`

  The map is keyed off the actual `analytics/events.go` constants. A table test
  (see Testing) asserts **every** event constant maps to a non-default path, so a
  newly added event that forgets a mapping fails loudly rather than silently landing
  in `/`.
- `data` ← sanitized `Event.Properties`: keep `string` / number / `bool`; stringify
  anything else; drop nil keys/values (mirrors Android `sanitizeProps`). Add
  `user_id` and `session_id` when present. Omit `data` entirely when empty.
  - **Content scrubbing (not just type-checking):** string values are **truncated to
    a max length** (e.g. 256 chars) to respect Umami's `data` value limits. The SMS
    `query` property is *uncontrolled user input* (an arbitrary SMS body), so it is
    truncated and must never carry raw PII beyond the query text itself. This matters
    more here than in the mobile clients, where inputs are app-controlled.

## User-Agent & session attribution

Umami runs the **full `isbot()`** on the request UA and silently drops bot-like UAs
(returns HTTP 200 + `{"beep":"boop"}`). The UA must be **browser-shaped by
construction**, not merely dodge isbot by accident. A bare product token like
`OneBusAway-Twilio/1.0 (SMS; <hash>)` only survives because the spaced parenthetical
breaks isbot's anchored `^\w+/[\w()]*$` pattern — that's fragile and could break when
isbot updates its pattern set. Instead, lead with a real browser prefix (the style
Umami's own server-side docs use, `Mozilla/5.0 (Server)`):

```text
Mozilla/5.0 (OneBusAway-Twilio; <Channel>; <first-12-of-hashed-caller>) Server/1.0
```

- Channel (`SMS` / `Voice` / `Server`) is inferred from the event-name prefix.
- The short hash is derived from the already-salted `Event.UserID`; `anon` when empty.
- Set per request because it varies per caller. This yields per-caller/per-device
  session rollups (Umami attributes sessions from client IP + UA for `type:"event"`).
  (If we ever need stable session attribution independent of IP/UA, Umami's
  `type:"identify"` with a `session` field is the supported mechanism — out of scope.)

### Success detection

```go
// isSuccessfulIngest reports whether Umami actually ingested the event.
// Umami returns the isbot drop as HTTP 200 + {"beep":"boop"}, so a status-only
// check is insufficient and the body must be inspected.
func isSuccessfulIngest(statusCode int, body []byte) bool
```

Contract (resolving the iOS-vs-Android disagreement the review flagged — iOS requires
a positive field, Android accepts any non-`beep` body):

- `statusCode` must be 2xx; otherwise `false`.
- A body containing `"beep"` (the `{"beep":"boop"}` drop) is `false`.
- Otherwise `true` when the body is **empty** OR contains one of
  `cache` / `sessionId` / `visitId`.
- Body parsing is **tolerant of non-JSON**: a non-JSON body must not error the whole
  track — fall back to the substring check for `"beep"`.

Note: a *missing* UA is not hard-rejected by Umami (the docs say "mandatory" but the
source falls through to `isbot(undefined)`); we always send a UA, so no test should
assert "missing UA → rejected."

## Configuration (env-only)

Loaded in `analytics/config_loader.go` via a new `loadUmamiConfig()` added to
`loadProviderConfigs()`, following the existing `loadPlausibleConfig()` pattern:

| Env var | Required | Default | Notes |
| --- | --- | --- | --- |
| `UMAMI_ENABLED` | no | `false` | Master switch for the provider. |
| `UMAMI_URL` | when enabled | — | Umami host; POSTs go to `<UMAMI_URL>/api/send`. |
| `UMAMI_WEBSITE_ID` | when enabled | — | Umami website UUID. |
| `UMAMI_HOSTNAME` | no | host of `ONEBUSAWAY_BASE_URL` | `hostname` field in the payload. |
| `UMAMI_HTTP_TIMEOUT` | no | `5s` | Go duration string. |

**Hostname default + fallback:** default to the parsed host of `ONEBUSAWAY_BASE_URL`.
If that env var is unset or unparseable, fall back to a fixed sentinel
`twilio.onebusaway.org` rather than emitting an empty `hostname` (an empty hostname
pollutes the Umami dashboard).

`main.go` gets a registration block mirroring the existing Plausible block:
extract config → `umami.NewProvider(...)` → `analyticsManager.RegisterProvider("umami", ...)`.
Misconfiguration when enabled → provider not registered, logged, app continues.

README and CLAUDE.md env tables updated with the `UMAMI_*` vars.

## Event coverage

The ticket's suggested events map onto existing event constructors in
`analytics/events.go`:

Current emission was audited against `handlers/`:

| Suggested event | Constructor | Status today | Action |
| --- | --- | --- | --- |
| Inbound call | `VoiceRequestEvent` | ✓ emitted (`handlers/voice/handler.go`) | none |
| SMS query | `SMSRequestEvent` | ✓ emitted (`handlers/sms.go`) | none |
| Stop / arrivals lookup (SMS) | `StopLookupEvent` | ✓ emitted (`handlers/sms.go`) | none |
| Error + disambiguation (SMS) | `ErrorEvent`, `Disambiguation*` | ✓ emitted (`handlers/sms.go`) | none |
| **Stop / arrivals lookup (voice)** | `StopLookupEvent` | ✗ **not emitted** in `handlers/voice/find_stop.go` | add emission |
| **Menu selection (voice)** | `EventVoiceMenuChoice` (const only, no constructor) | ✗ **not emitted** in `handlers/voice/menu_action.go` | add constructor + emission |
| Error / no-results | `ErrorEvent` | partial — verify the no-results/no-arrivals paths emit | add where missing |

The `EventVoiceMenuChoice` constant is `"voice_menu_choice"`, so it carries the
`voice_` prefix and maps to `/voice` — no path-map change needed.

As part of this work, **add the missing emissions** above so an exercised call/SMS
flow actually produces events in Umami (acceptance criterion). Keep this additive and
minimal — new `analytics.Event`s through the existing `middleware.Track*` pattern.

## Error handling

Fire-and-forget is guaranteed at three layers:

1. `middleware.Track*` dispatches each track on its own goroutine with a bounded
   context.
2. `Manager.TrackEvent` enqueues non-blocking and drops on a full queue.
3. The Umami provider uses a short HTTP timeout and converts all failures into a
   returned error that the `Manager` logs (and the circuit breaker consumes).

No analytics path can block or break a call or SMS.

## Testing

- `analytics/providers/umami` unit tests using `httptest.Server`:
  - Asserts the custom-event payload JSON shape (events-only; `name` always present —
    no pageview path exists, so there is no pageview test).
  - Asserts the synthetic `url` path mapping via a **table test over the actual
    `analytics/events.go` constants**, requiring every constant to map to a
    non-default path (adding an unmapped event later fails the test).
  - Asserts `data` sanitization: types kept/stringified/dropped **and** string
    truncation to the max length.
  - Asserts the request `User-Agent` header is **browser-shaped** — verified against
    a concrete known-good regex (and, if practical, by importing a Go isbot-equivalent
    or a vendored copy of isbot's generic pattern). The assertion must be falsifiable,
    not a vague "non-bot" check.
  - Asserts the server received a fully-drained/closed body (e.g. handler reads the
    whole request body without error).
  - Asserts a `{"beep":"boop"}` 200 response is detected as failure.
- Table-driven tests for `isSuccessfulIngest`: non-2xx, beep/boop body, positive-field
  bodies, empty body, and **non-JSON body** (must not panic/error).
- A "never blocks / swallows errors" test (server hangs past timeout or returns 500 →
  `TrackEvent` returns within the timeout and the caller is unaffected).
- `analytics/config_loader` tests for the `UMAMI_*` vars: enabled/disabled, missing
  required (`UMAMI_URL`/`UMAMI_WEBSITE_ID`), hostname default, and the
  hostname-sentinel fallback when `ONEBUSAWAY_BASE_URL` is unset/unparseable.
- New voice emission tests: `find_stop` stop-lookup and `menu_action` menu-choice
  events fire through the analytics manager.
- All existing tests stay green.
- `make lint`, `make vet`, `make test`, `make fmt` must pass before completion.

## Out of scope

- Region-feed (`regions-v3.json`) fetching and per-region matching (deferred; env-only
  config chosen instead).
- Removing or deprecating the Plausible provider (kept as-is).
- Any client-side / JavaScript tracker (the product is server-side only).
