package analytics

import "errors"

// Common errors for analytics operations
var (
	// Event validation errors
	ErrMissingEventID   = errors.New("event ID is required")
	ErrMissingEventName = errors.New("event name is required")
	ErrMissingTimestamp = errors.New("event timestamp is required")

	// Provider errors
	ErrProviderNotFound    = errors.New("analytics provider not found")
	ErrProviderUnavailable = errors.New("analytics provider is unavailable")
	ErrProviderClosed      = errors.New("analytics provider is closed")

	// Manager errors
	ErrManagerNotInitialized = errors.New("analytics manager not initialized")
	ErrManagerClosed         = errors.New("analytics manager is closed")
	ErrEventQueueFull        = errors.New("event queue is full")

	// Configuration errors
	ErrInvalidConfiguration = errors.New("invalid analytics configuration")
	ErrMissingAPIKey        = errors.New("analytics API key is required")
	ErrMissingDomain        = errors.New("analytics domain is required")
	ErrMissingServerURL     = errors.New("analytics server URL is required")
	ErrMissingWebsiteID     = errors.New("analytics website ID is required")
)
