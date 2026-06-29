# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OneBusAway Twilio Integration - A Go web application that bridges Twilio SMS and voice services with OneBusAway transit APIs, allowing users to get real-time bus arrival information via text message or phone call. The application supports multiple languages and provides a complete localization system.

## Important Rules

Before saying your work is finished, you must always run these commands and ensure they pass:

* make lint
* make vet
* make test
* make fmt

## Common Development Commands

### Building and Running
```bash
# Build the application
make build

# Run in development mode
make dev

# Build and run
make run

# Run with environment variables
PORT=3000 ONEBUSAWAY_API_KEY=your_key SUPPORTED_LANGUAGES=en-US,es-US go run main.go
```

### Testing
```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run tests with verbose output (includes live API calls)
make test-verbose

# Skip integration tests (faster for development)
make test-short

# Test individual packages
go test ./client         # Test OneBusAway client
go test ./formatters     # Test response formatting
go test ./localization   # Test localization system
go test .               # Test main application logic
```

### Dependencies
```bash
# Install/update dependencies
make deps

# Tidy up go.mod and go.sum
make tidy

# Clean build artifacts
make clean

# Format code
make fmt
```

## Architecture

### Core Components

**main.go** - HTTP server setup with Gin routes and initialization
- Loads environment configuration (.env file support)
- Initializes localization system with multi-language support
- Initializes OneBusAway client with coverage area detection
- Sets up SMS and voice handlers with localization context
- Configures health check endpoints

**client/onebusaway.go** - OneBusAway API client with caching and circuit breaker
- Manages API calls to OneBusAway REST API
- Handles stop ID resolution across multiple agencies (1_, 40_, 29_ prefixes for Puget Sound)
- Implements coverage area detection and geographic bounds for stop searching
- Includes caching (5min general, 1min arrivals, 60min coverage) and circuit breaker pattern

**handlers/** - Request handlers for Twilio webhooks
- `sms.go` - SMS message processing with disambiguation support
- `voice.go` - Voice/IVR call handling with DTMF input
- `session_store.go` - In-memory session management for multi-step interactions

**formatters/** - Response generation
- `response.go` - TwiML generation and text formatting for SMS/Voice responses
- `voice_templates.go` - Template-based voice response system using embedded XML templates
- `templates/*.xml` - Voice response templates (start, error, disambiguation, find_stop)

**localization/** - Multi-language support system
- `manager.go` - Thread-safe localization manager with concurrent string lookup
- `types.go` - Localization data structures and interfaces
- `test_helper.go` - Testing utilities for localization
- Supports 10 languages: English, Spanish, French, German, Arabic, Korean, Polish, Portuguese, Russian, Chinese

**locales/** - Language-specific string resources
- JSON files for each supported language (en-US.json, es-US.json, etc.)
- Voice prompts, error messages, and user interface strings
- Structured for easy addition of new languages

**metrics/** - Prometheus instrumentation
- Owns the private Prometheus registry, HTTP middleware (records `http_requests_total` / `http_request_duration_seconds`), and bridge collectors that surface OBA client and session-store counters at scrape time

**models/** - Data structures and error definitions
- `types.go` - Twilio requests and OneBusAway API response structures
- `errors.go` - Custom error types and error handling

### Key Features

**Multi-language Support**: Complete localization system supporting 10 languages with thread-safe string lookup

**Stop ID Resolution**: Automatically tries agency prefixes (1_75403, 40_75403, etc.) when user provides numeric stop ID (75403)

**Disambiguation System**: When multiple stops match the same ID, presents numbered choices via SMS and stores session state

**Coverage Area Detection**: Calculates geographic center and radius from all transit agencies at startup to optimize stop searches

**Template-based Voice Responses**: Uses embedded XML templates for consistent TwiML generation with localized strings

**Caching Strategy**: Multi-tier caching with different TTLs - 5min for general data, 1min for time-sensitive arrivals, 60min for static coverage data

## Environment Configuration

Required:
- `ONEBUSAWAY_API_KEY` - API key for OneBusAway server (must not be "test" or placeholder values)

Optional:
- `PORT` - Server port (default: 8080)
- `ONEBUSAWAY_BASE_URL` - API base URL (default: https://api.pugetsound.onebusaway.org)
- `SUPPORTED_LANGUAGES` - Comma-separated language codes (default: "en-US")
- `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN` - For outbound Twilio features
- `UMAMI_ENABLED` - Enable the Umami analytics provider (default: false)
- `UMAMI_URL` - Umami host; events POST to `<UMAMI_URL>/api/send` (required when enabled)
- `UMAMI_WEBSITE_ID` - Umami website UUID (required when enabled)
- `UMAMI_HOSTNAME` - `hostname` field in emitted events (default: host of `ONEBUSAWAY_BASE_URL`, else `twilio.onebusaway.org`)
- `UMAMI_HTTP_TIMEOUT` - Per-request timeout, Go duration (default: 5s)

Environment file support via `.env` file in project root.

## API Endpoints

- `POST /sms` - Handle incoming SMS messages (Twilio webhook)
- `POST /voice` - Handle incoming voice calls (Twilio webhook)
- `POST /voice/find_stop` - Handle voice input/DTMF (Twilio webhook)
- `GET /health` - Health check endpoint
- `GET /` - Application info with coverage area status
- `GET /metrics` - Prometheus metrics (client_golang)

## Testing Approach

**Unit Tests**: Focus on individual package functionality
- `client/*_test.go` - API client and coverage calculation tests
- `formatters/*_test.go` - Response formatting and template tests
- `handlers/*_test.go` - Request handling and disambiguation logic tests
- `localization/*_test.go` - Localization manager and string lookup tests

**Integration Tests**: Include live API calls (skipped with `-short` flag)
- Test real OneBusAway API responses
- Verify stop ID resolution across agencies
- Test coverage area calculation

**Manual Testing**: cURL commands for webhook simulation provided in README

## Multi-Server Support

Designed to work with any OneBusAway deployment by configuring `ONEBUSAWAY_BASE_URL`:
- Puget Sound (default): https://api.pugetsound.onebusaway.org
- Tampa: https://api.tampa.onebusaway.org
- UC Davis: https://api.unitrans.onebusawaycloud.com

Agency ID schemes vary by deployment - client automatically tries common prefixes and uses first successful match.