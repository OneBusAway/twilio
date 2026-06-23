package umami

import (
	"testing"
	"time"

	"oba-twilio/analytics"

	"github.com/stretchr/testify/assert"
)

func TestConfigValidate(t *testing.T) {
	t.Run("missing server URL", func(t *testing.T) {
		c := Config{WebsiteID: "abc"}
		assert.ErrorIs(t, c.Validate(), analytics.ErrMissingServerURL)
	})

	t.Run("missing website ID", func(t *testing.T) {
		c := Config{ServerURL: "https://umami.example.com"}
		assert.ErrorIs(t, c.Validate(), analytics.ErrMissingWebsiteID)
	})

	t.Run("defaults applied", func(t *testing.T) {
		c := Config{ServerURL: "https://umami.example.com", WebsiteID: "abc"}
		require := assert.New(t)
		require.NoError(c.Validate())
		require.Equal(DefaultHostname, c.Hostname)
		require.Equal(5*time.Second, c.HTTPTimeout)
	})

	t.Run("explicit values preserved", func(t *testing.T) {
		c := Config{ServerURL: "https://umami.example.com", WebsiteID: "abc", Hostname: "api.example.org", HTTPTimeout: 2 * time.Second}
		assert.NoError(t, c.Validate())
		assert.Equal(t, "api.example.org", c.Hostname)
		assert.Equal(t, 2*time.Second, c.HTTPTimeout)
	})
}
