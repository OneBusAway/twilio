package metrics

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns a gin.HandlerFunc that serves the registry in Prometheus
// exposition format. It also registers the standard promhttp scrape counters
// (promhttp_metric_handler_requests_total) on the same registry.
func (m *Metrics) Handler() gin.HandlerFunc {
	h := promhttp.InstrumentMetricHandler(
		m.reg,
		promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}),
	)
	return gin.WrapH(h)
}
