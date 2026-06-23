package voice

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"oba-twilio/analytics"
	"oba-twilio/localization"
	"oba-twilio/models"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForEvents polls the mock provider until it records at least n events or times out.
func waitForEvents(t *testing.T, mock *analytics.MockProvider, n int) []analytics.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mock.GetEventCount() >= n {
			return mock.GetEvents()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return mock.GetEvents()
}

func newTestVoiceHandler(t *testing.T) (*Handler, *analytics.Manager, *analytics.MockProvider) {
	t.Helper()
	locManager := localization.NewTestManager()

	cfg := analytics.DefaultConfig()
	cfg.Enabled = true
	cfg.HashSalt = "test-salt"
	mgr := analytics.NewManager(cfg)
	require.NoError(t, mgr.Start())
	mock := analytics.NewMockProvider()
	require.NoError(t, mgr.RegisterProvider("mock", mock))

	h := NewHandler(nil, locManager)
	h.SetAnalytics(mgr, "test-salt")
	return h, mgr, mock
}

func TestFindStopEmitsStopLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mgr, mock := newTestVoiceHandler(t)
	defer func() { _ = mgr.Close() }()

	// Wire in a real-enough OBA client so the stop lookup path executes.
	mockClient := &mockOBAClient{}
	expectSingleStopArrivals(mockClient, "12345", "1_12345", "8")
	h.OBAClient = mockClient

	r := gin.New()
	r.POST("/voice/find_stop", h.HandleFindStop)

	form := url.Values{"From": {"+15559876543"}, "Digits": {"12345"}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	events := waitForEvents(t, mock, 1)
	require.GreaterOrEqual(t, len(events), 1, "expected at least one analytics event")

	var found bool
	for _, e := range events {
		if e.Name == analytics.EventStopLookupSuccess {
			found = true
			assert.Equal(t, "12345", e.Properties[analytics.PropStopID])
		}
	}
	assert.True(t, found, "expected a stop_lookup_success event")
	mockClient.AssertExpectations(t)
}

func TestFindStopEmitsStopLookupFailureOnNoMatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mgr, mock := newTestVoiceHandler(t)
	defer func() { _ = mgr.Close() }()

	// No matching stops and no error: a user-facing failure, so analytics must
	// record stop_lookup_failure, not stop_lookup_success.
	mockClient := &mockOBAClient{}
	mockClient.On("FindAllMatchingStops", "54321").Return([]models.StopOption{}, nil)
	h.OBAClient = mockClient

	r := gin.New()
	r.POST("/voice/find_stop", h.HandleFindStop)

	form := url.Values{"From": {"+15559876543"}, "Digits": {"54321"}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	events := waitForEvents(t, mock, 1)
	require.GreaterOrEqual(t, len(events), 1, "expected at least one analytics event")

	var sawFailure, sawSuccess bool
	for _, e := range events {
		switch e.Name {
		case analytics.EventStopLookupFailure:
			sawFailure = true
			assert.Equal(t, "54321", e.Properties[analytics.PropStopID])
		case analytics.EventStopLookupSuccess:
			sawSuccess = true
		}
	}
	assert.True(t, sawFailure, "expected a stop_lookup_failure event for a zero-result lookup")
	assert.False(t, sawSuccess, "a zero-result lookup must not emit stop_lookup_success")
	mockClient.AssertExpectations(t)
}

func TestMenuActionEmitsMenuChoice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mgr, mock := newTestVoiceHandler(t)
	defer func() { _ = mgr.Close() }()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	form := url.Values{"From": {"+15551234567"}, "Digits": {"2"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/voice/menu_action", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	h.HandleVoiceMenuAction(c)

	events := waitForEvents(t, mock, 1)
	require.GreaterOrEqual(t, len(events), 1)
	var found bool
	for _, e := range events {
		if e.Name == analytics.EventVoiceMenuChoice {
			found = true
			assert.Equal(t, "2", e.Properties[analytics.PropDTMFDigits])
		}
	}
	assert.True(t, found, "expected a voice_menu_choice event")
}
