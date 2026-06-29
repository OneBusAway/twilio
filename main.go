package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"oba-twilio/analytics"
	"oba-twilio/analytics/providers/plausible"
	"oba-twilio/analytics/providers/umami"
	"oba-twilio/client"
	"oba-twilio/handlers"
	"oba-twilio/handlers/common"
	"oba-twilio/health"
	"oba-twilio/localization"
	"oba-twilio/metrics"
	"oba-twilio/middleware"
)

// validateAPIKey ensures an OneBusAway API key was explicitly provided. The key
// is required and has intentionally no default, so the server won't silently
// start with no credentials. Only an empty value is rejected; any non-empty
// value is accepted, including "test" — the public demo key for the Puget Sound
// OneBusAway server, valid for local development. (An earlier check that also
// rejected "test"/"placeholder" was removed: it broke that documented workflow.
// See issue #11.)
func validateAPIKey(apiKey string) error {
	if apiKey == "" {
		return errors.New("ONEBUSAWAY_API_KEY environment variable is required but not set")
	}
	return nil
}

// buildInternalEngine assembles the gin engine for the internal-only metrics
// server: Prometheus metrics plus the sensitive health endpoints, and none of
// the public webhook routes.
func buildInternalEngine(metricsHandler gin.HandlerFunc, healthHandler *health.Handler) *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	healthHandler.SetupInternalRoutes(engine, metricsHandler)
	return engine
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	metricsPort := resolveMetricsPort(os.Getenv("METRICS_PORT"))
	if metricsPort == port {
		log.Fatalf("METRICS_PORT (%s) must differ from PORT (%s); the two servers cannot share a port", metricsPort, port)
	}

	obaAPIKey := os.Getenv("ONEBUSAWAY_API_KEY")
	if err := validateAPIKey(obaAPIKey); err != nil {
		log.Fatal(err)
	}

	obaBaseURL := os.Getenv("ONEBUSAWAY_BASE_URL")
	if obaBaseURL == "" {
		obaBaseURL = "https://api.pugetsound.onebusaway.org"
	}

	supportedLanguages := os.Getenv("SUPPORTED_LANGUAGES")
	if supportedLanguages == "" {
		supportedLanguages = "en-US"
	}

	locManager, err := localization.NewManager(supportedLanguages)
	if err != nil {
		log.Fatal("Failed to initialize localization manager:", err)
	}

	if brand := strings.TrimSpace(os.Getenv("APP_BRAND_NAME")); brand != "" {
		locManager.SetBrandDisplayName(brand)
	}

	log.Printf("Localization initialized with languages: %s (brand: %s)", supportedLanguages, locManager.BrandDisplayName())

	// Load analytics configuration
	analyticsConfig, err := analytics.LoadConfigFromEnv()
	if err != nil {
		log.Printf("Analytics config error: %v", err)
		analyticsConfig = analytics.DefaultConfig()
	}

	// Create analytics manager
	analyticsManager := analytics.NewManager(analyticsConfig)

	// Register Plausible provider if enabled
	if analyticsConfig.Enabled {
		for _, providerConfig := range analyticsConfig.Providers {
			if providerConfig.Name == "plausible" && providerConfig.Enabled {
				// Extract config values safely
				domain, ok := providerConfig.Config["domain"].(string)
				if !ok {
					log.Printf("Invalid plausible domain configuration")
					continue
				}

				plausibleConfig := plausible.DefaultConfig()
				plausibleConfig.Domain = domain

				// Set optional configurations
				if apiURL, ok := providerConfig.Config["api_url"].(string); ok {
					plausibleConfig.APIURL = apiURL
				}
				if apiKey, ok := providerConfig.Config["api_key"].(string); ok {
					plausibleConfig.APIKey = apiKey
				}
				if batchSize, ok := providerConfig.Config["batch_size"].(int); ok {
					plausibleConfig.BatchSize = batchSize
				}
				if flushInterval, ok := providerConfig.Config["flush_interval"].(time.Duration); ok {
					plausibleConfig.FlushInterval = flushInterval
				}
				if httpTimeout, ok := providerConfig.Config["http_timeout"].(time.Duration); ok {
					plausibleConfig.HTTPTimeout = httpTimeout
				}
				if maxRetries, ok := providerConfig.Config["max_retries"].(int); ok {
					plausibleConfig.MaxRetries = maxRetries
				}
				if retryDelay, ok := providerConfig.Config["retry_delay"].(time.Duration); ok {
					plausibleConfig.RetryDelay = retryDelay
				}

				plausibleProvider, err := plausible.NewProvider(plausibleConfig)
				if err != nil {
					log.Printf("Failed to create plausible provider: %v", err)
					continue
				}

				if err := analyticsManager.RegisterProvider("plausible", plausibleProvider); err != nil {
					log.Printf("Failed to register plausible provider: %v", err)
				}
			}

			if providerConfig.Name == "umami" && providerConfig.Enabled {
				serverURL, ok := providerConfig.Config["server_url"].(string)
				if !ok {
					log.Printf("Invalid umami server_url configuration")
					continue
				}
				websiteID, ok := providerConfig.Config["website_id"].(string)
				if !ok {
					log.Printf("Invalid umami website_id configuration")
					continue
				}

				umamiConfig := umami.DefaultConfig()
				umamiConfig.ServerURL = serverURL
				umamiConfig.WebsiteID = websiteID
				if hostname, ok := providerConfig.Config["hostname"].(string); ok {
					umamiConfig.Hostname = hostname
				}
				if httpTimeout, ok := providerConfig.Config["http_timeout"].(time.Duration); ok {
					umamiConfig.HTTPTimeout = httpTimeout
				}

				umamiProvider, err := umami.NewProvider(umamiConfig)
				if err != nil {
					log.Printf("Failed to create umami provider: %v", err)
					continue
				}

				if err := analyticsManager.RegisterProvider("umami", umamiProvider); err != nil {
					log.Printf("Failed to register umami provider: %v", err)
				}
			}
		}
	}

	// Start analytics manager
	if err := analyticsManager.Start(); err != nil {
		log.Printf("Failed to start analytics manager: %v", err)
	}

	log.Printf("Analytics initialized - enabled: %v, providers: %v", analyticsConfig.Enabled, analyticsManager.GetProviderNames())

	obaClient := client.NewOneBusAwayClient(obaBaseURL, obaAPIKey)

	log.Printf("Initializing coverage area for OneBusAway server...")
	if err := obaClient.InitializeCoverage(); err != nil {
		log.Printf("Warning: Failed to initialize coverage area: %v", err)
		log.Printf("SearchStops functionality may not work properly")
	} else {
		coverage := obaClient.GetCoverageArea()
		log.Printf("Coverage area initialized: center=(%.4f,%.4f), radius=%.0fm",
			coverage.CenterLat, coverage.CenterLon, coverage.Radius)
	}

	smsHandler := handlers.NewSMSHandler(obaClient, locManager)
	voiceHandler := handlers.NewVoiceHandler(obaClient, locManager)
	defer smsHandler.Close()
	defer voiceHandler.Close()

	// Pass analytics manager to handlers
	handlers.SetAnalyticsManager(smsHandler, analyticsManager, analyticsConfig.HashSalt)
	voiceHandler.SetAnalytics(analyticsManager, analyticsConfig.HashSalt)

	arrivalFilterEnabled := parseEnvBool("ARRIVAL_FILTER_ENABLED", false)
	arrivalFilterFallback := parseEnvBool("ARRIVAL_FILTER_FALLBACK_TO_UNFILTERED", true)
	smsThreshold := parseEnvInt("ARRIVAL_FILTER_SMS_MAX_EARLY_MINUTES", 20)
	voiceThreshold := parseEnvInt("ARRIVAL_FILTER_VOICE_MAX_EARLY_MINUTES", 15)
	smsHandler.SetArrivalFilterConfig(common.ArrivalFilterConfig{
		Enabled:               arrivalFilterEnabled,
		MaxPredictedEarlyMins: smsThreshold,
		FallbackToUnfiltered:  arrivalFilterFallback,
	})
	voiceHandler.SetArrivalFilterConfig(common.ArrivalFilterConfig{
		Enabled:               arrivalFilterEnabled,
		MaxPredictedEarlyMins: voiceThreshold,
		FallbackToUnfiltered:  arrivalFilterFallback,
	})
	log.Printf(
		"Arrival filter config: enabled=%t fallback=%t sms_threshold=%d voice_threshold=%d",
		arrivalFilterEnabled, arrivalFilterFallback, smsThreshold, voiceThreshold,
	)

	// Prometheus metrics
	m := metrics.New()
	m.RegisterClientBridge(obaClient)
	m.RegisterSessionBridge("sms", smsHandler.SessionStore)
	m.RegisterSessionBridge("voice", voiceHandler.SessionStore)
	smsHandler.SetMetrics(m)
	voiceHandler.SetMetrics(m)

	// Initialize health check system
	healthManager := health.NewManager(
		health.WithTimeout(10*time.Second),
		health.WithCacheTTL(30*time.Second),
		health.WithMaxConcurrentChecks(5),
		health.WithSystemInfo(true),
		health.WithMetrics(true),
	)

	// Register health checkers
	healthManager.AddChecker(&health.SystemHealthChecker{})
	healthManager.AddChecker(health.NewOneBusAwayHealthChecker(obaClient))
	// Health-check the live store the SMS handler uses (the same store /metrics
	// reads), so /health and /metrics observe the same active session state.
	healthManager.AddChecker(health.NewSessionStoreHealthChecker(smsHandler.SessionStore))
	healthManager.AddChecker(health.NewLocalizationHealthChecker(locManager))
	healthManager.AddChecker(health.NewHTTPServerHealthChecker(port))

	// Create health handler
	healthHandler := health.NewHandler(healthManager)

	metricsHandler := m.Handler()

	r := gin.Default()

	// Add analytics middleware
	r.Use(middleware.NewAnalyticsMiddleware(analyticsManager, middleware.AnalyticsConfig{
		Enabled:  analyticsConfig.Enabled,
		HashSalt: analyticsConfig.HashSalt,
	}).Handler())

	// Add Prometheus metrics middleware (public engine only)
	r.Use(m.Middleware())

	// Add health check middleware
	r.Use(healthHandler.HealthMiddleware())

	// Application info endpoint
	r.GET("/", func(c *gin.Context) {
		coverage := obaClient.GetCoverageArea()
		response := gin.H{
			"message": locManager.BrandDisplayName() + " Twilio Integration",
			"status":  "healthy",
			"version": "1.0.0",
		}

		if coverage != nil {
			response["coverage"] = gin.H{
				"center_lat": coverage.CenterLat,
				"center_lon": coverage.CenterLon,
				"radius":     coverage.Radius,
			}
		}

		c.JSON(200, response)
	})

	// Public probes only; metrics + detailed health live on the internal server.
	healthHandler.SetupPublicRoutes(r)

	r.POST("/sms", smsHandler.HandleSMS)
	r.POST("/voice", voiceHandler.HandleVoiceStart)
	r.POST("/voice/find_stop", voiceHandler.HandleFindStop)
	r.POST("/voice/menu_action", voiceHandler.HandleVoiceMenuAction)

	internalEngine := buildInternalEngine(metricsHandler, healthHandler)

	publicSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	internalSrv := &http.Server{
		Addr:              ":" + metricsPort,
		Handler:           internalEngine,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("Starting public server on port %s", port)
	log.Printf("Starting internal metrics server on port %s", metricsPort)
	log.Printf("OneBusAway API: %s", obaBaseURL)
	log.Printf("Public endpoints: GET / , POST /sms , POST /voice* , GET /health , GET /health/ready")
	log.Printf("Internal endpoints (:%s): GET /metrics , GET /health/detailed , /health/stats , /health/config , /health/cache", metricsPort)

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := publicSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("Failed to start public server:", err)
		}
	}()
	go func() {
		if err := internalSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("Failed to start metrics server:", err)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down servers...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Drain both servers concurrently so they share the deadline in parallel
	// rather than the public server's drain starving the internal server (and
	// the analytics flush below) of the remaining budget.
	var shutdownWG sync.WaitGroup
	shutdownWG.Add(2)
	go func() {
		defer shutdownWG.Done()
		if err := publicSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Public server shutdown error: %v", err)
		}
	}()
	go func() {
		defer shutdownWG.Done()
		if err := internalSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Metrics server shutdown error: %v", err)
		}
	}()
	shutdownWG.Wait()

	// Flush and close analytics
	log.Println("Flushing analytics...")
	if err := analyticsManager.Flush(shutdownCtx); err != nil {
		log.Printf("Analytics flush error: %v", err)
	}
	if err := analyticsManager.Close(); err != nil {
		log.Printf("Analytics close error: %v", err)
	}

	log.Println("Server stopped gracefully")
}

func parseEnvBool(name string, defaultValue bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("Invalid bool for %s=%q, using default %t", name, v, defaultValue)
		return defaultValue
	}
	return parsed
}

func parseEnvInt(name string, defaultValue int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("Invalid int for %s=%q, using default %d", name, v, defaultValue)
		return defaultValue
	}
	if parsed < 0 {
		log.Printf("Invalid negative int for %s=%q, using default %d", name, v, defaultValue)
		return defaultValue
	}
	return parsed
}

const defaultMetricsPort = "9119"

// resolveMetricsPort validates a METRICS_PORT value. It accepts an integer in
// [1,65535] and returns it as a canonical string. An unset (empty) value
// silently defaults to defaultMetricsPort; a non-numeric or out-of-range value
// also defaults but emits a logged warning.
func resolveMetricsPort(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultMetricsPort
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 || parsed > 65535 {
		log.Printf("Invalid METRICS_PORT=%q, using default %s", raw, defaultMetricsPort)
		return defaultMetricsPort
	}
	return strconv.Itoa(parsed)
}
