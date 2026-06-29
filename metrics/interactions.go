package metrics

import "github.com/prometheus/client_golang/prometheus"

// RecordInteraction increments interactions_total. Nil-safe.
func (m *Metrics) RecordInteraction(channel, outcome string) {
	if m == nil || m.interactions == nil {
		return
	}
	m.interactions.WithLabelValues(channel, outcome).Inc()
}

// RecordStopLookup increments stop_lookups_total. Nil-safe.
func (m *Metrics) RecordStopLookup(result, agency string) {
	if m == nil || m.stopLookups == nil {
		return
	}
	m.stopLookups.WithLabelValues(result, agency).Inc()
}

func newInteractionMetrics(reg *prometheus.Registry) (*prometheus.CounterVec, *prometheus.CounterVec) {
	interactions := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "interactions_total",
			Help: "Total user interactions by channel and outcome.",
		},
		[]string{"channel", "outcome"},
	)
	stopLookups := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stop_lookups_total",
			Help: "Total stop lookups by result and matched agency prefix.",
		},
		[]string{"result", "agency"},
	)
	reg.MustRegister(interactions, stopLookups)
	return interactions, stopLookups
}
