package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"oba-twilio/client"
)

// Store scalars, not a client.Metrics value: client.Metrics embeds a
// sync.RWMutex, so storing/returning a stored value trips go vet's copylocks
// analyzer (make vet is a required gate). Build a fresh literal in the method —
// exactly what the real client.GetMetrics() does.
type fakeClientSource struct {
	hits, misses, calls, apiErrs, valErrs, cbOpen int64
	respTime                                      time.Duration
	state                                         int
}

func (f fakeClientSource) GetMetrics() client.Metrics {
	return client.Metrics{
		CacheHits: f.hits, CacheMisses: f.misses,
		APICallCount: f.calls, APIErrorCount: f.apiErrs,
		ValidationErrors: f.valErrs, CircuitBreakerOpen: f.cbOpen,
		TotalResponseTime: f.respTime,
	}
}
func (f fakeClientSource) CircuitBreakerState() int { return f.state }

func TestClientBridgeEmitsSeries(t *testing.T) {
	src := fakeClientSource{
		hits: 7, misses: 3, calls: 10, apiErrs: 2, valErrs: 1, cbOpen: 4,
		respTime: 5 * time.Second,
		state:    1,
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(newClientCollector(src))

	expected := `
# HELP oba_api_requests_total Total OneBusAway API calls.
# TYPE oba_api_requests_total counter
oba_api_requests_total 10
# HELP oba_circuit_breaker_state Current circuit-breaker state (0=closed,1=open,2=half-open).
# TYPE oba_circuit_breaker_state gauge
oba_circuit_breaker_state 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"oba_api_requests_total", "oba_circuit_breaker_state"); err != nil {
		t.Error(err)
	}
}

func TestClientBridgeEmitsAllSeries(t *testing.T) {
	src := fakeClientSource{
		hits: 7, misses: 3, calls: 10, apiErrs: 2, valErrs: 1, cbOpen: 4,
		respTime: 5 * time.Second,
		state:    1,
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(newClientCollector(src))

	expected := `
# HELP oba_api_errors_total Total OneBusAway API errors.
# TYPE oba_api_errors_total counter
oba_api_errors_total 2
# HELP oba_api_request_duration_seconds OneBusAway API request latency (sum/count derived from running totals).
# TYPE oba_api_request_duration_seconds histogram
oba_api_request_duration_seconds_bucket{le="+Inf"} 10
oba_api_request_duration_seconds_sum 5
oba_api_request_duration_seconds_count 10
# HELP oba_cache_hits_total Total OneBusAway API cache hits.
# TYPE oba_cache_hits_total counter
oba_cache_hits_total 7
# HELP oba_cache_misses_total Total OneBusAway API cache misses.
# TYPE oba_cache_misses_total counter
oba_cache_misses_total 3
# HELP oba_circuit_breaker_open_total Total times the circuit breaker opened.
# TYPE oba_circuit_breaker_open_total counter
oba_circuit_breaker_open_total 4
# HELP oba_validation_errors_total Total OneBusAway response validation errors.
# TYPE oba_validation_errors_total counter
oba_validation_errors_total 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"oba_api_errors_total",
		"oba_validation_errors_total",
		"oba_circuit_breaker_open_total",
		"oba_cache_hits_total",
		"oba_cache_misses_total",
		"oba_api_request_duration_seconds",
	); err != nil {
		t.Error(err)
	}
}
