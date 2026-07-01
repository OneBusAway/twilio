package formatters

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"oba-twilio/models"
)

func sit(summary, desc string) models.Situation {
	return models.Situation{Summary: summary, Description: desc}
}

func TestFormatSMSAlerts_Empty(t *testing.T) {
	assert.Equal(t, "", FormatSMSAlerts(nil, nil, "en-US"))
}

func TestFormatSMSAlerts_OneAlert_NilManagerFallsBackToEnglish(t *testing.T) {
	out := FormatSMSAlerts([]models.Situation{sit("Reroute on 40", "")}, nil, "en-US")
	assert.Contains(t, out, "Service alert")
	assert.Contains(t, out, "Reroute on 40")
}

func TestFormatSMSAlerts_DescriptionFallbackWhenNoSummary(t *testing.T) {
	out := FormatSMSAlerts([]models.Situation{sit("", "Body text")}, nil, "en-US")
	assert.Contains(t, out, "Body text")
}

func TestFormatSMSAlerts_TruncatesLongText(t *testing.T) {
	long := strings.Repeat("x", 300)
	out := FormatSMSAlerts([]models.Situation{sit(long, "")}, nil, "en-US")
	assert.Contains(t, out, "…")
	assert.NotContains(t, out, long)      // full 300-char string not present
	assert.Less(t, len([]rune(out)), 300) // materially shorter
}

func TestFormatSMSAlerts_SkipsEmptyAndCountsOverflow(t *testing.T) {
	in := []models.Situation{sit("A", ""), sit("", ""), sit("B", ""), sit("C", "")}
	out := FormatSMSAlerts(in, nil, "en-US")
	assert.Contains(t, out, "A")
	assert.Contains(t, out, "B")
	assert.NotContains(t, out, "C")   // capped at 2 renderable
	assert.Contains(t, out, "1 more") // 3 renderable - 2 shown = 1
}

func TestFormatVoiceAlerts_Empty(t *testing.T) {
	assert.Equal(t, "", FormatVoiceAlerts(nil, nil, "en-US"))
}

func TestFormatVoiceAlerts_ReadsSummaryAndDescription(t *testing.T) {
	out := FormatVoiceAlerts([]models.Situation{sit("Reroute", "Take 3rd Ave")}, nil, "en-US")
	assert.Contains(t, out, "Service alert")
	assert.Contains(t, out, "Reroute")
	assert.Contains(t, out, "Take 3rd Ave")
}

func TestFormatVoiceAlerts_NoURLReadAloud(t *testing.T) {
	s := models.Situation{Summary: "S", URL: "http://example.org/x"}
	out := FormatVoiceAlerts([]models.Situation{s}, nil, "en-US")
	assert.NotContains(t, out, "http")
}

func TestFormatVoiceAlerts_OverflowLine(t *testing.T) {
	in := []models.Situation{sit("A", ""), sit("B", ""), sit("C", "")}
	out := FormatVoiceAlerts(in, nil, "en-US")
	assert.True(t, strings.Contains(out, "1 more"))
}
