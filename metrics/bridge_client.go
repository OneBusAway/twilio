package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"oba-twilio/client"
)

// clientSource is the read interface the client bridge needs.
type clientSource interface {
	GetMetrics() client.Metrics
	CircuitBreakerState() int
}

type clientCollector struct {
	src clientSource

	apiRequests   *prometheus.Desc
	apiErrors     *prometheus.Desc
	validationErr *prometheus.Desc
	cbOpen        *prometheus.Desc
	cbState       *prometheus.Desc
	cacheHits     *prometheus.Desc
	cacheMisses   *prometheus.Desc
	apiDuration   *prometheus.Desc
}

func newClientCollector(src clientSource) *clientCollector {
	return &clientCollector{
		src:           src,
		apiRequests:   prometheus.NewDesc("oba_api_requests_total", "Total OneBusAway API calls.", nil, nil),
		apiErrors:     prometheus.NewDesc("oba_api_errors_total", "Total OneBusAway API errors.", nil, nil),
		validationErr: prometheus.NewDesc("oba_validation_errors_total", "Total OneBusAway response validation errors.", nil, nil),
		cbOpen:        prometheus.NewDesc("oba_circuit_breaker_open_total", "Total times the circuit breaker opened.", nil, nil),
		cbState:       prometheus.NewDesc("oba_circuit_breaker_state", "Current circuit-breaker state (0=closed,1=open,2=half-open).", nil, nil),
		cacheHits:     prometheus.NewDesc("oba_cache_hits_total", "Total OneBusAway API cache hits.", nil, nil),
		cacheMisses:   prometheus.NewDesc("oba_cache_misses_total", "Total OneBusAway API cache misses.", nil, nil),
		apiDuration:   prometheus.NewDesc("oba_api_request_duration_seconds", "OneBusAway API request latency (sum/count derived from running totals).", nil, nil),
	}
}

func (c *clientCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.apiRequests
	ch <- c.apiErrors
	ch <- c.validationErr
	ch <- c.cbOpen
	ch <- c.cbState
	ch <- c.cacheHits
	ch <- c.cacheMisses
	ch <- c.apiDuration
}

func (c *clientCollector) Collect(ch chan<- prometheus.Metric) {
	m := c.src.GetMetrics()
	ch <- prometheus.MustNewConstMetric(c.apiRequests, prometheus.CounterValue, float64(m.APICallCount))
	ch <- prometheus.MustNewConstMetric(c.apiErrors, prometheus.CounterValue, float64(m.APIErrorCount))
	ch <- prometheus.MustNewConstMetric(c.validationErr, prometheus.CounterValue, float64(m.ValidationErrors))
	ch <- prometheus.MustNewConstMetric(c.cbOpen, prometheus.CounterValue, float64(m.CircuitBreakerOpen))
	ch <- prometheus.MustNewConstMetric(c.cbState, prometheus.GaugeValue, float64(c.src.CircuitBreakerState()))
	ch <- prometheus.MustNewConstMetric(c.cacheHits, prometheus.CounterValue, float64(m.CacheHits))
	ch <- prometheus.MustNewConstMetric(c.cacheMisses, prometheus.CounterValue, float64(m.CacheMisses))
	ch <- prometheus.MustNewConstHistogram(
		c.apiDuration,
		uint64(m.APICallCount),
		m.TotalResponseTime.Seconds(),
		nil, // no buckets: emits _sum, _count, and _bucket{le="+Inf"}
	)
}

// RegisterClientBridge registers a bridge collector that reads the OBA client's
// in-memory counters at scrape time.
func (m *Metrics) RegisterClientBridge(src clientSource) {
	m.reg.MustRegister(newClientCollector(src))
}
