package umami

import (
	"bytes"
	"fmt"
	"strings"

	"oba-twilio/analytics"
)

// maxDataValueLen caps string values forwarded in the Umami "data" object.
// The SMS query property is uncontrolled user input, so it is truncated.
const maxDataValueLen = 256

// pathForEvent maps an analytics event name to a Umami url path so dashboards
// group sensibly. Known events never return the "/" default.
func pathForEvent(name string) string {
	switch {
	case strings.HasPrefix(name, "sms_"):
		return "/sms"
	case strings.HasPrefix(name, "voice_"):
		return "/voice"
	case strings.HasPrefix(name, "stop_lookup"):
		return "/stop"
	case strings.HasPrefix(name, "error"):
		return "/error"
	case name == analytics.EventLanguageDetected || name == analytics.EventAPILatency:
		return "/system"
	default:
		return "/"
	}
}

// channelForEvent derives the Twilio channel from the event name.
func channelForEvent(name string) string {
	switch {
	case strings.HasPrefix(name, "sms_"):
		return "SMS"
	case strings.HasPrefix(name, "voice_"):
		return "Voice"
	default:
		return "Server"
	}
}

// buildUserAgent returns a browser-shaped, per-caller User-Agent. It must lead
// with "Mozilla/5.0 " so Umami's isbot check does not drop the event.
func buildUserAgent(event analytics.Event) string {
	id := event.UserID
	switch id {
	case "":
		id = "anon"
	default:
		r := []rune(id)
		if len(r) > 12 {
			id = string(r[:12])
		}
	}
	return fmt.Sprintf("Mozilla/5.0 (OneBusAway-Twilio; %s; %s) Server/1.0", channelForEvent(event.Name), id)
}

// sanitizeData keeps JSON-friendly types, stringifies the rest, truncates long
// strings, and drops empty keys / nil values.
func sanitizeData(props map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(props))
	for k, v := range props {
		if k == "" || v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			out[k] = truncate(val)
		case bool,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64:
			out[k] = val
		default:
			out[k] = truncate(fmt.Sprintf("%v", val))
		}
	}
	return out
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) > maxDataValueLen {
		return string(r[:maxDataValueLen])
	}
	return s
}

// isSuccessfulIngest reports whether Umami actually ingested the event. Umami
// returns the isbot drop as HTTP 200 + {"beep":"boop"}, so the body must be
// inspected. Parsing is tolerant of non-JSON bodies.
func isSuccessfulIngest(statusCode int, body []byte) bool {
	if statusCode < 200 || statusCode >= 300 {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return true
	}
	if bytes.Contains(trimmed, []byte(`"beep"`)) {
		return false
	}
	return bytes.Contains(trimmed, []byte(`"cache"`)) ||
		bytes.Contains(trimmed, []byte(`"sessionId"`)) ||
		bytes.Contains(trimmed, []byte(`"visitId"`))
}
