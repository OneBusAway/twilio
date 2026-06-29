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

// RecordLookupOutcome records a stop-lookup outcome on both interactions_total
// and stop_lookups_total in one call. The interaction outcome and the lookup
// result use the same label, except that an "error" outcome maps to a
// "not_found" lookup result (the lookup yielded nothing usable). This keeps the
// two-counter mapping in one place so the SMS and voice call sites can't drift.
// Nil-safe.
func (m *Metrics) RecordLookupOutcome(channel, outcome, agency string) {
	m.RecordInteraction(channel, outcome)
	result := outcome
	if outcome == "error" {
		result = "not_found"
	}
	m.RecordStopLookup(result, agency)
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
