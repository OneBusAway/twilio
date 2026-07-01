package client

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"oba-twilio/models"
)

func TestGetStopInfo_InvalidResponse(t *testing.T) {
	tests := []struct {
		name     string
		response interface{}
		wantErr  string
	}{
		{
			name: "Missing stop ID",
			response: struct {
				Data struct {
					Entry struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"entry"`
					References struct {
						Agencies []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"agencies"`
					} `json:"references"`
				} `json:"data"`
				Code int    `json:"code"`
				Text string `json:"text"`
			}{
				Data: struct {
					Entry struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"entry"`
					References struct {
						Agencies []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"agencies"`
					} `json:"references"`
				}{
					Entry: struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					}{
						ID:   "", // Missing ID
						Name: "Test Stop",
					},
				},
				Code: 200,
				Text: "OK",
			},
			wantErr: "missing stop ID",
		},
		{
			name: "Missing stop name",
			response: struct {
				Data struct {
					Entry struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"entry"`
					References struct {
						Agencies []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"agencies"`
					} `json:"references"`
				} `json:"data"`
				Code int    `json:"code"`
				Text string `json:"text"`
			}{
				Data: struct {
					Entry struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"entry"`
					References struct {
						Agencies []struct {
							ID   string `json:"id"`
							Name string `json:"name"`
						} `json:"agencies"`
					} `json:"references"`
				}{
					Entry: struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					}{
						ID:   "1_12345",
						Name: "", // Missing name
					},
				},
				Code: 200,
				Text: "OK",
			},
			wantErr: "missing stop name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				err := json.NewEncoder(w).Encode(tt.response)
				require.NoError(t, err)
			}))
			defer server.Close()

			client := NewOneBusAwayClient(server.URL, "test-key")

			_, err := client.GetStopInfo("1_12345")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestInitializeCoverage_InvalidCoordinates(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		lon     float64
		wantErr string
	}{
		{
			name:    "Invalid latitude too high",
			lat:     91.0,
			lon:     -122.0,
			wantErr: "invalid latitude",
		},
		{
			name:    "Invalid latitude too low",
			lat:     -91.0,
			lon:     -122.0,
			wantErr: "invalid latitude",
		},
		{
			name:    "Invalid longitude too high",
			lat:     47.0,
			lon:     181.0,
			wantErr: "invalid longitude",
		},
		{
			name:    "Invalid longitude too low",
			lat:     47.0,
			lon:     -181.0,
			wantErr: "invalid longitude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockResponse := models.AgenciesWithCoverageResponse{
				Data: struct {
					LimitExceeded bool                       `json:"limitExceeded"`
					List          []models.AgencyCoverageRow `json:"list"`
				}{
					List: []models.AgencyCoverageRow{
						{
							AgencyID: "test",
							Lat:      tt.lat,
							LatSpan:  0.5,
							Lon:      tt.lon,
							LonSpan:  0.8,
						},
					},
				},
				Code: 200,
				Text: "OK",
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
					t.Errorf("Failed to encode response: %v", err)
				}
			}))
			defer server.Close()

			client := NewOneBusAwayClient(server.URL, "test-key")

			err := client.InitializeCoverage()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSearchStops_InvalidCoordinates(t *testing.T) {
	mockStopData := models.StopData{
		Data: struct {
			List []struct {
				ID   string  `json:"id"`
				Name string  `json:"name"`
				Lat  float64 `json:"lat"`
				Lon  float64 `json:"lon"`
			} `json:"list"`
		}{
			List: []struct {
				ID   string  `json:"id"`
				Name string  `json:"name"`
				Lat  float64 `json:"lat"`
				Lon  float64 `json:"lon"`
			}{
				{
					ID:   "1_12345",
					Name: "Test Stop",
					Lat:  91.0, // Invalid latitude
					Lon:  -122.3321,
				},
			},
		},
		Code: 200,
		Text: "OK",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mockStopData); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewOneBusAwayClient(server.URL, "test-key")

	// Initialize with valid coverage first
	client.coverageArea = &models.CoverageArea{
		CenterLat: 47.6062,
		CenterLon: -122.3321,
		Radius:    25000.0,
	}

	_, err := client.SearchStops("test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid latitude")
}

func TestGetArrivalsAndDepartures_MissingStopInfo(t *testing.T) {
	mockResponse := models.OneBusAwayResponse{
		Data: models.OBAResponseData{
			Entry: models.OBAStopEntry{
				StopId: "", // Missing stop ID
			},
		},
		Code: 200,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewOneBusAwayClient(server.URL, "test-key")

	_, err := client.GetArrivalsAndDepartures("1_12345")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing stop information")
}
