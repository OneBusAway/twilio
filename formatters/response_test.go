package formatters

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"oba-twilio/localization"
	"oba-twilio/models"
)

func TestFormatSMSResponse(t *testing.T) {
	lm := localization.NewTestManager()
	arrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Seattle Center",
			MinutesUntilArrival: 3,
		},
		{
			RouteShortName:      "43",
			TripHeadsign:        "Capitol Hill",
			MinutesUntilArrival: 8,
		},
		{
			RouteShortName:      "49",
			TripHeadsign:        "U District",
			MinutesUntilArrival: 12,
		},
		{
			RouteShortName:      "50",
			TripHeadsign:        "Fremont",
			MinutesUntilArrival: 15,
		},
	}

	result := FormatSMSResponse(arrivals, "Pine St & 3rd Ave", lm, "en-US")

	assert.Contains(t, result, "Pine St & 3rd Ave")
	assert.Contains(t, result, "Route 8 to Seattle Center: 3 min")
	assert.Contains(t, result, "Route 43 to Capitol Hill: 8 min")
	assert.Contains(t, result, "Route 49 to U District: 12 min")
	assert.NotContains(t, result, "Route 50")

	lines := strings.Split(result, "\n")
	assert.Equal(t, 4, len(lines))
}

func TestFormatSMSResponse_Empty(t *testing.T) {
	lm := localization.NewTestManager()
	result := FormatSMSResponse([]models.Arrival{}, "Test Stop", lm, "en-US")
	assert.Equal(t, "No upcoming arrivals found for this stop.", result)
}

func TestFormatSMSResponse_PolishLocalization(t *testing.T) {
	lm := localization.NewTestManagerWithStrings(
		map[string]map[string]string{
			"pl-PL": {
				"sms.arrival.stop_label": "Przystanek",
				"sms.arrival.route_to":   "Linia %s do %s: %s",
			},
		},
		[]string{"pl-PL"},
	)
	arrivals := []models.Arrival{
		{
			RouteShortName:      "1",
			TripHeadsign:        "Franowo",
			MinutesUntilArrival: 2,
		},
	}

	result := FormatSMSResponse(arrivals, "Franowo", lm, "pl-PL")
	assert.Contains(t, result, "Przystanek: Franowo")
	assert.Contains(t, result, "Linia 1 do Franowo: 2 min")
}

func TestFormatSMSResponse_LocalizesNow(t *testing.T) {
	lm := localization.NewTestManagerWithStrings(
		map[string]map[string]string{
			"pl-PL": {
				"sms.arrival.stop_label": "Przystanek",
				"sms.arrival.route_to":   "Linia %s do %s: %s",
				"voice.time.now":         "Teraz",
			},
		},
		[]string{"pl-PL"},
	)
	arrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Ogrody",
			MinutesUntilArrival: 0,
		},
	}

	result := FormatSMSResponse(arrivals, "Rondo", lm, "pl-PL")
	assert.Contains(t, result, "Linia 8 do Ogrody: Teraz")
	assert.NotContains(t, result, "Now")
}

func TestFormatSMSResponse_LocalizesMinutes(t *testing.T) {
	lm := localization.NewTestManagerWithStrings(
		map[string]map[string]string{
			"xx-XX": {
				"sms.arrival.stop_label": "StopX",
				"sms.arrival.route_to":   "R %s -> %s: %s",
				"voice.time.minute":      "ONE_MIN",
				"voice.time.minutes":     "%d_MINS",
			},
		},
		[]string{"xx-XX"},
	)
	arrivals := []models.Arrival{
		{RouteShortName: "1", TripHeadsign: "A", MinutesUntilArrival: 1},
		{RouteShortName: "2", TripHeadsign: "B", MinutesUntilArrival: 7},
	}

	result := FormatSMSResponse(arrivals, "S", lm, "xx-XX")
	assert.Contains(t, result, "R 1 -> A: ONE_MIN")
	assert.Contains(t, result, "R 2 -> B: 7_MINS")
	assert.NotContains(t, result, "1 min")
	assert.NotContains(t, result, "7 min")
}

func TestFormatVoiceResponse(t *testing.T) {
	// Create test localization manager
	lm := localization.NewTestManager()

	t.Run("Different routes", func(t *testing.T) {
		arrivals := []models.Arrival{
			{
				RouteShortName:      "8",
				TripHeadsign:        "Seattle Center",
				MinutesUntilArrival: 3,
			},
			{
				RouteShortName:      "43",
				TripHeadsign:        "Capitol Hill",
				MinutesUntilArrival: 1,
			},
		}

		result := FormatVoiceResponse(arrivals, "Pine St & 3rd Ave", lm, "en-US")

		assert.Contains(t, result, "Arrivals for Pine St & 3rd Ave")
		assert.Contains(t, result, "Route 8 to Seattle Center in 3 minutes")
		assert.Contains(t, result, "Route 43 to Capitol Hill in 1 minute")
	})

	t.Run("Same route multiple times", func(t *testing.T) {
		arrivals := []models.Arrival{
			{
				RouteShortName:      "128",
				TripHeadsign:        "Admiral District",
				MinutesUntilArrival: 2,
			},
			{
				RouteShortName:      "128",
				TripHeadsign:        "Admiral District",
				MinutesUntilArrival: 12,
			},
			{
				RouteShortName:      "512",
				TripHeadsign:        "Everett Station",
				MinutesUntilArrival: 8,
			},
			{
				RouteShortName:      "512",
				TripHeadsign:        "Everett Station",
				MinutesUntilArrival: 18,
			},
		}

		result := FormatVoiceResponse(arrivals, "Northgate TC", lm, "en-US")

		// Should group identical routes together
		assert.Contains(t, result, "Route 128 to Admiral District in 2 minutes, in 12 minutes")
		assert.Contains(t, result, "Route 512 to Everett Station in 8 minutes, in 18 minutes")

		// Should NOT have repeated route names
		assert.Equal(t, 1, strings.Count(result, "Route 128"))
		assert.Equal(t, 1, strings.Count(result, "Route 512"))
	})

	t.Run("Mixed routes and times", func(t *testing.T) {
		arrivals := []models.Arrival{
			{
				RouteShortName:      "8",
				TripHeadsign:        "Seattle Center",
				MinutesUntilArrival: 0,
			},
			{
				RouteShortName:      "8",
				TripHeadsign:        "Seattle Center",
				MinutesUntilArrival: 15,
			},
			{
				RouteShortName:      "43",
				TripHeadsign:        "Capitol Hill",
				MinutesUntilArrival: 1,
			},
		}

		result := FormatVoiceResponse(arrivals, "Test Stop", lm, "en-US")

		assert.Contains(t, result, "Route 8 to Seattle Center arriving now, in 15 minutes")
		assert.Contains(t, result, "Route 43 to Capitol Hill in 1 minute")
	})

	t.Run("Empty arrivals", func(t *testing.T) {
		result := FormatVoiceResponse([]models.Arrival{}, "Test Stop", lm, "en-US")
		assert.Equal(t, "No upcoming arrivals found for this stop.", result)
	})
}

func TestGenerateTwiMLSMS(t *testing.T) {
	result, err := GenerateTwiMLSMS("Test message")

	assert.NoError(t, err)
	assert.Contains(t, result, "<?xml version=\"1.0\"")
	assert.Contains(t, result, "<Response>")
	assert.Contains(t, result, `<Message statusCallback="/sms/status">Test message</Message>`)
	assert.Contains(t, result, "</Response>")
}

func TestExtractStopID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Valid numeric ID", "75403", "75403"},
		{"Valid with spaces", " 75403 ", "75403"},
		{"Valid alphanumeric ID", "sw44", "sw44"},
		{"Valid single-character ID", "A", "A"},
		{"Valid first token from text", "sw44 other text", "sw44"},
		{"Empty string", "", ""},
		{"Only spaces", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractStopID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatArrivalTime(t *testing.T) {
	tests := []struct {
		minutes  int
		expected string
	}{
		{0, "Now"},
		{-1, "Now"},
		{1, "1 min"},
		{5, "5 min"},
		{30, "30 min"},
	}

	for _, tt := range tests {
		result := formatArrivalTime(tt.minutes)
		assert.Equal(t, tt.expected, result)
	}
}

func TestFormatArrivalTimeVoice(t *testing.T) {
	tests := []struct {
		minutes  int
		expected string
	}{
		{0, "arriving now"},
		{-1, "arriving now"},
		{1, "in 1 minute"},
		{5, "in 5 minutes"},
		{30, "in 30 minutes"},
	}

	for _, tt := range tests {
		result := formatArrivalTimeVoice(tt.minutes)
		assert.Equal(t, tt.expected, result)
	}
}

func TestIsDisambiguationChoice(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"1", 1},
		{"2", 2},
		{"9", 9},
		{"10", 10},
		{"99", 99},
		{"0", 0},
		{"100", 0},
		{"a", 0},
		{"1a", 0},
		{" 1 ", 1},
		{" 10 ", 10},
		{"", 0},
		{"01", 1},                 // Leading zero should still work
		{"999999999999999999", 0}, // Overflow protection
	}

	for _, tt := range tests {
		result := IsDisambiguationChoice(tt.input)
		assert.Equal(t, tt.expected, result, "Input: %s", tt.input)
	}
}

func TestGroupArrivalsByRoute(t *testing.T) {
	arrivals := []models.Arrival{
		{
			RouteShortName:      "128",
			TripHeadsign:        "Admiral District",
			MinutesUntilArrival: 2,
		},
		{
			RouteShortName:      "128",
			TripHeadsign:        "Admiral District",
			MinutesUntilArrival: 12,
		},
		{
			RouteShortName:      "512",
			TripHeadsign:        "Everett Station",
			MinutesUntilArrival: 8,
		},
		{
			RouteShortName:      "128",
			TripHeadsign:        "Admiral District",
			MinutesUntilArrival: 22,
		},
		{
			RouteShortName:      "512",
			TripHeadsign:        "Everett Station",
			MinutesUntilArrival: 18,
		},
	}

	groups := groupArrivalsByRoute(arrivals)

	assert.Len(t, groups, 2)

	// First group should be Route 128
	assert.Equal(t, "128", groups[0].RouteShortName)
	assert.Equal(t, "Admiral District", groups[0].TripHeadsign)
	assert.Equal(t, []int{2, 12, 22}, groups[0].ArrivalTimes)

	// Second group should be Route 512
	assert.Equal(t, "512", groups[1].RouteShortName)
	assert.Equal(t, "Everett Station", groups[1].TripHeadsign)
	assert.Equal(t, []int{8, 18}, groups[1].ArrivalTimes)
}

func TestFormatDisambiguationMessage(t *testing.T) {
	tests := []struct {
		name             string
		stopOptions      []models.StopOption
		originalID       string
		expectedContains []string
	}{
		{
			name: "Multiple options",
			stopOptions: []models.StopOption{
				{
					FullStopID:  "1_12345",
					DisplayText: "King County Metro: Pine St & 3rd Ave",
				},
				{
					FullStopID:  "40_12345",
					DisplayText: "Sound Transit: University Street Station",
				},
			},
			originalID: "12345",
			expectedContains: []string{
				"Multiple stops found for 12345",
				"1) King County Metro: Pine St & 3rd Ave",
				"2) Sound Transit: University Street Station",
				"Reply with the number to choose",
			},
		},
		{
			name:        "No options",
			stopOptions: []models.StopOption{},
			originalID:  "99999",
			expectedContains: []string{
				"No stops found for ID 99999",
			},
		},
		{
			name: "Single option",
			stopOptions: []models.StopOption{
				{
					FullStopID:  "1_12345",
					DisplayText: "King County Metro: Pine St & 3rd Ave",
				},
			},
			originalID:       "12345",
			expectedContains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatDisambiguationMessage(tt.stopOptions, tt.originalID)

			if len(tt.stopOptions) == 1 {
				assert.Empty(t, result)
			} else {
				for _, expected := range tt.expectedContains {
					assert.Contains(t, result, expected)
				}
			}
		})
	}
}
