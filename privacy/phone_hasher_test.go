package privacy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewHasher(t *testing.T) {
	h := NewHasher("analytics", "logs")

	assert.NotNil(t, h)
	assert.Equal(t, "analytics", h.analyticsSalt)
	assert.Equal(t, "logs", h.logSalt)
}

func TestHashPhone(t *testing.T) {
	tests := []struct {
		name        string
		phoneNumber string
		salt        string
		diffSalt    bool
	}{
		{
			name:        "valid phone number",
			phoneNumber: "+12065551234",
			salt:        "test-salt",
		},
		{
			name:        "empty phone number",
			phoneNumber: "",
			salt:        "test-salt",
		},
		{
			name:        "different salt produces different hash",
			phoneNumber: "+12065551234",
			salt:        "different-salt",
			diffSalt:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hashPhoneNumber(tt.phoneNumber, tt.salt)

			if tt.phoneNumber == "" {
				assert.Empty(t, result)
				return
			}

			assert.NotEmpty(t, result)
			assert.Len(t, result, 64) // SHA256 produces 64 character hex string

			// Verify same input produces same output
			assert.Equal(t, result, hashPhoneNumber(tt.phoneNumber, tt.salt))

			// Verify different salt produces different hash
			if tt.diffSalt {
				result2 := hashPhoneNumber(tt.phoneNumber, "test-salt")
				assert.NotEqual(t, result, result2)
			}
		})
	}
}
func TestConstructUserId(t *testing.T) {
	h := NewHasher("analytics-salt", "log-salt")

	tests := []struct {
		name        string
		phoneNumber string
		wantEmpty   bool
	}{
		{
			name:        "valid phone number",
			phoneNumber: "+20123456789",
			wantEmpty:   false,
		},
		{
			name:        "empty phone number",
			phoneNumber: "",
			wantEmpty:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.ConstructUserId(tt.phoneNumber)

			if tt.wantEmpty {
				assert.Empty(t, result)
				return
			}

			assert.NotEmpty(t, result)
			assert.Len(t, result, 64)

			assert.Equal(t, result, h.ConstructUserId(tt.phoneNumber))
		})
	}
}

func TestConstructUserId_GoldenValue(t *testing.T) {
	h := NewHasher("test-salt", "")

	result := h.ConstructUserId("+15551234567")

	assert.Equal(
		t,
		"86b053469877b271a3872cf96c5e45b6487cb222d04df6a7250bcd83533e8fa5",
		result,
	)
}

func TestHashForLogs(t *testing.T) {
	h := NewHasher("analytics-salt", "log-salt")

	tests := []struct {
		name        string
		phoneNumber string
		wantEmpty   bool
	}{
		{
			name:        "valid phone number",
			phoneNumber: "+20123456789",
			wantEmpty:   false,
		},
		{
			name:        "empty phone number",
			phoneNumber: "",
			wantEmpty:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.HashForLogs(tt.phoneNumber)

			if tt.wantEmpty {
				assert.Empty(t, result)
				return
			}

			assert.NotEmpty(t, result)
			assert.Len(t, result, 64)

			assert.Equal(t, result, h.HashForLogs(tt.phoneNumber))
		})
	}
}

func TestConstructUserId_DifferentPhones(t *testing.T) {
	h := NewHasher("analytics-salt", "log-salt")

	hash1 := h.ConstructUserId("+20111111111")
	hash2 := h.ConstructUserId("+20222222222")

	assert.NotEqual(t, hash1, hash2)
}

func TestDifferentSaltsProduceDifferentHashes(t *testing.T) {
	h := NewHasher("analytics-salt", "log-salt")

	userID := h.ConstructUserId("+20123456789")
	logHash := h.HashForLogs("+20123456789")

	assert.NotEqual(t, userID, logHash)
}
