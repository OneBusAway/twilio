package analytics

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromEnv(t *testing.T) {
	// Save original env vars
	originalVars := make(map[string]string)
	envVars := []string{
		"ANALYTICS_ENABLED",
		"ANALYTICS_HASH_SALT",
		"ANALYTICS_WORKER_COUNT",
		"ANALYTICS_QUEUE_SIZE",
		"ANALYTICS_SHUTDOWN_TIMEOUT",
		"PLAUSIBLE_ENABLED",
		"PLAUSIBLE_DOMAIN",
	}

	for _, key := range envVars {
		originalVars[key] = os.Getenv(key)
		_ = os.Unsetenv(key)
	}

	// Restore env vars after test
	defer func() {
		for _, key := range envVars {
			if value, exists := originalVars[key]; exists {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}()

	tests := []struct {
		name      string
		setupEnv  func()
		wantError bool
		validate  func(*testing.T, Config)
	}{
		{
			name: "default config when no env vars",
			setupEnv: func() {
				// No environment variables set
			},
			wantError: false,
			validate: func(t *testing.T, config Config) {
				assert.False(t, config.Enabled)
				assert.Equal(t, DefaultConfig().WorkerCount, config.WorkerCount)
				assert.Equal(t, DefaultConfig().EventQueueSize, config.EventQueueSize)
				assert.Empty(t, config.Providers)
			},
		},
		{
			name: "analytics enabled without salt",
			setupEnv: func() {
				_ = os.Setenv("ANALYTICS_ENABLED", "true")
			},
			wantError: true,
		},
		{
			name: "analytics enabled with salt",
			setupEnv: func() {
				_ = os.Setenv("ANALYTICS_ENABLED", "true")
				_ = os.Setenv("ANALYTICS_HASH_SALT", "test-salt-123")
				_ = os.Setenv("ANALYTICS_WORKER_COUNT", "8")
				_ = os.Setenv("ANALYTICS_QUEUE_SIZE", "500")
				_ = os.Setenv("ANALYTICS_SHUTDOWN_TIMEOUT", "10s")
			},
			wantError: false,
			validate: func(t *testing.T, config Config) {
				assert.True(t, config.Enabled)
				assert.Equal(t, "test-salt-123", config.HashSalt)
				assert.Equal(t, 8, config.WorkerCount)
				assert.Equal(t, 500, config.EventQueueSize)
				assert.Equal(t, 10*time.Second, config.ShutdownTimeout)
			},
		},
		{
			name: "plausible enabled without domain",
			setupEnv: func() {
				_ = os.Setenv("ANALYTICS_ENABLED", "true")
				_ = os.Setenv("ANALYTICS_HASH_SALT", "test-salt")
				_ = os.Setenv("PLAUSIBLE_ENABLED", "true")
			},
			wantError: true,
		},
		{
			name: "plausible enabled with domain",
			setupEnv: func() {
				_ = os.Setenv("ANALYTICS_ENABLED", "true")
				_ = os.Setenv("ANALYTICS_HASH_SALT", "test-salt")
				_ = os.Setenv("PLAUSIBLE_ENABLED", "true")
				_ = os.Setenv("PLAUSIBLE_DOMAIN", "example.com")
			},
			wantError: false,
			validate: func(t *testing.T, config Config) {
				assert.True(t, config.Enabled)
				assert.Len(t, config.Providers, 1)

				provider := config.Providers[0]
				assert.Equal(t, "plausible", provider.Name)
				assert.True(t, provider.Enabled)
				assert.Equal(t, "example.com", provider.Config["domain"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean environment
			for _, key := range envVars {
				_ = os.Unsetenv(key)
			}

			// Setup test environment
			tt.setupEnv()

			// Load configuration
			config, err := LoadConfigFromEnv()

			// Check error expectation
			if tt.wantError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Run validation if provided
			if tt.validate != nil {
				tt.validate(t, config)
			}
		})
	}
}

func TestLoadPlausibleConfig(t *testing.T) {
	// Save and restore env vars
	originalVars := map[string]string{
		"PLAUSIBLE_ENABLED":        os.Getenv("PLAUSIBLE_ENABLED"),
		"PLAUSIBLE_DOMAIN":         os.Getenv("PLAUSIBLE_DOMAIN"),
		"PLAUSIBLE_API_URL":        os.Getenv("PLAUSIBLE_API_URL"),
		"PLAUSIBLE_API_KEY":        os.Getenv("PLAUSIBLE_API_KEY"),
		"PLAUSIBLE_BATCH_SIZE":     os.Getenv("PLAUSIBLE_BATCH_SIZE"),
		"PLAUSIBLE_FLUSH_INTERVAL": os.Getenv("PLAUSIBLE_FLUSH_INTERVAL"),
		"PLAUSIBLE_HTTP_TIMEOUT":   os.Getenv("PLAUSIBLE_HTTP_TIMEOUT"),
		"PLAUSIBLE_MAX_RETRIES":    os.Getenv("PLAUSIBLE_MAX_RETRIES"),
		"PLAUSIBLE_RETRY_DELAY":    os.Getenv("PLAUSIBLE_RETRY_DELAY"),
	}

	defer func() {
		for key, value := range originalVars {
			if value != "" {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}()

	tests := []struct {
		name      string
		setupEnv  func()
		wantError bool
		validate  func(*testing.T, ProviderConfig)
	}{
		{
			name: "disabled plausible",
			setupEnv: func() {
				for key := range originalVars {
					_ = os.Unsetenv(key)
				}
				_ = os.Setenv("PLAUSIBLE_ENABLED", "false")
			},
			wantError: false,
			validate: func(t *testing.T, config ProviderConfig) {
				assert.Equal(t, "plausible", config.Name)
				assert.False(t, config.Enabled)
			},
		},
		{
			name: "enabled without domain",
			setupEnv: func() {
				for key := range originalVars {
					_ = os.Unsetenv(key)
				}
				_ = os.Setenv("PLAUSIBLE_ENABLED", "true")
			},
			wantError: true,
		},
		{
			name: "enabled with all options",
			setupEnv: func() {
				for key := range originalVars {
					_ = os.Unsetenv(key)
				}
				_ = os.Setenv("PLAUSIBLE_ENABLED", "true")
				_ = os.Setenv("PLAUSIBLE_DOMAIN", "example.com")
				_ = os.Setenv("PLAUSIBLE_API_URL", "https://custom.plausible.io")
				_ = os.Setenv("PLAUSIBLE_API_KEY", "test-key")
				_ = os.Setenv("PLAUSIBLE_BATCH_SIZE", "50")
				_ = os.Setenv("PLAUSIBLE_FLUSH_INTERVAL", "5s")
				_ = os.Setenv("PLAUSIBLE_HTTP_TIMEOUT", "15s")
				_ = os.Setenv("PLAUSIBLE_MAX_RETRIES", "5")
				_ = os.Setenv("PLAUSIBLE_RETRY_DELAY", "2s")
			},
			wantError: false,
			validate: func(t *testing.T, config ProviderConfig) {
				assert.Equal(t, "plausible", config.Name)
				assert.True(t, config.Enabled)
				assert.Equal(t, "example.com", config.Config["domain"])
				assert.Equal(t, "https://custom.plausible.io", config.Config["api_url"])
				assert.Equal(t, "test-key", config.Config["api_key"])
				assert.Equal(t, 50, config.Config["batch_size"])
				assert.Equal(t, 5*time.Second, config.Config["flush_interval"])
				assert.Equal(t, 15*time.Second, config.Config["http_timeout"])
				assert.Equal(t, 5, config.Config["max_retries"])
				assert.Equal(t, 2*time.Second, config.Config["retry_delay"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupEnv()

			config, err := loadPlausibleConfig()

			if tt.wantError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			if tt.validate != nil {
				tt.validate(t, config)
			}
		})
	}
}

func TestCreateManager(t *testing.T) {
	config := Config{
		Enabled:        true,
		HashSalt:       "test-salt",
		WorkerCount:    5,
		EventQueueSize: 100,
		Providers:      []ProviderConfig{},
	}

	manager := CreateManager(config)
	assert.NotNil(t, manager)
	assert.Equal(t, config.WorkerCount, manager.config.WorkerCount)
	assert.Equal(t, config.EventQueueSize, manager.config.EventQueueSize)
	assert.Empty(t, manager.GetProviderNames())

	err := manager.Close()
	assert.NoError(t, err)
}

func TestResolveUmamiHostname(t *testing.T) {
	assert.Equal(t, "explicit.example.org", resolveUmamiHostname("explicit.example.org", "https://api.pugetsound.onebusaway.org"))
	assert.Equal(t, "api.pugetsound.onebusaway.org", resolveUmamiHostname("", "https://api.pugetsound.onebusaway.org"))
	assert.Equal(t, defaultUmamiHostname, resolveUmamiHostname("", ""))
	assert.Equal(t, defaultUmamiHostname, resolveUmamiHostname("", "::not a url::"))
}

func TestLoadUmamiConfig(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "")
		c, err := loadUmamiConfig()
		require.NoError(t, err)
		assert.False(t, c.Enabled)
	})

	t.Run("missing url errors when enabled", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "true")
		t.Setenv("UMAMI_URL", "")
		t.Setenv("UMAMI_WEBSITE_ID", "web-uuid")
		_, err := loadUmamiConfig()
		assert.Error(t, err)
	})

	t.Run("missing website id errors when enabled", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "true")
		t.Setenv("UMAMI_URL", "https://umami.example.com")
		t.Setenv("UMAMI_WEBSITE_ID", "")
		_, err := loadUmamiConfig()
		assert.Error(t, err)
	})

	t.Run("full config", func(t *testing.T) {
		t.Setenv("UMAMI_ENABLED", "true")
		t.Setenv("UMAMI_URL", "https://umami.example.com")
		t.Setenv("UMAMI_WEBSITE_ID", "web-uuid")
		t.Setenv("UMAMI_HOSTNAME", "")
		t.Setenv("ONEBUSAWAY_BASE_URL", "https://api.pugetsound.onebusaway.org")
		c, err := loadUmamiConfig()
		require.NoError(t, err)
		assert.True(t, c.Enabled)
		assert.Equal(t, "umami", c.Name)
		assert.Equal(t, "https://umami.example.com", c.Config["server_url"])
		assert.Equal(t, "web-uuid", c.Config["website_id"])
		assert.Equal(t, "api.pugetsound.onebusaway.org", c.Config["hostname"])
	})
}
