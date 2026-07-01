package models

import (
	"bytes"
	"encoding/json"
	"strings"
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
