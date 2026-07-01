package voice

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
	"github.com/stretchr/testify/require"

	"oba-twilio/localization"
	"oba-twilio/metrics"
	"oba-twilio/models"
)

// mockOBAClient is a testify mock implementing client.OneBusAwayClientInterface.
type mockOBAClient struct {
	mock.Mock
}

func (m *mockOBAClient) GetArrivalsAndDepartures(stopID string) (*models.OneBusAwayResponse, error) {
	args := m.Called(stopID)
	resp, _ := args.Get(0).(*models.OneBusAwayResponse)
	return resp, args.Error(1)
}

func (m *mockOBAClient) GetArrivalsAndDeparturesWithWindow(stopID string, minutesAfter int) (*models.OneBusAwayResponse, error) {
	args := m.Called(stopID, minutesAfter)
	resp, _ := args.Get(0).(*models.OneBusAwayResponse)
	return resp, args.Error(1)
}

func (m *mockOBAClient) ProcessArrivals(resp *models.OneBusAwayResponse, maxMinutesOut int) []models.Arrival {
	args := m.Called(resp, maxMinutesOut)
	arrivals, _ := args.Get(0).([]models.Arrival)
	return arrivals
}

func (m *mockOBAClient) SearchStops(query string) ([]models.Stop, error) {
	args := m.Called(query)
	stops, _ := args.Get(0).([]models.Stop)
	return stops, args.Error(1)
}

func (m *mockOBAClient) InitializeCoverage() error {
	return m.Called().Error(0)
}

func (m *mockOBAClient) GetCoverageArea() *models.CoverageArea {
	args := m.Called()
	area, _ := args.Get(0).(*models.CoverageArea)
	return area
}

func (m *mockOBAClient) FindAllMatchingStops(stopID string) ([]models.StopOption, error) {
	args := m.Called(stopID)
	stops, _ := args.Get(0).([]models.StopOption)
	return stops, args.Error(1)
}

func (m *mockOBAClient) GetStopInfo(fullStopID string) (*models.StopOption, error) {
	args := m.Called(fullStopID)
	stop, _ := args.Get(0).(*models.StopOption)
	return stop, args.Error(1)
}

// newArrivalsResponse builds a minimal OneBusAwayResponse carrying the given stop ID.
func newArrivalsResponse(stopID string) *models.OneBusAwayResponse {
	resp := &models.OneBusAwayResponse{Code: 200}
	resp.Data.Entry.StopId = stopID
	return resp
}

// expectSingleStopArrivals wires the four mock calls a successful single-stop
// lookup makes: resolve digits -> fullStopID, then fetch and format its arrivals.
func expectSingleStopArrivals(mockClient *mockOBAClient, digits, fullStopID, route string) {
	stops := []models.StopOption{{FullStopID: fullStopID, StopName: "Test Stop"}}
	resp := newArrivalsResponse(fullStopID)
	mockClient.On("FindAllMatchingStops", digits).Return(stops, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", fullStopID, 30).Return(resp, nil)
	mockClient.On("ProcessArrivals", resp, mock.Anything).Return([]models.Arrival{{RouteShortName: route, MinutesUntilArrival: 5}})
	mockClient.On("GetStopInfo", fullStopID).Return(&stops[0], nil)
}

func setupFindStopHandler() (*gin.Engine, *mockOBAClient, *Handler) {
	gin.SetMode(gin.TestMode)
	mockClient := &mockOBAClient{}
	h := NewHandler(mockClient, localization.NewTestManager())

	r := gin.New()
	r.POST("/voice/find_stop", h.HandleFindStop)
	return r, mockClient, h
}

func postFindStop(r *gin.Engine, from, digits string) *httptest.ResponseRecorder {
	form := url.Values{}
	form.Set("From", from)
	form.Set("Digits", digits)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)
	return w
}

func TestHandleFindStop_InvalidPhoneNumber(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	w := postFindStop(r, "abc", "12345")

	// Phone validation fails before any OBA lookup happens.
	mockClient.AssertNotCalled(t, "FindAllMatchingStops", mock.Anything)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotEqual(t, "", w.Body.String())
}

func TestHandleFindStop_InvalidCallSidStillProceeds(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	expectSingleStopArrivals(mockClient, "12345", "1_12345", "8")

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "12345")
	form.Set("CallSid", "not-a-valid-sid")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/find_stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Route 8")
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_SpeaksServiceAlert(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	stops := []models.StopOption{{FullStopID: "1_12345", StopName: "Test Stop"}}
	resp := newArrivalsResponse("1_12345")
	resp.CurrentTime = 1782940990927
	resp.Data.References = models.OBAReferences{Situations: []models.RawSituation{{
		ID:            "1_alert",
		Summary:       models.NLString{Value: "Elevator outage at this station"},
		ActiveWindows: []models.ActiveWindow{{From: 1776212160000, To: 1789383540000}},
	}}}
	mockClient.On("FindAllMatchingStops", "12345").Return(stops, nil)
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(resp, nil)
	mockClient.On("ProcessArrivals", resp, mock.Anything).Return([]models.Arrival{{RouteShortName: "8", MinutesUntilArrival: 5}})
	mockClient.On("GetStopInfo", "1_12345").Return(&stops[0], nil)

	w := postFindStop(r, "+14444444444", "12345")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Service alert")
	assert.Contains(t, body, "Elevator outage")
	// Alert is spoken before the arrivals line.
	assert.Less(t, strings.Index(body, "Service alert"), strings.Index(body, "Route 8"))
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_EmptyDigits(t *testing.T) {
	r, _, _ := setupFindStopHandler()

	w := postFindStop(r, "+14444444444", "")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "receive any digits")
}

func TestHandleFindStop_InvalidStopIDFormat(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	// 21 characters exceeds the 20-char stop ID limit, so it fails format validation.
	w := postFindStop(r, "+14444444444", "123456789012345678901")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid stop ID")
	mockClient.AssertNotCalled(t, "FindAllMatchingStops", mock.Anything)
}

func TestHandleFindStop_LookupError(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	mockClient.On("FindAllMatchingStops", "12345").Return(nil, fmt.Errorf("boom"))

	w := postFindStop(r, "+14444444444", "12345")

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_NoMatchingStops(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	mockClient.On("FindAllMatchingStops", "12345").Return([]models.StopOption{}, nil)

	w := postFindStop(r, "+14444444444", "12345")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "find any stops")
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_MultipleStopsPromptsDisambiguation(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	stops := []models.StopOption{
		{FullStopID: "1_999", StopName: "Pine Street"},
		{FullStopID: "40_999", StopName: "Oak Avenue"},
	}
	mockClient.On("FindAllMatchingStops", "999").Return(stops, nil)

	w := postFindStop(r, "+14444444444", "999")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<Gather")
	assert.Contains(t, body, "Press 1 for Pine Street")
	assert.Contains(t, body, "Press 2 for Oak Avenue")
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_SingleStopReturnsArrivals(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	expectSingleStopArrivals(mockClient, "12345", "1_12345", "8")

	w := postFindStop(r, "+14444444444", "12345")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Route 8")
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_MoreThanNineStopsClampsToNine(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	// Twelve matches exceed the single-DTMF-digit ceiling, so only the first
	// nine are offered (clampChoiceCount) and the caller is told so.
	stops := make([]models.StopOption, 12)
	for i := range stops {
		stops[i] = models.StopOption{
			FullStopID: fmt.Sprintf("1_%d", i+1),
			StopName:   fmt.Sprintf("Stop %d", i+1),
		}
	}
	mockClient.On("FindAllMatchingStops", "999").Return(stops, nil)

	w := postFindStop(r, "+14444444444", "999")

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Press 9 for Stop 9")
	assert.NotContains(t, body, "Press 10 for")
	assert.Contains(t, body, "Only showing first 9 options")
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_DisambiguationChoiceSelectsStop(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	stops := []models.StopOption{
		{FullStopID: "1_999", StopName: "Pine Street"},
		{FullStopID: "40_999", StopName: "Oak Avenue"},
	}
	resp := newArrivalsResponse("40_999")
	mockClient.On("FindAllMatchingStops", "999").Return(stops, nil).Once()
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "40_999", 30).Return(resp, nil).Once()
	mockClient.On("ProcessArrivals", resp, mock.Anything).Return([]models.Arrival{{RouteShortName: "99", MinutesUntilArrival: 4}})
	mockClient.On("GetStopInfo", "40_999").Return(&stops[1], nil)

	phone := "+16667778899"
	require.Equal(t, http.StatusOK, postFindStop(r, phone, "999").Code)

	w := postFindStop(r, phone, "2")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Route 99")
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_DisambiguationChoiceOutOfRange(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	stops := []models.StopOption{
		{FullStopID: "1_999", StopName: "Pine Street"},
		{FullStopID: "40_999", StopName: "Oak Avenue"},
	}
	mockClient.On("FindAllMatchingStops", "999").Return(stops, nil).Once()

	phone := "+16667779900"
	require.Equal(t, http.StatusOK, postFindStop(r, phone, "999").Code)

	// Choice 9 exceeds the 2 available options.
	w := postFindStop(r, phone, "9")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Please press a number between 1 and 2")
	mockClient.AssertExpectations(t)
}

func TestHandleFindStop_SingleDigitWithoutSessionTreatedAsStopID(t *testing.T) {
	r, mockClient, _ := setupFindStopHandler()

	expectSingleStopArrivals(mockClient, "1", "1_1", "8")

	w := postFindStop(r, "+14444444444", "1")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Route 8")
	assert.NotContains(t, w.Body.String(), "No active selection")
	mockClient.AssertExpectations(t)
}

func TestParseDisambiguationChoice(t *testing.T) {
	h := NewHandler(&mockOBAClient{}, localization.NewTestManager())

	assert.Equal(t, 0, h.parseDisambiguationChoice(""))
	assert.Equal(t, 0, h.parseDisambiguationChoice("12"))
	assert.Equal(t, 0, h.parseDisambiguationChoice("0"))
	assert.Equal(t, 0, h.parseDisambiguationChoice("a"))
	assert.Equal(t, 5, h.parseDisambiguationChoice("5"))
	assert.Equal(t, 9, h.parseDisambiguationChoice("9"))
}

func TestHandleVoiceDisambiguationChoice_NoSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(&mockOBAClient{}, localization.NewTestManager())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/voice/find_stop", nil)

	h.handleVoiceDisambiguationChoice(c, models.TwilioVoiceRequest{From: "+14444444444"}, 1)

	assert.Contains(t, w.Body.String(), "No active selection")
}

func TestGetAndFormatVoiceArrivals_LookupError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockClient := &mockOBAClient{}
	h := NewHandler(mockClient, localization.NewTestManager())

	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 30).Return(nil, fmt.Errorf("boom"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/voice/find_stop", nil)

	h.getAndFormatVoiceArrivalsWithSession(c, "+14444444444", "1_12345", 0)

	// A lookup failure should render a spoken error response, not just stop short.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<Say")
	mockClient.AssertExpectations(t)
}

func TestFindStopRecordsResolvedInteraction(t *testing.T) {
	router, mockClient, h := setupFindStopHandler()
	m := metrics.New()
	h.SetMetrics(m)

	expectSingleStopArrivals(mockClient, "75403", "1_75403", "44")

	w := postFindStop(router, "+14444444444", "75403")
	assert.Equal(t, http.StatusOK, w.Code)

	mr := gin.New()
	mr.GET("/metrics", m.Handler())
	mw := httptest.NewRecorder()
	mr.ServeHTTP(mw, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(mw.Body.String(), `interactions_total{channel="voice",outcome="resolved"} 1`) {
		t.Errorf("expected resolved voice interaction:\n%s", mw.Body.String())
	}
}

func TestFindStopRecordsErrorInteraction(t *testing.T) {
	router, mockClient, h := setupFindStopHandler()
	m := metrics.New()
	h.SetMetrics(m)

	mockClient.On("FindAllMatchingStops", "75403").Return(nil, fmt.Errorf("boom"))

	w := postFindStop(router, "+14444444444", "75403")
	assert.Equal(t, http.StatusOK, w.Code)

	mr := gin.New()
	mr.GET("/metrics", m.Handler())
	mw := httptest.NewRecorder()
	mr.ServeHTTP(mw, httptest.NewRequest("GET", "/metrics", nil))
	body := mw.Body.String()
	if !strings.Contains(body, `interactions_total{channel="voice",outcome="error"} 1`) {
		t.Errorf("expected error voice interaction:\n%s", body)
	}
	if !strings.Contains(body, `stop_lookups_total{agency="none",result="error"} 1`) {
		t.Errorf("expected error stop lookup:\n%s", body)
	}
}

func TestFindStopRecordsNotFoundInteraction(t *testing.T) {
	router, mockClient, h := setupFindStopHandler()
	m := metrics.New()
	h.SetMetrics(m)

	mockClient.On("FindAllMatchingStops", "75403").Return([]models.StopOption{}, nil)

	w := postFindStop(router, "+14444444444", "75403")
	assert.Equal(t, http.StatusOK, w.Code)

	mr := gin.New()
	mr.GET("/metrics", m.Handler())
	mw := httptest.NewRecorder()
	mr.ServeHTTP(mw, httptest.NewRequest("GET", "/metrics", nil))
	body := mw.Body.String()
	if !strings.Contains(body, `interactions_total{channel="voice",outcome="not_found"} 1`) {
		t.Errorf("expected not_found voice interaction:\n%s", body)
	}
	if !strings.Contains(body, `stop_lookups_total{agency="none",result="not_found"} 1`) {
		t.Errorf("expected not_found stop lookup:\n%s", body)
	}
}

func TestFindStopRecordsAmbiguousInteraction(t *testing.T) {
	router, mockClient, h := setupFindStopHandler()
	m := metrics.New()
	h.SetMetrics(m)

	stops := []models.StopOption{
		{FullStopID: "1_75403", StopName: "Pine Street"},
		{FullStopID: "40_75403", StopName: "Oak Avenue"},
	}
	mockClient.On("FindAllMatchingStops", "75403").Return(stops, nil)

	w := postFindStop(router, "+14444444444", "75403")
	assert.Equal(t, http.StatusOK, w.Code)

	mr := gin.New()
	mr.GET("/metrics", m.Handler())
	mw := httptest.NewRecorder()
	mr.ServeHTTP(mw, httptest.NewRequest("GET", "/metrics", nil))
	body := mw.Body.String()
	if !strings.Contains(body, `interactions_total{channel="voice",outcome="ambiguous"} 1`) {
		t.Errorf("expected ambiguous voice interaction:\n%s", body)
	}
	if !strings.Contains(body, `stop_lookups_total{agency="1",result="ambiguous"} 1`) {
		t.Errorf("expected ambiguous stop lookup:\n%s", body)
	}
}

// TestHandleVoiceMenuAction_ExtendDoesNotRepeatServiceAlert locks in that service
// alerts are spoken only on the initial stop lookup (minutesAfter == 0), not repeated
// on each "extend departures" menu loop, mirroring the SMS first-page-only behavior.
func TestHandleVoiceMenuAction_ExtendDoesNotRepeatServiceAlert(t *testing.T) {
	r, mockClient, h := setupFindStopHandler()
	r.POST("/voice/menu_action", h.HandleVoiceMenuAction)

	// Prime a voice session as if the caller already looked up this stop.
	require.NoError(t, h.SessionStore.SetVoiceSession("+14444444444", &models.VoiceSession{StopID: "1_12345"}))

	// The extend fetches arrivals with the extended (60-min) window; the response carries
	// an active alert that must NOT be re-spoken.
	resp := newArrivalsResponse("1_12345")
	resp.CurrentTime = 1782940990927
	resp.Data.References = models.OBAReferences{Situations: []models.RawSituation{{
		ID:            "1_alert",
		Summary:       models.NLString{Value: "Elevator outage at this station"},
		ActiveWindows: []models.ActiveWindow{{From: 1776212160000, To: 1789383540000}},
	}}}
	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_12345", 60).Return(resp, nil)
	mockClient.On("ProcessArrivals", resp, mock.Anything).Return([]models.Arrival{{RouteShortName: "8", MinutesUntilArrival: 5}})
	mockClient.On("GetStopInfo", "1_12345").Return(&models.StopOption{FullStopID: "1_12345", StopName: "Test Stop"}, nil)

	form := url.Values{}
	form.Set("From", "+14444444444")
	form.Set("Digits", "1") // press 1 = extend departures
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/voice/menu_action?minutesAfter=60&lang=en-US", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Route 8")          // arrivals still returned on extend
	assert.NotContains(t, body, "Service alert") // alert NOT repeated on extend
	mockClient.AssertExpectations(t)
}
