package umami

import (
	"encoding/json"
	"testing"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertEvent(t *testing.T) {
	cfg := Config{ServerURL: "https://umami.example.com", WebsiteID: "web-uuid", Hostname: "api.example.org"}
	ev := analytics.Event{
		Name:       analytics.EventSMSRequest,
		UserID:     "hashed-user",
		SessionID:  "sess-1",
		Properties: map[string]interface{}{"language": "en-US", "query": "75403"},
	}

	p := convertEvent(cfg, ev)
	assert.Equal(t, "event", p.Type)
	assert.Equal(t, "web-uuid", p.Payload.Website)
	assert.Equal(t, "api.example.org", p.Payload.Hostname)
	assert.Equal(t, "/sms", p.Payload.URL)
	assert.Equal(t, "sms_request", p.Payload.Name)
	assert.Equal(t, "en-US", p.Payload.Data["language"])
	assert.Equal(t, "75403", p.Payload.Data["query"])
	assert.Equal(t, "hashed-user", p.Payload.Data["user_id"])
	assert.Equal(t, "sess-1", p.Payload.Data["session_id"])
}

func TestConvertEventOmitsEmptyData(t *testing.T) {
	cfg := Config{ServerURL: "https://umami.example.com", WebsiteID: "web-uuid", Hostname: "api.example.org"}
	ev := analytics.Event{Name: analytics.EventVoiceRequest}

	p := convertEvent(cfg, ev)
	b, err := json.Marshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"data"`)
	assert.Contains(t, string(b), `"name":"voice_request"`)
	assert.Contains(t, string(b), `"url":"/voice"`)
}
