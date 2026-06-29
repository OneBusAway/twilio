package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupTestHandler() (*Handler, *gin.Engine) {
	gin.SetMode(gin.TestMode)

	manager := NewManager(WithTimeout(1*time.Second), WithSystemInfo(true))
	manager.AddChecker(&MockHealthChecker{
		name:   "test-checker",
		result: CheckResult{Status: StatusHealthy, Message: "Test is healthy"},
	})

	handler := NewHandler(manager)
	router := gin.New()
	handler.SetupPublicRoutes(router)
	handler.SetupInternalRoutes(router, func(c *gin.Context) {})

	return handler, router
}

func TestPublicLivenessRouteIsSlim(t *testing.T) {
	_, router := setupTestHandler()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"checks", "system_info", "goroutines", "go_version", "metadata"} {
		if strings.Contains(body, leak) {
			t.Errorf("public /health leaked %q: %s", leak, body)
		}
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache, no-store, must-revalidate" {
		t.Errorf("expected no-cache header, got %s", cc)
	}
}

func TestPublicReadinessRouteIsSlim(t *testing.T) {
	_, router := setupTestHandler()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/health/ready", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "system_info") {
		t.Errorf("public /health/ready leaked system_info: %s", w.Body.String())
	}
}

func TestPublicRouterRejectsInternalRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(NewManager())
	public := gin.New()
	h.SetupPublicRoutes(public)

	for _, path := range []string{"/metrics", "/health/detailed", "/health/config", "/health/stats", "/health/cache"} {
		w := httptest.NewRecorder()
		public.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s should be 404 on the public router, got %d", path, w.Code)
		}
	}
}

func TestStatusCode(t *testing.T) {
	cases := []struct {
		status Status
		want   int
	}{
		{StatusHealthy, http.StatusOK},
		{StatusDegraded, http.StatusOK},
		{StatusUnhealthy, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		if got := statusCode(c.status); got != c.want {
			t.Errorf("statusCode(%q) = %d, want %d", c.status, got, c.want)
		}
	}
}

func TestDetailedHandler(t *testing.T) {
	_, router := setupTestHandler()

	req, _ := http.NewRequest("GET", "/health/detailed", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Status != StatusHealthy {
		t.Errorf("Expected status healthy, got %s", response.Status)
	}

	if response.SystemInfo == nil {
		t.Error("Expected system info to be present in detailed response")
	}
}

func TestMetricsRouteDelegatesToProvidedHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewHandler(NewManager())
	handler.SetupInternalRoutes(router, func(c *gin.Context) { c.String(200, "stubbed") })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 200 || w.Body.String() != "stubbed" {
		t.Fatalf("expected delegated handler, got %d %q", w.Code, w.Body.String())
	}
}

func TestStatsHandler(t *testing.T) {
	_, router := setupTestHandler()

	req, _ := http.NewRequest("GET", "/health/stats", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", response["status"])
	}

	if response["stats"] == nil {
		t.Error("Expected stats to be present")
	}
}

func TestConfigHandler(t *testing.T) {
	_, router := setupTestHandler()

	req, _ := http.NewRequest("GET", "/health/config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	expectedKeys := []string{"timeout", "cache_ttl", "max_concurrent_checks", "registered_checkers"}
	for _, key := range expectedKeys {
		if config[key] == nil {
			t.Errorf("Expected config key %s to be present", key)
		}
	}
}

func TestCacheHandler_GET(t *testing.T) {
	_, router := setupTestHandler()

	req, _ := http.NewRequest("GET", "/health/cache", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["cache_size"] == nil {
		t.Error("Expected cache_size in response")
	}

	if response["cache_ttl"] == nil {
		t.Error("Expected cache_ttl in response")
	}
}

func TestCacheHandler_DELETE(t *testing.T) {
	_, router := setupTestHandler()

	req, _ := http.NewRequest("DELETE", "/health/cache", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", response["status"])
	}

	if response["message"] == nil {
		t.Error("Expected message in response")
	}
}

func TestCacheHandler_InvalidMethod(t *testing.T) {
	_, router := setupTestHandler()

	req, _ := http.NewRequest("POST", "/health/cache", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// POST method returns 404 because no route is registered for it
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestHealthHandler_UnhealthyStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := NewManager()
	manager.AddChecker(&MockHealthChecker{
		name: "unhealthy-checker",
		result: CheckResult{
			Status: StatusUnhealthy,
			Error:  "Something is broken",
		},
	})

	handler := NewHandler(manager)
	router := gin.New()
	handler.SetupPublicRoutes(router)

	req, _ := http.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 for unhealthy service, got %d", w.Code)
	}

	var response HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Status != StatusUnhealthy {
		t.Errorf("Expected status unhealthy, got %s", response.Status)
	}
}

func TestHealthHandler_DegradedStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := NewManager()
	manager.AddChecker(&MockHealthChecker{
		name: "degraded-checker",
		result: CheckResult{
			Status:  StatusDegraded,
			Message: "Performance is degraded",
		},
	})

	handler := NewHandler(manager)
	router := gin.New()
	handler.SetupPublicRoutes(router)

	req, _ := http.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Degraded should still return 200 for liveness checks
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for degraded liveness check, got %d", w.Code)
	}

	var response HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Status != StatusDegraded {
		t.Errorf("Expected status degraded, got %s", response.Status)
	}
}

func TestSimpleHealthHandler(t *testing.T) {
	handler, _ := setupTestHandler()

	router := gin.New()
	router.GET("/health", handler.SimpleHealthHandler)

	req, _ := http.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response HealthCheckResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Status != "ok" {
		t.Errorf("Expected status 'ok', got %s", response.Status)
	}

	if response.Uptime == "" {
		t.Error("Expected uptime to be present")
	}
}

func TestPingHandler(t *testing.T) {
	handler, _ := setupTestHandler()

	router := gin.New()
	router.GET("/ping", handler.PingHandler)

	req, _ := http.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if body != "pong" {
		t.Errorf("Expected 'pong', got %s", body)
	}
}

func TestHealthMiddleware(t *testing.T) {
	handler, _ := setupTestHandler()

	router := gin.New()
	router.Use(handler.HealthMiddleware())
	router.GET("/test", func(c *gin.Context) {
		time.Sleep(50 * time.Millisecond) // Simulate some work
		c.String(200, "ok")
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// The middleware should not interfere with the response
	body := w.Body.String()
	if body != "ok" {
		t.Errorf("Expected 'ok', got %s", body)
	}
}

func TestSetupMinimalRoutes(t *testing.T) {
	handler, _ := setupTestHandler()

	router := gin.New()
	handler.SetupMinimalRoutes(router)

	// Test /health endpoint
	req, _ := http.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /health, got %d", w.Code)
	}

	// Test /ping endpoint
	req, _ = http.NewRequest("GET", "/ping", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /ping, got %d", w.Code)
	}

	body := w.Body.String()
	if body != "pong" {
		t.Errorf("Expected 'pong', got %s", body)
	}
}

func TestResponseHeaders(t *testing.T) {
	_, router := setupTestHandler()

	endpoints := []string{"/health", "/health/ready", "/health/detailed", "/health/stats", "/health/config"}

	for _, endpoint := range endpoints {
		req, _ := http.NewRequest("GET", endpoint, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Check cache control headers
		cacheControl := w.Header().Get("Cache-Control")
		if cacheControl != "no-cache, no-store, must-revalidate" {
			t.Errorf("Endpoint %s: expected no-cache header, got %s", endpoint, cacheControl)
		}

		// Check content type
		contentType := w.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Endpoint %s: expected JSON content type, got %s", endpoint, contentType)
		}
	}
}

func TestConcurrentHealthRequests(t *testing.T) {
	_, router := setupTestHandler()

	// Test concurrent requests to health endpoint
	const numRequests = 10
	done := make(chan bool, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			req, _ := http.NewRequest("GET", "/health", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			done <- true
		}()
	}

	// Wait for all requests to complete
	for i := 0; i < numRequests; i++ {
		<-done
	}
}

func TestPublicProbesOmitInternals(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager(WithTimeout(1*time.Second), WithSystemInfo(true))
	manager.AddChecker(&MockHealthChecker{
		name:   "secret-checker",
		result: CheckResult{Status: StatusHealthy, Message: "ok", Metadata: map[string]string{"secret": "x"}},
	})
	h := NewHandler(manager)
	router := gin.New()
	router.GET("/health", h.PublicLivenessHandler)
	// Readiness runs the registered checker, so its full body would include the
	// checker's "secret" metadata — proving the slim handler strips it.
	router.GET("/health/ready", h.PublicReadinessHandler)

	for _, path := range []string{"/health", "/health/ready"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))

		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: expected application/json, got %s", path, ct)
		}
		body := w.Body.String()
		for _, leak := range []string{"system_info", "checks", "goroutines", "go_version", "metadata", "secret"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s leaked %q: %s", path, leak, body)
			}
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s unmarshal: %v", path, err)
		}
		if resp["status"] != "healthy" {
			t.Errorf("%s status = %v, want healthy", path, resp["status"])
		}
	}
}
