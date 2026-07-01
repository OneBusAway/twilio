package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"oba-twilio/handlers/common"
	"oba-twilio/localization"
	"oba-twilio/models"
)

type MockOneBusAwayClientDisambiguation struct {
	mock.Mock
}

func (m *MockOneBusAwayClientDisambiguation) GetArrivalsAndDepartures(stopID string) (*models.OneBusAwayResponse, error) {
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

func (m *MockOneBusAwayClientDisambiguation) GetArrivalsAndDeparturesWithWindow(stopID string, minutesAfter int) (*models.OneBusAwayResponse, error) {
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

func (m *MockOneBusAwayClientDisambiguation) ProcessArrivals(resp *models.OneBusAwayResponse, maxMinutes int) []models.Arrival {
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

func (m *MockOneBusAwayClientDisambiguation) SearchStops(query string) ([]models.Stop, error) {
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

func (m *MockOneBusAwayClientDisambiguation) InitializeCoverage() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockOneBusAwayClientDisambiguation) GetCoverageArea() *models.CoverageArea {
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

func (m *MockOneBusAwayClientDisambiguation) FindAllMatchingStops(stopID string) ([]models.StopOption, error) {
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

func (m *MockOneBusAwayClientDisambiguation) GetStopInfo(fullStopID string) (*models.StopOption, error) {
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

func setupDisambiguationTestRouter() (*gin.Engine, *MockOneBusAwayClientDisambiguation, *SMSHandler) {
	gin.SetMode(gin.TestMode)

	mockClient := &MockOneBusAwayClientDisambiguation{}
	locManager := localization.NewTestManager()
	smsHandler := NewSMSHandler(mockClient, locManager)

	r := gin.New()
	r.POST("/sms", smsHandler.HandleSMS)

	return r, mockClient, smsHandler
}

func TestSMSHandler_SingleStopFound(t *testing.T) {
	r, mockClient, _ := setupDisambiguationTestRouter()

	mockStopOptions := []models.StopOption{
		{
			FullStopID:  "1_12345",
			AgencyName:  "King County Metro",
			StopName:    "Test Stop",
			DisplayText: "King County Metro: Test Stop",
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
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Body", "12345")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Stop: Test Stop")
	assert.Contains(t, w.Body.String(), "Route 8")
	mockClient.AssertExpectations(t)
}

func TestSMSHandler_MultipleStopsFound_Disambiguation(t *testing.T) {
	r, mockClient, _ := setupDisambiguationTestRouter()

	mockStopOptions := []models.StopOption{
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

	mockClient.On("FindAllMatchingStops", "12345").Return(mockStopOptions, nil)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Body", "12345")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Multiple stops found for 12345")
	assert.Contains(t, body, "1) King County Metro: Pine St &amp; 3rd Ave")
	assert.Contains(t, body, "2) Sound Transit: University Street Station")
	assert.Contains(t, body, "Reply with the number to choose")
	mockClient.AssertExpectations(t)
}

func TestSMSHandler_DisambiguationChoice_Valid(t *testing.T) {
	r, mockClient, smsHandler := setupDisambiguationTestRouter()

	// Set up disambiguation session
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

	session := &models.DisambiguationSession{
		StopOptions: stopOptions,
	}
	err := smsHandler.SessionStore.SetDisambiguationSession("+14444444444", session)
	assert.NoError(t, err)

	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "40_12345",
			},
		},
		Code: 200,
	}

	mockArrivals := []models.Arrival{
		{
			RouteShortName:      "Link",
			TripHeadsign:        "SeaTac Airport",
			MinutesUntilArrival: 3,
		},
	}

	mockClient.On("GetArrivalsAndDeparturesWithWindow", "40_12345", 30).Return(mockResponse, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return(mockArrivals)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Body", "2")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Stop: University Street Station")
	assert.Contains(t, w.Body.String(), "Route Link")
	mockClient.AssertExpectations(t)
}

func TestSMSHandler_DisambiguationChoice_Invalid(t *testing.T) {
	r, _, smsHandler := setupDisambiguationTestRouter()

	// Set up disambiguation session with 2 options
	stopOptions := []models.StopOption{
		{FullStopID: "1_12345", DisplayText: "Option 1"},
		{FullStopID: "40_12345", DisplayText: "Option 2"},
	}

	session := &models.DisambiguationSession{
		StopOptions: stopOptions,
	}
	err := smsHandler.SessionStore.SetDisambiguationSession("+14444444444", session)
	assert.NoError(t, err)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Body", "3")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Please choose a number between 1 and 2")
}

func TestSMSHandler_DisambiguationChoice_NoSession(t *testing.T) {
	r, mockClient, _ := setupDisambiguationTestRouter()

	mockClient.On("FindAllMatchingStops", "1").Return([]models.StopOption{}, nil)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Body", "1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Sorry, no stops found with ID 1")
	mockClient.AssertExpectations(t)
}

func TestSessionStore_SetAndGet(t *testing.T) {
	store := common.NewSessionStore()
	defer store.Close()

	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_12345", DisplayText: "Test Stop"},
		},
	}

	err := store.SetDisambiguationSession("+14444444444", session)
	assert.NoError(t, err)

	retrieved := store.GetDisambiguationSession("+14444444444")
	assert.NotNil(t, retrieved)
	assert.Len(t, retrieved.StopOptions, 1)
	assert.Equal(t, "1_12345", retrieved.StopOptions[0].FullStopID)
}

func TestSessionStore_Clear(t *testing.T) {
	store := common.NewSessionStore()
	defer store.Close()

	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_12345", DisplayText: "Test Stop"},
		},
	}

	err := store.SetDisambiguationSession("+14444444444", session)
	assert.NoError(t, err)
	store.ClearDisambiguationSession("+14444444444")

	retrieved := store.GetDisambiguationSession("+14444444444")
	assert.Nil(t, retrieved)
}

func TestSessionStore_ConcurrentAccess(t *testing.T) {
	store := common.NewSessionStore()
	defer store.Close()

	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_12345", DisplayText: "Test Stop"},
		},
	}

	const numGoroutines = 100
	const phoneNumber = "+14444444444"

	// Test concurrent reads and writes
	done := make(chan bool, numGoroutines*2)

	// Writers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			if err := store.SetDisambiguationSession(phoneNumber, session); err != nil {
				// Error is expected in concurrent test scenario
				t.Logf("Expected error in concurrent write: %v", err)
			}
			done <- true
		}()
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			store.GetDisambiguationSession(phoneNumber)
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines*2; i++ {
		<-done
	}

	// Should still be able to get the session
	retrieved := store.GetDisambiguationSession(phoneNumber)
	assert.NotNil(t, retrieved)
}

func TestSessionStore_ProperCleanup(t *testing.T) {
	store := common.NewSessionStore()

	// Add a session that should be cleaned up - we'll manipulate it after setting
	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_12345", DisplayText: "Test Stop"},
		},
	}

	err := store.SetDisambiguationSession("+14444444444", session)
	assert.NoError(t, err)

	// Manually expire the session using the helper method
	store.ExpireSession("+14444444444")

	// Getting expired session should return nil and clean it up
	retrieved := store.GetDisambiguationSession("+14444444444")
	assert.Nil(t, retrieved)

	store.Close()
}

func TestSessionStore_PhoneNumberValidation(t *testing.T) {
	store := common.NewSessionStore()
	defer store.Close()

	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_12345", DisplayText: "Test Stop"},
		},
	}

	tests := []struct {
		name        string
		phoneNumber string
		shouldError bool
	}{
		{"Valid US phone number", "+14444444444", false},
		{"Valid US phone number 2", "+15551234567", false},
		{"Valid international phone number", "+48500100200", false},
		{"Invalid format - no plus", "14444444444", true},
		{"Invalid format - too short", "+1444444", true},
		{"Invalid format - too long", "+14444444444444444", true},
		{"Invalid format - empty", "", true},
		{"Invalid format - letters", "+1abcdefghij", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.SetDisambiguationSession(tt.phoneNumber, session)
			if tt.shouldError {
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), "invalid phone number format")
				}
			} else {
				assert.NoError(t, err)
				// Clean up valid entries
				store.ClearDisambiguationSession(tt.phoneNumber)
			}
		})
	}
}

func TestSessionStore_GetWithInvalidPhoneNumber(t *testing.T) {
	store := common.NewSessionStore()
	defer store.Close()

	tests := []struct {
		name        string
		phoneNumber string
	}{
		{"Invalid format - no plus", "14444444444"},
		{"Invalid format - too short", "+144444444"},
		{"Invalid format - empty", ""},
		{"Invalid format - letters", "+1abcdefghij"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := store.GetDisambiguationSession(tt.phoneNumber)
			assert.Nil(t, session, "Invalid phone number should return nil")
		})
	}
}
