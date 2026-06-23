package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"oba-twilio/analytics"
	"oba-twilio/analytics/providers/plausible"
	"oba-twilio/client"
	"oba-twilio/handlers"
	"oba-twilio/handlers/common"
	"oba-twilio/health"
	"oba-twilio/localization"
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

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
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

	// Initialize session store
	sessionStore := common.NewImprovedSessionStore()
	defer sessionStore.Close()

	smsHandler := handlers.NewSMSHandler(obaClient, locManager)
	voiceHandler := handlers.NewVoiceHandler(obaClient, locManager)

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
	healthManager.AddChecker(health.NewSessionStoreHealthChecker(sessionStore))
	healthManager.AddChecker(health.NewLocalizationHealthChecker(locManager))
	healthManager.AddChecker(health.NewHTTPServerHealthChecker(port))

	// Create health handler
	healthHandler := health.NewHandler(healthManager)

	r := gin.Default()

	// Add analytics middleware
	r.Use(middleware.NewAnalyticsMiddleware(analyticsManager, middleware.AnalyticsConfig{
		Enabled:  analyticsConfig.Enabled,
		HashSalt: analyticsConfig.HashSalt,
	}).Handler())

	// Add health check middleware
	r.Use(healthHandler.HealthMiddleware())
	r.Use(healthHandler.HealthResponseMiddleware())

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

	// Setup comprehensive health check endpoints
	healthHandler.SetupRoutes(r)

	r.POST("/sms", smsHandler.HandleSMS)
	r.POST("/voice", voiceHandler.HandleVoiceStart)
	r.POST("/voice/find_stop", voiceHandler.HandleFindStop)
	r.POST("/voice/menu_action", voiceHandler.HandleVoiceMenuAction)

	log.Printf("Starting server on port %s", port)
	log.Printf("OneBusAway API: %s", obaBaseURL)
	log.Printf("Health check endpoints configured:")
	log.Printf("  - GET /health (liveness probe)")
	log.Printf("  - GET /health/ready (readiness probe)")
	log.Printf("  - GET /health/detailed (comprehensive status)")
	log.Printf("  - GET /metrics (Prometheus metrics)")
	log.Printf("  - GET /health/stats (basic statistics)")
	log.Printf("  - GET /health/config (configuration info)")

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := r.Run(":" + port); err != nil {
			log.Fatal("Failed to start server:", err)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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
