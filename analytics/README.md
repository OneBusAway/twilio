# Analytics Package

The analytics package provides a flexible, extensible system for tracking user interactions and system events in the OneBusAway Twilio integration.

## Features

- **Privacy-first design**: Phone numbers are hashed before tracking
- **Non-blocking**: Analytics never impacts user request handling
- **Extensible**: Easy to add new analytics providers
- **Fault-tolerant**: Circuit breaker pattern prevents cascading failures
- **Performant**: Worker pool pattern for controlled concurrency

## Architecture

### Core Components

- **Analytics Interface**: Defines the contract all providers must implement
- **Event Types**: Structured events with validation
- **Circuit Breaker**: Protects against provider failures
- **Mock Provider**: For testing analytics integration

### Event Types

The package defines several standard event types:

- `sms_request`: Incoming SMS message
- `voice_request`: Incoming voice call
- `stop_lookup_success/failure`: Stop information queries
- `disambiguation_presented/selected`: Multiple stop choices
- `error_occurred`: User-facing errors
- `api_latency`: Performance metrics

## Usage

```go
// Create a Hasher
h := privacy.NewHasher(analyticsSalt,logSalt)
// Create an event
event := analytics.SMSRequestEvent(
    h.ConstructUserId(phoneNumber),
    "en-US",
    "75403",
)

// Track the event (non-blocking)
err := analyticsManager.TrackEvent(ctx, event)
```

## Privacy

- Phone numbers are hashed using SHA256 with a salt
- No message content is ever logged
- Minimal data collection principle

## Adding a New Provider

1. Create a new package under `analytics/providers/`
2. Implement the `Analytics` interface
3. Register with the manager during initialization

## Testing

The package includes a `MockProvider` for testing:

```go
mock := analytics.NewMockProvider()
// ... use mock in tests ...
events := mock.GetEvents()
```