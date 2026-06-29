package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func scrape(m *Metrics) string {
	r := gin.New()
	r.GET("/metrics", m.Handler())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	return w.Body.String()
}

func TestMiddlewareRecordsRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/voice/find_stop", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/voice/find_stop", nil))

	body := scrape(m)
	if !strings.Contains(body, `http_requests_total{method="GET",route="/voice/find_stop",status="200"} 1`) {
		t.Errorf("missing/incorrect http_requests_total series:\n%s", body)
	}
	if !strings.Contains(body, "http_request_duration_seconds_bucket") {
		t.Errorf("missing duration histogram")
	}
}

func TestMiddlewareUnmatchedAndUnknownMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PROPFIND", "/nope", nil))

	body := scrape(m)
	if !strings.Contains(body, `route="unmatched"`) {
		t.Errorf("expected route=unmatched, got:\n%s", body)
	}
	if !strings.Contains(body, `method="unknown"`) {
		t.Errorf("expected method=unknown, got:\n%s", body)
	}
}

func TestSanitizeMethodLowercase(t *testing.T) {
	if got := sanitizeMethod("get"); got != "GET" {
		t.Errorf("sanitizeMethod(\"get\") = %q, want \"GET\"", got)
	}
}

func TestMiddlewareDurationLabels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/voice/find_stop", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/voice/find_stop", nil))

	body := scrape(m)
	if !strings.Contains(body, `http_request_duration_seconds_count{method="GET",route="/voice/find_stop"} 1`) {
		t.Errorf("missing duration count with labels:\n%s", body)
	}
}

func TestMiddlewareSkipsMetricsAndHealth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/metrics", m.Handler())
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })

	for _, p := range []string{"/metrics", "/health"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	}
	body := scrape(m)
	if strings.Contains(body, `route="/health"`) || strings.Contains(body, `route="/metrics"`) {
		t.Errorf("scrape/health traffic should be skipped:\n%s", body)
	}
}
