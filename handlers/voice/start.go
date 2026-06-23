package voice

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/twilio/twilio-go/twiml"

	"github.com/gin-gonic/gin"
)

func (h *Handler) HandleVoiceStart(c *gin.Context) {
	req, err := h.preprocessRequest(c)

	if err != nil {
		return
	}

	language := h.getLanguageFromRequest(c)

	slog.Info("Received voice call", "from", h.phoneHasher.HashForLogs(req.From), "callSid", req.CallSid)

	h.renderMainMenuWithLanguage(c, language)
}

// renderMainMenuWithLanguage renders the main menu for single language setup
func (h *Handler) renderMainMenuWithLanguage(c *gin.Context, language string) {
	slog.Info("Rendering main menu", "language", language)

	c.Header("Content-Type", "text/xml")

	var innerElts []twiml.Element
	innerElts = append(innerElts, &twiml.VoiceSay{
		Message:  h.LocalizationManager.GetString("voice.welcome", language),
		Language: language,
	})

	if h.LocalizationManager.GetLanguageCount() == 2 {
		langs := h.LocalizationManager.GetSupportedLanguages()
		innerElts = append(innerElts, &twiml.VoiceSay{
			Message:  "Press * for " + langs[1],
			Language: language,
		})
	} else if h.LocalizationManager.GetLanguageCount() > 2 {
		innerElts = append(innerElts, &twiml.VoiceSay{
			Message:  "Press * for other languages",
			Language: language,
		})
	}

	gather := &twiml.VoiceGather{
		Action:        fmt.Sprint("/voice/find_stop?lang=", language),
		Method:        "POST",
		Timeout:       "30",
		FinishOnKey:   "#",
		NumDigits:     "6",
		Language:      language,
		InnerElements: innerElts,
	}

	timeoutSay := &twiml.VoiceSay{
		Message: h.LocalizationManager.GetString("voice.timeout", language),
	}

	if twimlResult, err := twiml.Voice([]twiml.Element{gather, timeoutSay}); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
	} else {
		c.Header("Content-Type", "text/xml")
		c.String(http.StatusOK, twimlResult)
	}
}
