package umami

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- p.TrackEvent(context.Background(), newTestEvent()) }()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		assert.Error(t, err) // timed out -> error, but returned promptly
		assert.Less(t, elapsed, 800*time.Millisecond)
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

// TestTrackEventReusesConnection verifies that TrackEvent fully drains and
// closes the response body so that the underlying TCP connection is returned to
// the keep-alive pool. If both calls share the same RemoteAddr (client port),
// the connection was reused — which only happens when the response body is
// properly drained and closed.
func TestTrackEventReusesConnection(t *testing.T) {
	var mu sync.Mutex
	var addrs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		addrs = append(addrs, r.RemoteAddr)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sessionId":"s"}`))
	}))
	defer srv.Close()

	p, err := NewProvider(Config{ServerURL: srv.URL, WebsiteID: "web-uuid"})
	require.NoError(t, err)

	require.NoError(t, p.TrackEvent(context.Background(), newTestEvent()))
	require.NoError(t, p.TrackEvent(context.Background(), newTestEvent()))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, addrs, 2, "expected exactly two requests")
	assert.Equal(t, addrs[0], addrs[1], "both requests should reuse the same TCP connection (same RemoteAddr)")
}
