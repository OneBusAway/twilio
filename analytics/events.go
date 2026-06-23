package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
)

// Common event names used throughout the application
const (
	// SMS events
	EventSMSRequest               = "sms_request"
	EventSMSResponse              = "sms_response"
	EventSMSDisambiguationPresent = "sms_disambiguation_presented"
	EventSMSDisambiguationSelect  = "sms_disambiguation_selected"

	// Voice events
	EventVoiceRequest    = "voice_request"
	EventVoiceResponse   = "voice_response"
	EventVoiceDTMFInput  = "voice_dtmf_input"
	EventVoiceMenuChoice = "voice_menu_choice"

	// Stop lookup events
	EventStopLookup        = "stop_lookup"
	EventStopLookupSuccess = "stop_lookup_success"
	EventStopLookupFailure = "stop_lookup_failure"

	// System events
	EventLanguageDetected = "language_detected"
	EventErrorOccurred    = "error_occurred"
	EventAPILatency       = "api_latency"
)

// Property keys for event properties
const (
	PropLanguage     = "language"
	PropStopID       = "stop_id"
	PropAgencyID     = "agency_id"
	PropQuery        = "query"
	PropErrorType    = "error_type"
	PropErrorMessage = "error_message"
	PropLatencyMS    = "latency_ms"
	PropAPIEndpoint  = "api_endpoint"
	PropChoiceCount  = "choice_count"
	PropChoiceIndex  = "choice_index"
	PropDTMFDigits   = "dtmf_digits"
	PropResponseType = "response_type"
	PropArrivalCount = "arrival_count"
	ProphasArrivals  = "has_arrivals"
)

// NewEvent creates a new event with common fields populated.
func NewEvent(name string, userID string) Event {
	return Event{
		ID:         uuid.New().String(),
		Name:       name,
		Timestamp:  time.Now().UTC(),
		Version:    1,
		UserID:     userID,
		Properties: make(map[string]interface{}),
	}
}

// NewEventWithSession creates a new event with session information.
func NewEventWithSession(name string, userID string, sessionID string) Event {
	event := NewEvent(name, userID)
	event.SessionID = sessionID
	return event
}

// HashPhoneNumber creates a privacy-preserving hash of a phone number.
// It uses SHA256 with a salt to prevent rainbow table attacks.
func HashPhoneNumber(phoneNumber string, salt string) string {
	if phoneNumber == "" {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(phoneNumber + salt))
	return hex.EncodeToString(h.Sum(nil))
}

// SMSRequestEvent creates an event for incoming SMS requests.
func SMSRequestEvent(hashedUserID, language, query string) Event {
	event := NewEvent(EventSMSRequest, hashedUserID)
	event.Properties[PropLanguage] = language
	event.Properties[PropQuery] = query
	return event
}

// VoiceRequestEvent creates an event for incoming voice calls.
func VoiceRequestEvent(hashedUserID, language string) Event {
	event := NewEvent(EventVoiceRequest, hashedUserID)
	event.Properties[PropLanguage] = language
	return event
}

// VoiceMenuChoiceEvent creates an event for a voice menu (DTMF) selection.
func VoiceMenuChoiceEvent(hashedUserID, digits string) Event {
	event := NewEvent(EventVoiceMenuChoice, hashedUserID)
	event.Properties[PropDTMFDigits] = digits
	return event
}

// StopLookupEvent creates an event for stop lookup attempts.
func StopLookupEvent(hashedUserID, stopID, agencyID string, success bool, latencyMS int64) Event {
	eventName := EventStopLookupSuccess
	if !success {
		eventName = EventStopLookupFailure
	}

	event := NewEvent(eventName, hashedUserID)
	event.Properties[PropStopID] = stopID
	event.Properties[PropAgencyID] = agencyID
	event.Properties[PropLatencyMS] = latencyMS
	return event
}

// ErrorEvent creates an event for tracking errors.
func ErrorEvent(hashedUserID, errorType, errorMessage string) Event {
	event := NewEvent(EventErrorOccurred, hashedUserID)
	event.Properties[PropErrorType] = errorType
	event.Properties[PropErrorMessage] = errorMessage
	return event
}

// DisambiguationPresentedEvent creates an event for when multiple stop choices are shown.
func DisambiguationPresentedEvent(hashedUserID, sessionID string, choiceCount int) Event {
	event := NewEventWithSession(EventSMSDisambiguationPresent, hashedUserID, sessionID)
	event.Properties[PropChoiceCount] = choiceCount
	return event
}

// DisambiguationSelectedEvent creates an event for when a user selects from multiple stops.
func DisambiguationSelectedEvent(hashedUserID, sessionID string, choiceIndex int, stopID string) Event {
	event := NewEventWithSession(EventSMSDisambiguationSelect, hashedUserID, sessionID)
	event.Properties[PropChoiceIndex] = choiceIndex
	event.Properties[PropStopID] = stopID
	return event
}
