package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"oba-twilio/models"
)

func TestValidateStopID(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		errorCode   models.ErrorCode
		description string
	}{
		{
			name:        "valid numeric stop ID",
			input:       "12345",
			expectError: false,
			description: "Valid 5-digit stop ID should pass",
		},
		{
			name:        "valid single-character stop ID",
			input:       "A",
			expectError: false,
			description: "Single-character alphanumeric stop ID should pass",
		},
		{
			name:        "valid stop ID with surrounding whitespace",
			input:       "  12345  ",
			expectError: false,
			description: "Stop ID with surrounding whitespace should be trimmed and pass",
		},
		{
			name:        "valid stop ID with underscore prefix",
			input:       "1_12345",
			expectError: false,
			description: "Valid stop ID with agency prefix should pass",
		},
		{
			name:        "valid stop ID with hyphen",
			input:       "abc-123",
			expectError: false,
			description: "Stop ID with hyphen should pass",
		},
		{
			name:        "valid stop ID with underscore and hyphen",
			input:       "1_stop-id",
			expectError: false,
			description: "Stop ID with both underscore and hyphen should pass",
		},
		{
			name:        "valid minimum length stop ID",
			input:       "123",
			expectError: false,
			description: "Minimum length stop ID should pass",
		},
		{
			name:        "valid maximum length stop ID",
			input:       "12345678901234567890",
			expectError: false,
			description: "20-character stop ID at maximum length should pass",
		},
		{
			name:        "empty stop ID",
			input:       "",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Empty stop ID should be rejected",
		},
		{
			name:        "stop ID too long",
			input:       "123456789012345678901",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID longer than 20 characters should be rejected",
		},
		{
			name:        "stop ID with special characters",
			input:       "123!@#",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID with special characters should be rejected",
		},
		{
			name:        "stop ID with spaces",
			input:       "123 456",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID with spaces should be rejected",
		},
		{
			name:        "stop ID with SQL injection pattern",
			input:       "123'; DROP TABLE stops; --",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID with SQL injection pattern should be rejected",
		},
		{
			name:        "stop ID with XSS pattern",
			input:       "<script>alert('xss')</script>",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID with XSS pattern should be rejected",
		},
		{
			name:        "stop ID with XML injection",
			input:       "123<xml>test</xml>",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID with XML tags should be rejected",
		},
		{
			name:        "stop ID with TwiML injection",
			input:       "123</Say><Hangup/>",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID with TwiML injection should be rejected",
		},
		{
			name:        "stop ID with URL encoding",
			input:       "123%20456",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Stop ID with URL encoding should be rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStopID(tt.input)

			if tt.expectError {
				assert.Error(t, err, tt.description)
				if appErr, ok := err.(*models.AppError); ok {
					assert.Equal(t, tt.errorCode, appErr.Code, "Error code should match expected")
				}
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

func TestValidatePhoneNumber(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		errorCode   models.ErrorCode
		description string
	}{
		{
			name:        "valid US phone number with country code",
			input:       "+15551234567",
			expectError: false,
			description: "Valid US phone number should pass",
		},
		{
			name:        "valid US phone number without country code",
			input:       "15551234567",
			expectError: false,
			description: "Valid US phone number without + should pass",
		},
		{
			name:        "valid international phone number",
			input:       "+441234567890",
			expectError: false,
			description: "Valid international phone number should pass",
		},
		{
			name:        "empty phone number",
			input:       "",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Empty phone number should be rejected",
		},
		{
			name:        "phone number too short",
			input:       "+1555123",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Phone number too short should be rejected",
		},
		{
			name:        "phone number too long",
			input:       "+15551234567890123456",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Phone number too long should be rejected",
		},
		{
			name:        "phone number with invalid characters",
			input:       "+1555abc1234",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Phone number with letters should be rejected",
		},
		{
			name:        "phone number with spaces",
			input:       "+1 555 123 4567",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Phone number with spaces should be rejected",
		},
		{
			name:        "phone number with special characters",
			input:       "+1-555-123-4567",
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Phone number with dashes should be rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePhoneNumber(tt.input)

			if tt.expectError {
				assert.Error(t, err, tt.description)
				if appErr, ok := err.(*models.AppError); ok {
					assert.Equal(t, tt.errorCode, appErr.Code, "Error code should match expected")
				}
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

func TestSanitizeUserInput(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    string
		description string
	}{
		{
			name:        "normal text",
			input:       "Hello World",
			expected:    "Hello World",
			description: "Normal text should be unchanged",
		},
		{
			name:        "text with leading/trailing spaces",
			input:       "  Hello World  ",
			expected:    "Hello World",
			description: "Leading and trailing spaces should be trimmed",
		},
		{
			name:        "text with HTML tags",
			input:       "Hello <script>alert('xss')</script> World",
			expected:    "Hello World",
			description: "HTML tags should be removed",
		},
		{
			name:        "text with XML tags",
			input:       "Hello <xml>test</xml> World",
			expected:    "Hello test World",
			description: "XML tags should be removed",
		},
		{
			name:        "text with TwiML tags",
			input:       "Hello </Say><Hangup/> World",
			expected:    "Hello World",
			description: "TwiML tags should be removed",
		},
		{
			name:        "text with multiple line breaks",
			input:       "Hello\n\n\nWorld",
			expected:    "Hello World",
			description: "Multiple line breaks should be normalized",
		},
		{
			name:        "text with control characters",
			input:       "Hello\x00\x01\x02World",
			expected:    "HelloWorld",
			description: "Control characters should be removed",
		},
		{
			name:        "empty string",
			input:       "",
			expected:    "",
			description: "Empty string should remain empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeUserInput(tt.input)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestValidateDisambiguationChoice(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		maxChoices  int
		expectError bool
		errorCode   models.ErrorCode
		description string
	}{
		{
			name:        "valid choice 1",
			input:       "1",
			maxChoices:  5,
			expectError: false,
			description: "Valid choice 1 should pass",
		},
		{
			name:        "valid choice at max",
			input:       "5",
			maxChoices:  5,
			expectError: false,
			description: "Valid choice at maximum should pass",
		},
		{
			name:        "choice too high",
			input:       "6",
			maxChoices:  5,
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Choice higher than maximum should be rejected",
		},
		{
			name:        "choice zero",
			input:       "0",
			maxChoices:  5,
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Choice zero should be rejected",
		},
		{
			name:        "negative choice",
			input:       "-1",
			maxChoices:  5,
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Negative choice should be rejected",
		},
		{
			name:        "non-numeric choice",
			input:       "abc",
			maxChoices:  5,
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Non-numeric choice should be rejected",
		},
		{
			name:        "empty choice",
			input:       "",
			maxChoices:  5,
			expectError: true,
			errorCode:   models.ErrorCodeValidationFailed,
			description: "Empty choice should be rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDisambiguationChoice(tt.input, tt.maxChoices)

			if tt.expectError {
				assert.Error(t, err, tt.description)
				if appErr, ok := err.(*models.AppError); ok {
					assert.Equal(t, tt.errorCode, appErr.Code, "Error code should match expected")
				}
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}
