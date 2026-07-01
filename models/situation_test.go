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
