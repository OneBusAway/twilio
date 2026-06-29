package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"oba-twilio/localization"
	"oba-twilio/metrics"
	"oba-twilio/models"
)

type MockOneBusAwayClientSMS struct {
	mock.Mock
}

func (m *MockOneBusAwayClientSMS) GetArrivalsAndDepartures(stopID string) (*models.OneBusAwayResponse, error) {
	args := m.Called(stopID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.OneBusAwayResponse), args.Error(1)
}

func (m *MockOneBusAwayClientSMS) GetArrivalsAndDeparturesWithWindow(stopID string, minutesAfter int) (*models.OneBusAwayResponse, error) {
	args := m.Called(stopID, minutesAfter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.OneBusAwayResponse), args.Error(1)
}

func (m *MockOneBusAwayClientSMS) ProcessArrivals(resp *models.OneBusAwayResponse, maxMinutes int) []models.Arrival {
	args := m.Called(resp, maxMinutes)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).([]models.Arrival)
}

func (m *MockOneBusAwayClientSMS) SearchStops(query string) ([]models.Stop, error) {
	args := m.Called(query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Stop), args.Error(1)
}

func (m *MockOneBusAwayClientSMS) InitializeCoverage() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockOneBusAwayClientSMS) GetCoverageArea() *models.CoverageArea {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*models.CoverageArea)
}

func (m *MockOneBusAwayClientSMS) FindAllMatchingStops(stopID string) ([]models.StopOption, error) {
	args := m.Called(stopID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.StopOption), args.Error(1)
}

func (m *MockOneBusAwayClientSMS) GetStopInfo(fullStopID string) (*models.StopOption, error) {
	args := m.Called(fullStopID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.StopOption), args.Error(1)
}

func setupSMSTestRouter() (*gin.Engine, *MockOneBusAwayClientSMS, *SMSHandler) {
	gin.SetMode(gin.TestMode)

	mockClient := &MockOneBusAwayClientSMS{}
	locManager := createTestManagerWithSpanish()
	smsHandler := NewSMSHandler(mockClient, locManager)

	r := gin.New()
	r.POST("/sms", smsHandler.HandleSMS)

	return r, mockClient, smsHandler
}

func createTestManagerWithSpanish() *localization.LocalizationManager {
	// Create a test manager with both English and Spanish support
	strings := map[string]map[string]string{
		"en-US": {
			"sms.help":                "Send a stop ID (e.g., 75403) to get bus arrivals. Reply 'more' for later buses, 'help' for this message.",
			"sms.no_stops_found":      "Sorry, no stops found with ID %s. Please check and try again.",
			"sms.no_arrivals":         "No upcoming arrivals found for this stop.",
			"sms.service_unavailable": "{brand} service is temporarily unavailable. Please try again.",
			"sms.menu.more_hint":      "Reply 'more' for later buses",
			"sms.menu.help_hint":      "Reply 'help' for usage info",
			"sms.menu.new_hint":       "Reply 'new' for a different stop",
			"sms.error.invalid_stop":  "Please send a valid stop ID (e.g., 75403).",
			"sms.keyword.more":        "Showing buses in the next %d minutes:",
			"sms.keyword.invalid":     "I don't understand '%s'. Send a stop ID or reply 'help' for usage info.",
			"sms.session.expired":     "Session expired. Please send a stop ID to get started.",
			"sms.language.switched":   "Language switched to English",
			"sms.error.search_failed": "Sorry, I couldn't search for stop %s. Please check the stop ID and try again.",
			"sms.more.no_additional":  "No additional buses in this time range. Try 'more' again later or send a new stop ID.",
		},
		"es-US": {
			"sms.help":                "Envíe un ID de parada (ej. 75403) para obtener llegadas. Responda 'more' para autobuses posteriores, 'help' para este mensaje.",
			"sms.no_stops_found":      "Lo siento, no se encontraron paradas con ID %s. Por favor verifique e inténtelo de nuevo.",
			"sms.no_arrivals":         "No se encontraron próximas llegadas para esta parada.",
			"sms.service_unavailable": "El servicio {brand} no está disponible temporalmente. Por favor inténtelo de nuevo.",
			"sms.menu.more_hint":      "Responda 'more' para autobuses posteriores",
			"sms.menu.help_hint":      "Responda 'help' para información de uso",
			"sms.menu.new_hint":       "Responda 'new' para una parada diferente",
			"sms.error.invalid_stop":  "Por favor envíe un ID de parada válido (ej. 75403).",
			"sms.keyword.more":        "Mostrando autobuses en los próximos %d minutos:",
			"sms.keyword.invalid":     "No entiendo '%s'. Envíe un ID de parada o responda 'help' para información de uso.",
			"sms.session.expired":     "Sesión expirada. Por favor envíe un ID de parada para comenzar.",
			"sms.language.switched":   "Idioma cambiado a Español",
			"sms.error.search_failed": "Lo siento, no pude buscar la parada %s. Por favor verifique el ID de la parada e inténtelo de nuevo.",
			"sms.more.no_additional":  "No hay más autobuses en este rango. Pruebe 'more' más tarde o envíe un nuevo ID de parada.",
		},
	}

	return localization.NewTestManagerWithStrings(strings, []string{"en-US", "es-US"})
}

func createMockResponse(stopID string) *models.OneBusAwayResponse {
	return &models.OneBusAwayResponse{
		Data: struct {
			Entry struct {
				ArrivalsAndDepartures []struct {
					RouteShortName       string `json:"routeShortName"`
					TripHeadsign         string `json:"tripHeadsign"`
					PredictedArrivalTime int64  `json:"predictedArrivalTime"`
					ScheduledArrivalTime int64  `json:"scheduledArrivalTime"`
					Status               string `json:"status"`
				} `json:"arrivalsAndDepartures"`
				StopId string `json:"stopId"`
			} `json:"entry"`
		}{
			Entry: struct {
				ArrivalsAndDepartures []struct {
					RouteShortName       string `json:"routeShortName"`
					TripHeadsign         string `json:"tripHeadsign"`
					PredictedArrivalTime int64  `json:"predictedArrivalTime"`
					ScheduledArrivalTime int64  `json:"scheduledArrivalTime"`
					Status               string `json:"status"`
				} `json:"arrivalsAndDepartures"`
				StopId string `json:"stopId"`
			}{
				StopId: stopID,
			},
		},
		Code: 200,
	}
}

func createMockArrivals() []models.Arrival {
	return []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 5,
		},
		{
			RouteShortName:      "10",
			TripHeadsign:        "Capitol Hill",
			MinutesUntilArrival: 12,
		},
	}
}

// Arrivals in the "more" slice (beyond the first 30-minute horizon shown initially).
func createMockArrivalsLaterChunk() []models.Arrival {
	return []models.Arrival{
		{
			RouteShortName:      "99",
			TripHeadsign:        "Later Terminal",
			MinutesUntilArrival: 42,
		},
	}
}

func sendSMSRequest(r *gin.Engine, from, body string) *httptest.ResponseRecorder {
	form := url.Values{}
	form.Set("From", from)
	form.Set("Body", body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)
	return w
}

// Basic SMS Handler Tests

func TestSMSHandler_BasicStopQuery(t *testing.T) {
	r, mockClient, _ := setupSMSTestRouter()

	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_75403",
			AgencyName:  "King County Metro",
			StopName:    "Pine St & 3rd Ave",
			DisplayText: "King County Metro: Pine St & 3rd Ave",
		},
	}

	mockResponse := createMockResponse("1_75403")
	mockArrivals := createMockArrivals()

	mockClient.On("FindAllMatchingStops", "75403").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	w := sendSMSRequest(r, "+12345678901", "75403")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Stop: Pine St &amp; 3rd Ave")
	assert.Contains(t, body, "Route 8")
	assert.Contains(t, body, "Downtown")
	assert.Contains(t, body, "Reply &#39;more&#39; for later buses")
	assert.Contains(t, body, "Reply &#39;help&#39; for usage info")
	mockClient.AssertExpectations(t)
}

func TestSMSHandler_NoStopsFound(t *testing.T) {
	r, mockClient, _ := setupSMSTestRouter()

	mockClient.On("FindAllMatchingStops", "99999").Return([]models.StopOption{}, nil)

	w := sendSMSRequest(r, "+12345678901", "99999")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Sorry, no stops found with ID 99999")
	mockClient.AssertExpectations(t)
}

func TestSMSHandler_EmptyBody(t *testing.T) {
	r, _, _ := setupSMSTestRouter()

	w := sendSMSRequest(r, "+12345678901", "")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Please send a valid stop ID")
}

func TestSMSHandler_InvalidStopID(t *testing.T) {
	r, _, _ := setupSMSTestRouter()

	// Use a string that fails ValidateStopID (invalid character '@')
	w := sendSMSRequest(r, "+12345678901", "abc@123")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Please send a valid stop ID")
}

// Keyword Processing Tests

func TestSMSHandler_HelpKeyword(t *testing.T) {
	r, _, _ := setupSMSTestRouter()

	w := sendSMSRequest(r, "+12345678901", "help")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Send a stop ID")
	assert.Contains(t, body, "Reply &#39;more&#39; for later buses")
}

func TestSMSHandler_HelpKeywordCaseInsensitive(t *testing.T) {
	r, _, _ := setupSMSTestRouter()

	testCases := []string{"HELP", "Help", "hElP"}

	for _, helpText := range testCases {
		w := sendSMSRequest(r, "+12345678901", helpText)
		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Send a stop ID", "Failed for: %s", helpText)
	}
}

func TestSMSHandler_MoreKeyword_NoSession(t *testing.T) {
	r, _, _ := setupSMSTestRouter()

	w := sendSMSRequest(r, "+12345678901", "more")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Session expired")
	assert.Contains(t, body, "Please send a stop ID")
}

func TestSMSHandler_MoreKeyword_WithSession(t *testing.T) {
	r, mockClient, smsHandler := setupSMSTestRouter()

	// First, create a session by querying a stop
	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_75403",
			AgencyName:  "King County Metro",
			StopName:    "Pine St & 3rd Ave",
			DisplayText: "King County Metro: Pine St & 3rd Ave",
		},
	}

	mockResponse := createMockResponse("1_75403")
	mockArrivals := createMockArrivals()

	// Initial query
	mockClient.On("FindAllMatchingStops", "75403").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	// "more" query with extended window
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 60).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 60).Return(append(createMockArrivals(), createMockArrivalsLaterChunk()...))
	// "more" flow calls GetStopInfo to obtain the stop name header
	mockClient.On("GetStopInfo", "1_75403").Return(&models.StopOption{FullStopID: "1_75403", StopName: "Pine St & 3rd Ave"}, nil)

	// First request to establish session
	w1 := sendSMSRequest(r, "+12345678901", "75403")
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second request for "more"
	w2 := sendSMSRequest(r, "+12345678901", "more")
	assert.Equal(t, http.StatusOK, w2.Code)
	body := w2.Body.String()
	assert.Contains(t, body, "Route 99")
	assert.Contains(t, body, "Later Terminal")
	assert.Contains(t, body, "Reply &#39;more&#39; for later buses")

	// Verify session was updated
	session := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, session)
	assert.Equal(t, 60, session.WindowMinutes)

	mockClient.AssertExpectations(t)
}

func TestSMSHandler_NewKeyword(t *testing.T) {
	r, mockClient, smsHandler := setupSMSTestRouter()

	// First, create a session
	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_75403",
			AgencyName:  "King County Metro",
			StopName:    "Pine St & 3rd Ave",
			DisplayText: "King County Metro: Pine St & 3rd Ave",
		},
	}

	mockResponse := createMockResponse("1_75403")
	mockArrivals := createMockArrivals()

	mockClient.On("FindAllMatchingStops", "75403").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	// First request to establish session
	w1 := sendSMSRequest(r, "+12345678901", "75403")
	assert.Equal(t, http.StatusOK, w1.Code)

	// Verify session exists
	session := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, session)

	// Send "new" command
	w2 := sendSMSRequest(r, "+12345678901", "new")
	assert.Equal(t, http.StatusOK, w2.Code)
	body := w2.Body.String()
	assert.Contains(t, body, "Send a stop ID")

	// Verify session was cleared
	session = smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.Nil(t, session)

	mockClient.AssertExpectations(t)
}

func TestSMSHandler_InvalidKeyword(t *testing.T) {
	r, mockClient, _ := setupSMSTestRouter()

	// With relaxed stop ID parsing, an unrecognized keyword falls back to stop lookup.
	// Mock the lookup to return no stops.
	mockClient.On("FindAllMatchingStops", "invalid").Return([]models.StopOption{}, nil)

	w := sendSMSRequest(r, "+12345678901", "invalid")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Sorry, no stops found with ID invalid")
	mockClient.AssertExpectations(t)
}

// Time Parsing Tests

func TestSMSHandler_TimeQuery_PlusMinutes(t *testing.T) {
	r, mockClient, smsHandler := setupSMSTestRouter()

	// Set up session directly
	session := &models.SMSSession{
		LastStopID:    "1_75403",
		Language:      "en-US",
		WindowMinutes: 30,
		LastQueryTime: time.Now().Unix(),
	}
	err := smsHandler.SessionStore.SetSMSSession("+12345678901", session)
	assert.NoError(t, err)

	// Test "+30" format (uses default method since window is 30)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(createMockResponse("1_75403"), nil)
	mockClient.On("ProcessArrivals", mock.Anything, mock.Anything).Return(createMockArrivals())
	mockClient.On("GetStopInfo", "1_75403").Return(&models.StopOption{FullStopID: "1_75403", StopName: "Pine St & 3rd Ave"}, nil)

	w := sendSMSRequest(r, "+12345678901", "+30")
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify session window was updated
	updatedSession := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, updatedSession)
	assert.Equal(t, 30, updatedSession.WindowMinutes)

	mockClient.AssertExpectations(t)
}

func TestSMSHandler_TimeQuery_PlusHours(t *testing.T) {
	r, mockClient, smsHandler := setupSMSTestRouter()

	// Set up session directly
	session := &models.SMSSession{
		LastStopID:    "1_75403",
		Language:      "en-US",
		WindowMinutes: 30,
		LastQueryTime: time.Now().Unix(),
	}
	err := smsHandler.SessionStore.SetSMSSession("+12345678901", session)
	assert.NoError(t, err)

	// Test "+1h" format
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 60).Return(createMockResponse("1_75403"), nil)
	mockClient.On("ProcessArrivals", mock.Anything, mock.Anything).Return(createMockArrivals())
	mockClient.On("GetStopInfo", "1_75403").Return(&models.StopOption{FullStopID: "1_75403", StopName: "Pine St & 3rd Ave"}, nil)

	w := sendSMSRequest(r, "+12345678901", "+1h")
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify session window was updated
	updatedSession := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, updatedSession)
	assert.Equal(t, 60, updatedSession.WindowMinutes)

	mockClient.AssertExpectations(t)
}

func TestSMSHandler_TimeQuery_NextHour(t *testing.T) {
	r, mockClient, smsHandler := setupSMSTestRouter()

	// Set up session directly
	session := &models.SMSSession{
		LastStopID:    "1_75403",
		Language:      "en-US",
		WindowMinutes: 30,
		LastQueryTime: time.Now().Unix(),
	}
	err := smsHandler.SessionStore.SetSMSSession("+12345678901", session)
	assert.NoError(t, err)

	// Test "next hour" format
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 60).Return(createMockResponse("1_75403"), nil)
	mockClient.On("ProcessArrivals", mock.Anything, mock.Anything).Return(createMockArrivals())
	mockClient.On("GetStopInfo", "1_75403").Return(&models.StopOption{FullStopID: "1_75403", StopName: "Pine St & 3rd Ave"}, nil)

	w := sendSMSRequest(r, "+12345678901", "next hour")
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify session window was updated
	updatedSession := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, updatedSession)
	assert.Equal(t, 60, updatedSession.WindowMinutes)

	mockClient.AssertExpectations(t)
}

func TestSMSHandler_TimeQuery_NextTwoHours(t *testing.T) {
	r, mockClient, smsHandler := setupSMSTestRouter()

	// Set up session directly
	session := &models.SMSSession{
		LastStopID:    "1_75403",
		Language:      "en-US",
		WindowMinutes: 30,
		LastQueryTime: time.Now().Unix(),
	}
	err := smsHandler.SessionStore.SetSMSSession("+12345678901", session)
	assert.NoError(t, err)

	// Test "next 2 hours" format
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 120).Return(createMockResponse("1_75403"), nil)
	mockClient.On("ProcessArrivals", mock.Anything, mock.Anything).Return(createMockArrivals())
	mockClient.On("GetStopInfo", "1_75403").Return(&models.StopOption{FullStopID: "1_75403", StopName: "Pine St & 3rd Ave"}, nil)

	w := sendSMSRequest(r, "+12345678901", "next 2 hours")
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify session window was updated
	updatedSession := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, updatedSession)
	assert.Equal(t, 120, updatedSession.WindowMinutes)

	mockClient.AssertExpectations(t)
}

func TestSMSHandler_TimeQuery_InvalidTime(t *testing.T) {
	r, _, smsHandler := setupSMSTestRouter()

	// Set up session directly
	session := &models.SMSSession{
		LastStopID:    "1_75403",
		Language:      "en-US",
		WindowMinutes: 30,
		LastQueryTime: time.Now().Unix(),
	}
	err := smsHandler.SessionStore.SetSMSSession("+12345678901", session)
	assert.NoError(t, err)

	// Test invalid time queries - these should be treated as invalid stop IDs
	invalidQueries := []string{"+999", "+5h", "+0"}

	for _, query := range invalidQueries {
		w := sendSMSRequest(r, "+12345678901", query)
		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Please send a valid stop ID", "Failed for query: %s", query)
	}
}

func TestSMSHandler_TimeQuery_NoSession(t *testing.T) {
	r, _, _ := setupSMSTestRouter()

	w := sendSMSRequest(r, "+12345678901", "+30")
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Please send a valid stop ID")
}

// Language Tests

func TestSMSHandler_LanguageSwitch_Spanish(t *testing.T) {
	r, _, smsHandler := setupSMSTestRouter()

	w := sendSMSRequest(r, "+12345678901", "español")
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Idioma cambiado a Español")

	// Verify session language was updated
	session := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, session)
	assert.Equal(t, "es-US", session.Language)
}

func TestSMSHandler_LanguageSwitch_English(t *testing.T) {
	r, _, smsHandler := setupSMSTestRouter()

	// First switch to Spanish
	sendSMSRequest(r, "+12345678901", "español")

	// Then switch back to English
	w := sendSMSRequest(r, "+12345678901", "english")
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Language switched to English")

	// Verify session language was updated
	session := smsHandler.SessionStore.GetSMSSession("+12345678901")
	assert.NotNil(t, session)
	assert.Equal(t, "en-US", session.Language)
}

// Session Management Tests

func TestSMSHandler_SessionTimeout(t *testing.T) {
	r, _, smsHandler := setupSMSTestRouter()

	// Create a session with expired CreatedAt
	session := &models.SMSSession{
		LastStopID:    "1_75403",
		WindowMinutes: 30,
		Language:      "en-US",
		LastQueryTime: time.Now().Unix(),
		CreatedAt:     time.Now().Unix() - 16*60, // 16 minutes ago (expired)
	}
	// Manually insert expired session using helper method
	smsHandler.SessionStore.SetExpiredSMSSession("+12345678901", session)

	// Try to use "more" command with expired session
	w := sendSMSRequest(r, "+12345678901", "more")
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Session expired")
}

func TestSMSHandler_WindowProgression(t *testing.T) {
	r, mockClient, smsHandler := setupSMSTestRouter()

	// Set up session first
	setupSessionForTimeTests(r, mockClient, smsHandler)

	// Test progression: 30 -> 60 -> 90 -> 120 -> 120 (capped)
	expectedWindows := []int{60, 90, 120, 120}

	for i, expectedWindow := range expectedWindows {
		mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", expectedWindow).Return(createMockResponse("1_75403"), nil)
		mockClient.On("ProcessArrivals", mock.Anything, mock.Anything).Return(createMockArrivals())

		w := sendSMSRequest(r, "+12345678901", "more")
		assert.Equal(t, http.StatusOK, w.Code)

		session := smsHandler.SessionStore.GetSMSSession("+12345678901")
		assert.NotNil(t, session)
		assert.Equal(t, expectedWindow, session.WindowMinutes, "Iteration %d", i)
	}

	mockClient.AssertExpectations(t)
}

// Helper function to set up a session for time-based tests
func setupSessionForTimeTests(r *gin.Engine, mockClient *MockOneBusAwayClientSMS, smsHandler *SMSHandler) {
	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_75403",
			AgencyName:  "King County Metro",
			StopName:    "Test Stop",
			DisplayText: "King County Metro: Test Stop",
		},
	}

	mockResponse := createMockResponse("1_75403")
	mockArrivals := createMockArrivals()

	mockClient.On("FindAllMatchingStops", "75403").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)
	// "more" flow (and time-window progression) calls GetStopInfo to render the stop name header.
	mockClient.On("GetStopInfo", "1_75403").Return(&models.StopOption{
		FullStopID: "1_75403",
		StopName:   "Test Stop",
	}, nil)

	// Create initial session
	sendSMSRequest(r, "+12345678901", "75403")
}

func TestSMSHandlerRecordsResolvedInteraction(t *testing.T) {
	router, mockClient, h := setupSMSTestRouter()
	m := metrics.New()
	h.SetMetrics(m)

	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_75403",
			AgencyName:  "King County Metro",
			StopName:    "Pine St & 3rd Ave",
			DisplayText: "King County Metro: Pine St & 3rd Ave",
		},
	}

	mockResponse := createMockResponse("1_75403")
	mockArrivals := createMockArrivals()

	mockClient.On("FindAllMatchingStops", "75403").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	w := sendSMSRequest(router, "+12345678901", "75403")
	assert.Equal(t, http.StatusOK, w.Code)

	// Scrape and assert the interaction counter.
	mr := gin.New()
	mr.GET("/metrics", m.Handler())
	w2 := httptest.NewRecorder()
	mr.ServeHTTP(w2, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w2.Body.String(), `interactions_total{channel="sms",outcome="resolved"} 1`) {
		t.Errorf("expected resolved interaction:\n%s", w2.Body.String())
	}
}

func TestSMSHandlerRecordsResolvedOnDisambiguationChoice(t *testing.T) {
	_, mockClient, h := setupSMSTestRouter()
	m := metrics.New()
	h.SetMetrics(m)

	stopOptions := []models.StopOption{
		{
			FullStopID:  "1_12345",
			AgencyName:  "King County Metro",
			StopName:    "Pine St & 3rd Ave",
			DisplayText: "King County Metro: Pine St & 3rd Ave",
		},
		{
			FullStopID:  "40_12345",
			AgencyName:  "Sound Transit",
			StopName:    "University Street Station",
			DisplayText: "Sound Transit: University Street Station",
		},
	}
	session := &models.DisambiguationSession{StopOptions: stopOptions}
	err := h.SessionStore.SetDisambiguationSession("+12345678901", session)
	assert.NoError(t, err)

	mockResponse := createMockResponse("1_12345")
	mockArrivals := createMockArrivals()
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	// Build a fresh router attached to our metrics-instrumented handler.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/sms", h.HandleSMS)

	w := sendSMSRequest(router, "+12345678901", "1")
	assert.Equal(t, http.StatusOK, w.Code)

	// Scrape and assert the interaction counter.
	mr := gin.New()
	mr.GET("/metrics", m.Handler())
	w2 := httptest.NewRecorder()
	mr.ServeHTTP(w2, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w2.Body.String(), `interactions_total{channel="sms",outcome="resolved"} 1`) {
		t.Errorf("expected resolved interaction after disambiguation choice:\n%s", w2.Body.String())
	}
	mockClient.AssertExpectations(t)
}
