package analytics

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

// LoadConfigFromEnv loads analytics configuration from environment variables.
func LoadConfigFromEnv() (Config, error) {
	config := DefaultConfig()

	// Global analytics settings
	if enabled := os.Getenv("ANALYTICS_ENABLED"); enabled != "" {
		if parsedEnabled, err := strconv.ParseBool(enabled); err == nil {
			config.Enabled = parsedEnabled
		}
	}

	if hashSalt := os.Getenv("ANALYTICS_HASH_SALT"); hashSalt != "" {
		config.HashSalt = hashSalt
	} else if config.Enabled {
		return config, fmt.Errorf("ANALYTICS_HASH_SALT is required when analytics is enabled")
	}

	// Worker pool configuration
	if workerCount := os.Getenv("ANALYTICS_WORKER_COUNT"); workerCount != "" {
		if parsedCount, err := strconv.Atoi(workerCount); err == nil && parsedCount > 0 {
			config.WorkerCount = parsedCount
		}
	}

	if queueSize := os.Getenv("ANALYTICS_QUEUE_SIZE"); queueSize != "" {
		if parsedSize, err := strconv.Atoi(queueSize); err == nil && parsedSize > 0 {
			config.EventQueueSize = parsedSize
		}
	}

	if timeout := os.Getenv("ANALYTICS_SHUTDOWN_TIMEOUT"); timeout != "" {
		if parsedTimeout, err := time.ParseDuration(timeout); err == nil {
			config.ShutdownTimeout = parsedTimeout
		}
	}

	// Load provider configurations
	providers, err := loadProviderConfigs()
	if err != nil {
		return config, fmt.Errorf("failed to load provider configs: %w", err)
	}
	config.Providers = providers

	return config, nil
}

// loadProviderConfigs loads provider-specific configurations.
func loadProviderConfigs() ([]ProviderConfig, error) {
	var providers []ProviderConfig

	// Plausible provider
	plausibleConfig, err := loadPlausibleConfig()
	if err != nil {
		if os.Getenv("PLAUSIBLE_ENABLED") == "true" {
			return nil, fmt.Errorf("plausible configuration error: %w", err)
		}
		// If Plausible is not enabled, ignore the error and skip it
	} else if plausibleConfig.Enabled {
		// Only add enabled providers
		providers = append(providers, plausibleConfig)
	}

	// Umami provider
	umamiConfig, err := loadUmamiConfig()
	if err != nil {
		if os.Getenv("UMAMI_ENABLED") == "true" {
			return nil, fmt.Errorf("umami configuration error: %w", err)
		}
		// If Umami is not enabled, ignore the error and skip it
	} else if umamiConfig.Enabled {
		providers = append(providers, umamiConfig)
	}

	return providers, nil
}

// loadPlausibleConfig loads Plausible provider configuration.
func loadPlausibleConfig() (ProviderConfig, error) {
	enabled := false
	if enabledStr := os.Getenv("PLAUSIBLE_ENABLED"); enabledStr != "" {
		if parsedEnabled, err := strconv.ParseBool(enabledStr); err == nil {
			enabled = parsedEnabled
		}
	}

	config := ProviderConfig{
		Name:    "plausible",
		Enabled: enabled,
		Config:  make(map[string]interface{}),
	}

	if !enabled {
		return config, nil
	}

	// Required domain
	domain := os.Getenv("PLAUSIBLE_DOMAIN")
	if domain == "" {
		return config, fmt.Errorf("PLAUSIBLE_DOMAIN is required when Plausible is enabled")
	}
	config.Config["domain"] = domain

	// Optional API URL
	if apiURL := os.Getenv("PLAUSIBLE_API_URL"); apiURL != "" {
		config.Config["api_url"] = apiURL
	}

	// Optional API Key
	if apiKey := os.Getenv("PLAUSIBLE_API_KEY"); apiKey != "" {
		config.Config["api_key"] = apiKey
	}

	// Optional batch size
	if batchSize := os.Getenv("PLAUSIBLE_BATCH_SIZE"); batchSize != "" {
		if parsedSize, err := strconv.Atoi(batchSize); err == nil && parsedSize > 0 {
			config.Config["batch_size"] = parsedSize
		}
	}

	// Optional flush interval
	if flushInterval := os.Getenv("PLAUSIBLE_FLUSH_INTERVAL"); flushInterval != "" {
		if parsedInterval, err := time.ParseDuration(flushInterval); err == nil {
			config.Config["flush_interval"] = parsedInterval
		}
	}

	// Optional HTTP timeout
	if httpTimeout := os.Getenv("PLAUSIBLE_HTTP_TIMEOUT"); httpTimeout != "" {
		if parsedTimeout, err := time.ParseDuration(httpTimeout); err == nil {
			config.Config["http_timeout"] = parsedTimeout
		}
	}

	// Optional max retries
	if maxRetries := os.Getenv("PLAUSIBLE_MAX_RETRIES"); maxRetries != "" {
		if parsedRetries, err := strconv.Atoi(maxRetries); err == nil && parsedRetries >= 0 {
			config.Config["max_retries"] = parsedRetries
		}
	}

	// Optional retry delay
	if retryDelay := os.Getenv("PLAUSIBLE_RETRY_DELAY"); retryDelay != "" {
		if parsedDelay, err := time.ParseDuration(retryDelay); err == nil {
			config.Config["retry_delay"] = parsedDelay
		}
	}

	return config, nil
}

// CreateManager creates an analytics manager from configuration.
// Provider registration must be done separately to avoid import cycles.
func CreateManager(config Config) *Manager {
	return NewManager(config)
}

// DefaultUmamiHostname is the fallback payload hostname when none can be derived
// from configuration. It is the single source of truth for this sentinel; the
// umami provider's DefaultHostname aliases it (the analytics package configures
// the provider via a string map and cannot import the umami package).
const DefaultUmamiHostname = "twilio.onebusaway.org"

// resolveUmamiHostname picks the payload hostname: explicit override, else the
// host of ONEBUSAWAY_BASE_URL, else a fixed sentinel (never empty).
func resolveUmamiHostname(explicit, baseURL string) string {
	if explicit != "" {
		return explicit
	}
	if baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return DefaultUmamiHostname
}

// loadUmamiConfig loads Umami provider configuration from the environment.
func loadUmamiConfig() (ProviderConfig, error) {
	enabled := false
	if enabledStr := os.Getenv("UMAMI_ENABLED"); enabledStr != "" {
		if parsed, err := strconv.ParseBool(enabledStr); err == nil {
			enabled = parsed
		}
	}

	config := ProviderConfig{
		Name:    "umami",
		Enabled: enabled,
		Config:  make(map[string]interface{}),
	}

	if !enabled {
		return config, nil
	}

	serverURL := os.Getenv("UMAMI_URL")
	if serverURL == "" {
		return config, fmt.Errorf("UMAMI_URL is required when Umami is enabled")
	}
	websiteID := os.Getenv("UMAMI_WEBSITE_ID")
	if websiteID == "" {
		return config, fmt.Errorf("UMAMI_WEBSITE_ID is required when Umami is enabled")
	}

	config.Config["server_url"] = serverURL
	config.Config["website_id"] = websiteID
	config.Config["hostname"] = resolveUmamiHostname(os.Getenv("UMAMI_HOSTNAME"), os.Getenv("ONEBUSAWAY_BASE_URL"))

	if httpTimeout := os.Getenv("UMAMI_HTTP_TIMEOUT"); httpTimeout != "" {
		if parsed, err := time.ParseDuration(httpTimeout); err == nil {
			config.Config["http_timeout"] = parsed
		}
	}

	return config, nil
}
