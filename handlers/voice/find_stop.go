package voice

import (
	"fmt"
	"log"
	"net/http"
	"oba-twilio/analytics"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/twilio/twilio-go/twiml"

	"oba-twilio/formatters"
	"oba-twilio/handlers/common"
	"oba-twilio/middleware"
	"oba-twilio/models"
	"oba-twilio/validation"
)

func (h *Handler) HandleFindStop(c *gin.Context) {
	req, ok := h.bindFindStopRequest(c)
	if !ok {
		return
	}

	c.Header("Content-Type", "text/xml")

	// A single digit may be a selection from a pending disambiguation prompt.
	if h.tryHandleDisambiguationChoice(c, req) {
		return
	}

	h.SessionStore.ClearDisambiguationSession(req.From)

	stopID, ok := h.resolveStopID(c, req)
	if !ok {
		return
	}

	h.respondForStopID(c, req, stopID)
}

// bindFindStopRequest binds and validates the incoming request. On failure it
// writes the error response and returns ok=false.
func (h *Handler) bindFindStopRequest(c *gin.Context) (models.TwilioVoiceRequest, bool) {
	var req models.TwilioVoiceRequest
	if err := c.ShouldBind(&req); err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleValidationError(c, err, "voice", language)
		return req, false
	}

	if err := validation.ValidatePhoneNumber(req.From); err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleValidationError(c, err, "voice", language)
		return req, false
	}

	// A malformed call SID is logged but not fatal: the call can still proceed.
	if req.CallSid != "" {
		if err := validation.ValidateTwilioCallSid(req.CallSid); err != nil {
			log.Printf("Invalid call SID from %s: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), err)
		}
	}

	req.Digits = validation.SanitizeUserInput(req.Digits)
	log.Printf("Received voice input from %s: %s", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), req.Digits)

	return req, true
}

// tryHandleDisambiguationChoice handles the digits as a choice for a pending
// disambiguation session. It returns true when the request was handled (a
// response was written); false means the digits should be treated as a stop ID.
func (h *Handler) tryHandleDisambiguationChoice(c *gin.Context, req models.TwilioVoiceRequest) bool {
	choice := h.parseDisambiguationChoice(req.Digits)
	if choice == 0 {
		return false
	}

	session := h.SessionStore.GetDisambiguationSession(req.From)
	if session == nil {
		return false
	}

	maxChoices := clampChoiceCount(len(session.StopOptions))

	if err := validation.ValidateDisambiguationChoice(req.Digits, maxChoices); err != nil {
		log.Printf("Invalid disambiguation choice from %s: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), err)
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.invalid_choice", language, maxChoices)
		h.respondVoiceSay(c, language, errorMsg)
		return true
	}

	h.handleVoiceDisambiguationChoice(c, req, choice)
	return true
}

// resolveStopID extracts and validates the stop ID from the request digits. On
// failure it writes the error response and returns ok=false.
func (h *Handler) resolveStopID(c *gin.Context, req models.TwilioVoiceRequest) (string, bool) {
	stopID := req.Digits
	if stopID == "" {
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.no_digits", language)
		if errorMsg == "" {
			errorMsg = "I didn't receive any digits. Please try calling again."
		}
		h.respondVoiceSay(c, language, errorMsg)
		return "", false
	}

	if err := validation.ValidateStopID(stopID); err != nil {
		log.Printf("Invalid stop ID from %s: %s, error: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), stopID, err)
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.invalid_stop_id", language)
		h.respondVoiceSay(c, language, errorMsg)
		return "", false
	}

	return stopID, true
}

// respondForStopID looks up the stops matching stopID and responds: arrivals for
// a single match, a disambiguation prompt for several, or an error otherwise.
func (h *Handler) respondForStopID(c *gin.Context, req models.TwilioVoiceRequest, stopID string) {
	startTime := time.Now()
	matchingStops, err := h.OBAClient.FindAllMatchingStops(stopID)
	latencyMS := time.Since(startTime).Milliseconds()

	if h.analyticsManager != nil {
		// A lookup with no matching stops is a user-facing failure, not a success.
		success := err == nil && len(matchingStops) > 0
		agencyName := ""
		if len(matchingStops) > 0 {
			agencyName = matchingStops[0].AgencyName
		}
		middleware.TrackStopLookup(c.Request.Context(), h.analyticsManager, req.From, stopID, agencyName, h.analyticsHashSalt, success, latencyMS)
	}

	if err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleVoiceError(c, err, language)
		return
	}

	if len(matchingStops) == 0 {
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.stop_not_found", language)
		if errorMsg == "" {
			errorMsg = "Sorry, I couldn't find any stops with that ID. Please check the stop ID and try again."
		}
		h.respondVoiceSay(c, language, errorMsg)
		return
	}

	if len(matchingStops) > 1 {
		h.respondVoiceStopDisambiguation(c, req.From, stopID, matchingStops)
		return
	}

	h.getAndFormatVoiceArrivalsWithSession(c, req.From, matchingStops[0].FullStopID, 0)
}

func (h *Handler) respondVoiceSay(c *gin.Context, language, message string) {
	say := &twiml.VoiceSay{
		Message:  message,
		Language: language,
	}
	twimlResult, err := twiml.Voice([]twiml.Element{say})
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.String(http.StatusOK, twimlResult)
}

func (h *Handler) respondVoiceStopDisambiguation(c *gin.Context, fromPhone, stopID string, matchingStops []models.StopOption) {
	disambiguationMsg := h.formatVoiceDisambiguationMessage(c, matchingStops, stopID)
	session := &models.DisambiguationSession{
		StopOptions: matchingStops,
	}
	if err := h.SessionStore.SetDisambiguationSession(fromPhone, session); err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleInternalError(c, err, "voice", language)
		return
	}

	language := h.getLanguageFromRequest(c)
	innerElts := []twiml.Element{
		&twiml.VoiceSay{
			Message:  disambiguationMsg,
			Language: language,
		},
	}
	gather := &twiml.VoiceGather{
		Action:        fmt.Sprintf("/voice/find_stop?lang=%s", language),
		Method:        "POST",
		Timeout:       "10",
		NumDigits:     "1",
		InnerElements: innerElts,
	}
	// GetString falls back to the default locale and ultimately the key name, so
	// it never returns an empty string; a manual fallback here would be dead code.
	timeoutMsg := h.LocalizationManager.GetString("voice.timeout", language)
	timeoutSay := &twiml.VoiceSay{
		Message:  timeoutMsg,
		Language: language,
	}
	twimlResult, err := twiml.Voice([]twiml.Element{gather, timeoutSay})
	if err != nil {
		log.Printf("Failed to generate TwiML gather: %v", err)
		// The session was persisted above; if we can't deliver the options, clear
		// it so a later stray single-digit press isn't treated as a stale choice.
		h.SessionStore.ClearDisambiguationSession(fromPhone)
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.String(http.StatusOK, twimlResult)
}

// maxVoiceChoices is the most disambiguation options a caller can select, bounded
// by the single DTMF digit (1-9) used to choose one.
const maxVoiceChoices = 9

// clampChoiceCount limits a number of options to what a single keypress can select.
func clampChoiceCount(n int) int {
	if n > maxVoiceChoices {
		return maxVoiceChoices
	}
	return n
}

// parseDisambiguationChoice checks if the input digits represent a single-digit choice (1-9)
func (h *Handler) parseDisambiguationChoice(digits string) int {
	if len(digits) != 1 {
		return 0
	}

	choice, err := strconv.Atoi(digits)
	if err != nil || choice < 1 || choice > maxVoiceChoices {
		return 0
	}

	return choice
}

// handleVoiceDisambiguationChoice processes the user's disambiguation choice (multiple stops for one ID).
func (h *Handler) handleVoiceDisambiguationChoice(c *gin.Context, req models.TwilioVoiceRequest, choice int) {
	session := h.SessionStore.GetDisambiguationSession(req.From)
	if session == nil {
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.no_active_session", language)
		if errorMsg == "" {
			errorMsg = "No active selection. Please call again and enter a stop ID to get started."
		}
		h.respondVoiceSay(c, language, errorMsg)
		return
	}

	effectiveMax := clampChoiceCount(len(session.StopOptions))

	if choice < 1 || choice > effectiveMax {
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.invalid_choice", language, effectiveMax)
		if errorMsg == "" {
			errorMsg = fmt.Sprintf("Please press a number between 1 and %d.", effectiveMax)
		}
		h.respondVoiceSay(c, language, errorMsg)
		return
	}

	selectedStop := session.StopOptions[choice-1]
	h.SessionStore.ClearDisambiguationSession(req.From)

	log.Printf("User %s selected stop %s: %s", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), selectedStop.FullStopID, selectedStop.DisplayText)

	h.getAndFormatVoiceArrivalsWithSession(c, req.From, selectedStop.FullStopID, 0)
}

// formatVoiceDisambiguationMessage creates a voice-friendly disambiguation message
func (h *Handler) formatVoiceDisambiguationMessage(c *gin.Context, stops []models.StopOption, stopID string) string {
	language := h.getLanguageFromRequest(c)

	// Use localized string or fall back to English
	msg := h.LocalizationManager.GetString("voice.disambiguation.prompt", language, len(stops), stopID)
	if msg == "" {
		msg = fmt.Sprintf("I found %d stops with ID %s. ", len(stops), stopID)
	} else {
		msg += " " // Add space after prompt
	}

	// Limit to first 9 options for voice interface (single digit choices)
	maxStops := clampChoiceCount(len(stops))

	for i := 0; i < maxStops; i++ {
		stop := stops[i]
		msg += fmt.Sprintf("Press %d for %s. ", i+1, stop.StopName)
	}

	if len(stops) > maxVoiceChoices {
		msg += fmt.Sprintf("Only showing first %d options. ", maxVoiceChoices)
	}

	msg += "Which stop would you like?"

	return msg
}

// getAndFormatVoiceArrivalsWithSession fetches arrival information with a custom window and formats it for voice response
func (h *Handler) getAndFormatVoiceArrivalsWithSession(c *gin.Context, phoneNumber, fullStopID string, minutesAfter int) {
	var obaResp *models.OneBusAwayResponse
	var err error

	// Use 30 minutes as default window if minutesAfter is 0, otherwise use provided value
	window := minutesAfter
	if window == 0 {
		window = 30
	}

	obaResp, err = h.OBAClient.GetArrivalsAndDeparturesWithWindow(fullStopID, window)

	if err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleVoiceError(c, err, language)
		return
	}

	arrivals := h.OBAClient.ProcessArrivals(obaResp, window)
	filteredArrivals, excluded, fallbackUsed := common.FilterArrivals(arrivals, h.arrivalFilterConfig)
	arrivals = filteredArrivals

	// Get the human-readable stop name instead of using the technical stop ID
	stopName := ""
	stopInfo, err := h.OBAClient.GetStopInfo(fullStopID)
	if err != nil {
		log.Printf("Failed to get stop info for %s: %v", fullStopID, err)
	}
	if err == nil && stopInfo != nil && stopInfo.StopName != "" {
		stopName = stopInfo.StopName
	} else if obaResp.Data.Entry.StopId != "" {
		// Fall back to the response's stop ID if we can't get the stop name.
		stopName = obaResp.Data.Entry.StopId
	} else {
		// Last resort: fullStopID is always non-empty, so we never read a blank name.
		stopName = fullStopID
	}

	language := h.getLanguageFromRequest(c)
	log.Printf(
		"Formatting voice response for %s: stop=%s, arrivals=%d, excluded=%d, fallback=%t",
		analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), stopName, len(arrivals), excluded, fallbackUsed,
	)

	message := formatters.FormatVoiceResponse(arrivals, stopName, h.LocalizationManager, language)
	log.Printf("Voice message for %s: %s", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), message)
	if message == "" {
		log.Printf("Empty voice response generated for %s", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt))
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleVoiceError(c, fmt.Errorf("failed to format voice response"), language)
		return
	}

	// Set up voice session for menu options
	session := &models.VoiceSession{
		StopID:       fullStopID,
		MinutesAfter: minutesAfter,
	}
	if err := h.SessionStore.SetVoiceSession(phoneNumber, session); err != nil {
		log.Printf("Failed to set voice session for %s: %v", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), err)
	}
	menuPrompt := h.LocalizationManager.GetString("voice.menu.more_departures", language) + " " + h.LocalizationManager.GetString("voice.menu.main_menu", language)

	log.Printf("Rendering TwiML for %s with message length: %d", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), len(message))

	// Create TwiML elements
	var elements []twiml.Element

	// Add arrivals message
	arrivalsSay := &twiml.VoiceSay{
		Message:  message,
		Language: language,
	}
	elements = append(elements, arrivalsSay)

	// Add gather for menu options
	innerElts := []twiml.Element{
		&twiml.VoiceSay{
			Message:  menuPrompt,
			Language: language,
		},
	}

	var actionURL string
	if minutesAfter == 0 {
		actionURL = fmt.Sprintf("/voice/menu_action?minutesAfter=60&lang=%s", language)
	} else {
		actionURL = fmt.Sprintf("/voice/menu_action?minutesAfter=%d&lang=%s", minutesAfter+30, language)
	}

	gather := &twiml.VoiceGather{
		Action:        actionURL,
		Method:        "POST",
		NumDigits:     "1",
		InnerElements: innerElts,
	}
	elements = append(elements, gather)

	// Generate TwiML
	if twimlResult, err := twiml.Voice(elements); err != nil {
		log.Printf("Failed to generate TwiML for %s: %v", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), err)
		errorMsg := h.LocalizationManager.GetString("voice.error.template_failed", language)

		// Try to generate error response
		errorSay := &twiml.VoiceSay{
			Message:  errorMsg,
			Language: language,
		}
		if errorTwiml, err2 := twiml.Voice([]twiml.Element{errorSay}); err2 != nil {
			log.Printf("Failed to generate error TwiML for %s: %v", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), err2)
			// Use absolute fallback
			fallback := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Response><Say>%s</Say></Response>`, errorMsg)
			c.String(http.StatusOK, fallback)
		} else {
			c.String(http.StatusOK, errorTwiml)
		}
		return
	} else {
		log.Printf("Generated TwiML for %s, length: %d", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), len(twimlResult))
		// Log first 500 chars of TwiML for debugging
		if len(twimlResult) > 500 {
			log.Printf("TwiML content preview: %s...", twimlResult[:500])
		} else {
			log.Printf("TwiML content: %s", twimlResult)
		}
		c.String(http.StatusOK, twimlResult)
	}
}
