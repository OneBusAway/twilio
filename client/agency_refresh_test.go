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

// agencyCoverageServer returns a test server that serves agencies-with-coverage
// with the given agency IDs, or a 500 when fail is true.
func agencyCoverageServer(t *testing.T, ids []string, fail bool) *httptest.Server {
	t.Helper()
	rows := make([]models.AgencyCoverageRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, models.AgencyCoverageRow{AgencyID: id, Lat: 47.6, LatSpan: 0.5, Lon: -122.3, LonSpan: 0.8})
	}
	resp := models.AgenciesWithCoverageResponse{Code: 200, Text: "OK"}
	resp.Data.List = rows

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/where/agencies-with-coverage.json") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// TestEnsureAgencyIDs_ExpiredCacheRefreshes verifies that an expired agency-ID
// cache does not short-circuit: the refresh runs and fresh IDs win over stale.
func TestEnsureAgencyIDs_ExpiredCacheRefreshes(t *testing.T) {
	server := agencyCoverageServer(t, []string{"fresh1", "fresh2"}, false)
	defer server.Close()

	c := NewOneBusAwayClient(server.URL, "test-key")
	// Seed an already-expired entry with stale IDs.
	c.cache.Set(coverageAgencyIDsCacheKey, []string{"stale"}, -time.Minute)

	err := c.ensureAgencyIDsForSearch()
	assert.NoError(t, err)
	assert.Equal(t, []string{"fresh1", "fresh2"}, c.getAgencyList(),
		"expired cache should be refreshed from the API, not served stale")
}

// TestEnsureAgencyIDs_StaleFallbackOnFailure verifies that when the refresh
// fails and stale IDs exist, we degrade gracefully (no error, stale applied).
func TestEnsureAgencyIDs_StaleFallbackOnFailure(t *testing.T) {
	server := agencyCoverageServer(t, nil, true)
	defer server.Close()

	c := NewOneBusAwayClient(server.URL, "test-key")
	c.cache.Set(coverageAgencyIDsCacheKey, []string{"stale1", "stale2"}, -time.Minute)

	err := c.ensureAgencyIDsForSearch()
	assert.NoError(t, err, "stale fallback should suppress the refresh error")
	assert.Equal(t, []string{"stale1", "stale2"}, c.getAgencyList(),
		"failed refresh with stale cache should keep using stale IDs")
}

// TestEnsureAgencyIDs_NoStaleReturnsError verifies that when the refresh fails
// and there is no stale cache to fall back on, the error propagates.
func TestEnsureAgencyIDs_NoStaleReturnsError(t *testing.T) {
	server := agencyCoverageServer(t, nil, true)
	defer server.Close()

	c := NewOneBusAwayClient(server.URL, "test-key")

	err := c.ensureAgencyIDsForSearch()
	assert.Error(t, err, "failed refresh with no stale cache should return an error")
}
