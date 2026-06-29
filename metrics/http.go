package metrics

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var httpMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {},
	"HEAD": {}, "OPTIONS": {}, "CONNECT": {}, "TRACE": {},
}

// sanitizeMethod bounds the method label to a fixed allow-list; anything else
// (including arbitrary attacker-supplied verbs) collapses to "unknown". Real
// HTTP methods are already uppercase, so the lookup happens before any
// allocation; ToUpper only runs for the rare non-canonical verb.
func sanitizeMethod(method string) string {
	if _, ok := httpMethods[method]; ok {
		return method
	}
	if m := strings.ToUpper(method); m != method {
		if _, ok := httpMethods[m]; ok {
			return m
		}
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

		start := time.Now()
		c.Next()

		// Resolve labels once, after routing: c.FullPath() is only populated
		// once Gin has matched the route.
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		method := sanitizeMethod(c.Request.Method)

		m.httpRequests.WithLabelValues(method, route, strconv.Itoa(c.Writer.Status())).Inc()
		m.httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	}
}
