# Service Alerts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface active OneBusAway service alerts ("situations") tied to a queried stop or its routes in both SMS and voice responses, localized across all 10 languages.

**Architecture:** The OBA REST `arrivals-and-departures-for-stop` response already embeds alerts under `data.references.situations`. We parse them into an exported model, filter to those active at the server's `currentTime` via a `resp.ActiveSituations()` method (no client-interface change), format them with two new localized formatter functions, and prepend them to the existing SMS body / voice TwiML. Empty alert sets are a no-op, so today's output is unchanged when there are no alerts.

**Tech Stack:** Go 1.24, Gin, `twilio/twilio-go/twiml`, `encoding/json`, testify. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-01-service-alerts-design.md` (read it first).

## Global Constraints

- Go module `oba-twilio`, Go 1.24.2. No new third-party dependencies.
- All four gates MUST pass before "done": `make lint`, `make vet`, `make test`, `make fmt`.
- Localization parity is enforced: `localization/locale_parity_test.go` fails if any of the
  10 locale files (`ar-US, de-US, en-US, es-US, fr-US, ko-US, pl-PL, pt-US, ru-US, zh-CN`)
  is missing/has-extra keys vs `en-US.json`, or lacks a trailing newline. Any new key MUST
  be added to **all 10** files, and each file MUST end with a newline.
- `en-US.json` is the source of truth for the key set.
- Timestamps: OBA `currentTime` and situation `activeWindows.from/to` are **milliseconds**
  (verified live 2026-07-01). Parser still normalizes a `< 1e12` value as seconds×1000 for
  deployment variance.
- No interface changes to `client.OneBusAwayClientInterface`; no changes to the ~5 mock
  client method sets. Alert extraction is a method on `*models.OneBusAwayResponse`.
- Situation model types MUST be **exported** (tests in other packages populate them).

---

### Task 1: Refactor `OneBusAwayResponse` to named types (pure refactor, no behavior change)

Adding situation fields to the current *anonymous* nested `Data` struct would break every
test literal that spells the anonymous type. Converting to named types first makes those
literals short and keyed, so this and future field additions won't break them. Field
**access paths are unchanged** (`resp.Data.Entry.StopId`, `resp.Data.Entry.ArrivalsAndDepartures[i].RouteShortName`, `resp.Code`, `resp.Text`), so only composite *literals* change.

**Files:**
- Modify: `models/types.go` (replace the `OneBusAwayResponse` definition, lines 47-62)
- Create: `models/situation.go` (type definitions only — see Step 1)
- Modify (compiler-guided literal updates): `main_test.go`, `client/onebusaway_test.go`,
  `client/validation_test.go`, `handlers/sms_test.go`, `handlers/voice_menu_test.go`,
  `handlers/sms_session_test.go`, `handlers/disambiguation_test.go`

> The guaranteed break among handler tests is `createMockResponse` in
> `handlers/sms_test.go:138-167` (it spells the anonymous `Data` struct). Files that only
> set top-level fields or use field-assignment do **not** break — notably
> `handlers/voice/find_stop_test.go:74` (`&models.OneBusAwayResponse{Code: 200}` then
> `resp.Data.Entry.StopId = stopID`) needs no change. Trust the Step 3 compiler output over
> this list; it is the authoritative set.

**Interfaces:**
- Produces: named types `OBAArrivalDeparture`, `OBAStopEntry`, `OBAReferences`,
  `OBAResponseData`, the reshaped `OneBusAwayResponse` (with `CurrentTime int64`), and the
  situation *type definitions* `Situation`, `NLString`, `ActiveWindow`, `RawSituation`
  (the `ActiveSituations()` method + filtering logic land in Task 2).

- [ ] **Step 1: Replace the `OneBusAwayResponse` definition in `models/types.go`**

Replace lines 47-62 (the current `type OneBusAwayResponse struct {...}`) with:

```go
// OBAArrivalDeparture is one predicted arrival/departure at a stop.
type OBAArrivalDeparture struct {
	RouteShortName       string `json:"routeShortName"`
	TripHeadsign         string `json:"tripHeadsign"`
	PredictedArrivalTime int64  `json:"predictedArrivalTime"`
	ScheduledArrivalTime int64  `json:"scheduledArrivalTime"`
	Status               string `json:"status"`
	// SituationIds references alerts affecting this arrival's trip/route. Present on
	// Puget Sound; treated as optional/non-load-bearing (MVP filters via references).
	SituationIds []string `json:"situationIds"`
}

// OBAReferences holds objects referenced by the response.
type OBAReferences struct {
	Situations []RawSituation `json:"situations"`
}

// OBAStopEntry is the stop payload of an arrivals-and-departures-for-stop response.
type OBAStopEntry struct {
	ArrivalsAndDepartures []OBAArrivalDeparture `json:"arrivalsAndDepartures"`
	StopId                string                `json:"stopId"`
	SituationIds          []string              `json:"situationIds"`
}

// OBAResponseData is the data envelope.
type OBAResponseData struct {
	References OBAReferences `json:"references"`
	Entry      OBAStopEntry  `json:"entry"`
}

type OneBusAwayResponse struct {
	// CurrentTime is the server clock in ms; reference time for active-window filtering.
	CurrentTime int64           `json:"currentTime"`
	Data        OBAResponseData `json:"data"`
	Code        int             `json:"code"`
	Text        string          `json:"text"`
}
```

Then, in the **same step**, create `models/situation.go` with the *type definitions only*
(this defines `RawSituation` referenced above, so Task 1 compiles with no placeholder;
Task 2 adds the `ActiveSituations()` method + filtering logic to this same file):

```go
package models

import (
	"bytes"
	"encoding/json"
)

// Situation is a presentation-ready service alert derived from an OBA situation.
type Situation struct {
	ID          string
	Summary     string
	Description string
	URL         string
	Reason      string // e.g. MAINTENANCE (captured for future use)
	Severity    string // e.g. severe, noImpact (captured for future use)
}

// NLString unmarshals an OBA NaturalLanguageString, which may arrive as an object
// {"lang":"en","value":"..."} or as a bare JSON string "...".
type NLString struct {
	Value string
}

func (n *NLString) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		n.Value = ""
		return nil
	}
	if trimmed[0] == '"' {
		return json.Unmarshal(trimmed, &n.Value)
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return err
	}
	n.Value = obj.Value
	return nil
}

// ActiveWindow is a time range during which a situation is in effect (ms since epoch;
// seconds tolerated via normalization in Task 2). Zero bound means unbounded on that side.
type ActiveWindow struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// RawSituation mirrors the OBA references.situations wire shape.
type RawSituation struct {
	ID            string         `json:"id"`
	Reason        string         `json:"reason"`
	Severity      string         `json:"severity"`
	Summary       NLString       `json:"summary"`
	Description   NLString       `json:"description"`
	URL           NLString       `json:"url"`
	ActiveWindows []ActiveWindow `json:"activeWindows"`
}
```

- [ ] **Step 2: Compile tests to find every broken literal**

Run: `go vet ./... 2>&1 | head -40`
(Use `go vet`, not `go build ./...` — plain build skips `_test.go` files, and every broken
literal lives in a test file.)
Expected: FAIL — a list of `cannot use struct literal ... in ... field value` / `unknown
field` errors, one per test literal that spelled the old anonymous `Data` struct
(grep anchor: `grep -rn "OneBusAwayResponse{" --include="*.go" .`).

- [ ] **Step 3: Rewrite each broken literal to the named keyed form**

Transformation rule — replace the verbose anonymous form with keyed named types. Example,
`handlers/sms_test.go` `createMockResponse` (lines 138-167) becomes:

```go
func createMockResponse(stopID string) *models.OneBusAwayResponse {
	return &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: stopID,
			},
		},
		Code: 200,
	}
}
```

Apply the same shape to each reported literal: `Data: struct{...}{...}` →
`Data: models.OBAResponseData{Entry: models.OBAStopEntry{ ...same field values... }}`,
and any inline arrival elements → `[]models.OBAArrivalDeparture{ {RouteShortName: ...}, }`.
Keep every existing field value identical. Only the type spelling changes.

- [ ] **Step 4: Rebuild until clean**

Run: `go build ./... && go vet ./...`
Expected: PASS (no output from build).

- [ ] **Step 5: Run the full existing suite — behavior must be unchanged**

Run: `make test`
Expected: PASS (same tests green as before the refactor).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(models): name OneBusAwayResponse nested types; add situation types"
```

---

### Task 2: Situation model + `ActiveSituations()` (TDD)

**Files:**
- Modify: `models/situation.go` (add the method + filtering helpers to the type-only file
  created in Task 1)
- Create: `models/situation_test.go`

**Interfaces:**
- Consumes: the situation *types* (`Situation`, `NLString`, `ActiveWindow`, `RawSituation`)
  and `OBAReferences.Situations []RawSituation` — all defined in Task 1.
- Produces: `func (r *OneBusAwayResponse) ActiveSituations() []Situation` (plus unexported
  helpers `normalizeToMillis`, `RawSituation.isActiveAt`).

- [ ] **Step 1: Write the failing test** — `models/situation_test.go`

```go
package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

const nowMs = int64(1782940990927) // matches an active window below

func respWith(situations []RawSituation, currentTime int64) *OneBusAwayResponse {
	return &OneBusAwayResponse{
		CurrentTime: currentTime,
		Data:        OBAResponseData{References: OBAReferences{Situations: situations}},
	}
}

func TestNLString_ObjectForm(t *testing.T) {
	var n NLString
	assert.NoError(t, json.Unmarshal([]byte(`{"lang":"en","value":"Reroute"}`), &n))
	assert.Equal(t, "Reroute", n.Value)
}

func TestNLString_BareStringForm(t *testing.T) {
	var n NLString
	assert.NoError(t, json.Unmarshal([]byte(`"Bare"`), &n))
	assert.Equal(t, "Bare", n.Value)
}

func TestNLString_Null(t *testing.T) {
	var n NLString
	assert.NoError(t, json.Unmarshal([]byte(`null`), &n))
	assert.Equal(t, "", n.Value)
}

func TestActiveSituations_ActiveWindowIncluded(t *testing.T) {
	r := respWith([]RawSituation{{
		ID:            "1_1",
		Summary:       NLString{Value: "  Reroute on 40  "},
		Description:   NLString{Value: "Detour"},
		ActiveWindows: []ActiveWindow{{From: 1776212160000, To: 1789383540000}},
	}}, nowMs)
	got := r.ActiveSituations()
	assert.Len(t, got, 1)
	assert.Equal(t, "Reroute on 40", got[0].Summary) // trimmed
	assert.Equal(t, "Detour", got[0].Description)
}

func TestActiveSituations_ExpiredWindowExcluded(t *testing.T) {
	r := respWith([]RawSituation{{
		ID:            "1_2",
		Summary:       NLString{Value: "Old"},
		ActiveWindows: []ActiveWindow{{From: 1000000000000, To: 1000000001000}},
	}}, nowMs)
	assert.Empty(t, r.ActiveSituations())
}

func TestActiveSituations_FutureWindowExcluded(t *testing.T) {
	r := respWith([]RawSituation{{
		ID:            "1_3",
		Summary:       NLString{Value: "Future"},
		ActiveWindows: []ActiveWindow{{From: 1999999999000, To: 2000000000000}},
	}}, nowMs)
	assert.Empty(t, r.ActiveSituations())
}

func TestActiveSituations_NoWindowsAlwaysActive(t *testing.T) {
	r := respWith([]RawSituation{{ID: "1_4", Summary: NLString{Value: "Always"}}}, nowMs)
	assert.Len(t, r.ActiveSituations(), 1)
}

func TestActiveSituations_ZeroCurrentTimeFailsOpen(t *testing.T) {
	r := respWith([]RawSituation{{
		ID:            "1_5",
		Summary:       NLString{Value: "ShowAnyway"},
		ActiveWindows: []ActiveWindow{{From: 1000000000000, To: 1000000001000}}, // expired
	}}, 0)
	assert.Len(t, r.ActiveSituations(), 1) // fail open when server time unknown
}

func TestActiveSituations_SecondsNormalized(t *testing.T) {
	// from/to in seconds (10-digit); now in ms sits inside once normalized ×1000.
	r := respWith([]RawSituation{{
		ID:            "1_6",
		Summary:       NLString{Value: "Sec"},
		ActiveWindows: []ActiveWindow{{From: 1776212160, To: 1789383540}},
	}}, nowMs)
	assert.Len(t, r.ActiveSituations(), 1)
}

func TestActiveSituations_OpenEndedBounds(t *testing.T) {
	r := respWith([]RawSituation{{
		ID:            "1_7",
		Summary:       NLString{Value: "OpenEnd"},
		ActiveWindows: []ActiveWindow{{From: 1000000000000, To: 0}}, // started, no end
	}}, nowMs)
	assert.Len(t, r.ActiveSituations(), 1)
}

func TestActiveSituations_EmptyReferencesReturnsNil(t *testing.T) {
	assert.Nil(t, respWith(nil, nowMs).ActiveSituations())
}

func TestActiveSituations_MapsAllFields(t *testing.T) {
	r := respWith([]RawSituation{{
		ID: "1_8", Reason: "MAINTENANCE", Severity: "severe",
		Summary: NLString{Value: "S"}, Description: NLString{Value: "D"},
		URL: NLString{Value: "http://x"},
	}}, nowMs)
	got := r.ActiveSituations()
	assert.Equal(t, Situation{ID: "1_8", Summary: "S", Description: "D", URL: "http://x", Reason: "MAINTENANCE", Severity: "severe"}, got[0])
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./models/ -run TestActiveSituations -v 2>&1 | head -20`
Expected: FAIL — `r.ActiveSituations undefined (type *OneBusAwayResponse has no field or
method ActiveSituations)`. (The `NLString`/`RawSituation` types already exist from Task 1,
so the `TestNLString_*` tests will pass; only the `ActiveSituations` cases fail here.)

- [ ] **Step 3: Implement** — append to the existing `models/situation.go`

Add `"strings"` to the import block (it currently imports only `"bytes"` and
`"encoding/json"`), then append these three declarations below `RawSituation`:

```go
// normalizeToMillis converts a possibly-seconds epoch to ms. Modern ms values are
// >= 1e12; GTFS-RT-derived seconds are < 1e12. Zero stays zero (unbounded).
func normalizeToMillis(v int64) int64 {
	if v != 0 && v < 1_000_000_000_000 {
		return v * 1000
	}
	return v
}

// isActiveAt reports whether the situation is active at nowMillis. Fails open when the
// server time is unknown (0) or when no usable window bound is present.
func (r RawSituation) isActiveAt(nowMillis int64) bool {
	if nowMillis == 0 || len(r.ActiveWindows) == 0 {
		return true
	}
	sawBound := false
	for _, w := range r.ActiveWindows {
		from := normalizeToMillis(w.From)
		to := normalizeToMillis(w.To)
		if from == 0 && to == 0 {
			continue // unparseable/empty window
		}
		sawBound = true
		if (from == 0 || nowMillis >= from) && (to == 0 || nowMillis <= to) {
			return true
		}
	}
	return !sawBound // fail open if no bound was usable
}

// ActiveSituations returns presentation-ready alerts from the response references that are
// active at the server's currentTime. Returns nil when there are none.
func (r *OneBusAwayResponse) ActiveSituations() []Situation {
	raws := r.Data.References.Situations
	if len(raws) == 0 {
		return nil
	}
	var out []Situation
	for _, rs := range raws {
		if !rs.isActiveAt(r.CurrentTime) {
			continue
		}
		out = append(out, Situation{
			ID:          rs.ID,
			Summary:     strings.TrimSpace(rs.Summary.Value),
			Description: strings.TrimSpace(rs.Description.Value),
			URL:         strings.TrimSpace(rs.URL.Value),
			Reason:      rs.Reason,
			Severity:    rs.Severity,
		})
	}
	return out
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./models/ -v 2>&1 | tail -25`
Expected: PASS (all situation tests green).

- [ ] **Step 5: Full build + vet (ensures Task 1's `[]RawSituation` reference resolves)**

Run: `go build ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add models/situation.go models/situation_test.go
git commit -m "feat(models): ActiveSituations() active-window filter over OBA situations"
```

---

### Task 3: Localized alert strings across all 10 locales

**Files:** Modify all 10 `locales/*.json`. Add these 4 keys to **each** file (parity + trailing
newline enforced by `localization/locale_parity_test.go`).

**Interfaces:**
- Produces keys: `sms.alert.prefix`, `sms.alert.more`, `voice.alert.lead_in`, `voice.alert.more`.

- [ ] **Step 1: Add the 4 keys to every locale file**

Insert these key/value pairs into each JSON object (append before the closing `}`, adding a
comma to the previously-last entry; JSON key order is irrelevant). Values per language:

```
en-US.json:
  "sms.alert.prefix": "⚠ Service alert:",
  "sms.alert.more": "+%d more service alerts.",
  "voice.alert.lead_in": "Service alert.",
  "voice.alert.more": "There are %d more service alerts."

es-US.json:
  "sms.alert.prefix": "⚠ Alerta de servicio:",
  "sms.alert.more": "+%d alertas de servicio más.",
  "voice.alert.lead_in": "Alerta de servicio.",
  "voice.alert.more": "Hay %d alertas de servicio más."

fr-US.json:
  "sms.alert.prefix": "⚠ Alerte de service :",
  "sms.alert.more": "+%d autres alertes de service.",
  "voice.alert.lead_in": "Alerte de service.",
  "voice.alert.more": "Il y a %d autres alertes de service."

de-US.json:
  "sms.alert.prefix": "⚠ Servicemeldung:",
  "sms.alert.more": "+%d weitere Servicemeldungen.",
  "voice.alert.lead_in": "Servicemeldung.",
  "voice.alert.more": "Es gibt %d weitere Servicemeldungen."

ar-US.json:
  "sms.alert.prefix": "⚠ تنبيه الخدمة:",
  "sms.alert.more": "+%d تنبيهات خدمة أخرى.",
  "voice.alert.lead_in": "تنبيه الخدمة.",
  "voice.alert.more": "هناك %d تنبيهات خدمة أخرى."

ko-US.json:
  "sms.alert.prefix": "⚠ 서비스 알림:",
  "sms.alert.more": "+%d개의 추가 서비스 알림.",
  "voice.alert.lead_in": "서비스 알림.",
  "voice.alert.more": "%d개의 추가 서비스 알림이 있습니다."

pl-PL.json:
  "sms.alert.prefix": "⚠ Komunikat serwisowy:",
  "sms.alert.more": "+%d więcej komunikatów serwisowych.",
  "voice.alert.lead_in": "Komunikat serwisowy.",
  "voice.alert.more": "Jest %d więcej komunikatów serwisowych."

pt-US.json:
  "sms.alert.prefix": "⚠ Alerta de serviço:",
  "sms.alert.more": "+%d alertas de serviço adicionais.",
  "voice.alert.lead_in": "Alerta de serviço.",
  "voice.alert.more": "Há %d alertas de serviço adicionais."

ru-US.json:
  "sms.alert.prefix": "⚠ Служебное оповещение:",
  "sms.alert.more": "+%d других служебных оповещений.",
  "voice.alert.lead_in": "Служебное оповещение.",
  "voice.alert.more": "Есть ещё %d служебных оповещений."

zh-CN.json:
  "sms.alert.prefix": "⚠ 服务提醒：",
  "sms.alert.more": "+%d 条更多服务提醒。",
  "voice.alert.lead_in": "服务提醒。",
  "voice.alert.more": "还有 %d 条服务提醒。"
```

- [ ] **Step 2: Verify each file is valid JSON and ends with a newline**

Run: `for f in locales/*.json; do python3 -c "import json;json.load(open('$f'))" || echo "BAD JSON $f"; [ "$(tail -c1 "$f")" = "" ] || echo "NO NEWLINE $f"; done`
Expected: no `BAD JSON` / `NO NEWLINE` lines.

- [ ] **Step 3: Run the parity test**

Run: `go test ./localization/ -run 'TestLocale' -v 2>&1 | tail -15`
Expected: PASS (`TestLocaleKeyParity`, `TestLocaleFilesEndWithNewline`).

- [ ] **Step 4: Commit**

```bash
git add locales/
git commit -m "i18n: add service-alert strings to all 10 locales"
```

---

### Task 4: Alert formatters (TDD)

**Files:**
- Create: `formatters/alerts.go`
- Create: `formatters/alerts_test.go`

**Interfaces:**
- Consumes: `[]models.Situation` (Task 2); `*localization.LocalizationManager`.
- Produces:
  - `func FormatSMSAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string`
  - `func FormatVoiceAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string`
  - Both return `""` when there is nothing to render. Cap: `smsMaxAlerts = 2`, `voiceMaxAlerts = 2`.

- [ ] **Step 1: Write the failing test** — `formatters/alerts_test.go`

```go
package formatters

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"oba-twilio/models"
)

func sit(summary, desc string) models.Situation {
	return models.Situation{Summary: summary, Description: desc}
}

func TestFormatSMSAlerts_Empty(t *testing.T) {
	assert.Equal(t, "", FormatSMSAlerts(nil, nil, "en-US"))
}

func TestFormatSMSAlerts_OneAlert_NilManagerFallsBackToEnglish(t *testing.T) {
	out := FormatSMSAlerts([]models.Situation{sit("Reroute on 40", "")}, nil, "en-US")
	assert.Contains(t, out, "Service alert")
	assert.Contains(t, out, "Reroute on 40")
}

func TestFormatSMSAlerts_DescriptionFallbackWhenNoSummary(t *testing.T) {
	out := FormatSMSAlerts([]models.Situation{sit("", "Body text")}, nil, "en-US")
	assert.Contains(t, out, "Body text")
}

func TestFormatSMSAlerts_TruncatesLongText(t *testing.T) {
	long := strings.Repeat("x", 300)
	out := FormatSMSAlerts([]models.Situation{sit(long, "")}, nil, "en-US")
	assert.Contains(t, out, "…")
	assert.NotContains(t, out, long)                       // full 300-char string not present
	assert.Less(t, len([]rune(out)), 300)                  // materially shorter
}

func TestFormatSMSAlerts_SkipsEmptyAndCountsOverflow(t *testing.T) {
	in := []models.Situation{sit("A", ""), sit("", ""), sit("B", ""), sit("C", "")}
	out := FormatSMSAlerts(in, nil, "en-US")
	assert.Contains(t, out, "A")
	assert.Contains(t, out, "B")
	assert.NotContains(t, out, "C")     // capped at 2 renderable
	assert.Contains(t, out, "1 more")   // 3 renderable - 2 shown = 1
}

func TestFormatVoiceAlerts_Empty(t *testing.T) {
	assert.Equal(t, "", FormatVoiceAlerts(nil, nil, "en-US"))
}

func TestFormatVoiceAlerts_ReadsSummaryAndDescription(t *testing.T) {
	out := FormatVoiceAlerts([]models.Situation{sit("Reroute", "Take 3rd Ave")}, nil, "en-US")
	assert.Contains(t, out, "Service alert")
	assert.Contains(t, out, "Reroute")
	assert.Contains(t, out, "Take 3rd Ave")
}

func TestFormatVoiceAlerts_NoURLReadAloud(t *testing.T) {
	s := models.Situation{Summary: "S", URL: "http://example.org/x"}
	out := FormatVoiceAlerts([]models.Situation{s}, nil, "en-US")
	assert.NotContains(t, out, "http")
}

func TestFormatVoiceAlerts_OverflowLine(t *testing.T) {
	in := []models.Situation{sit("A", ""), sit("B", ""), sit("C", "")}
	out := FormatVoiceAlerts(in, nil, "en-US")
	assert.True(t, strings.Contains(out, "1 more"))
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./formatters/ -run Alerts -v 2>&1 | head -15`
Expected: FAIL — `undefined: FormatSMSAlerts` / `FormatVoiceAlerts`.

- [ ] **Step 3: Implement** — `formatters/alerts.go`

```go
package formatters

import (
	"fmt"
	"strings"

	"oba-twilio/localization"
	"oba-twilio/models"
)

const (
	smsMaxAlerts   = 2
	voiceMaxAlerts = 2
	// smsAlertMaxRunes bounds a single SMS alert line so a long OBA summary/description
	// (real ones run ~180+ chars) can't balloon the message. Rune-safe.
	smsAlertMaxRunes = 140
)

// truncateRunes cuts s to at most max runes, appending "…" when it truncates.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

// localizedOr returns the localized value for key, or fallback if missing.
// (localizedOrEmpty lives in response.go in this same package.)
func localizedOr(lm *localization.LocalizationManager, key, language, fallback string, params ...interface{}) string {
	if lm != nil {
		if v := localizedOrEmpty(lm.GetString(key, language, params...), key); v != "" {
			return v
		}
	}
	return fallback
}

// smsBody returns the SMS text for a situation (summary, else description), truncated to
// keep the message short, or "" when the situation has neither.
func smsBody(s models.Situation) string {
	text := s.Summary
	if text == "" {
		text = s.Description
	}
	return truncateRunes(text, smsAlertMaxRunes)
}

// voiceBody returns the spoken text for a situation (summary then description), or "".
func voiceBody(s models.Situation) string {
	parts := make([]string, 0, 2)
	if s.Summary != "" {
		parts = append(parts, s.Summary)
	}
	if s.Description != "" {
		parts = append(parts, s.Description)
	}
	return strings.Join(parts, " ")
}

// FormatSMSAlerts renders a compact, localized alert block for SMS, or "" if none.
func FormatSMSAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string {
	var bodies []string
	for _, s := range situations {
		if b := smsBody(s); b != "" {
			bodies = append(bodies, b)
		}
	}
	if len(bodies) == 0 {
		return ""
	}
	prefix := localizedOr(lm, "sms.alert.prefix", language, "⚠ Service alert:")

	shown := bodies
	overflow := 0
	if len(bodies) > smsMaxAlerts {
		shown = bodies[:smsMaxAlerts]
		overflow = len(bodies) - smsMaxAlerts
	}
	lines := make([]string, 0, len(shown)+1)
	for _, b := range shown {
		lines = append(lines, prefix+" "+b)
	}
	if overflow > 0 {
		lines = append(lines, localizedOr(lm, "sms.alert.more", language,
			fmt.Sprintf("+%d more service alerts.", overflow), overflow))
	}
	return strings.Join(lines, "\n")
}

// FormatVoiceAlerts renders a spoken, localized alert block for voice, or "" if none.
func FormatVoiceAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string {
	leadIn := localizedOr(lm, "voice.alert.lead_in", language, "Service alert.")
	var spoken []string
	for _, s := range situations {
		if b := voiceBody(s); b != "" {
			spoken = append(spoken, leadIn+" "+b)
		}
	}
	if len(spoken) == 0 {
		return ""
	}
	shown := spoken
	overflow := 0
	if len(spoken) > voiceMaxAlerts {
		shown = spoken[:voiceMaxAlerts]
		overflow = len(spoken) - voiceMaxAlerts
	}
	out := strings.Join(shown, " ")
	if overflow > 0 {
		out += " " + localizedOr(lm, "voice.alert.more", language,
			fmt.Sprintf("There are %d more service alerts.", overflow), overflow)
	}
	return out
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./formatters/ -v 2>&1 | tail -25`
Expected: PASS (new alert tests + existing formatter tests green).

- [ ] **Step 5: Commit**

```bash
git add formatters/alerts.go formatters/alerts_test.go
git commit -m "feat(formatters): localized SMS + voice service-alert formatting"
```

---

### Task 5: Inject alerts into the SMS handler (TDD)

**Files:**
- Modify: `handlers/sms.go` (in `getAndFormatArrivalsWithStopNameAndSession`, after the
  `message` is built at line ~335, before the menu-hint block at line ~337)
- Modify: `handlers/sms_test.go` (add a test; extend a mock response with situations)

**Interfaces:**
- Consumes: `formatters.FormatSMSAlerts` (Task 4); `resp.ActiveSituations()` (Task 2).

- [ ] **Step 1: Write the failing test** — append to `handlers/sms_test.go`

```go
func createMockResponseWithAlert(stopID string) *models.OneBusAwayResponse {
	resp := createMockResponse(stopID)
	resp.CurrentTime = 1782940990927
	resp.Data.References = models.OBAReferences{Situations: []models.RawSituation{{
		ID:            "1_alert",
		// Deliberately contains no arrivals token (route/headsign) so the ordering
		// assertion below tests real placement, not a within-alert-line coincidence.
		Summary:       models.NLString{Value: "Elevator outage at this station"},
		ActiveWindows: []models.ActiveWindow{{From: 1776212160000, To: 1789383540000}},
	}}}
	return resp
}

func TestSMSHandler_ShowsServiceAlert(t *testing.T) {
	r, mockClient, _ := setupSMSTestRouter()

	mockStopOptions := []models.StopOption{{
		FullStopID: "1_75403", AgencyName: "King County Metro",
		StopName: "Pine St & 3rd Ave", DisplayText: "King County Metro: Pine St & 3rd Ave",
	}}
	mockResponse := createMockResponseWithAlert("1_75403")
	mockArrivals := createMockArrivals()

	mockClient.On("FindAllMatchingStops", "75403").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	w := sendSMSRequest(r, "+12345678901", "75403")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Service alert")
	assert.Contains(t, body, "Elevator outage")
	// Alert appears before the arrivals line ("Downtown" is Route 8's headsign from
	// createMockArrivals, and does not appear in the alert text).
	assert.Less(t, strings.Index(body, "Service alert"), strings.Index(body, "Downtown"))
	mockClient.AssertExpectations(t)
}

func TestSMSHandler_NoAlertWhenNoSituations(t *testing.T) {
	r, mockClient, _ := setupSMSTestRouter()
	mockStopOptions := []models.StopOption{{
		FullStopID: "1_75403", StopName: "Pine St & 3rd Ave",
		DisplayText: "King County Metro: Pine St & 3rd Ave",
	}}
	mockResponse := createMockResponse("1_75403") // no situations
	mockClient.On("FindAllMatchingStops", "75403").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(createMockArrivals())

	w := sendSMSRequest(r, "+12345678902", "75403")
	assert.NotContains(t, w.Body.String(), "Service alert")
	mockClient.AssertExpectations(t)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./handlers/ -run 'TestSMSHandler_ShowsServiceAlert|TestSMSHandler_NoAlertWhenNoSituations' -v 2>&1 | head -20`
Expected: FAIL — `TestSMSHandler_ShowsServiceAlert` fails (no "Service alert" in body). The
"no alert" test passes already (proves no regression); that's fine.

- [ ] **Step 3: Implement — insert the alert prepend in `handlers/sms.go`**

Immediately after the `message` is assigned (the `if len(arrivals) == 0 { ... } else { ... }`
block ending at line ~335) and **before** the `// Add menu hints if there are arrivals`
block, insert:

```go
	// Prepend active service alerts on the first page of a stop's arrivals so a rider
	// whose route is disrupted sees why before the times. Empty => unchanged output.
	if offset == 0 {
		if alertText := formatters.FormatSMSAlerts(obaResp.ActiveSituations(), h.LocalizationManager, session.Language); alertText != "" {
			message = alertText + "\n\n" + message
		}
	}
```

(`obaResp` and `offset` are already in scope in this function.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./handlers/ -run 'TestSMSHandler' -v 2>&1 | tail -30`
Expected: PASS (new alert tests + all existing SMS tests green).

- [ ] **Step 5: Commit**

```bash
git add handlers/sms.go handlers/sms_test.go
git commit -m "feat(sms): prepend active service alerts to stop arrivals"
```

---

### Task 6: Inject alerts into the voice handler (TDD)

**Files:**
- Modify: `handlers/voice/find_stop.go` (in `getAndFormatVoiceArrivalsWithSession`, right
  after `var elements []twiml.Element` at line ~384, before the arrivals `VoiceSay`)
- Modify: `handlers/voice/find_stop_test.go` (add a test; extend the mock response)

**Interfaces:**
- Consumes: `formatters.FormatVoiceAlerts` (Task 4); `resp.ActiveSituations()` (Task 2).

- [ ] **Step 1: Write the failing test** — append to `handlers/voice/find_stop_test.go`

This mirrors the existing `TestHandleFindStop_InvalidCallSidStillProceeds` single-stop
setup (mock wiring via `mockClient.On(...)`, `postFindStop`, route "8"), adding a situation
to the response. The mock and helpers (`newArrivalsResponse`, `postFindStop`,
`setupFindStopHandler`) already exist in this file.

```go
func TestHandleFindStop_SpeaksServiceAlert(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	stops := []models.StopOption{{FullStopID: "1_12345", StopName: "Test Stop"}}
	resp := newArrivalsResponse("1_12345")
	resp.CurrentTime = 1782940990927
	resp.Data.References = models.OBAReferences{Situations: []models.RawSituation{{
		ID:            "1_alert",
		Summary:       models.NLString{Value: "Elevator outage at this station"},
		ActiveWindows: []models.ActiveWindow{{From: 1776212160000, To: 1789383540000}},
	}}}
	mockClient.On("FindAllMatchingStops", "12345").Return(stops, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(resp, nil)
	mockClient.On("ProcessArrivals", resp, mock.Anything).Return([]models.Arrival{{RouteShortName: "8", MinutesUntilArrival: 5}})
	mockClient.On("GetStopInfo", "1_12345").Return(&stops[0], nil)

	w := postFindStop(r, "+14444444444", "12345")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Service alert")
	assert.Contains(t, body, "Elevator outage")
	// Alert is spoken before the arrivals line.
	assert.Less(t, strings.Index(body, "Service alert"), strings.Index(body, "Route 8"))
	mockClient.AssertExpectations(t)
}
```

> The test's localization manager (`localization.NewTestManager()`) may not carry the new
> `voice.alert.*` keys; that's fine — `FormatVoiceAlerts` falls back to the English
> "Service alert." lead-in when a key is absent, so the assertion holds either way.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./handlers/voice/ -run TestHandleFindStop_SpeaksServiceAlert -v 2>&1 | head -20`
Expected: FAIL — body does not contain "Service alert".

- [ ] **Step 3: Implement — insert the alert `VoiceSay` in `handlers/voice/find_stop.go`**

Immediately after `var elements []twiml.Element` (line ~384) and **before** the
`// Add arrivals message` / `arrivalsSay` block, insert:

```go
	// Speak active service alerts before arrivals so a caller on a disrupted route hears
	// why first. Empty => unchanged output.
	if alertText := formatters.FormatVoiceAlerts(obaResp.ActiveSituations(), h.LocalizationManager, language); alertText != "" {
		elements = append(elements, &twiml.VoiceSay{
			Message:  alertText,
			Language: language,
		})
	}
```

(`obaResp` and `language` are already in scope.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./handlers/voice/ -v 2>&1 | tail -30`
Expected: PASS (new test + existing voice tests green).

- [ ] **Step 5: Commit**

```bash
git add handlers/voice/find_stop.go handlers/voice/find_stop_test.go
git commit -m "feat(voice): speak active service alerts before arrivals"
```

---

### Task 7: Full gates + live smoke check

**Files:** none (verification only).

- [ ] **Step 1: Run all four required gates**

Run: `make fmt && make lint && make vet && make test`
Expected: all PASS. If `make fmt` changes files, review and commit them.

- [ ] **Step 2: Commit any formatting changes**

```bash
git add -A
git commit -m "chore: gofmt" || echo "nothing to format"
```

- [ ] **Step 3 (optional but recommended): Live smoke against Puget Sound**

Confirms real situations parse. Requires `ONEBUSAWAY_API_KEY` in `.env` (present in this repo).

Run:
```bash
go build -o /tmp/oba-twilio . && \
( set -a; . ./.env; set +a; PORT=8080 /tmp/oba-twilio & echo $! > /tmp/oba.pid; sleep 3; \
  curl -s -X POST localhost:8080/sms --data-urlencode 'From=+12065551234' --data-urlencode 'Body=1_75403'; \
  echo; kill "$(cat /tmp/oba.pid)" )
```
Expected: a `<Response><Message>...` body. If the queried stop currently has an active alert,
the message begins with the localized "Service alert:" prefix; otherwise it's the normal
arrivals text (both are correct outcomes — alerts are intermittent). Stop `1_75403` had an
active alert as of 2026-07-01.

- [ ] **Step 4: Final confirmation**

Run: `make test 2>&1 | tail -5`
Expected: PASS. Feature complete.

---

## Notes for the executor

- **Out of scope (do not build):** system-wide alerts at call start, GTFS-RT protobuf feed,
  severity-based filtering, reading URLs aloud/in SMS. These are deferred (spec §8).
- **No interface / mock-method changes.** If you feel tempted to add a method to
  `OneBusAwayClientInterface`, stop — extraction is `resp.ActiveSituations()` by design.
- Keep alert output a strict no-op when there are no active situations (regression safety).
