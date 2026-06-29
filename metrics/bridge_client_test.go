package metrics

import (
	"strings"
	"testing"

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
	state                                         int
}

func (f fakeClientSource) GetMetrics() client.Metrics {
	return client.Metrics{
		CacheHits: f.hits, CacheMisses: f.misses,
		APICallCount: f.calls, APIErrorCount: f.apiErrs,
		ValidationErrors: f.valErrs, CircuitBreakerOpen: f.cbOpen,
	}
}
func (f fakeClientSource) CircuitBreakerState() int { return f.state }

func TestClientBridgeEmitsSeries(t *testing.T) {
	src := fakeClientSource{
		hits: 7, misses: 3, calls: 10, apiErrs: 2, valErrs: 1, cbOpen: 4,
		state: 1,
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
