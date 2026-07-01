package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"oba-twilio/handlers/common"
	"oba-twilio/localization"
	"oba-twilio/models"
)

type MockOneBusAwayClientVoiceMenu struct {
	mock.Mock
}

func (m *MockOneBusAwayClientVoiceMenu) GetArrivalsAndDepartures(stopID string) (*models.OneBusAwayResponse, error) {
	args := m.Called(stopID)
	resp := args.Get(0)
	if resp == nil {
		return nil, args.Error(1)
	}
	if response, ok := resp.(*models.OneBusAwayResponse); ok {
		return response, args.Error(1)
	}
	return nil, fmt.Errorf("mock returned invalid type for GetArrivalsAndDepartures")
}

func (m *MockOneBusAwayClientVoiceMenu) GetArrivalsAndDeparturesWithWindow(stopID string, minutesAfter int) (*models.OneBusAwayResponse, error) {
	args := m.Called(stopID, minutesAfter)
	resp := args.Get(0)
	if resp == nil {
		return nil, args.Error(1)
	}
	if response, ok := resp.(*models.OneBusAwayResponse); ok {
		return response, args.Error(1)
	}
	return nil, fmt.Errorf("mock returned invalid type for GetArrivalsAndDeparturesWithWindow")
}

func (m *MockOneBusAwayClientVoiceMenu) ProcessArrivals(resp *models.OneBusAwayResponse, maxMinutes int) []models.Arrival {
	args := m.Called(resp, maxMinutes)
	result := args.Get(0)
	if result == nil {
		return nil
	}
	if arrivals, ok := result.([]models.Arrival); ok {
		return arrivals
	}
	return nil
}

func (m *MockOneBusAwayClientVoiceMenu) SearchStops(query string) ([]models.Stop, error) {
	args := m.Called(query)
	result := args.Get(0)
	if result == nil {
		return nil, args.Error(1)
	}
	if stops, ok := result.([]models.Stop); ok {
		return stops, args.Error(1)
	}
	return nil, fmt.Errorf("mock returned invalid type for SearchStops")
}

func (m *MockOneBusAwayClientVoiceMenu) InitializeCoverage() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockOneBusAwayClientVoiceMenu) GetCoverageArea() *models.CoverageArea {
	args := m.Called()
	result := args.Get(0)
	if result == nil {
		return nil
	}
	if coverage, ok := result.(*models.CoverageArea); ok {
		return coverage
	}
	return nil
}

func (m *MockOneBusAwayClientVoiceMenu) FindAllMatchingStops(stopID string) ([]models.StopOption, error) {
	args := m.Called(stopID)
	result := args.Get(0)
	if result == nil {
		return nil, args.Error(1)
	}
	if stops, ok := result.([]models.StopOption); ok {
		return stops, args.Error(1)
	}
	return nil, fmt.Errorf("mock returned invalid type for FindAllMatchingStops")
}

func (m *MockOneBusAwayClientVoiceMenu) GetStopInfo(fullStopID string) (*models.StopOption, error) {
	args := m.Called(fullStopID)
	result := args.Get(0)
	if result == nil {
		return nil, args.Error(1)
	}
	if stopOption, ok := result.(*models.StopOption); ok {
		return stopOption, args.Error(1)
	}
	return nil, fmt.Errorf("mock returned invalid type for GetStopInfo")
}

func setupVoiceMenuTestRouter() (*gin.Engine, *MockOneBusAwayClientVoiceMenu, *VoiceHandler) {
	gin.SetMode(gin.TestMode)

	mockClient := &MockOneBusAwayClientVoiceMenu{}
	locManager := localization.NewTestManager()
	voiceHandler := NewVoiceHandler(mockClient, locManager)

	r := gin.New()
	r.POST("/voice", voiceHandler.HandleVoiceStart)
	r.POST("/voice/find_stop", voiceHandler.HandleFindStop)
	r.POST("/voice/menu_action", voiceHandler.HandleVoiceMenuAction)

	return r, mockClient, voiceHandler
}

func TestVoiceHandler_FindStopWithMenuOptions(t *testing.T) {
	r, mockClient, _ := setupVoiceMenuTestRouter()

	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_12345",
			AgencyName:  "King County Metro",
			StopName:    "Pine St & 3rd Ave",
			DisplayText: "King County Metro: Pine St & 3rd Ave",
		},
	}

	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "1_12345",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 5,
		},
	}

	mockClient.On("FindAllMatchingStops", "12345").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "1_12345").Return(&mockStopOptions[0], nil)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "12345")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Should contain the arrival message
	assert.Contains(t, body, "Route 8")

	// Should contain menu options
	assert.Contains(t, body, "<Gather")
	assert.Contains(t, body, "To hear more departures")
	assert.Contains(t, body, "press 1")
	assert.Contains(t, body, "To go back to the main menu")
	assert.Contains(t, body, "press 2")
	assert.Contains(t, body, "/voice/menu_action?minutesAfter=60")
	assert.Contains(t, body, "lang=en-US")

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_SingleDigitWithoutDisambiguationSession_TreatedAsStopID(t *testing.T) {
	r, mockClient, _ := setupVoiceMenuTestRouter()

	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_1",
			AgencyName:  "King County Metro",
			StopName:    "Test Stop 1",
			DisplayText: "King County Metro: Test Stop 1",
		},
	}

	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "1_1",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 5,
		},
	}

	mockClient.On("FindAllMatchingStops", "1").Return(mockStopOptions, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_1", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "1_1").Return(&mockStopOptions[0], nil)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Route 8")
	assert.NotContains(t, body, "No active selection")

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_FindStop_MultipleStops_PromptsDisambiguation(t *testing.T) {
	r, mockClient, _ := setupVoiceMenuTestRouter()

	twoStops := []models.StopOption{
		{FullStopID: "1_999", AgencyName: "Metro", StopName: "Pine Street", DisplayText: "Metro: Pine Street"},
		{FullStopID: "40_999", AgencyName: "Sound", StopName: "Oak Avenue", DisplayText: "Sound: Oak Avenue"},
	}

	mockClient.On("FindAllMatchingStops", "999").Return(twoStops, nil)

	form := url.Values{}
	form.Set("From", "+16667778888")
	form.Set("Digits", "999")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<Gather")
	assert.Contains(t, body, "action=\"/voice/find_stop?lang=en-US\"")
	assert.Contains(t, body, "Press 1 for Pine Street")
	assert.Contains(t, body, "Press 2 for Oak Avenue")
	assert.Contains(t, body, "Which stop would you like?")

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_FindStop_DisambiguationChoiceLoadsArrivals(t *testing.T) {
	r, mockClient, _ := setupVoiceMenuTestRouter()

	twoStops := []models.StopOption{
		{FullStopID: "1_999", AgencyName: "Metro", StopName: "Pine Street", DisplayText: "Metro: Pine Street"},
		{FullStopID: "40_999", AgencyName: "Sound", StopName: "Oak Avenue", DisplayText: "Sound: Oak Avenue"},
	}

	mockResponseSecond := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "40_999",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{RouteShortName: "99", TripHeadsign: "South", MinutesUntilArrival: 4},
	}

	mockClient.On("FindAllMatchingStops", "999").Return(twoStops, nil).Once()
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "40_999", 30).Return(mockResponseSecond, nil).Once()
	mockClient.On("ProcessArrivals", mockResponseSecond, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "40_999").Return(&twoStops[1], nil)

	phone := "+16667778899"

	form1 := url.Values{}
	form1.Set("From", phone)
	form1.Set("Digits", "999")
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form1.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)
	assert.Contains(t, w1.Body.String(), "<Gather")

	form2 := url.Values{}
	form2.Set("From", phone)
	form2.Set("Digits", "2")
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	body2 := w2.Body.String()
	assert.Contains(t, body2, "Route 99")
	assert.Contains(t, body2, "<Gather")
	assert.Contains(t, body2, "/voice/menu_action?minutesAfter=60")

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_FindStop_DisambiguationInvalidChoice(t *testing.T) {
	r, mockClient, _ := setupVoiceMenuTestRouter()

	twoStops := []models.StopOption{
		{FullStopID: "1_999", StopName: "Pine Street"},
		{FullStopID: "40_999", StopName: "Oak Avenue"},
	}

	mockClient.On("FindAllMatchingStops", "999").Return(twoStops, nil).Once()

	phone := "+16667779900"

	form1 := url.Values{}
	form1.Set("From", phone)
	form1.Set("Digits", "999")
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form1.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)

	form2 := url.Values{}
	form2.Set("From", phone)
	form2.Set("Digits", "9")
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), "Please press a number between 1 and 2")

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_FindStop_EmptyDigits(t *testing.T) {
	r, mockClient, _ := setupVoiceMenuTestRouter()

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "receive any digits")

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_MenuActionExtendDepartures(t *testing.T) {
	r, mockClient, voiceHandler := setupVoiceMenuTestRouter()

	// Set up voice session
	session := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 0,
		CreatedAt:    time.Now().Unix(),
	}
	err := voiceHandler.SessionStore.SetVoiceSession("+14444444444", session)
	assert.NoError(t, err)

	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "1_12345",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 35,
		},
		{
			RouteShortName:      "45",
			TripHeadsign:        "Fremont",
			MinutesUntilArrival: 45,
		},
	}

	// Should call with extended window (30 minutes)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "1_12345").Return(&models.StopOption{
		FullStopID: "1_12345",
		StopName:   "Test Stop",
		AgencyName: "Test Agency",
	}, nil)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "1") // Choice 1: extend departures

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/menu_action?minutesAfter=30", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Should contain extended arrivals
	assert.Contains(t, body, "Route 8")
	assert.Contains(t, body, "Route 45")

	// Should still contain menu options for further extension
	assert.Contains(t, body, "<Gather")
	assert.Contains(t, body, "To hear more departures")

	// Verify session was updated
	updatedSession := voiceHandler.SessionStore.GetVoiceSession("+14444444444")
	assert.NotNil(t, updatedSession)
	assert.Equal(t, 30, updatedSession.MinutesAfter)

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_MenuActionReturnToMainMenu(t *testing.T) {
	r, _, voiceHandler := setupVoiceMenuTestRouter()

	// Set up voice session
	session := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 30,
		CreatedAt:    time.Now().Unix(),
	}
	err := voiceHandler.SessionStore.SetVoiceSession("+14444444444", session)
	assert.NoError(t, err)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "2") // Choice 2: return to main menu

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/menu_action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Should return to start menu
	assert.Contains(t, body, "Welcome to OneBusAway")
	assert.Contains(t, body, "enter your stop ID")
	assert.Contains(t, body, "action=\"/voice/find_stop?lang=en-US\"")

	// Verify session was cleared
	clearedSession := voiceHandler.SessionStore.GetVoiceSession("+14444444444")
	assert.Nil(t, clearedSession)
}

func TestVoiceHandler_MenuActionInvalidChoice(t *testing.T) {
	r, _, voiceHandler := setupVoiceMenuTestRouter()

	// Set up voice session
	session := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 0,
		CreatedAt:    time.Now().Unix(),
	}
	err := voiceHandler.SessionStore.SetVoiceSession("+14444444444", session)
	assert.NoError(t, err)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "3") // Invalid choice

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/menu_action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Should contain error message
	assert.Contains(t, body, "Please press a number between 1 and 2")
}

func TestVoiceHandler_MenuActionNoSession(t *testing.T) {
	r, _, _ := setupVoiceMenuTestRouter()

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/menu_action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Should return to main menu when no session exists
	assert.Contains(t, body, "Welcome to OneBusAway")
	assert.Contains(t, body, "enter your stop ID")
}

func TestVoiceHandler_ExtendedDeparturesMultipleTimes(t *testing.T) {
	r, mockClient, voiceHandler := setupVoiceMenuTestRouter()

	// Set up voice session with already extended window
	session := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 60, // Already extended twice (30 + 30)
		CreatedAt:    time.Now().Unix(),
	}
	err := voiceHandler.SessionStore.SetVoiceSession("+14444444444", session)
	assert.NoError(t, err)

	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "1_12345",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 75,
		},
	}

	// Should call with further extended window (60 + 30 = 90 minutes)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 90).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "1_12345").Return(&models.StopOption{
		FullStopID: "1_12345",
		StopName:   "Test Stop",
		AgencyName: "Test Agency",
	}, nil)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "1") // Choice 1: extend departures again

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/menu_action?minutesAfter=90", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify session was updated to 90 minutes
	updatedSession := voiceHandler.SessionStore.GetVoiceSession("+14444444444")
	assert.NotNil(t, updatedSession)
	assert.Equal(t, 90, updatedSession.MinutesAfter)

	mockClient.AssertExpectations(t)
}

func TestSessionStore_VoiceSessionManagement(t *testing.T) {
	store := common.NewSessionStore()
	defer store.Close()

	session := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 30,
		CreatedAt:    time.Now().Unix(),
	}

	err := store.SetVoiceSession("+14444444444", session)
	assert.NoError(t, err)

	retrieved := store.GetVoiceSession("+14444444444")
	assert.NotNil(t, retrieved)
	assert.Equal(t, "1_12345", retrieved.StopID)
	assert.Equal(t, 30, retrieved.MinutesAfter)

	store.ClearVoiceSession("+14444444444")
	cleared := store.GetVoiceSession("+14444444444")
	assert.Nil(t, cleared)
}

func TestSessionStore_VoiceSessionTimeout(t *testing.T) {
	store := common.NewSessionStore()
	defer store.Close()

	session := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 30,
		CreatedAt:    time.Now().Unix() - (common.SessionTimeoutMinutes+1)*60, // Expired
	}

	err := store.SetVoiceSession("+14444444444", session)
	assert.NoError(t, err)

	// Manually expire the session using the helper method
	store.ExpireSession("+14444444444")

	// Getting expired session should return nil and clean it up
	retrieved := store.GetVoiceSession("+14444444444")
	assert.Nil(t, retrieved)
}

func TestVoiceHandler_MenuActionWithQueryParameter(t *testing.T) {
	mockClient := &MockOneBusAwayClientVoiceMenu{}
	locManager := localization.NewTestManager()
	voiceHandler := NewVoiceHandler(mockClient, locManager)
	defer voiceHandler.Close()

	// Set up initial session
	initialSession := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 30,
		CreatedAt:    time.Now().Unix(),
	}
	err := voiceHandler.SessionStore.SetVoiceSession("+14444444444", initialSession)
	require.NoError(t, err)

	// Create a simple mock response using the existing structure
	mockResponse := &models.OneBusAwayResponse{}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 5,
		},
	}

	// Should use the query parameter value (90) instead of session value + 30
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 90).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "1_12345").Return(&models.StopOption{
		FullStopID: "1_12345",
		StopName:   "Test Stop",
		AgencyName: "Test Agency",
	}, nil)

	r := gin.New()
	r.POST("/voice/menu_action", voiceHandler.HandleVoiceMenuAction)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "1") // Choice 1: extend departures

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/menu_action?minutesAfter=90", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify session was updated to the query parameter value (90)
	updatedSession := voiceHandler.SessionStore.GetVoiceSession("+14444444444")
	assert.NotNil(t, updatedSession)
	assert.Equal(t, 90, updatedSession.MinutesAfter)

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_MenuActionMissingQueryParameter(t *testing.T) {
	mockClient := &MockOneBusAwayClientVoiceMenu{}
	locManager := localization.NewTestManager()
	voiceHandler := NewVoiceHandler(mockClient, locManager)
	defer voiceHandler.Close()

	// Set up initial session
	initialSession := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 30,
		CreatedAt:    time.Now().Unix(),
	}
	err := voiceHandler.SessionStore.SetVoiceSession("+14444444444", initialSession)
	require.NoError(t, err)

	r := gin.New()
	r.POST("/voice/menu_action", voiceHandler.HandleVoiceMenuAction)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "1") // Choice 1: extend departures

	w := httptest.NewRecorder()
	// Request without minutesAfter query parameter should fail
	req, _ := http.NewRequest("POST", "/voice/menu_action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Should contain error message
	assert.Contains(t, body, "An internal error occurred. Please try again")

	// Should NOT contain arrivals data or menu options
	assert.NotContains(t, body, "<Gather")
	assert.NotContains(t, body, "To hear more departures")

	// Session should remain unchanged
	session := voiceHandler.SessionStore.GetVoiceSession("+14444444444")
	assert.NotNil(t, session)
	assert.Equal(t, 30, session.MinutesAfter) // Should still be original value

	// No API calls should have been made
	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_StopNameRetrievalInResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	mockClient := new(MockOneBusAwayClientVoiceMenu)
	lm := localization.NewTestManager()
	voiceHandler := NewVoiceHandler(mockClient, lm)

	r.POST("/voice/find_stop", voiceHandler.HandleFindStop)

	// Mock the arrivals response
	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				ArrivalsAndDepartures: []models.OBAArrivalDeparture{
					{
						RouteShortName: "8",
						TripHeadsign:   "Downtown",
						Status:         "scheduled",
					},
				},
				StopId: "1_12345",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 5,
		},
	}

	// Mock the stop info response with proper stop name
	mockStopInfo := &models.StopOption{
		FullStopID: "1_12345",
		StopName:   "Pine St & 5th Ave",
		AgencyName: "Metro Transit",
	}

	// Mock the matching stops response (single stop found)
	mockMatchingStops := []models.StopOption{*mockStopInfo}

	// Set up expectations
	mockClient.On("FindAllMatchingStops", "12345").Return(mockMatchingStops, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "1_12345").Return(mockStopInfo, nil)

	form := url.Values{}
	form.Set("From", "+15555555555")
	form.Set("Digits", "12345")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Verify that the response contains the stop name, not the stop ID (XML-escaped)
	assert.Contains(t, body, "Pine St &amp; 5th Ave", "Response should contain human-readable stop name")
	assert.NotContains(t, body, "1_12345", "Response should not contain technical stop ID")

	mockClient.AssertExpectations(t)
}

func TestVoiceHandler_StopNameRetrievalFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	mockClient := new(MockOneBusAwayClientVoiceMenu)
	lm := localization.NewTestManager()
	voiceHandler := NewVoiceHandler(mockClient, lm)

	r.POST("/voice/find_stop", voiceHandler.HandleFindStop)

	// Mock the arrivals response
	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				ArrivalsAndDepartures: []models.OBAArrivalDeparture{
					{
						RouteShortName: "8",
						TripHeadsign:   "Downtown",
						Status:         "scheduled",
					},
				},
				StopId: "1_12345",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "8",
			TripHeadsign:        "Downtown",
			MinutesUntilArrival: 5,
		},
	}

	// Mock matching stops response (single stop found)
	mockStopInfo := &models.StopOption{
		FullStopID: "1_12345",
		StopName:   "Pine St & 5th Ave",
		AgencyName: "Metro Transit",
	}
	mockMatchingStops := []models.StopOption{*mockStopInfo}

	// Set up expectations - GetStopInfo fails
	mockClient.On("FindAllMatchingStops", "12345").Return(mockMatchingStops, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, mock.Anything).Return(mockArrivals)
	mockClient.On("GetStopInfo", "1_12345").Return(nil, fmt.Errorf("stop not found"))

	form := url.Values{}
	form.Set("From", "+15555555555")
	form.Set("Digits", "12345")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// When GetStopInfo fails, should fall back to using the stop ID
	assert.Contains(t, body, "1_12345", "Response should contain stop ID as fallback when stop name retrieval fails")

	mockClient.AssertExpectations(t)
}
