// Package metrics provides Prometheus instrumentation for the application:
// a custom registry, HTTP middleware, and bridge collectors that surface
// already-tracked client/session counters at scrape time.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics owns the Prometheus registry and all registered collectors.
type Metrics struct {
	reg *prometheus.Registry
}

// New creates a Metrics with a private registry and the standard Go runtime
// and process collectors registered.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &Metrics{reg: reg}
}

// Registry exposes the underlying registry (for tests and registration).
func (m *Metrics) Registry() *prometheus.Registry {
	return m.reg
}
