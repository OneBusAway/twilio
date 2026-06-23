package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"oba-twilio/privacy"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oba-twilio/analytics"
)

// Mock analytics manager for testing
type mockAnalyticsManager struct {
	events []analytics.Event
}

func (m *mockAnalyticsManager) TrackEvent(ctx context.Context, event analytics.Event) error {
	m.events = append(m.events, event.Clone())
	return nil
}

func (m *mockAnalyticsManager) getEvents() []analytics.Event {
	return m.events
}

func (m *mockAnalyticsManager) clear() {
	m.events = nil
}

func TestNewAnalyticsMiddleware(t *testing.T) {
	manager := &mockAnalyticsManager{}
	phoneHasher := privacy.NewHasher("test-salt", "")
	config := AnalyticsConfig{
		Enabled:     true,
		PhoneHasher: phoneHasher,
	}

	middleware := NewAnalyticsMiddleware(manager, config)

	assert.NotNil(t, middleware)
	assert.Equal(t, manager, middleware.manager)
	assert.Equal(t, config, middleware.config)
}

func TestAnalyticsMiddleware_HandlerDisabled(t *testing.T) {
	manager := &mockAnalyticsManager{}
	config := AnalyticsConfig{
		Enabled: false,
	}

	middleware := NewAnalyticsMiddleware(manager, config)
	handler := middleware.Handler()

	// Set up test request
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)

	// Execute middleware
	handler(c)

	// Should not track any events when disabled
	assert.Empty(t, manager.getEvents())
}

func TestAnalyticsMiddleware_HandlerNilManager(t *testing.T) {
	config := AnalyticsConfig{
		Enabled: true,
	}

	middleware := NewAnalyticsMiddleware(nil, config)
	handler := middleware.Handler()

	// Set up test request
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)

	// Execute middleware
	handler(c)

	// Should not panic with nil manager
}

func TestAnalyticsMiddleware_HandlerEnabled(t *testing.T) {
	manager := &mockAnalyticsManager{}
	phoneHasher := privacy.NewHasher("test-salt", "")
	config := AnalyticsConfig{
		Enabled:     true,
		PhoneHasher: phoneHasher,
	}

	middleware := NewAnalyticsMiddleware(manager, config)
	handler := middleware.Handler()

	// Set up test request with phone number
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	form := url.Values{}
	form.Add("From", "+12065551234")
	c.Request = httptest.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request.Header.Set("User-Agent", "Test-Agent")

	// Set up router with middleware and test handler
	router := gin.New()
	router.Use(handler)

	nextCalled := false
	router.POST("/sms", func(c *gin.Context) {
		nextCalled = true
		c.Status(http.StatusOK)
	})

	// Execute request
	router.ServeHTTP(w, c.Request)

	// Verify handler was called
	assert.True(t, nextCalled)
	assert.Equal(t, http.StatusOK, w.Code)

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	// Should track request completion event
	events := manager.getEvents()
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "request_completed", event.Name)
	assert.NotEmpty(t, event.UserID) // Hashed phone number
	assert.Equal(t, "POST", event.Properties["method"])
	assert.Equal(t, "/sms", event.Properties["path"])
	assert.Equal(t, 200, event.Properties["status_code"])
	assert.Contains(t, event.Properties, "duration_ms")
	assert.Equal(t, "Test-Agent", event.Properties["user_agent"])
}

func TestAnalyticsMiddleware_ExtractPhoneNumber(t *testing.T) {
	middleware := &AnalyticsMiddleware{}

	tests := []struct {
		name         string
		setupRequest func(*gin.Context)
		expected     string
	}{
		{
			name: "from form data",
			setupRequest: func(c *gin.Context) {
				form := url.Values{}
				form.Add("From", "+12065551234")
				c.Request = httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
				c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			},
			expected: "+12065551234",
		},
		{
			name: "from query parameter",
			setupRequest: func(c *gin.Context) {
				c.Request = httptest.NewRequest("GET", "/?From=%2B12065551234", nil)
			},
			expected: "+12065551234",
		},
		{
			name: "no phone number",
			setupRequest: func(c *gin.Context) {
				c.Request = httptest.NewRequest("GET", "/", nil)
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			tt.setupRequest(c)

			result := middleware.extractPhoneNumber(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTrackSMSRequest(t *testing.T) {
	manager := &mockAnalyticsManager{}
	ctx := context.WithValue(context.Background(), RequestIDKey, "test-request-123")
	phoneHasher := privacy.NewHasher("test-salt", "")
	userID := phoneHasher.ConstructUserId("+12065551234")

	TrackSMSRequest(ctx, manager, userID, "es-US", "75403")

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	events := manager.getEvents()
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "sms_request", event.Name)
	assert.NotEmpty(t, event.UserID)
	assert.Equal(t, "es-US", event.Properties["language"])
	assert.Equal(t, "75403", event.Properties["query"])
	assert.Equal(t, "test-request-123", event.Properties["request_id"])
}

func TestTrackVoiceRequest(t *testing.T) {
	manager := &mockAnalyticsManager{}
	ctx := context.WithValue(context.Background(), RequestIDKey, "test-request-456")

	phoneHasher := privacy.NewHasher("test-salt", "")
	userID := phoneHasher.ConstructUserId("+12065551234")
	TrackVoiceRequest(ctx, manager, userID, "fr-US")

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	events := manager.getEvents()
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "voice_request", event.Name)
	assert.NotEmpty(t, event.UserID)
	assert.Equal(t, "fr-US", event.Properties["language"])
	assert.Equal(t, "test-request-456", event.Properties["request_id"])
}

func TestTrackStopLookup(t *testing.T) {
	manager := &mockAnalyticsManager{}
	ctx := context.Background()
	phoneHasher := privacy.NewHasher("test-salt", "")
	userID := phoneHasher.ConstructUserId("+12065551234")

	// Test successful lookup
	TrackStopLookup(ctx, manager, userID, "1_75403", "1", true, 150)

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	events := manager.getEvents()
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "stop_lookup_success", event.Name)
	assert.NotEmpty(t, event.UserID)
	assert.Equal(t, "1_75403", event.Properties["stop_id"])
	assert.Equal(t, "1", event.Properties["agency_id"])
	assert.Equal(t, int64(150), event.Properties["latency_ms"])

	// Test failed lookup
	manager.clear()
	TrackStopLookup(ctx, manager, userID, "invalid", "", false, 50)

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	events = manager.getEvents()
	assert.Len(t, events, 1)

	event = events[0]
	assert.Equal(t, "stop_lookup_failure", event.Name)
}

func TestTrackError(t *testing.T) {
	manager := &mockAnalyticsManager{}
	ctx := context.Background()
	phoneHasher := privacy.NewHasher("test-salt", "")
	userID := phoneHasher.ConstructUserId("+12065551234")

	TrackError(ctx, manager, userID, "api_error", "connection timeout")

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	events := manager.getEvents()
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "error_occurred", event.Name)
	assert.NotEmpty(t, event.UserID)
	assert.Equal(t, "api_error", event.Properties["error_type"])
	assert.Equal(t, "connection timeout", event.Properties["error_message"])
}

func TestTrackDisambiguationPresented(t *testing.T) {
	manager := &mockAnalyticsManager{}
	ctx := context.Background()
	phoneHasher := privacy.NewHasher("test-salt", "")
	userID := phoneHasher.ConstructUserId("+12065551234")

	TrackDisambiguationPresented(ctx, manager, userID, "session-123", 3)

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	events := manager.getEvents()
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "sms_disambiguation_presented", event.Name)
	assert.NotEmpty(t, event.UserID)
	assert.Equal(t, "session-123", event.SessionID)
	assert.Equal(t, 3, event.Properties["choice_count"])
}

func TestTrackDisambiguationSelected(t *testing.T) {
	manager := &mockAnalyticsManager{}
	ctx := context.Background()
	phoneHasher := privacy.NewHasher("test-salt", "")
	userID := phoneHasher.ConstructUserId("+12065551234")

	TrackDisambiguationSelected(ctx, manager, userID, "session-123", 2, "1_75403")

	// Give time for goroutine to complete
	time.Sleep(50 * time.Millisecond)

	events := manager.getEvents()
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "sms_disambiguation_selected", event.Name)
	assert.NotEmpty(t, event.UserID)
	assert.Equal(t, "session-123", event.SessionID)
	assert.Equal(t, 2, event.Properties["choice_index"])
	assert.Equal(t, "1_75403", event.Properties["stop_id"])
}

func TestTrackFunctionsWithNilManager(t *testing.T) {
	ctx := context.Background()

	// All functions should handle nil manager gracefully
	phoneHasher := privacy.NewHasher("test-salt", "")
	userID := phoneHasher.ConstructUserId("+12065551234")

	TrackSMSRequest(ctx, nil, userID, "en-US", "75403")
	TrackVoiceRequest(ctx, nil, userID, "en-US")
	TrackStopLookup(ctx, nil, userID, "75403", "1", true, 100)
	TrackError(ctx, nil, userID, "error", "message")
	TrackDisambiguationPresented(ctx, nil, userID, "session", 3)
	TrackDisambiguationSelected(ctx, nil, userID, "session", 1, "stop")

	// Should not panic
}

func TestGenerateRequestID(t *testing.T) {
	id1 := generateRequestID()
	time.Sleep(1 * time.Millisecond)
	id2 := generateRequestID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
}
