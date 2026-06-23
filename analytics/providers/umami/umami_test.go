package umami

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEvent() analytics.Event {
	return analytics.Event{
		ID:         "evt-1",
		Name:       analytics.EventSMSRequest,
		Timestamp:  time.Now().UTC(),
		Version:    1,
		UserID:     "hashed-user-id",
		Properties: map[string]interface{}{"language": "en-US"},
	}
}

func TestTrackEventPostsCorrectRequest(t *testing.T) {
	var gotUA, gotPath string
	var gotBody payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body) // fully consume the request body
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cache":"c","sessionId":"s","visitId":"v"}`))
	}))
	defer srv.Close()

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid", Hostname: "api.example.org"})
	require.NoError(t, err)

	require.NoError(t, p.TrackEvent(context.Background(), newTestEvent()))
	assert.Equal(t, "/api/send", gotPath)
	assert.Contains(t, gotUA, "Mozilla/5.0 ")
	assert.Equal(t, "web-uuid", gotBody.Payload.Website)
	assert.Equal(t, "/sms", gotBody.Payload.URL)
	assert.Equal(t, "sms_request", gotBody.Payload.Name)
}

func TestTrackEventTreatsBeepBoopAsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"beep":"boop"}`))
	}))
	defer srv.Close()

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid"})
	require.NoError(t, err)
	assert.Error(t, p.TrackEvent(context.Background(), newTestEvent()))
}

func TestTrackEventNeverBlocksOnSlowServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hang until released
	}))
	defer srv.Close()
	defer close(release)

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid", HTTPTimeout: 100 * time.Millisecond})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- p.TrackEvent(context.Background(), newTestEvent()) }()

	select {
	case err := <-done:
		assert.Error(t, err) // timed out -> error, but returned promptly
	case <-time.After(2 * time.Second):
		t.Fatal("TrackEvent did not return within timeout")
	}
}

func TestCloseIsIdempotentlyGuarded(t *testing.T) {
	p, err := NewProvider(Config{ServerURL: "https://umami.example.com", WebsiteID: "web-uuid"})
	require.NoError(t, err)
	assert.NoError(t, p.Close())
	assert.ErrorIs(t, p.Close(), analytics.ErrProviderClosed)
	assert.ErrorIs(t, p.TrackEvent(context.Background(), newTestEvent()), analytics.ErrProviderClosed)
}

func TestServerDrainsRequestBody(t *testing.T) {
	var ok int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err == nil {
			atomic.StoreInt32(&ok, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid"})
	require.NoError(t, err)
	_ = p.TrackEvent(context.Background(), newTestEvent())
	assert.Equal(t, int32(1), atomic.LoadInt32(&ok))
}
