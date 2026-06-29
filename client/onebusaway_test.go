package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"oba-twilio/models"
)

func TestNewOneBusAwayClient(t *testing.T) {
	client := NewOneBusAwayClient("https://api.example.com", "test-key")

	assert.Equal(t, "https://api.example.com", client.BaseURL)
	assert.Equal(t, "test-key", client.APIKey)
	assert.NotNil(t, client.Client)
	assert.Equal(t, 10*time.Second, client.Client.Timeout)
}

func TestResolveStopID(t *testing.T) {
	client := NewOneBusAwayClient("https://api.example.com", "test-key")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Full stop ID", "1_12345", "1_12345"},
		{"Numeric only", "12345", "1_12345"},
		{"With spaces", " 12345 ", "1_12345"},
		{"Empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.resolveStopID(tt.input)
			if tt.expected == "" {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetArrivalsAndDepartures_Success(t *testing.T) {
	mockCoverage := models.AgenciesWithCoverageResponse{
		Data: struct {
			LimitExceeded bool                       `json:"limitExceeded"`
			List          []models.AgencyCoverageRow `json:"list"`
		}{
			List: []models.AgencyCoverageRow{
				{AgencyID: "test", Lat: 47.6, LatSpan: 0.5, Lon: -122.3, LonSpan: 0.8},
			},
		},
		Code: 200,
		Text: "OK",
	}

	mockResponse := models.OneBusAwayResponse{
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
				ArrivalsAndDepartures: []struct {
					RouteShortName       string `json:"routeShortName"`
					TripHeadsign         string `json:"tripHeadsign"`
					PredictedArrivalTime int64  `json:"predictedArrivalTime"`
					ScheduledArrivalTime int64  `json:"scheduledArrivalTime"`
					Status               string `json:"status"`
				}{
					{
						RouteShortName:       "8",
						TripHeadsign:         "Seattle Center",
						PredictedArrivalTime: time.Now().Unix()*1000 + 300000,
						ScheduledArrivalTime: time.Now().Unix()*1000 + 240000,
						Status:               "default",
					},
				},
				StopId: "1_75403",
			},
		},
		Code: 200,
		Text: "OK",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "agencies-with-coverage") {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(mockCoverage); err != nil {
				t.Errorf("Failed to encode response: %v", err)
			}
		} else if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			assert.Equal(t, "test-key", r.URL.Query().Get("key"))
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
				t.Errorf("Failed to encode response: %v", err)
			}
		} else if strings.Contains(r.URL.Path, "/api/where/stop/") {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewOneBusAwayClient(server.URL, "test-key")

	resp, err := client.GetArrivalsAndDepartures("75403")

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 200, resp.Code)
	assert.Equal(t, "1_75403", resp.Data.Entry.StopId)
	assert.Len(t, resp.Data.Entry.ArrivalsAndDepartures, 1)
}

func TestGetArrivalsAndDepartures_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewOneBusAwayClient(server.URL, "test-key")

	_, err := client.GetArrivalsAndDepartures("invalid")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestProcessArrivals(t *testing.T) {
	client := NewOneBusAwayClient("https://api.example.com", "test-key")
	now := time.Now().Unix() * 1000

	mockResponse := &models.OneBusAwayResponse{
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
				ArrivalsAndDepartures: []struct {
					RouteShortName       string `json:"routeShortName"`
					TripHeadsign         string `json:"tripHeadsign"`
					PredictedArrivalTime int64  `json:"predictedArrivalTime"`
					ScheduledArrivalTime int64  `json:"scheduledArrivalTime"`
					Status               string `json:"status"`
				}{
					{
						RouteShortName:       "8",
						TripHeadsign:         "Seattle Center",
						PredictedArrivalTime: now + 300000,
						Status:               "default",
					},
					{
						RouteShortName:       "43",
						TripHeadsign:         "Capitol Hill",
						PredictedArrivalTime: now - 60000,
						Status:               "default",
					},
					{
						RouteShortName:       "49",
						TripHeadsign:         "U District",
						PredictedArrivalTime: now + 4000000,
						Status:               "default",
					},
				},
				StopId: "test_stop",
			},
		},
	}

	arrivals := client.ProcessArrivals(mockResponse, 60)

	assert.Len(t, arrivals, 1)
	assert.Equal(t, "8", arrivals[0].RouteShortName)
	assert.Equal(t, "Seattle Center", arrivals[0].TripHeadsign)
	assert.Equal(t, 5, arrivals[0].MinutesUntilArrival)
}

func TestStopExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/where/stop/1_75403.json" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewOneBusAwayClient(server.URL, "test-key")

	assert.True(t, client.stopExists("1_75403"))
	assert.False(t, client.stopExists("1_invalid"))
}

func TestCircuitBreakerStateAccessor(t *testing.T) {
	c := NewOneBusAwayClient("https://example.com", "test")
	if got := c.CircuitBreakerState(); got != int(CircuitClosed) {
		t.Errorf("expected closed (%d), got %d", CircuitClosed, got)
	}
}
