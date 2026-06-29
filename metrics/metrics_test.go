package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandlerExposesRuntimeMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()

	r := gin.New()
	r.GET("/metrics", m.Handler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"go_goroutines", "process_", "# TYPE"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
