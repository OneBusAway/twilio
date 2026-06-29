// Package metrics provides Prometheus instrumentation for the application:
// a custom registry, HTTP middleware, and bridge collectors that surface
// already-tracked client/session counters at scrape time.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics owns the Prometheus registry and all registered collectors.
// Use New() to construct — the zero value is unusable. The Record* methods are
// nil-safe, but Middleware and Handler require a fully constructed instance.
type Metrics struct {
	reg          *prometheus.Registry
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	interactions *prometheus.CounterVec
	stopLookups  *prometheus.CounterVec
}

// New creates a Metrics with a private registry and the standard Go runtime
// and process collectors registered.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	m := &Metrics{reg: reg}
	m.httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, route template, and status code.",
		},
		[]string{"method", "route", "status"},
	)
	m.httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method and route template.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
	reg.MustRegister(m.httpRequests, m.httpDuration)
	m.interactions, m.stopLookups = newInteractionMetrics(reg)
	return m
}
