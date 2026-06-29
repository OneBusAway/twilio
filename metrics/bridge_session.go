package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"oba-twilio/handlers/common"
)

type sessionSource interface {
	GetMetrics() *common.SessionMetrics
}

type sessionCollector struct {
	src sessionSource

	active  *prometheus.Desc
	hits    *prometheus.Desc
	misses  *prometheus.Desc
	evicted *prometheus.Desc
	expired *prometheus.Desc
	created *prometheus.Desc
}

func newSessionCollector(store string, src sessionSource) *sessionCollector {
	constLabels := prometheus.Labels{"store": store}
	return &sessionCollector{
		src:     src,
		active:  prometheus.NewDesc("session_store_active_sessions", "Active sessions in the store.", nil, constLabels),
		hits:    prometheus.NewDesc("session_store_cache_hits_total", "Session-store cache hits.", nil, constLabels),
		misses:  prometheus.NewDesc("session_store_cache_misses_total", "Session-store cache misses.", nil, constLabels),
		evicted: prometheus.NewDesc("session_store_evictions_total", "Sessions evicted.", nil, constLabels),
		expired: prometheus.NewDesc("session_store_expired_total", "Sessions expired.", nil, constLabels),
		created: prometheus.NewDesc("session_store_created_total", "Sessions created.", nil, constLabels),
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
	ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(m.TotalSessions))
	ch <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(m.CacheHits))
	ch <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(m.CacheMisses))
	ch <- prometheus.MustNewConstMetric(c.evicted, prometheus.CounterValue, float64(m.Evictions))
	ch <- prometheus.MustNewConstMetric(c.expired, prometheus.CounterValue, float64(m.ExpiredSessions))
	ch <- prometheus.MustNewConstMetric(c.created, prometheus.CounterValue, float64(m.CreatedSessions))
}

// RegisterSessionBridge registers a bridge for one session store, tagged with a
// store label ("sms" or "voice").
func (m *Metrics) RegisterSessionBridge(store string, src sessionSource) {
	m.reg.MustRegister(newSessionCollector(store, src))
}
