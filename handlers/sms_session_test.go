package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oba-twilio/models"
)

// Test that verifies SMS responses are always sent even if session storage fails
func TestSMSHandler_AlwaysSendsResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup
	r, mockClient, _ := setupSMSTestRouter()

	// Mock FindAllMatchingStops to return a single stop
	mockClient.On("FindAllMatchingStops", "75403").Return([]models.StopOption{
		{
			FullStopID:  "1_75403",
			DisplayText: "15th Ave NE & NE Campus Pkwy",
			StopName:    "15th Ave NE & NE Campus Pkwy",
			AgencyName:  "Metro Transit",
		},
	}, nil)

	// Mock successful arrivals response
	mockResponse := &models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "1_75403",
			},
		},
	}

	mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 30).Return(mockResponse, nil)

	// "more" flow calls GetStopInfo to obtain the stop name header.
	mockClient.On("GetStopInfo", "1_75403").Return(&models.StopOption{
		FullStopID: "1_75403",
		StopName:   "15th Ave NE & NE Campus Pkwy",
	}, nil)
	mockClient.On("ProcessArrivals", mockResponse, 30).Return([]models.Arrival{
		{
			RouteShortName:      "71",
			TripHeadsign:        "Downtown Seattle",
			MinutesUntilArrival: 8,
		},
		{
			RouteShortName:      "73",
			TripHeadsign:        "Jackson Park",
			MinutesUntilArrival: 12,
		},
	})

	// Test that a response is sent for valid stop ID
	t.Run("Valid stop ID returns TwiML response", func(t *testing.T) {
		form := url.Values{}
		form.Add("From", "+12065551234")
		form.Add("Body", "75403")

		req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// Verify response
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/xml", w.Header().Get("Content-Type"))

		responseBody := w.Body.String()
		assert.NotEmpty(t, responseBody, "Response should not be empty")
		assert.Contains(t, responseBody, "<?xml")
		assert.Contains(t, responseBody, "<Response>")
		assert.Contains(t, responseBody, "<Message>")
		assert.Contains(t, responseBody, "Route 71")
		assert.Contains(t, responseBody, "Route 73")
		assert.Contains(t, responseBody, "</Message>")
		assert.Contains(t, responseBody, "</Response>")
	})

	// Test that "more" command also sends response
	t.Run("More command returns TwiML response", func(t *testing.T) {
		// First set up a session with a stop
		form := url.Values{}
		form.Add("From", "+12065551234")
		form.Add("Body", "75403")

		req, _ := http.NewRequest("POST", "/sms", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// Now test "more" command
		mockClient.On("GetArrivalsAndDeparturesWithWindow", "1_75403", 60).Return(mockResponse, nil)
		mockClient.On("ProcessArrivals", mockResponse, 60).Return([]models.Arrival{
			{RouteShortName: "77", TripHeadsign: "Later", MinutesUntilArrival: 45},
		})

		form2 := url.Values{}
		form2.Add("From", "+12065551234")
		form2.Add("Body", "more")

		req2, _ := http.NewRequest("POST", "/sms", strings.NewReader(form2.Encode()))
		req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)

		// Verify response
		assert.Equal(t, http.StatusOK, w2.Code)
		responseBody := w2.Body.String()
		assert.NotEmpty(t, responseBody, "More command should return response")
		assert.Contains(t, responseBody, "<Response>")
	})
}
