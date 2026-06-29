<img src="readme-resources/header.png" alt="" />

# OneBusAway Twilio Integration

A Go web application that bridges Twilio SMS and voice services with OneBusAway transit APIs, allowing users to get
real-time bus arrival information via text message or phone call.

## 🚌 Features

- **SMS Support**: Text a stop ID to get real-time arrival information
- **Voice/IVR Support**: Call and enter stop ID via keypad for spoken arrivals
- **Smart Stop Disambiguation**: Detects multiple stops with same ID and asks user to choose
- **Automatic Stop Resolution**: Handles both numeric stop IDs (e.g., `75403`) and full agency IDs (e.g., `1_75403`)
- **Multi-Server Support**: Configurable to work with any OneBusAway server deployment
- **Dynamic Coverage Detection**: Automatically detects server coverage area at startup
- **Multi-Agency Support**: Works with multiple transit agencies within a region
- **Intelligent Stop Search**: Uses server-specific geographic bounds for stop searching
- **Real-time Data**: Fetches live arrival predictions from OneBusAway API
- **Production Ready**: Comprehensive error handling, logging, and health checks

## 📁 Project Structure

```
oba-twilio/
├── main.go                     # HTTP server with Gin routes and configuration
├── go.mod                      # Go module dependencies
├── go.sum                      # Dependency checksums
├── .env.example               # Environment configuration template
├── project-plan.md            # Original project specification
├── CLAUDE.md                  # Claude Code project instructions
├── README.md                  # This file
│
├── models/
│   └── types.go               # Data structures for Twilio requests and OBA responses
│
├── client/
│   ├── interface.go           # OneBusAway client interface (for testing)
│   ├── onebusaway.go         # OneBusAway API client implementation
│   ├── coverage_test.go      # Coverage area calculation tests
│   └── onebusaway_test.go    # Client unit tests
│
├── handlers/
│   ├── sms.go                # Twilio SMS webhook handler with disambiguation
│   ├── voice.go              # Twilio voice/IVR webhook handlers
│   ├── session_store.go      # In-memory session management for disambiguation
│   └── disambiguation_test.go # Tests for stop disambiguation logic
│
└── formatters/
    ├── response.go           # TwiML generation and text formatting
    └── response_test.go      # Formatter unit tests
```

## 🚀 Quick Start

### Prerequisites

- **Go 1.18+** ([installation guide](https://golang.org/doc/install))

### Installation

1. **Clone the repository**:
   ```bash
   git clone <repository-url>
   cd oba-twilio
   ```

2. **Install dependencies**:
   ```bash
   go mod download
   ```

3. **Set up environment**:
   ```bash
   cp .env.example .env
   # Edit .env to configure your OneBusAway server and settings
   # Default: Puget Sound (Seattle area)
   # See "Supported OneBusAway Servers" section for other options
   ```

4. **Build the application**:
   ```bash
   go build -o oba-twilio .
   ```

### Running the Application

**Development mode**:
```bash
go run main.go
```

**Production mode**:
```bash
./oba-twilio
```

The server will start on port 8080 by default. You should see:
```
2025/06/13 12:02:45 Initializing coverage area for OneBusAway server...
2025/06/13 12:02:45 Coverage area initialized: center=(47.7655,-122.3079), radius=92564m
2025/06/13 12:02:45 Starting server on port 8080
2025/06/13 12:02:45 OneBusAway API: https://api.pugetsound.onebusaway.org
[GIN-debug] Listening and serving HTTP on :8080
```

The coverage area initialization shows:
- **Center coordinates**: Geographic center of all transit agencies
- **Radius**: Calculated search radius in meters for stop queries
- **Automatic fallback**: If initialization fails, the app continues with limited functionality

## 🧪 Testing

### Run All Tests
```bash
go test ./...
```

### Run Tests with Coverage
```bash
go test -cover ./...
```

### Run Integration Tests (includes live API calls)
```bash
go test -v ./...
```

### Skip Integration Tests (faster for development)
```bash
go test -short ./...
```

### Test Individual Packages
```bash
go test ./client         # Test OneBusAway client
go test ./formatters     # Test response formatting
go test .               # Test main application logic
```

## 📞 API Endpoints

### Health Check
- **GET** `/health` - Returns server health status
- **GET** `/` - Returns application info and coverage area status

### Twilio Webhooks
- **POST** `/sms` - Handle incoming SMS messages
- **POST** `/voice` - Handle incoming voice calls (initial menu)
- **POST** `/voice/input` - Handle voice input (DTMF digits)

### Prometheus Metrics
- **GET** `/metrics` - Prometheus metrics in standard exposition format (public, rate-limited)

The endpoint requires no additional environment variables. `/metrics` and `/health*` traffic is excluded from HTTP latency series so scrape and probe requests do not skew percentiles.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| **HTTP** | | | |
| `http_requests_total` | Counter | `method`, `route`, `status` | Total HTTP requests by method, route template, and status code |
| `http_request_duration_seconds` | Histogram | `method`, `route` | HTTP request latency |
| **OneBusAway Client** | | | |
| `oba_api_requests_total` | Counter | — | Total OneBusAway API calls |
| `oba_api_errors_total` | Counter | — | Total OneBusAway API errors |
| `oba_api_request_duration_seconds` | Histogram | — | OneBusAway API request latency |
| `oba_validation_errors_total` | Counter | — | Total OneBusAway response validation errors |
| `oba_circuit_breaker_state` | Gauge | — | Circuit-breaker state (0=closed, 1=open, 2=half-open) |
| `oba_circuit_breaker_open_total` | Counter | — | Total times the circuit breaker opened |
| `oba_cache_hits_total` | Counter | — | Total OneBusAway API cache hits |
| `oba_cache_misses_total` | Counter | — | Total OneBusAway API cache misses |
| **Session Store** | | | |
| `session_store_active_sessions` | Gauge | `store` | Active sessions (`store`: `sms` or `voice`) |
| `session_store_cache_hits_total` | Counter | `store` | Session-store cache hits |
| `session_store_cache_misses_total` | Counter | `store` | Session-store cache misses |
| `session_store_evictions_total` | Counter | `store` | Sessions evicted |
| `session_store_expired_total` | Counter | `store` | Sessions expired |
| `session_store_created_total` | Counter | `store` | Sessions created |
| **Interactions** | | | |
| `interactions_total` | Counter | `channel`, `outcome` | Total user interactions by channel (`sms`/`voice`) and outcome |
| `stop_lookups_total` | Counter | `result`, `agency` | Total stop lookups by result and matched agency prefix |
| **Runtime** | | | |
| `go_*` | various | — | Go runtime metrics (goroutines, GC, memory, etc.) |
| `process_*` | various | — | OS process metrics (CPU, file descriptors, etc.) |

### Internal API Methods (OneBusAway Client)
- `InitializeCoverage()` - Fetches server coverage area at startup
- `GetCoverageArea()` - Returns calculated center point and radius
- `SearchStops(query)` - Searches stops using dynamic geographic bounds
- `FindAllMatchingStops(stopID)` - Finds all stops matching a numeric ID across agencies
- `GetStopInfo(fullStopID)` - Gets detailed information for a specific stop

## 💬 Usage Examples

### SMS Usage

Send an SMS to your Twilio number with a stop ID:

**Input**: `75403`

**Response** (if single stop found):
```
Stop: Pine St & 3rd Ave
Route 45 to Loyal Heights Greenwood: 2 min
Route 372 to U-District Station: 11 min
Route 67 to Northgate Station Roosevelt Station: 14 min
```

**Response** (if multiple stops found):
```
Multiple stops found for 1234:
1) King County Metro: Pine St & 3rd Ave
2) Sound Transit: University Street Station
Reply with the number to choose.
```

**Follow-up**: User texts `2`

**Response**:
```
Stop: University Street Station
Route Link to SeaTac Airport: 3 min
Route Link to Northgate: 8 min
```

### Voice Usage

1. Call your Twilio number
2. Listen to: "Welcome to OneBusAway transit information. Please enter your stop ID followed by the pound key."
3. Enter stop ID on keypad: `75403#`
4. Hear: "Arrivals for Pine St & 3rd Ave. Route 45 to Loyal Heights Greenwood in 2 minutes. Route 372 to U-District Station in 11 minutes..."

### Manual Testing (curl)

**Test SMS endpoint**:
```bash
curl -X POST \
  -d "From=%2B14444444444&To=%2B15555555555&Body=75403&MessageSid=test" \
  http://localhost:8080/sms
```

**Test Voice endpoint**:
```bash
curl -X POST \
  -d "From=%2B14444444444&To=%2B15555555555&CallSid=test" \
  http://localhost:8080/voice
```

**Test Voice input**:
```bash
curl -X POST \
  -d "From=%2B14444444444&To=%2B15555555555&CallSid=test&Digits=75403" \
  http://localhost:8080/voice/input
```

## ⚙️ Configuration

### Environment Variables

Create a `.env` file or set environment variables:

| Variable | Description | Default Value | Required/Optional |
|----------|-------------|---------------|-------------------|
| **Server Configuration** | | | |
| `PORT` | Server port number | `8080` | Optional |
| **OneBusAway API Configuration** | | | |
| `ONEBUSAWAY_API_KEY` | API key for OneBusAway server (no default; `test` is the public demo key for the Puget Sound server and works for local development) | - | Required |
| `ONEBUSAWAY_BASE_URL` | OneBusAway API base URL | `https://api.pugetsound.onebusaway.org` | Optional |
| **Localization** | | | |
| `SUPPORTED_LANGUAGES` | Comma-separated list of supported language codes | `en-US` | Optional |
| **Twilio Configuration** | | | |
| `TWILIO_ACCOUNT_SID` | Twilio account SID (for outbound features) | - | Optional |
| `TWILIO_AUTH_TOKEN` | Twilio auth token (for outbound features) | - | Optional |
| **Analytics Configuration** | | | |
| `ANALYTICS_ENABLED` | Enable/disable analytics collection | `false` | Optional |
| `ANALYTICS_HASH_SALT` | Salt for hashing sensitive analytics data (32+ char random string) | - | Required if analytics enabled |
| `ANALYTICS_WORKER_COUNT` | Number of analytics worker threads | `4` | Optional |
| `ANALYTICS_QUEUE_SIZE` | Size of analytics event queue | `1000` | Optional |
| `ANALYTICS_SHUTDOWN_TIMEOUT` | Timeout for analytics shutdown (seconds) | `30` | Optional |
| **Plausible Analytics Provider** | | | |
| `PLAUSIBLE_ENABLED` | Enable/disable Plausible analytics provider | `false` | Optional |
| `PLAUSIBLE_DOMAIN` | Domain for Plausible analytics tracking | - | Required if Plausible enabled |
| `PLAUSIBLE_API_URL` | Custom Plausible API URL | `https://plausible.io` | Optional |
| `PLAUSIBLE_API_KEY` | Plausible API key (for custom domains) | - | Optional |
| `PLAUSIBLE_BATCH_SIZE` | Batch size for Plausible events | `50` | Optional |
| `PLAUSIBLE_FLUSH_INTERVAL` | Flush interval for Plausible events (seconds) | `30` | Optional |
| `PLAUSIBLE_HTTP_TIMEOUT` | HTTP timeout for Plausible requests (seconds) | `10` | Optional |
| `PLAUSIBLE_MAX_RETRIES` | Maximum retries for failed Plausible requests | `3` | Optional |
| `PLAUSIBLE_RETRY_DELAY` | Delay between Plausible retries (seconds) | `1` | Optional |
| **Umami Analytics Provider** | | | |
| `UMAMI_ENABLED` | Enable the Umami analytics provider | `false` | Optional |
| `UMAMI_URL` | Umami host; events POST to `<UMAMI_URL>/api/send` | - | Required if Umami enabled |
| `UMAMI_WEBSITE_ID` | Umami website UUID | - | Required if Umami enabled |
| `UMAMI_HOSTNAME` | `hostname` field in emitted events (default: host of `ONEBUSAWAY_BASE_URL`, else `twilio.onebusaway.org`) | - | Optional |
| `UMAMI_HTTP_TIMEOUT` | Per-request timeout, Go duration (e.g. `5s`, `10s`) | `5s` | Optional |

> **Note**: Analytics is API-driven (no JS tracker); the Umami provider POSTs events server-side with a browser-shaped User-Agent.

### Example .env file:

```bash
# Server Configuration
PORT=8080

# OneBusAway API Configuration
ONEBUSAWAY_API_KEY=your_actual_api_key
ONEBUSAWAY_BASE_URL=https://api.pugetsound.onebusaway.org

# Localization (optional)
SUPPORTED_LANGUAGES=en-US,es-US,fr-FR

# Twilio (optional - for outbound features)
TWILIO_ACCOUNT_SID=your_account_sid_here
TWILIO_AUTH_TOKEN=your_auth_token_here

# Analytics (optional)
ANALYTICS_ENABLED=true
# Generate a secure salt using one of these methods:
# - openssl rand -base64 32
# - cat /dev/urandom | head -c 32 | base64
# - pwgen -s 32 1
ANALYTICS_HASH_SALT=kJ8Q2m5bC9tL3nP7sR4wX6aY1dF0eG2h

# Plausible Analytics (optional)
PLAUSIBLE_ENABLED=true
PLAUSIBLE_DOMAIN=your-domain.com
```

### Creating a Secure Hash Salt

The `ANALYTICS_HASH_SALT` is used to anonymize sensitive data like phone numbers before storing them in analytics. To generate a secure salt:

**Option 1: Using OpenSSL (recommended)**
```bash
openssl rand -base64 32
# Example output: kJ8Q2m5bC9tL3nP7sR4wX6aY1dF0eG2h
```

**Option 2: Using /dev/urandom**
```bash
cat /dev/urandom | head -c 32 | base64
# Example output: 7xNmP3sK9wL2bQ5tR8jC1vF6aE4dG0hY
```

**Option 3: Using pwgen**
```bash
pwgen -s 32 1
# Example output: Xk9Lm2Np7Qr3Ws5Yt8Zu1Av4Bx6Cz0De
```

The salt should be:
- At least 32 characters long
- Randomly generated
- Kept secret and not shared
- Different for each deployment

### Stop ID Resolution

The app automatically resolves stop IDs by trying different agency prefixes:

- Input: `75403` → Tries `1_75403`, `40_75403`, `29_75403`, etc.
- Input: `1_75403` → Uses as-is (already has agency prefix)

**Note**: Different OneBusAway servers use different agency ID schemes.

The app will attempt all prefixes and return the first successful match, making it flexible across different deployments.

## 🚀 Deployment

### Local Development with ngrok

For testing with real Twilio webhooks:

1. Install [ngrok](https://ngrok.com/)
2. Start your app: `go run main.go`
3. Expose via ngrok: `ngrok http 8080`
4. Configure Twilio webhooks to point to ngrok URL:
   - SMS: `https://your-id.ngrok.io/sms`
   - Voice: `https://your-id.ngrok.io/voice`

### Production Deployment

**Heroku**:
```bash
# Create Procfile
echo "web: ./oba-twilio" > Procfile

# Deploy
heroku create your-app-name
heroku config:set ONEBUSAWAY_API_KEY=your_actual_api_key
git push heroku main
```

**Docker**:
```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o oba-twilio .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/oba-twilio .
EXPOSE 8080
CMD ["./oba-twilio"]
```

**Digital Ocean/AWS/GCP**:
- Build binary: `go build -o oba-twilio .`
- Upload to server
- Set environment variables
- Run with process manager (systemd, supervisor, etc.)

## 🔧 Troubleshooting

### Common Issues

**1. "No upcoming arrivals found"**
- **Cause**: Invalid stop ID or no current service
- **Solution**: Verify stop ID exists
- **Example**: Stop 75403 should work, but 12345 might not exist

**2. "API returned status 404"**
- **Cause**: Stop ID doesn't exist with any agency prefix
- **Solution**: Double-check the stop ID number
- **Debug**: Check OneBusAway website for correct stop ID

**3. Tests failing with network errors**
- **Cause**: No internet connection or OneBusAway API down
- **Solution**: Run tests with `-short` flag to skip integration tests
- **Command**: `go test -short ./...`

**4. "Port already in use"**
- **Cause**: Another process using port 8080
- **Solution**: Set different port with `PORT=3000` or kill existing process

**5. XML parsing errors in Twilio**
- **Cause**: Invalid TwiML response format
- **Solution**: Check logs for malformed XML, ensure proper escaping

**6. "API returned status 404" with different servers**
- **Cause**: Server not responding or incorrect URL
- **Solution**: Verify server URL and test connectivity:
  ```bash
  curl https://your-server.com/api/where/agencies-with-coverage.json?key=test
  ```

**7. Different agency ID schemes**
- **Cause**: Each OneBusAway deployment uses different agency prefixes
- **Solution**: Check agency IDs for your target server:
  ```bash
  curl https://api.tampa.onebusaway.org/api/where/agencies-with-coverage.json?key=test
  ```
- **Debugging**: Look for `"id"` fields in the agency response to understand the prefix scheme

**8. "Coverage area initialization failed"**
- **Cause**: Server doesn't support agencies-with-coverage endpoint or API key issues
- **Solution**: App will continue with limited stop search functionality
- **Example**: `Warning: Failed to initialize coverage area: API returned status 401`
- **Workaround**: Use full stop IDs (with agency prefix) instead of stop name searches

**9. Stop disambiguation not working**
- **Cause**: Multiple stops exist with same ID but system returns first match
- **Solution**: Check logs for "FindAllMatchingStops" calls
- **Debug**: Test with known conflicting stop IDs like transit station stops
- **Example**: Try stop IDs that exist in both Metro (1_) and Sound Transit (40_) systems

**10. Session timeouts for disambiguation**
- **Cause**: User doesn't respond to disambiguation choice within 10 minutes
- **Solution**: App automatically clears old sessions, user needs to send stop ID again
- **Prevention**: Respond to disambiguation messages promptly

### Debug Mode

Enable verbose logging:
```bash
export GIN_MODE=debug
go run main.go
```

View detailed request/response logs in console.

### Performance Tuning

**For high traffic**:
- Set `GIN_MODE=release` in production
- Implement caching for frequent stop ID queries
- Add rate limiting middleware
- Use connection pooling for OneBusAway API calls

## 🤝 Contributing

1. Fork the repository
2. Create feature branch: `git checkout -b feature-name`
3. Write tests for new functionality
4. Ensure proper formatting, and that linting and testing passes: `make fmt && make lint && make test`
5. Submit pull request

### Code Style

- Follow `gofmt` formatting
- Use `golint` for style checking
- Add tests for new functions
- Keep functions small and focused
- Document exported functions

## 📄 License

This project is open source and made available under the Apache 2.0 license. See the [LICENSE](LICENSE) file for details.

## 🆘 Support

- **Issues**: Create GitHub issue with reproduction steps
- **Questions**: Check existing issues or create new one

## 🔗 Related Links

### OneBusAway Resources
- [OneBusAway Developer API](https://developer.onebusaway.org/)
- [OneBusAway Multi-Region Documentation](https://github.com/OneBusAway/onebusaway/wiki/Multi-Region)
- [OneBusAway Server Directory](http://regions.onebusaway.org/regions-v3.json)

### Twilio & Development Resources
- [Twilio SMS Webhooks](https://www.twilio.com/docs/messaging/guides/webhook-request)
- [Twilio Voice TwiML](https://www.twilio.com/docs/voice/twiml)
- [Gin Web Framework](https://gin-gonic.com/docs/)