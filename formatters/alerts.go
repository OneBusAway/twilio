package formatters

import (
	"fmt"
	"strings"

	"oba-twilio/localization"
	"oba-twilio/models"
)

const (
	smsMaxAlerts   = 2
	voiceMaxAlerts = 2
	// smsAlertMaxRunes bounds a single SMS alert line so a long OBA summary/description
	// (real ones run ~180+ chars) can't balloon the message. Rune-safe.
	smsAlertMaxRunes = 140
)

// truncateRunes cuts s to at most max runes, appending "…" when it truncates.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

// localizedOr returns the localized value for key, or fallback if missing.
// (localizedOrEmpty lives in response.go in this same package.)
func localizedOr(lm *localization.LocalizationManager, key, language, fallback string, params ...interface{}) string {
	if lm != nil {
		if v := localizedOrEmpty(lm.GetString(key, language, params...), key); v != "" {
			return v
		}
	}
	return fallback
}

// smsBody returns the SMS text for a situation (summary, else description), truncated to
// keep the message short, or "" when the situation has neither.
func smsBody(s models.Situation) string {
	text := s.Summary
	if text == "" {
		text = s.Description
	}
	return truncateRunes(text, smsAlertMaxRunes)
}

// voiceBody returns the spoken text for a situation (summary then description), or "".
func voiceBody(s models.Situation) string {
	parts := make([]string, 0, 2)
	if s.Summary != "" {
		parts = append(parts, s.Summary)
	}
	if s.Description != "" {
		parts = append(parts, s.Description)
	}
	return strings.Join(parts, " ")
}

// FormatSMSAlerts renders a compact, localized alert block for SMS, or "" if none.
func FormatSMSAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string {
	var bodies []string
	for _, s := range situations {
		if b := smsBody(s); b != "" {
			bodies = append(bodies, b)
		}
	}
	if len(bodies) == 0 {
		return ""
	}
	prefix := localizedOr(lm, "sms.alert.prefix", language, "⚠ Service alert:")

	shown := bodies
	overflow := 0
	if len(bodies) > smsMaxAlerts {
		shown = bodies[:smsMaxAlerts]
		overflow = len(bodies) - smsMaxAlerts
	}
	lines := make([]string, 0, len(shown)+1)
	for _, b := range shown {
		lines = append(lines, prefix+" "+b)
	}
	if overflow > 0 {
		lines = append(lines, localizedOr(lm, "sms.alert.more", language,
			fmt.Sprintf("+%d more service alerts.", overflow), overflow))
	}
	return strings.Join(lines, "\n")
}

// FormatVoiceAlerts renders a spoken, localized alert block for voice, or "" if none.
func FormatVoiceAlerts(situations []models.Situation, lm *localization.LocalizationManager, language string) string {
	leadIn := localizedOr(lm, "voice.alert.lead_in", language, "Service alert.")
	var spoken []string
	for _, s := range situations {
		if b := voiceBody(s); b != "" {
			spoken = append(spoken, leadIn+" "+b)
		}
	}
	if len(spoken) == 0 {
		return ""
	}
	shown := spoken
	overflow := 0
	if len(spoken) > voiceMaxAlerts {
		shown = spoken[:voiceMaxAlerts]
		overflow = len(spoken) - voiceMaxAlerts
	}
	out := strings.Join(shown, " ")
	if overflow > 0 {
		out += " " + localizedOr(lm, "voice.alert.more", language,
			fmt.Sprintf("There are %d more service alerts.", overflow), overflow)
	}
	return out
}
