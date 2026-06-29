package voice

import (
	"oba-twilio/metrics"
	"oba-twilio/models"
	"oba-twilio/validation"

	"github.com/gin-gonic/gin"

	"oba-twilio/client"
	"oba-twilio/handlers/common"
	"oba-twilio/localization"
	"oba-twilio/middleware"
)

type Handler struct {
	OBAClient           client.OneBusAwayClientInterface
	SessionStore        *common.SessionStore
	LocalizationManager *localization.LocalizationManager
	ErrorHandler        *common.ErrorHandler
	arrivalFilterConfig common.ArrivalFilterConfig
	analyticsManager    middleware.AnalyticsManager
	analyticsHashSalt   string
	metrics             *metrics.Metrics
}

func NewHandler(obaClient client.OneBusAwayClientInterface, locManager *localization.LocalizationManager) *Handler {
	return &Handler{
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

func (h *Handler) Close() {
	if h.SessionStore != nil {
		h.SessionStore.Close()
	}
}

func (h *Handler) SetAnalytics(analyticsManager middleware.AnalyticsManager, hashSalt string) {
	h.analyticsManager = analyticsManager
	h.analyticsHashSalt = hashSalt
}

func (h *Handler) SetMetrics(m *metrics.Metrics) { h.metrics = m }

func (h *Handler) SetArrivalFilterConfig(cfg common.ArrivalFilterConfig) {
	h.arrivalFilterConfig = cfg
}

// getLanguageFromRequest extracts language from URL parameter or defaults to primary language
func (h *Handler) getLanguageFromRequest(c *gin.Context) string {
	language := c.Query("lang")
	if language != "" && h.LocalizationManager.IsSupported(language) {
		return language
	}
	return h.LocalizationManager.GetPrimaryLanguage()
}

func (h *Handler) preprocessRequest(c *gin.Context) (*models.TwilioVoiceRequest, error) {
	language := h.getLanguageFromRequest(c)

	var req models.TwilioVoiceRequest

	if err := c.ShouldBind(&req); err != nil {
		h.ErrorHandler.HandleValidationError(c, err, "voice", language)
		return nil, err
	}

	// Validate phone number
	if err := validation.ValidatePhoneNumber(req.From); err != nil {
		h.ErrorHandler.HandleValidationError(c, err, "voice", language)
		return nil, err
	}

	// Track voice request
	if h.analyticsManager != nil {
		middleware.TrackVoiceRequest(c.Request.Context(), h.analyticsManager, req.From, language, h.analyticsHashSalt)
	}

	return &req, nil
}
