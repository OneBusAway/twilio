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

func TestRecordLookupOutcome_ErrorRemapsToNotFound(t *testing.T) {
	m := New()
	m.RecordLookupOutcome("sms", "error", "none")

	body := scrape(m)
	if !strings.Contains(body, `interactions_total{channel="sms",outcome="error"} 1`) {
		t.Errorf("missing interactions_total for error:\n%s", body)
	}
	if !strings.Contains(body, `stop_lookups_total{agency="none",result="not_found"} 1`) {
		t.Errorf("missing stop_lookups_total not_found remap:\n%s", body)
	}
}

func TestRecordLookupOutcome_NonErrorPassesThrough(t *testing.T) {
	m := New()
	m.RecordLookupOutcome("voice", "ambiguous", "40")

	body := scrape(m)
	if !strings.Contains(body, `interactions_total{channel="voice",outcome="ambiguous"} 1`) {
		t.Errorf("missing interactions_total for ambiguous:\n%s", body)
	}
	if !strings.Contains(body, `stop_lookups_total{agency="40",result="ambiguous"} 1`) {
		t.Errorf("missing stop_lookups_total ambiguous passthrough:\n%s", body)
	}
}
