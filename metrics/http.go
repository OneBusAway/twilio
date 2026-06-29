package metrics

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

var httpMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {},
	"HEAD": {}, "OPTIONS": {}, "CONNECT": {}, "TRACE": {},
}

// sanitizeMethod bounds the method label to a fixed allow-list; anything else
// (including arbitrary attacker-supplied verbs) collapses to "unknown".
func sanitizeMethod(method string) string {
	m := strings.ToUpper(method)
	if _, ok := httpMethods[m]; ok {
		return m
	}
	return "unknown"
}

// Middleware records HTTP request counts and durations. It skips /metrics and
// /health* so scrape and probe traffic don't dominate the series.
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/metrics" || strings.HasPrefix(path, "/health") {
			c.Next()
			return
		}

		timer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			m.httpDuration.WithLabelValues(sanitizeMethod(c.Request.Method), route).Observe(v)
		}))
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		m.httpRequests.WithLabelValues(
			sanitizeMethod(c.Request.Method),
			route,
			strconv.Itoa(c.Writer.Status()),
		).Inc()
		timer.ObserveDuration()
	}
}
