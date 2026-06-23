package voice

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/twilio/twilio-go/twiml"

	"oba-twilio/middleware"
	"oba-twilio/models"
)

func (h *Handler) HandleVoiceMenuAction(c *gin.Context) {
	var req models.TwilioVoiceRequest
	if err := c.ShouldBind(&req); err != nil {
		language := h.getLanguageFromRequest(c)
		h.ErrorHandler.HandleValidationError(c, err, "voice", language)
		return
	}

	log.Printf("Received voice menu action from %s: %s", req.From, req.Digits)

	if h.analyticsManager != nil {
		middleware.TrackVoiceMenuChoice(c.Request.Context(), h.analyticsManager, req.From, h.analyticsHashSalt, req.Digits)
	}

	c.Header("Content-Type", "text/xml")

	switch req.Digits {
	case "1":
		// Option 1: Hear more departures
		h.handleExtendDepartures(c, req)
	case "2":
		// Option 2: Return to main menu
		h.handleReturnToMainMenu(c, req)
	default:
		// Invalid choice
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("voice.error.invalid_choice", language, 2)
		if errorMsg == "" {
			errorMsg = "Please press 1 or 2."
		}
		say := &twiml.VoiceSay{
			Message:  errorMsg,
			Language: language,
		}
		if twimlResult, err := twiml.Voice([]twiml.Element{say}); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
		} else {
			c.String(http.StatusOK, twimlResult)
		}
	}
}

// handleExtendDepartures extends the departure window and retrieves more arrivals
func (h *Handler) handleExtendDepartures(c *gin.Context, req models.TwilioVoiceRequest) {
	session := h.SessionStore.GetVoiceSession(req.From)
	if session == nil {
		// No session exists, return to main menu
		h.returnToMainMenu(c)
		return
	}

	// Get minutesAfter from query parameter
	minutesAfterStr := c.Query("minutesAfter")
	if minutesAfterStr == "" {
		log.Printf("Missing minutesAfter parameter in request from %s", req.From)
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("error.internal_error", language)
		if errorMsg == "" {
			errorMsg = "Sorry, there was an error processing your request. Please try again."
		}
		say := &twiml.VoiceSay{
			Message:  errorMsg,
			Language: language,
		}
		if twimlResult, err := twiml.Voice([]twiml.Element{say}); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
		} else {
			c.String(http.StatusOK, twimlResult)
		}
		return
	}

	newMinutesAfter, err := strconv.Atoi(minutesAfterStr)
	if err != nil {
		log.Printf("Invalid minutesAfter parameter: %s from %s", minutesAfterStr, req.From)
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("error.internal_error", language)
		if errorMsg == "" {
			errorMsg = "Sorry, there was an error processing your request. Please try again."
		}
		say := &twiml.VoiceSay{
			Message:  errorMsg,
			Language: language,
		}
		if twimlResult, err := twiml.Voice([]twiml.Element{say}); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
		} else {
			c.String(http.StatusOK, twimlResult)
		}
		return
	}

	// Update the session
	session.MinutesAfter = newMinutesAfter
	if err := h.SessionStore.SetVoiceSession(req.From, session); err != nil {
		log.Printf("Failed to update voice session for %s: %v", req.From, err)
		language := h.getLanguageFromRequest(c)
		errorMsg := h.LocalizationManager.GetString("error.internal_error", language)
		if errorMsg == "" {
			errorMsg = "Sorry, there was an error processing your request. Please try again."
		}
		say := &twiml.VoiceSay{
			Message:  errorMsg,
			Language: language,
		}
		if twimlResult, err := twiml.Voice([]twiml.Element{say}); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
		} else {
			c.String(http.StatusOK, twimlResult)
		}
		return
	}

	log.Printf("Extended departures window for %s to %d minutes", req.From, newMinutesAfter)

	// Get arrivals with extended window
	h.getAndFormatVoiceArrivalsWithSession(c, req.From, session.StopID, newMinutesAfter)
}

// handleReturnToMainMenu clears the voice session and returns to the start menu
func (h *Handler) handleReturnToMainMenu(c *gin.Context, req models.TwilioVoiceRequest) {
	h.SessionStore.ClearVoiceSession(req.From)
	log.Printf("Cleared voice session for %s, returning to main menu", req.From)
	h.returnToMainMenu(c)
}

// returnToMainMenu renders the main menu
func (h *Handler) returnToMainMenu(c *gin.Context) {
	language := h.getLanguageFromRequest(c)
	h.renderMainMenuWithLanguage(c, language)
}
