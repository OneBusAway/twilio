package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"oba-twilio/handlers/common"
)

type sessionSource interface {
	GetMetrics() *common.SessionMetrics
}

type sessionCollector struct {
	store string
	src   sessionSource

	active  *prometheus.Desc
	hits    *prometheus.Desc
	misses  *prometheus.Desc
	evicted *prometheus.Desc
	expired *prometheus.Desc
	created *prometheus.Desc
}

func newSessionCollector(store string, src sessionSource) *sessionCollector {
	labels := []string{"store"}
	return &sessionCollector{
		store:   store,
		src:     src,
		active:  prometheus.NewDesc("session_store_active_sessions", "Active sessions in the store.", labels, nil),
		hits:    prometheus.NewDesc("session_store_cache_hits_total", "Session-store cache hits.", labels, nil),
		misses:  prometheus.NewDesc("session_store_cache_misses_total", "Session-store cache misses.", labels, nil),
		evicted: prometheus.NewDesc("session_store_evictions_total", "Sessions evicted.", labels, nil),
		expired: prometheus.NewDesc("session_store_expired_total", "Sessions expired.", labels, nil),
		created: prometheus.NewDesc("session_store_created_total", "Sessions created.", labels, nil),
	}
}

func (c *sessionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.active
	ch <- c.hits
	ch <- c.misses
	ch <- c.evicted
	ch <- c.expired
	ch <- c.created
}

func (c *sessionCollector) Collect(ch chan<- prometheus.Metric) {
	m := c.src.GetMetrics()
	if m == nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(m.TotalSessions), c.store)
	ch <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(m.CacheHits), c.store)
	ch <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(m.CacheMisses), c.store)
	ch <- prometheus.MustNewConstMetric(c.evicted, prometheus.CounterValue, float64(m.Evictions), c.store)
	ch <- prometheus.MustNewConstMetric(c.expired, prometheus.CounterValue, float64(m.ExpiredSessions), c.store)
	ch <- prometheus.MustNewConstMetric(c.created, prometheus.CounterValue, float64(m.CreatedSessions), c.store)
}

// RegisterSessionBridge registers a bridge for one session store, tagged with a
// store label ("sms" or "voice").
func (m *Metrics) RegisterSessionBridge(store string, src sessionSource) {
	m.reg.MustRegister(newSessionCollector(store, src))
}
