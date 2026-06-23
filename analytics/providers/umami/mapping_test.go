package umami

import (
	"strings"
	"testing"
	"unicode/utf8"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
)

// emittedEventNames lists every event-name constant the app sends to Umami.
// Adding a new emitted event means adding it here; the test then forces a
// non-default path mapping so events never silently land in "/".
var emittedEventNames = []string{
	analytics.EventSMSRequest,
	analytics.EventSMSResponse,
	analytics.EventSMSDisambiguationPresent,
	analytics.EventSMSDisambiguationSelect,
	analytics.EventVoiceRequest,
	analytics.EventVoiceResponse,
	analytics.EventVoiceDTMFInput,
	analytics.EventVoiceMenuChoice,
	analytics.EventStopLookup,
	analytics.EventStopLookupSuccess,
	analytics.EventStopLookupFailure,
	analytics.EventLanguageDetected,
	analytics.EventErrorOccurred,
	analytics.EventAPILatency,
}

func TestPathForEventNeverDefaultForKnownEvents(t *testing.T) {
	for _, name := range emittedEventNames {
		assert.NotEqualf(t, "/", pathForEvent(name), "event %q must map to a non-default path", name)
	}
}

func TestPathForEvent(t *testing.T) {
	cases := map[string]string{
		analytics.EventSMSRequest:       "/sms",
		analytics.EventVoiceMenuChoice:  "/voice",
		analytics.EventStopLookup:       "/stop",
		analytics.EventErrorOccurred:    "/error",
		analytics.EventLanguageDetected: "/system",
		"unknown_event":                 "/",
	}
	for name, want := range cases {
		assert.Equalf(t, want, pathForEvent(name), "path for %q", name)
	}
}

func TestBuildUserAgentIsBrowserShaped(t *testing.T) {
	ua := buildUserAgent(analytics.Event{Name: analytics.EventSMSRequest, UserID: "0123456789abcdef"})
	assert.True(t, strings.HasPrefix(ua, "Mozilla/5.0 "), "UA must be browser-shaped: %q", ua)
	assert.Contains(t, ua, "SMS")
	assert.Contains(t, ua, "0123456789ab") // first 12 of the hash
	assert.NotContains(t, ua, "0123456789abcdef")

	// Bare product-token style (the rejected design) must NOT be produced.
	assert.NotRegexp(t, `^\w+/[\w()]*$`, ua)

	anon := buildUserAgent(analytics.Event{Name: analytics.EventVoiceRequest})
	assert.Contains(t, anon, "Voice")
	assert.Contains(t, anon, "anon")
}

func TestSanitizeData(t *testing.T) {
	long := strings.Repeat("x", 300)
	out := sanitizeData(map[string]interface{}{
		"keep_str":  "hello",
		"keep_int":  42,
		"keep_bool": true,
		"":          "drop_empty_key",
		"nil_val":   nil,
		"struct":    struct{ A int }{1},
		"long":      long,
	})
	assert.Equal(t, "hello", out["keep_str"])
	assert.Equal(t, 42, out["keep_int"])
	assert.Equal(t, true, out["keep_bool"])
	assert.NotContains(t, out, "")
	assert.NotContains(t, out, "nil_val")
	assert.Equal(t, "{1}", out["struct"]) // stringified
	assert.Len(t, out["long"], maxDataValueLen)

	// Rune-safety: a 300-rune multi-byte string must truncate to exactly
	// maxDataValueLen runes and remain valid UTF-8.
	multiByteInput := strings.Repeat("é", 300) // "é" is 2 bytes in UTF-8
	out2 := sanitizeData(map[string]interface{}{"mb": multiByteInput})
	result, _ := out2["mb"].(string)
	assert.Equal(t, maxDataValueLen, utf8.RuneCountInString(result), "truncation must count runes not bytes")
	assert.True(t, utf8.ValidString(result), "truncated string must be valid UTF-8")
}

func TestIsSuccessfulIngest(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want bool
	}{
		{"beep boop", 200, `{"beep":"boop"}`, false},
		{"success cache", 200, `{"cache":"x","sessionId":"y","visitId":"z"}`, true},
		{"success sessionId only", 200, `{"sessionId":"y"}`, true},
		{"empty body", 200, ``, true},
		{"non-2xx", 500, `{"sessionId":"y"}`, false},
		{"non-json no positive field treated as failure", 200, `OK`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isSuccessfulIngest(tc.code, []byte(tc.body)))
		})
	}
}
