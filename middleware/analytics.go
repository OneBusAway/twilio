// Package middleware provides HTTP middleware for the OneBusAway Twilio integration.
package middleware

import (
	"context"
	"oba-twilio/privacy"
	"time"

	"github.com/gin-gonic/gin"

	"oba-twilio/analytics"
)

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

const (
	// RequestIDKey is the context key for request IDs.
	RequestIDKey ContextKey = "request_id"
)

// AnalyticsMiddleware provides analytics tracking for HTTP requests.
type AnalyticsMiddleware struct {
	manager AnalyticsManager
	config  AnalyticsConfig
}

// AnalyticsManager defines the interface for analytics management.
type AnalyticsManager interface {
	TrackEvent(ctx context.Context, event analytics.Event) error
}

// AnalyticsConfig holds configuration for analytics middleware.
type AnalyticsConfig struct {
	Enabled     bool
	PhoneHasher *privacy.Hasher
}

// NewAnalyticsMiddleware creates a new analytics middleware.
func NewAnalyticsMiddleware(manager AnalyticsManager, config AnalyticsConfig) *AnalyticsMiddleware {
	return &AnalyticsMiddleware{
		manager: manager,
		config:  config,
	}
}

// Handler returns a Gin middleware function for analytics tracking.
func (am *AnalyticsMiddleware) Handler() gin.HandlerFunc {
	if !am.config.Enabled || am.manager == nil {
		// Return no-op middleware if analytics is disabled
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		start := time.Now()

		// Add analytics context to request
		ctx := am.addAnalyticsContext(c)
		c.Request = c.Request.WithContext(ctx)

		// Process request
		c.Next()

		// Track request completion
		am.trackRequestCompletion(c, start)
	}
}

// addAnalyticsContext adds analytics-specific context to the request.
func (am *AnalyticsMiddleware) addAnalyticsContext(c *gin.Context) context.Context {
	ctx := c.Request.Context()

	// Add request ID for correlation
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = generateRequestID()
	}

	ctx = context.WithValue(ctx, RequestIDKey, requestID)
	return ctx
}

// trackRequestCompletion tracks request completion metrics.
func (am *AnalyticsMiddleware) trackRequestCompletion(c *gin.Context, start time.Time) {
	duration := time.Since(start)

	// Extract phone number for user identification
	phoneNumber := am.extractPhoneNumber(c)
	userID := ""
	if phoneNumber != "" {
		userID = am.config.PhoneHasher.ConstructUserId(phoneNumber)
	}

	// Create request completion event
	event := analytics.NewEvent("request_completed", userID)
	event.Properties["method"] = c.Request.Method
	event.Properties["path"] = c.Request.URL.Path
	event.Properties["status_code"] = c.Writer.Status()
	event.Properties["duration_ms"] = duration.Milliseconds()
	event.Properties["user_agent"] = c.GetHeader("User-Agent")

	// Add request ID if available
	if requestID, ok := c.Request.Context().Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	// Track the event (non-blocking)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := am.manager.TrackEvent(ctx, event); err != nil {
			// Log error but don't fail the request
			// In a real implementation, you might want to use a proper logger
			_ = err // Explicitly ignore error to silence staticcheck
		}
	}()
}

// extractPhoneNumber extracts the phone number from the request.
func (am *AnalyticsMiddleware) extractPhoneNumber(c *gin.Context) string {
	// Try form data first (Twilio webhooks)
	if phoneNumber := c.PostForm("From"); phoneNumber != "" {
		return phoneNumber
	}

	// Try query parameters
	if phoneNumber := c.Query("From"); phoneNumber != "" {
		return phoneNumber
	}

	return ""
}

// generateRequestID generates a simple request ID.
func generateRequestID() string {
	// In a real implementation, you might want to use a proper UUID library
	// For now, use timestamp-based ID
	return time.Now().Format("20060102150405.000000")
}

// Helper functions for handlers to track specific events

// TrackSMSRequest tracks an incoming SMS request.
func TrackSMSRequest(ctx context.Context, manager AnalyticsManager, userID, language, query string) {
	if manager == nil {
		return
	}

	event := analytics.SMSRequestEvent(userID, language, query)

	// Add request ID if available
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}

// TrackVoiceMenuChoice tracks a voice menu (DTMF) selection.
func TrackVoiceMenuChoice(ctx context.Context, manager AnalyticsManager, userID, digits string) {
	if manager == nil {
		return
	}

	event := analytics.VoiceMenuChoiceEvent(userID, digits)

	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}

// TrackVoiceRequest tracks an incoming voice call.
func TrackVoiceRequest(ctx context.Context, manager AnalyticsManager, userID, language string) {
	if manager == nil {
		return
	}

	event := analytics.VoiceRequestEvent(userID, language)

	// Add request ID if available
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}

// TrackStopLookup tracks a stop lookup attempt.
func TrackStopLookup(ctx context.Context, manager AnalyticsManager, userID, stopID, agencyID string, success bool, latencyMS int64) {
	if manager == nil {
		return
	}

	event := analytics.StopLookupEvent(userID, stopID, agencyID, success, latencyMS)

	// Add request ID if available
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}

// TrackError tracks an error occurrence.
func TrackError(ctx context.Context, manager AnalyticsManager, userID, errorType, errorMessage string) {
	if manager == nil {
		return
	}

	event := analytics.ErrorEvent(userID, errorType, errorMessage)

	// Add request ID if available
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}

// TrackDisambiguationPresented tracks when multiple stop choices are shown.
func TrackDisambiguationPresented(ctx context.Context, manager AnalyticsManager, userID, sessionID string, choiceCount int) {
	if manager == nil {
		return
	}

	event := analytics.DisambiguationPresentedEvent(userID, sessionID, choiceCount)

	// Add request ID if available
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}

// TrackDisambiguationSelected tracks when a user selects from multiple stops.
func TrackDisambiguationSelected(ctx context.Context, manager AnalyticsManager, userID, sessionID string, choiceIndex int, stopID string) {
	if manager == nil {
		return
	}

	event := analytics.DisambiguationSelectedEvent(userID, sessionID, choiceIndex, stopID)

	// Add request ID if available
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		event.Properties["request_id"] = requestID
	}

	go func() {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.TrackEvent(trackCtx, event)
	}()
}
