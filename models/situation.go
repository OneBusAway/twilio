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
