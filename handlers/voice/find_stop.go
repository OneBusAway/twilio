package voice

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/twilio/twilio-go/twiml"

	"oba-twilio/formatters"
	"oba-twilio/handlers/common"
	"oba-twilio/models"
	"oba-twilio/validation"
)

func (h *Handler) HandleFindStop(c *gin.Context) {
	var req models.TwilioVoiceRequest
	if err := c.ShouldBind(&req); err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleValidationError(c, err, "voice", language)
		return
	}

	// Validate phone number
	if err := validation.ValidatePhoneNumber(req.From); err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleValidationError(c, err, "voice", language)
		return
	}

	// Validate call SID if provided
	if req.CallSid != "" {
		if err := validation.ValidateTwilioCallSid(req.CallSid); err != nil {
			log.Printf("Invalid call SID from %s: %v", req.From, err)
		}
	}

	// Sanitize digits input
	req.Digits = validation.SanitizeUserInput(req.Digits)

	log.Printf("Received voice input from %s: %s", req.From, req.Digits)

	c.Header("Content-Type", "text/xml")

	if choice := h.parseDisambiguationChoice(req.Digits); choice > 0 {
		if session := h.SessionStore.GetDisambiguationSession(req.From); session != nil {
			maxChoices := len(session.StopOptions)
			if maxChoices > 9 {
				maxChoices = 9
			}
			if err := validation.ValidateDisambiguationChoice(req.Digits, maxChoices); err != nil {
				log.Printf("Invalid disambiguation choice from %s: %v", req.From, err)
				language := h.getLanguageFromRequest(c)
				errorMsg := h.LocalizationManager.GetString("voice.error.invalid_choice", language, maxChoices)
				h.respondVoiceSay(c, language, errorMsg)
				return
			}
			h.handleVoiceDisambiguationChoice(c, req, choice)
			return
		}
	}

	h.SessionStore.ClearDisambiguationSession(req.From)

	stopID := req.Digits
	if stopID == "" {
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.no_digits", language)
		if errorMsg == "" {
			errorMsg = "I didn't receive any digits. Please try calling again."
		}
		h.respondVoiceSay(c, language, errorMsg)
		return
	}

	// Validate stop ID format and security
	if err := validation.ValidateStopID(stopID); err != nil {
		log.Printf("Invalid stop ID from %s: %s, error: %v", req.From, stopID, err)
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.invalid_stop_id", language)
		h.respondVoiceSay(c, language, errorMsg)
		return
	}

	// Find all matching stops for the given ID
	matchingStops, err := h.OBAClient.FindAllMatchingStops(stopID)
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
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.String(http.StatusOK, twimlResult)
}

// parseDisambiguationChoice checks if the input digits represent a single-digit choice (1-9)
func (h *Handler) parseDisambiguationChoice(digits string) int {
	if len(digits) != 1 {
		return 0
	}

	choice, err := strconv.Atoi(digits)
	if err != nil || choice < 1 || choice > 9 {
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

	effectiveMax := len(session.StopOptions)
	if effectiveMax > 9 {
		effectiveMax = 9
	}

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

	log.Printf("User %s selected stop %s: %s", req.From, selectedStop.FullStopID, selectedStop.DisplayText)

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
	maxStops := len(stops)
	if maxStops > 9 {
		maxStops = 9
	}

	for i := 0; i < maxStops; i++ {
		stop := stops[i]
		msg += fmt.Sprintf("Press %d for %s. ", i+1, stop.StopName)
	}

	if len(stops) > 9 {
		msg += "Only showing first 9 options. "
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
	} else {
		// Fall back to stop ID if we can't get the stop name
		stopName = obaResp.Data.Entry.StopId
	}

	language := h.getLanguageFromRequest(c)
	log.Printf(
		"Formatting voice response for %s: stop=%s, arrivals=%d, excluded=%d, fallback=%t",
		phoneNumber, stopName, len(arrivals), excluded, fallbackUsed,
	)

	message := formatters.FormatVoiceResponse(arrivals, stopName, h.LocalizationManager, language)
	log.Printf("Voice message for %s: %s", phoneNumber, message)
	if message == "" {
		log.Printf("Empty voice response generated for %s", phoneNumber)
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
		log.Printf("Failed to set voice session for %s: %v", phoneNumber, err)
	}
	menuPrompt := h.LocalizationManager.GetString("voice.menu.more_departures", language) + " " + h.LocalizationManager.GetString("voice.menu.main_menu", language)

	log.Printf("Rendering TwiML for %s with message length: %d", phoneNumber, len(message))

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
		log.Printf("Failed to generate TwiML for %s: %v", phoneNumber, err)
		errorMsg := h.LocalizationManager.GetString("voice.error.template_failed", language)

		// Try to generate error response
		errorSay := &twiml.VoiceSay{
			Message:  errorMsg,
			Language: language,
		}
		if errorTwiml, err2 := twiml.Voice([]twiml.Element{errorSay}); err2 != nil {
			log.Printf("Failed to generate error TwiML for %s: %v", phoneNumber, err2)
			// Use absolute fallback
			fallback := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Response><Say>%s</Say></Response>`, errorMsg)
			c.String(http.StatusOK, fallback)
		} else {
			c.String(http.StatusOK, errorTwiml)
		}
		return
	} else {
		log.Printf("Generated TwiML for %s, length: %d", phoneNumber, len(twimlResult))
		// Log first 500 chars of TwiML for debugging
		if len(twimlResult) > 500 {
			log.Printf("TwiML content preview: %s...", twimlResult[:500])
		} else {
			log.Printf("TwiML content: %s", twimlResult)
		}
		c.String(http.StatusOK, twimlResult)
	}
}
