# Design Spec: Service Alerts for Stop & Route Queries

**Issue:** [#8 — Add support for GTFS-RT Service Alerts](https://github.com/OneBusAway/twilio/issues/8)
**Date:** 2026-07-01
**Status:** Approved scope, ready for implementation planning

## 1. Problem & Scope

OneBusAway agencies publish **service alerts** (GTFS-RT Service Alerts / "situations"):
delays, reroutes, elevator outages, weather disruptions. Today this Twilio bridge
never surfaces them. A caller or texter asking about a stop gets arrival times with
no indication that their route is rerouted or suspended.

The issue envisions alerts tied to routes/stops **and** system-wide disruptions
played at the start of a call with a language affordance.

### In scope (this iteration)

- Surface active service alerts **tied to a queried stop or its routes** in both SMS
  and voice responses.
- Consume alerts from the **OneBusAway REST API**, which already embeds them as
  `situations` in the `arrivals-and-departures-for-stop` response. No new data source,
  no new configuration, no protobuf.
- Localize alert presentation across all 10 supported languages.

### Explicitly out of scope (deferred to a separate issue)

- **System-wide disruptions played at call start before a stop is chosen.** The OBA
  REST API has no global "list all alerts" endpoint; delivering this reliably requires
  a separate global source (e.g. a GTFS-RT ServiceAlerts protobuf feed) and per-deployment
  configuration. We design the alert layer so that source can be added later without
  reworking the stop/route path, but we do not build it now.
- The dedicated "hear this alert in another language" menu affordance at call open.

**Decision rationale:** The REST situations path is deployment-agnostic (works against
any OBA server via the existing `ONEBUSAWAY_BASE_URL`), needs zero new config, and
delivers the highest-value 80% — telling a rider "route X is rerouted" exactly when they
ask about it. See §8 for the deferred system-wide follow-up.

## 2. Data Source: OBA REST "situations"

The existing `GET /api/where/arrivals-and-departures-for-stop/<id>.json` response
already carries alerts the app currently discards. Relevant shape (JSON):

```json
{
  "currentTime": 1330945364170,
  "data": {
    "references": {
      "situations": [
        {
          "id": "1_1289973261968",
          "reason": "MAINTENANCE",
          "severity": "severe",
          "summary":     { "value": "Reroute on Route 40" },
          "description": { "value": "Route 40 is rerouted around 3rd Ave due to construction." },
          "url":         { "value": "https://example.org/alerts/40" },
          "activeWindows": [ { "from": 1330000000000, "to": 1340000000000 } ],
          "allAffects": [ { "agencyId": "1", "routeId": "1_100", "stopId": "1_75403" } ]
        }
      ]
    },
    "entry": {
      "stopId": "1_75403",
      "situationIds": ["1_1289973261968"],
      "arrivalsAndDepartures": [
        { "routeShortName": "40", "situationIds": ["1_1289973261968"], "...": "..." }
      ]
    }
  }
}
```

Key facts that drive the design:

- **`references.situations` is already scoped to this response.** OBA only includes
  situations referenced by this stop or its routes/trips. So "alerts relevant to this
  stop query" == "active situations in `references.situations`". We do **not** need to
  cross-filter against `situationIds`; those IDs confirm relevance but the references
  block is already the correct, deduped set.
- **`summary` / `description` / `url` are wrapped** in a `{ "value": ... }` object
  (the OBA NaturalLanguageString shape). Some deployments send a bare string. The
  parser must tolerate both.
- **`activeWindows`** bound when the alert is live. Empty/absent windows ⇒ always active.
  **Canonical unit is milliseconds** — every OBA where-API timestamp (`currentTime`,
  `creationTime`, `scheduledArrivalTime`, `serviceDate`, …) is documented as ms since
  epoch, so the ms example above is the expected shape. However, OBA ingests GTFS-RT
  alerts whose `TimeRange.start/end` are POSIX **seconds**, so some deployments may emit
  seconds-valued windows. The parser normalizes defensively for that deployment-variance
  case (§4.2). Do **not** confuse `activeWindows` with the sibling `publicationWindows`
  field — we filter on `activeWindows` only.
- **`currentTime`** (top-level, ms) is the server's clock and is the reference time for
  active-window filtering — preferred over local wall-clock so the app and API agree.

### 2.1 Verification against a live response — DONE (2026-07-01)

The field shapes below were confirmed against a **live**
`GET /api/where/arrivals-and-departures-for-stop/1_75403.json` from
`api.pugetsound.onebusaway.org`, which returned one active situation (`id 1_86608`). No
remaining unknowns block implementation:

- **`activeWindows` — CONFIRMED milliseconds.** Field name is exactly `activeWindows`;
  the sample had `{ "from": 1776212160000, "to": 1789383540000 }` (13-digit ms), bracketing
  `currentTime: 1782940990927`. `publicationWindows` is a separate, sibling field (empty in
  the sample) — we filter on `activeWindows` only. The §4.2 seconds→ms normalization stays
  as cheap insurance for other deployments, but ms is the confirmed Puget Sound reality.
- **`summary` / `description` / `url` — CONFIRMED wrapped**, as `{ "lang": "en", "value": ... }`.
  `url` **does** exist (e.g. a trip-planner link). Our `nlString` reads `value` and ignores
  `lang`; the bare-string tolerance remains for other deployments.
- **`severity` and `reason` — CONFIRMED present** (`"noImpact"`, `"OTHER_CAUSE"`). Captured
  for future use; **no MVP logic filters on severity** — we show all active situations
  regardless (a "noImpact" temporary-stop relocation is still useful to a rider at that stop).
- **Per-arrival `situationIds` — CONFIRMED present** on this deployment (e.g. `["1_86608"]`
  on the affected arrival, `[]` on others). Still treated as optional/non-load-bearing;
  MVP filtering uses `references.situations` only.
- **Reality check on length:** real summaries are long (~180 chars) and descriptions longer.
  This validates the per-channel caps (§4.3) and means the SMS alert block can add multiple
  segments — acceptable for MVP, noted for the implementer (consider `smsMaxAlerts = 1` if
  segment cost matters).

## 3. Architecture Overview

Data flows through four existing layers; the change threads alerts through each with
minimal surface area:

```
OBA REST JSON ──parse──▶ models.OneBusAwayResponse (extended)
                              │
                              ├─ resp.ActiveSituations()  ──▶ []models.Situation   (model method; filters by active window)
                              │
        handlers/sms.go ──────┤
        handlers/voice/… ─────┘
                              │
              formatters.FormatSMSAlerts(...)   ──▶ prefix text  ──▶ prepended to SMS body
              formatters.FormatVoiceAlerts(...) ──▶ spoken text  ──▶ prepended VoiceSay
                              │
              localization (10 locale files, parity-enforced)
```

**Key design choice — no interface churn.** Handlers already hold the raw
`*models.OneBusAwayResponse` (they pass it to `ProcessArrivals`). Situation extraction is
therefore a **method on the response model** (`resp.ActiveSituations()`), *not* a new
method on `OneBusAwayClientInterface`. This means:

- The `OneBusAwayClientInterface` is unchanged.
- None of the ~5 mock clients in the test suite need new methods.
- Tests exercise alerts by populating `Data.References.Situations` on the mock's returned
  response — the same struct real parsing fills.

## 4. Component Design

### 4.1 `models/types.go` — new types & response extensions

Add a domain `Situation` (the clean, presentation-ready shape) plus raw JSON fields on
`OneBusAwayResponse`.

```go
// Situation is a presentation-ready service alert.
type Situation struct {
    ID          string
    Summary     string // short headline; may be empty
    Description string // longer body; may be empty
    URL         string // optional
    Reason      string // e.g. MAINTENANCE, CONSTRUCTION (optional, for future use)
    Severity    string // e.g. severe, warning (optional, for future use)
}
```

Extend `OneBusAwayResponse` (additive; existing fields untouched):

```go
type OneBusAwayResponse struct {
    CurrentTime int64 `json:"currentTime"` // server clock (ms); reference for active-window filtering
    Data struct {
        References struct {
            Situations []rawSituation `json:"situations"`
        } `json:"references"`
        Entry struct {
            ArrivalsAndDepartures []struct {
                RouteShortName       string   `json:"routeShortName"`
                TripHeadsign         string   `json:"tripHeadsign"`
                PredictedArrivalTime int64    `json:"predictedArrivalTime"`
                ScheduledArrivalTime int64    `json:"scheduledArrivalTime"`
                Status               string   `json:"status"`
                SituationIds         []string `json:"situationIds"` // relevance signal (not required for MVP filtering)
            } `json:"arrivalsAndDepartures"`
            StopId       string   `json:"stopId"`
            SituationIds []string `json:"situationIds"`
        } `json:"entry"`
    } `json:"data"`
    Code int    `json:"code"`
    Text string `json:"text"`
}
```

`rawSituation` (unexported) mirrors the wire format and tolerates both wrapped and bare
string forms via a custom type for the value fields:

```go
type rawSituation struct {
    ID            string          `json:"id"`
    Reason        string          `json:"reason"`
    Severity      string          `json:"severity"`
    Summary       nlString        `json:"summary"`
    Description   nlString        `json:"description"`
    URL           nlString        `json:"url"`
    ActiveWindows []activeWindow  `json:"activeWindows"`
}

type activeWindow struct {
    From int64 `json:"from"`
    To   int64 `json:"to"`
}

// nlString unmarshals either {"value":"x"} or "x".
type nlString struct{ Value string }
func (n *nlString) UnmarshalJSON(b []byte) error { /* try object then string */ }
```

### 4.2 `resp.ActiveSituations()` — extraction & active-window filtering

A method on `*OneBusAwayResponse`:

```go
func (r *OneBusAwayResponse) ActiveSituations() []Situation
```

Behavior:

1. Iterate `Data.References.Situations`.
2. Keep a situation if it is **active** at `r.CurrentTime`:
   - No `activeWindows` ⇒ active.
   - Otherwise active if any window contains now: `from <= now <= to`, where a zero
     `from` means "from the beginning" and a zero `to` means "no end".
3. **Timestamp normalization:** windows may arrive in seconds or ms. Normalize each
   bound to ms before comparing (heuristic: a nonzero value `< 1e12` is treated as
   seconds and multiplied by 1000). If `r.CurrentTime` is 0 (older/edge responses),
   skip window filtering and treat all situations as active (fail open — better to show
   a possibly-stale alert than hide a real one). Apply the same fail-open stance to a
   situation whose windows are all unparseable/zero on both bounds: treat it as active.
4. Map surviving `rawSituation` → `Situation`, trimming whitespace. Situations already
   unique by ID in `references`; no extra dedupe needed.
5. Preserve source order (OBA orders by relevance/severity in practice).

Returns `nil`/empty when there are no active alerts — callers treat empty as "no alerts,"
so existing output is byte-for-byte unchanged when there are none.

### 4.3 `formatters/response.go` — presentation

Two new functions, mirroring the existing `FormatSMSResponse` / `FormatVoiceResponse`
conventions (localized-with-English-fallback via `localizedOrEmpty`):

```go
func FormatSMSAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string
func FormatVoiceAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string
```

**SMS** (`FormatSMSAlerts`): terse, because SMS length matters.
- Returns `""` when no situations.
- Shows at most **2** alerts (`smsMaxAlerts = 2`); if more exist, append a localized
  "+N more alerts" line.
- Per alert: a localized prefix label + the alert's `Summary` (fall back to `Description`
  truncated if `Summary` empty). Format like: `⚠ Service alert: Reroute on Route 40`.
- Uses key `sms.alert.prefix` (label) and `sms.alert.more` (the overflow line,
  `+%d more service alerts.`). No "reply for details" keyword in MVP — the summary is
  shown inline and overflow is a plain count.

**Voice** (`FormatVoiceAlerts`): spoken, fuller.
- Returns `""` when no situations.
- Shows at most **2** alerts (`voiceMaxAlerts = 2`) to keep the call listenable; if more,
  append localized "There are N more service alerts."
- Per alert: localized lead-in (`voice.alert.lead_in`, e.g. "Service alert.") + `Summary`
  + (if present) `Description`. URLs are **not** read aloud.

Both functions never panic on empty fields; they skip a situation that has neither
summary nor description.

### 4.4 `handlers/sms.go` — inject into SMS

In `getAndFormatArrivalsWithStopNameAndSession` (the single-stop success path, ~sms.go:326),
after `FormatSMSResponse` builds the arrivals `message` and before the `more`/`help` hint
is appended:

```go
if alertText := formatters.FormatSMSAlerts(resp.ActiveSituations(), h.LocalizationManager, language); alertText != "" {
    message = alertText + "\n\n" + message
}
```

`resp` is the `*models.OneBusAwayResponse` already fetched in this method. Alerts go
**above** arrivals so they're seen first. Hints remain last.

### 4.5 `handlers/voice/find_stop.go` — inject into voice

In `getAndFormatVoiceArrivalsWithSession` (~find_stop.go:318), before assembling the
`arrivalsSay` element (~:387), prepend an alert `VoiceSay` (same language) so it is spoken
first, then arrivals, then the existing menu gather:

```go
elements := []twiml.Element{}
if alertText := formatters.FormatVoiceAlerts(resp.ActiveSituations(), h.LocalizationManager, language); alertText != "" {
    elements = append(elements, &twiml.VoiceSay{Message: alertText, Language: twilioVoiceLang(language)})
}
elements = append(elements, arrivalsSay /*, existing gather/menu */)
```

Follows the existing inline-`twiml` construction pattern; language mapping reuses whatever
the handler already uses for `VoiceSay.Language`.

### 4.6 `localization/` — new keys across 10 locales

`localization/locale_parity_test.go` enforces that every locale has exactly the same keys
as `en-US.json` and ends in a newline. New keys (added to **all 10** files):

| Key | en-US value | Notes |
|-----|-------------|-------|
| `sms.alert.prefix`   | `⚠ Service alert:` | SMS label |
| `sms.alert.more`     | `+%d more service alerts.` | `%d` = remaining count |
| `voice.alert.lead_in`| `Service alert.` | spoken lead-in |
| `voice.alert.more`   | `There are %d more service alerts.` | `%d` = remaining count |

For non-English locales, provide real translations (the localizer skill / existing
translations); never leave English placeholders, since parity + quality both matter.
Values follow existing printf-verb conventions.

## 5. Data Flow (end to end)

1. Caller/texter asks about stop `75403`.
2. Handler resolves the stop and calls `GetArrivalsAndDeparturesWithWindow` → `resp`.
3. `resp.ActiveSituations()` returns active alerts for that stop/its routes (often empty).
4. `FormatSMSAlerts` / `FormatVoiceAlerts` render localized text (empty ⇒ no-op).
5. SMS: alert block prepended to the arrivals message. Voice: alert `VoiceSay` prepended
   before the arrivals `Say` and the menu `Gather`.
6. Everything else (pagination, disambiguation, menu) is unchanged.

## 6. Error Handling & Edge Cases

- **No alerts** (the common case): `ActiveSituations()` empty → formatters return `""` →
  output identical to today. Zero regression risk for the 99% path.
- **Malformed/partial situation JSON:** `nlString.UnmarshalJSON` tolerates object or
  string; missing fields yield empty strings; a situation with no summary *and* no
  description is skipped by the formatters.
- **Missing `currentTime`:** fail open — treat all situations as active (§4.2).
- **Ambiguous timestamp units:** normalized to ms via magnitude heuristic (§4.2).
- **Very long descriptions:** SMS truncates the fallback description; voice reads summary +
  description but is capped at 2 alerts to bound call length.
- **Alerts present but arrivals empty:** alerts still shown (prepended to the
  "no arrivals" message) — a rider whose route is suspended should hear/see why.

## 7. Testing Strategy

Follow the repo's package-level unit-test convention; all four `make` gates
(`lint`, `vet`, `test`, `fmt`) must pass.

- **`models` (`ActiveSituations`)**: table-driven tests for wrapped vs. bare string
  values; active vs. expired vs. future windows; empty windows (always active); zero
  `currentTime` (fail open); seconds vs. ms normalization; empty references.
- **`formatters`**: `FormatSMSAlerts` / `FormatVoiceAlerts` for zero/one/many alerts,
  the "+N more" cap, summary-only, description-only, neither (skipped), and localized vs.
  English-fallback output.
- **`handlers` (SMS & voice)**: using existing mock clients, populate the mock's returned
  `OneBusAwayResponse` with situations and assert the SMS body / TwiML contains the alert
  text and that it precedes arrivals; assert **no** change when there are no situations.
- **Locale parity**: adding the 4 keys to all 10 files keeps `TestLocaleKeyParity` /
  `TestLocaleFilesEndWithNewline` green (these act as the guard).

## 8. Deferred Follow-up (new issue to file)

**System-wide disruptions at call start.** Requires a global alert source. Recommended
future approach: add optional `GTFS_RT_ALERTS_URL` config; poll & cache the protobuf feed
(`google.golang.org/protobuf` is already an indirect dep); classify alerts whose
`informed_entity` names only an agency as system-wide; play them (with a language menu)
in `HandleVoiceStart` before stop selection. The `Situation` model and formatter layer
built here are reused as-is — only the *source* and the *call-open placement* are new.

## 9. Non-Goals / YAGNI

- No caching layer specific to alerts (they ride the existing arrivals cache/TTL).
- No `Reason`/`Severity`-based styling or filtering in MVP (fields captured for future use).
- No new endpoint calls, no interface changes, no mock-client changes.
- No reading of alert URLs aloud or sending them via SMS in MVP (kept short).
