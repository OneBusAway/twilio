package handlers

import (
	"fmt"
	"log"
	"net/http"
	"oba-twilio/analytics"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"oba-twilio/client"
	"oba-twilio/formatters"
	"oba-twilio/handlers/common"
	"oba-twilio/localization"
	"oba-twilio/metrics"
	"oba-twilio/middleware"
	"oba-twilio/models"
	"oba-twilio/validation"
)

const smsPageSize = 3

// Pre-compiled regex patterns for performance
var (
	timeRegex     = regexp.MustCompile(`^\+(\d+)$`)
	hourRegex     = regexp.MustCompile(`^\+(\d+)h$`)
	nextHourRegex = regexp.MustCompile(`^next\s+(\d+)?\s*hours?$`)
)

type SMSHandler struct {
	OBAClient           client.OneBusAwayClientInterface
	SessionStore        *common.SessionStore
	LocalizationManager *localization.LocalizationManager
	ErrorHandler        *common.ErrorHandler
	arrivalFilterConfig common.ArrivalFilterConfig
	analyticsManager    middleware.AnalyticsManager
	analyticsHashSalt   string
	metrics             *metrics.Metrics
}

func NewSMSHandler(obaClient client.OneBusAwayClientInterface, locManager *localization.LocalizationManager) *SMSHandler {
	return &SMSHandler{
		OBAClient:           obaClient,
		SessionStore:        common.NewSessionStore(),
		LocalizationManager: locManager,
		ErrorHandler:        common.NewErrorHandler(locManager),
		arrivalFilterConfig: common.ArrivalFilterConfig{
			Enabled:               false,
			MaxPredictedEarlyMins: 15,
			FallbackToUnfiltered:  true,
		},
	}
}

func (h *SMSHandler) SetArrivalFilterConfig(cfg common.ArrivalFilterConfig) {
	h.arrivalFilterConfig = cfg
}

// SetMetrics attaches the Prometheus metrics holder. Safe to leave unset (the
// record calls are nil-safe).
func (h *SMSHandler) SetMetrics(m *metrics.Metrics) {
	h.metrics = m
}

func (h *SMSHandler) Close() {
	if h.SessionStore != nil {
		h.SessionStore.Close()
	}
}

func (h *SMSHandler) HandleSMS(c *gin.Context) {
	var req models.TwilioSMSRequest
	if err := c.ShouldBind(&req); err != nil {
		h.ErrorHandler.HandleValidationError(c, err, "sms", h.LocalizationManager.GetPrimaryLanguage())
		return
	}

	// Validate phone number
	if err := validation.ValidatePhoneNumber(req.From); err != nil {
		h.ErrorHandler.HandleValidationError(c, err, "sms", h.LocalizationManager.GetPrimaryLanguage())
		return
	}

	// Validate and sanitize message body
	if err := validation.ValidateMessageBody(req.Body); err != nil {
		h.ErrorHandler.HandleValidationError(c, err, "sms", h.LocalizationManager.GetPrimaryLanguage())
		return
	}

	// Sanitize the message body
	req.Body = validation.SanitizeUserInput(req.Body)

	log.Printf("Received SMS from %s: %s", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), req.Body)

	c.Header("Content-Type", "text/xml")

	// Get or create SMS session for language persistence
	smsSession := h.getOrCreateSMSSession(req.From)

	// Track SMS request
	if h.analyticsManager != nil {
		middleware.TrackSMSRequest(c.Request.Context(), h.analyticsManager, req.From, smsSession.Language, req.Body, h.analyticsHashSalt)
	}

	// Check for keywords first
	if h.handleKeywords(c, req, smsSession) {
		return
	}

	// Check if user is responding to a disambiguation request.
	// Treat short numeric messages (e.g. "1") as a choice only when there is an active
	// disambiguation session for this sender and the choice is within the valid range.
	if choice := formatters.IsDisambiguationChoice(req.Body); choice > 0 {
		session := h.SessionStore.GetDisambiguationSession(req.From)
		if session != nil {
			if err := validation.ValidateDisambiguationChoice(req.Body, len(session.StopOptions)); err != nil {
				log.Printf("Invalid disambiguation choice from %s: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), err)
				errorMsg := h.LocalizationManager.GetString("sms.error.invalid_choice", h.getLanguageForUser(req.From), len(session.StopOptions))
				twiml, _ := formatters.GenerateTwiMLSMS(errorMsg)
				c.String(http.StatusOK, twiml)
				return
			}
			h.handleDisambiguationChoice(c, req, choice)
			return
		}
		// No active session: fall through and treat as a stop query.
	}

	// Clear any existing disambiguation session for new queries
	h.SessionStore.ClearDisambiguationSession(req.From)

	// Extract language and stop ID from message
	language, stopID := h.extractLanguageFromMessage(req.Body, smsSession)
	if stopID == "" {
		errorMsg := h.LocalizationManager.GetString("sms.error.invalid_stop", language)
		twiml, _ := formatters.GenerateTwiMLSMS(errorMsg)
		c.String(http.StatusOK, twiml)
		return
	}

	// Update SMS session with current language and stop
	smsSession.Language = language
	smsSession.LastStopID = stopID
	smsSession.ArrivalHorizonShownMinutes = 0 // new stop query — show nearest slice from scratch
	smsSession.LastQueryTime = time.Now().Unix()
	if err := h.SessionStore.SetSMSSession(req.From, smsSession); err != nil {
		log.Printf("Failed to set SMS session for %s: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), err)
	}

	// Find all matching stops for the given ID
	startTime := time.Now()
	matchingStops, err := h.OBAClient.FindAllMatchingStops(stopID)
	latencyMS := time.Since(startTime).Milliseconds()

	// Track stop lookup
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
		// Track error
		if h.analyticsManager != nil {
			middleware.TrackError(c.Request.Context(), h.analyticsManager, req.From, "stop_lookup", err.Error(), h.analyticsHashSalt)
		}
		h.metrics.RecordInteraction("sms", "error")
		h.metrics.RecordStopLookup("not_found", "none")
		h.ErrorHandler.HandleSMSError(c, err, language)
		return
	}

	if len(matchingStops) == 0 {
		h.metrics.RecordInteraction("sms", "not_found")
		h.metrics.RecordStopLookup("not_found", "none")
		errorMsg := h.LocalizationManager.GetString("sms.no_stops_found", language, stopID)
		twiml, _ := formatters.GenerateTwiMLSMS(errorMsg)
		c.String(http.StatusOK, twiml)
		return
	}

	// If multiple stops found, ask user to disambiguate
	if len(matchingStops) > 1 {
		disambiguationMsg := formatters.FormatDisambiguationMessage(matchingStops, stopID)

		// Store disambiguation session
		session := &models.DisambiguationSession{
			StopOptions: matchingStops,
		}
		if err := h.SessionStore.SetDisambiguationSession(req.From, session); err != nil {
			h.ErrorHandler.HandleInternalError(c, err, "sms", language)
			return
		}

		// Track disambiguation presented
		if h.analyticsManager != nil {
			sessionID := fmt.Sprintf("sms_%s_%d", req.From, time.Now().Unix())
			middleware.TrackDisambiguationPresented(c.Request.Context(), h.analyticsManager, req.From, sessionID, h.analyticsHashSalt, len(matchingStops))
		}
		h.metrics.RecordInteraction("sms", "ambiguous")
		h.metrics.RecordStopLookup("ambiguous", common.AgencyPrefix(matchingStops[0].FullStopID))

		twiml, _ := formatters.GenerateTwiMLSMS(disambiguationMsg)
		c.String(http.StatusOK, twiml)
		return
	}

	// Single stop found, get arrivals directly
	h.metrics.RecordInteraction("sms", "resolved")
	h.metrics.RecordStopLookup("resolved", common.AgencyPrefix(matchingStops[0].FullStopID))
	// For SMS "Stop:" header we want the stop name (not "Agency: Stop").
	h.getAndFormatArrivalsWithStopNameAndSession(c, req.From, matchingStops[0].FullStopID, matchingStops[0].StopName, smsSession)
}

func (h *SMSHandler) handleDisambiguationChoice(c *gin.Context, req models.TwilioSMSRequest, choice int) {
	language := h.getLanguageForUser(req.From)
	session := h.SessionStore.GetDisambiguationSession(req.From)
	if session == nil {
		errorMsg := h.LocalizationManager.GetString("sms.error.no_active_session", language)
		twiml, _ := formatters.GenerateTwiMLSMS(errorMsg)
		c.String(http.StatusOK, twiml)
		return
	}

	if choice < 1 || choice > len(session.StopOptions) {
		errorMsg := h.LocalizationManager.GetString("sms.error.invalid_choice", language, len(session.StopOptions))
		twiml, _ := formatters.GenerateTwiMLSMS(errorMsg)
		c.String(http.StatusOK, twiml)
		return
	}

	selectedStop := session.StopOptions[choice-1]
	h.SessionStore.ClearDisambiguationSession(req.From)

	log.Printf("User %s selected stop %s: %s", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), selectedStop.FullStopID, selectedStop.DisplayText)

	// Track disambiguation selection
	if h.analyticsManager != nil {
		sessionID := fmt.Sprintf("sms_%s_%d", req.From, time.Now().Unix())
		middleware.TrackDisambiguationSelected(c.Request.Context(), h.analyticsManager, req.From, sessionID, h.analyticsHashSalt, choice, selectedStop.FullStopID)
	}

	// Get SMS session for this user to maintain consistency
	smsSession := h.getOrCreateSMSSession(req.From)
	smsSession.LastStopID = selectedStop.FullStopID
	smsSession.ArrivalHorizonShownMinutes = 0
	// For SMS "Stop:" header we want the stop name (not "Agency: Stop").
	h.getAndFormatArrivalsWithStopNameAndSession(c, req.From, selectedStop.FullStopID, selectedStop.StopName, smsSession)
}

func (h *SMSHandler) getAndFormatArrivalsWithStopNameAndSession(c *gin.Context, phoneNumber string, fullStopID string, stopDisplayName string, session *models.SMSSession) {
	var obaResp *models.OneBusAwayResponse
	var err error

	// Use session window or default
	window := session.WindowMinutes
	if window == 0 {
		window = 30
	}

	obaResp, err = h.OBAClient.GetArrivalsAndDeparturesWithWindow(fullStopID, window)
	if err != nil {
		h.ErrorHandler.HandleSMSError(c, err, session.Language)
		return
	}

	arrivalsAll := h.OBAClient.ProcessArrivals(obaResp, window)
	filteredArrivals, excluded, fallbackUsed := common.FilterArrivals(arrivalsAll, h.arrivalFilterConfig)
	arrivalsAll = filteredArrivals

	// Use paged continuation for SMS: "more" returns the next chunk, not an arbitrary time jump.
	// ArrivalHorizonShownMinutes stores how many arrivals were already shown in this session.
	//
	// TRADEOFF: this is a positional offset into a list that is re-fetched on every
	// request. ProcessArrivals filters out departed buses (arrivalTime <= now), so the
	// head of the sorted list shrinks as time passes. If a bus at the head departs
	// between the first page and a later "more", the list shifts left and the arrival
	// now sitting at this offset can be skipped. A content-based cursor (e.g. the last
	// shown predicted arrival time) would be stable across re-fetches; the positional
	// offset is kept here for simplicity, accepting that a boundary arrival may be
	// skipped in the window between requests.
	offset := session.ArrivalHorizonShownMinutes
	if offset < 0 {
		offset = 0
	}
	if offset > len(arrivalsAll) {
		offset = len(arrivalsAll)
	}

	arrivals := arrivalsAll[offset:]
	if len(arrivals) > smsPageSize {
		arrivals = arrivals[:smsPageSize]
	}
	log.Printf(
		"SMS pagination for %s: stop=%s window=%d total_arrivals=%d excluded=%d fallback=%t offset=%d page_size=%d returned=%d",
		analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt),
		fullStopID,
		window,
		len(arrivalsAll),
		excluded,
		fallbackUsed,
		offset,
		smsPageSize,
		len(arrivals),
	)

	// If we don't have a display name yet, try fetching stop info to obtain the stop name.
	if stopDisplayName == "" {
		stopInfo, err := h.OBAClient.GetStopInfo(fullStopID)
		if err != nil {
			log.Printf("Failed to get stop info for %s: %v", fullStopID, err)
		}
		if err == nil && stopInfo != nil && stopInfo.StopName != "" {
			stopDisplayName = stopInfo.StopName
		} else if obaResp.Data.Entry.StopId != "" {
			// Last resort: fall back to stop ID (works even if name can't be resolved).
			stopDisplayName = obaResp.Data.Entry.StopId
		}
	}

	var message string
	if len(arrivals) == 0 {
		if offset > 0 {
			message = h.LocalizationManager.GetString("sms.more.no_additional", session.Language)
		} else {
			message = formatters.FormatSMSResponse(arrivals, stopDisplayName, h.LocalizationManager, session.Language)
		}
	} else {
		message = formatters.FormatSMSResponse(arrivals, stopDisplayName, h.LocalizationManager, session.Language)
	}

	// Add menu hints if there are arrivals
	if len(arrivals) > 0 {
		moreHint := h.LocalizationManager.GetString("sms.menu.more_hint", session.Language)
		helpHint := h.LocalizationManager.GetString("sms.menu.help_hint", session.Language)
		message += fmt.Sprintf("\n\n%s | %s", moreHint, helpHint)
	}

	// Generate TwiML response first to ensure we always send a response
	twiml, err := formatters.GenerateTwiMLSMS(message)
	if err != nil {
		h.ErrorHandler.HandleInternalError(c, err, "sms", session.Language)
		return
	}

	// Update session with current stop atomically
	newSession := *session
	newSession.LastStopID = fullStopID
	newSession.LastQueryTime = time.Now().Unix()
	newSession.ArrivalHorizonShownMinutes = offset + len(arrivals)
	if err := h.SessionStore.SetSMSSession(phoneNumber, &newSession); err != nil {
		// Log error but still send the response
		log.Printf("Failed to set SMS session for %s: %v", analytics.HashPhoneNumber(phoneNumber, h.analyticsHashSalt), err)
	} else {
		*session = newSession
	}

	c.String(http.StatusOK, twiml)
}

// getOrCreateSMSSession gets existing SMS session or creates a new one
func (h *SMSHandler) getOrCreateSMSSession(phoneNumber string) *models.SMSSession {
	session := h.SessionStore.GetSMSSession(phoneNumber)
	if session == nil {
		session = &models.SMSSession{
			Language:      h.LocalizationManager.GetPrimaryLanguage(),
			WindowMinutes: 30, // Default window
			LastQueryTime: time.Now().Unix(),
		}
	}
	return session
}

// handleKeywords processes special keywords like 'more', 'help', 'new'
func (h *SMSHandler) handleKeywords(c *gin.Context, req models.TwilioSMSRequest, session *models.SMSSession) bool {
	message := strings.TrimSpace(strings.ToLower(req.Body))

	switch message {
	case "help":
		h.handleHelpRequest(c, req, session)
		return true
	case "more", "later", "extend":
		h.handleMoreRequest(c, req, session)
		return true
	case "new", "stop", "clear":
		h.handleNewRequest(c, req, session)
		return true
	}

	// Check for language switching keywords
	if h.handleLanguageSwitching(c, req, session, message) {
		return true
	}

	// Check for time-based queries like "+30", "next hour"
	if h.handleTimeQuery(c, req, session, message) {
		return true
	}

	return false
}

// handleHelpRequest provides usage help
func (h *SMSHandler) handleHelpRequest(c *gin.Context, req models.TwilioSMSRequest, session *models.SMSSession) {
	helpMsg := h.LocalizationManager.GetString("sms.help", session.Language)
	twiml, _ := formatters.GenerateTwiMLSMS(helpMsg)
	c.String(http.StatusOK, twiml)
}

// handleMoreRequest extends the departure window
func (h *SMSHandler) handleMoreRequest(c *gin.Context, req models.TwilioSMSRequest, session *models.SMSSession) {
	if session.LastStopID == "" {
		errorMsg := h.LocalizationManager.GetString("sms.session.expired", session.Language)
		twiml, _ := formatters.GenerateTwiMLSMS(errorMsg)
		c.String(http.StatusOK, twiml)
		return
	}

	// Extend the window (30, 60, 90 minutes)
	newWindow := session.WindowMinutes + 30
	if newWindow > 120 {
		newWindow = 120 // Cap at 2 hours
	}

	// Update session atomically
	updatedSession := *session
	updatedSession.WindowMinutes = newWindow
	updatedSession.LastQueryTime = time.Now().Unix()
	if err := h.SessionStore.SetSMSSession(req.From, &updatedSession); err != nil {
		// Log error but continue to send response
		log.Printf("Failed to set SMS session for %s: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), err)
	} else {
		*session = updatedSession
	}

	h.getAndFormatArrivalsWithStopNameAndSession(c, req.From, session.LastStopID, "", session)
}

// handleNewRequest clears session for new stop query
func (h *SMSHandler) handleNewRequest(c *gin.Context, req models.TwilioSMSRequest, session *models.SMSSession) {
	h.SessionStore.ClearSMSSession(req.From)
	h.SessionStore.ClearDisambiguationSession(req.From)
	helpMsg := h.LocalizationManager.GetString("sms.help", session.Language)
	twiml, _ := formatters.GenerateTwiMLSMS(helpMsg)
	c.String(http.StatusOK, twiml)
}

// handleLanguageSwitching processes language change requests
func (h *SMSHandler) handleLanguageSwitching(c *gin.Context, req models.TwilioSMSRequest, session *models.SMSSession, message string) bool {
	languageMap := map[string]string{
		"english":  "en-US",
		"español":  "es-US",
		"spanish":  "es-US",
		"français": "fr-US",
		"french":   "fr-US",
		"deutsch":  "de-US",
		"german":   "de-US",
		"polish":   "pl-PL",
	}

	if newLang, found := languageMap[message]; found && h.LocalizationManager.IsSupported(newLang) {
		session.Language = newLang
		if err := h.SessionStore.SetSMSSession(req.From, session); err != nil {
			log.Printf("Failed to set SMS session for %s: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), err)
		}
		switchedMsg := h.LocalizationManager.GetString("sms.language.switched", newLang)
		twiml, _ := formatters.GenerateTwiMLSMS(switchedMsg)
		c.String(http.StatusOK, twiml)
		return true
	}

	return false
}

// handleTimeQuery processes time-based queries like "+30", "next hour"
func (h *SMSHandler) handleTimeQuery(c *gin.Context, req models.TwilioSMSRequest, session *models.SMSSession, message string) bool {
	if session.LastStopID == "" {
		return false
	}

	// Match patterns like "+30", "+1h", "next hour", "next 2 hours"

	var newWindow int

	if matches := timeRegex.FindStringSubmatch(message); len(matches) > 1 {
		if minutes, err := strconv.Atoi(matches[1]); err == nil && minutes > 0 && minutes <= 120 {
			newWindow = minutes
		}
	} else if matches := hourRegex.FindStringSubmatch(message); len(matches) > 1 {
		if hours, err := strconv.Atoi(matches[1]); err == nil && hours > 0 && hours <= 2 {
			newWindow = hours * 60
		}
	} else if matches := nextHourRegex.FindStringSubmatch(message); len(matches) > 0 {
		hours := 1
		if matches[1] != "" {
			if h, err := strconv.Atoi(matches[1]); err == nil && h > 0 && h <= 2 {
				hours = h
			}
		}
		newWindow = hours * 60
	} else if message == "next hour" {
		newWindow = 60
	}

	if newWindow > 0 {
		// Update session atomically
		updatedSession := *session
		updatedSession.WindowMinutes = newWindow
		updatedSession.ArrivalHorizonShownMinutes = 0 // explicit window change — full slice for that window
		updatedSession.LastQueryTime = time.Now().Unix()
		if err := h.SessionStore.SetSMSSession(req.From, &updatedSession); err != nil {
			// Log error but continue to send response
			log.Printf("Failed to set SMS session for %s: %v", analytics.HashPhoneNumber(req.From, h.analyticsHashSalt), err)
		} else {
			*session = updatedSession
		}
		h.getAndFormatArrivalsWithStopNameAndSession(c, req.From, session.LastStopID, "", session)
		return true
	}

	return false
}

// extractLanguageFromMessage extracts language prefix and stop ID from SMS message
func (h *SMSHandler) extractLanguageFromMessage(message string, session *models.SMSSession) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(message), " ", 2)
	if len(parts) == 2 {
		// Validate language code format
		if err := validation.ValidateLanguageCode(parts[0]); err == nil && h.LocalizationManager.IsSupported(parts[0]) {
			stopID := h.extractAndValidateStopID(parts[1])
			return parts[0], stopID
		}
	}
	// Use session language if available, otherwise primary language
	language := session.Language
	if language == "" {
		language = h.LocalizationManager.GetPrimaryLanguage()
	}
	return language, h.extractAndValidateStopID(message)
}

// extractAndValidateStopID extracts and validates a stop ID from a message
func (h *SMSHandler) extractAndValidateStopID(message string) string {
	// Use the existing extraction logic but add validation
	stopID := formatters.ExtractStopID(message)
	if stopID != "" {
		if err := validation.ValidateStopID(stopID); err != nil {
			log.Printf("Invalid stop ID format: %s, error: %v", stopID, err)
			return ""
		}
	}
	return stopID
}

// getLanguageForUser gets the preferred language for a user
func (h *SMSHandler) getLanguageForUser(phoneNumber string) string {
	session := h.getOrCreateSMSSession(phoneNumber)
	if session.Language != "" {
		return session.Language
	}
	return h.LocalizationManager.GetPrimaryLanguage()
}

// SetAnalyticsManager sets the analytics manager for the SMS handler
func SetAnalyticsManager(handler interface{}, manager middleware.AnalyticsManager, hashSalt string) {
	switch h := handler.(type) {
	case *SMSHandler:
		h.analyticsManager = manager
		h.analyticsHashSalt = hashSalt
	}
}
