package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP handlers for health check endpoints
type Handler struct {
	manager     *Manager
	rateLimiter *RateLimiter
}

// RateLimiter provides simple rate limiting for health endpoints
type RateLimiter struct {
	mu        sync.Mutex
	requests  map[string][]time.Time
	maxReqs   int
	window    time.Duration
	cleanupAt time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(maxReqs int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		maxReqs:  maxReqs,
		window:   window,
	}
}

// Allow checks if a request is allowed for the given IP
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Clean up old entries periodically
	if now.After(rl.cleanupAt) {
		rl.cleanup(now)
		rl.cleanupAt = now.Add(rl.window)
	}

	// Get request history for this IP
	requests := rl.requests[ip]

	// Remove old requests outside the window
	cutoff := now.Add(-rl.window)
	valid := requests[:0]
	for _, req := range requests {
		if req.After(cutoff) {
			valid = append(valid, req)
		}
	}

	// Check if under limit
	if len(valid) >= rl.maxReqs {
		rl.requests[ip] = valid
		return false
	}

	// Add new request and allow
	rl.requests[ip] = append(valid, now)
	return true
}

// cleanup removes old entries from the rate limiter
func (rl *RateLimiter) cleanup(now time.Time) {
	cutoff := now.Add(-rl.window)
	for ip, requests := range rl.requests {
		valid := requests[:0]
		for _, req := range requests {
			if req.After(cutoff) {
				valid = append(valid, req)
			}
		}
		if len(valid) == 0 {
			delete(rl.requests, ip)
		} else {
			rl.requests[ip] = valid
		}
	}
}

// NewHandler creates a new health check handler
func NewHandler(manager *Manager) *Handler {
	return &Handler{
		manager:     manager,
		rateLimiter: NewRateLimiter(300, time.Minute), // 300 requests per minute per IP
	}
}

// rateLimitMiddleware provides rate limiting for health endpoints
func (h *Handler) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		if !h.rateLimiter.Allow(clientIP) {
			c.Header("Retry-After", "60")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": "too many health check requests",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// HealthHandler handles basic health check requests (liveness probe)
// GET /health
func (h *Handler) HealthHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	response := h.manager.CheckHealthLiveness(ctx)

	// Determine HTTP status code based on health status
	statusCode := http.StatusOK
	switch response.Status {
	case StatusHealthy:
		statusCode = http.StatusOK
	case StatusDegraded:
		statusCode = http.StatusOK // Still considered "alive" for liveness probes
	case StatusUnhealthy:
		statusCode = http.StatusServiceUnavailable
	}

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(statusCode, response)
}

// ReadinessHandler handles readiness check requests (readiness probe)
// GET /health/ready
func (h *Handler) ReadinessHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	response := h.manager.CheckHealthReadiness(ctx)

	// Determine HTTP status code based on health status
	statusCode := http.StatusOK
	switch response.Status {
	case StatusHealthy:
		statusCode = http.StatusOK
	case StatusDegraded:
		statusCode = http.StatusOK // Still ready to serve traffic with degraded performance
	case StatusUnhealthy:
		statusCode = http.StatusServiceUnavailable
	}

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(statusCode, response)
}

// DetailedHandler handles detailed health check requests
// GET /health/detailed
func (h *Handler) DetailedHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	response := h.manager.CheckHealthDetailed(ctx)

	statusCode := http.StatusOK
	switch response.Status {
	case StatusHealthy:
		statusCode = http.StatusOK
	case StatusDegraded:
		statusCode = http.StatusOK
	case StatusUnhealthy:
		statusCode = http.StatusServiceUnavailable
	}

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(statusCode, response)
}

// StatsHandler provides basic statistics
// GET /health/stats
func (h *Handler) StatsHandler(c *gin.Context) {
	stats := h.manager.GetStats()

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"stats":  stats,
	})
}

// ConfigHandler provides health check configuration information
// GET /health/config
func (h *Handler) ConfigHandler(c *gin.Context) {
	checkers := h.manager.GetCheckers()
	checkerNames := make([]string, len(checkers))
	for i, checker := range checkers {
		checkerNames[i] = checker.Name()
	}

	config := gin.H{
		"timeout":               h.manager.config.Timeout.String(),
		"cache_ttl":             h.manager.config.CacheTTL.String(),
		"max_concurrent_checks": h.manager.config.MaxConcurrentChecks,
		"system_info_enabled":   h.manager.config.EnableSystemInfo,
		"metrics_enabled":       h.manager.config.EnableMetrics,
		"registered_checkers":   checkerNames,
		"cache_size":            h.manager.GetCacheSize(),
	}

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, config)
}

// CacheHandler manages health check cache
// DELETE /health/cache - clears cache
// GET /health/cache - shows cache status
func (h *Handler) CacheHandler(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodDelete:
		h.manager.ClearCache()
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "Health check cache cleared",
		})
	case http.MethodGet:
		c.JSON(http.StatusOK, gin.H{
			"cache_size": h.manager.GetCacheSize(),
			"cache_ttl":  h.manager.config.CacheTTL.String(),
		})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error": "Method not allowed",
		})
	}
}

// SetupPublicRoutes registers the internet-facing endpoints: status-only
// liveness and readiness probes, rate-limited. Metrics and sensitive health
// detail are deliberately NOT registered here — see SetupInternalRoutes.
func (h *Handler) SetupPublicRoutes(router *gin.Engine) {
	rateLimited := router.Group("/")
	rateLimited.Use(h.rateLimitMiddleware())

	rateLimited.GET("/health", h.PublicLivenessHandler)
	rateLimited.GET("/health/ready", h.PublicReadinessHandler)
}

// SetupInternalRoutes registers the endpoints that must only be reachable on the
// internal metrics port: Prometheus metrics plus detailed/stats/config/cache.
// No rate limiting — the port is private and the scraper is trusted.
func (h *Handler) SetupInternalRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc) {
	router.GET("/metrics", metricsHandler)

	healthGroup := router.Group("/health")
	{
		healthGroup.GET("/detailed", h.DetailedHandler)
		healthGroup.GET("/stats", h.StatsHandler)
		healthGroup.GET("/config", h.ConfigHandler)
		healthGroup.GET("/cache", h.CacheHandler)
		healthGroup.DELETE("/cache", h.CacheHandler)
	}
}

// SetupRoutes is a temporary shim that keeps main.go compiling during the port
// split. It is removed once main wires the dedicated internal server.
func (h *Handler) SetupRoutes(router *gin.Engine, metricsHandler gin.HandlerFunc) {
	h.SetupPublicRoutes(router)
	h.SetupInternalRoutes(router, metricsHandler)
}

// HealthMiddleware provides middleware for automatic health monitoring
func (h *Handler) HealthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Process request
		c.Next()

		// Record metrics for non-health endpoints
		if c.Request.URL.Path != "/health" &&
			c.Request.URL.Path != "/health/ready" &&
			c.Request.URL.Path != "/health/detailed" &&
			c.Request.URL.Path != "/metrics" {

			duration := time.Since(start)
			statusCode := c.Writer.Status()

			// You could extend this to collect request metrics
			// For now, just log long-running requests
			if duration > 10*time.Second {
				// Log slow requests (in a real implementation, use a proper logger)
				// log.Printf("Slow request: %s %s took %v (status: %d)",
				//     c.Request.Method, c.Request.URL.Path, duration, statusCode)
				_ = statusCode // Avoid unused variable
			}
		}
	}
}

// Custom response writer to capture response data
type responseWriter struct {
	gin.ResponseWriter
	body []byte
}

func (w *responseWriter) Write(data []byte) (int, error) {
	w.body = append(w.body, data...)
	return w.ResponseWriter.Write(data)
}

// HealthResponseMiddleware captures response data for health monitoring
func (h *Handler) HealthResponseMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Create custom response writer
		writer := &responseWriter{
			ResponseWriter: c.Writer,
			body:           make([]byte, 0),
		}
		c.Writer = writer

		// Process request
		c.Next()

		// Monitor response for health indicators
		statusCode := c.Writer.Status()
		if statusCode >= 500 {
			// Server error - could indicate health issues
			// In a real implementation, you might want to:
			// - Increment error counters
			// - Trigger alerts
			// - Log detailed error information
			_ = statusCode // Placeholder to satisfy linter
		}
	}
}

// formatDuration formats a duration for human readability
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.String()
	}
	if d < time.Second {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

// HealthCheckResponse is a simplified response for basic health checks
type HealthCheckResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Uptime    string    `json:"uptime,omitempty"`
	Version   string    `json:"version,omitempty"`
}

// SimpleHealthHandler provides a very basic health check endpoint
func (h *Handler) SimpleHealthHandler(c *gin.Context) {
	uptime := time.Since(h.manager.startTime)

	response := HealthCheckResponse{
		Status:    "ok",
		Timestamp: time.Now(),
		Uptime:    formatDuration(uptime),
		Version:   "1.0.0",
	}

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, response)
}

// PingHandler provides a minimal ping endpoint
func (h *Handler) PingHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.String(http.StatusOK, "pong")
}

// SetupMinimalRoutes sets up only essential health check routes
func (h *Handler) SetupMinimalRoutes(router *gin.Engine) {
	router.GET("/health", h.SimpleHealthHandler)
	router.GET("/ping", h.PingHandler)
}

// JSONError represents a JSON error response
type JSONError struct {
	Error   string    `json:"error"`
	Message string    `json:"message,omitempty"`
	Time    time.Time `json:"timestamp"`
}

// statusCode maps a health Status to the probe HTTP status code: healthy and
// degraded are "up" (200); unhealthy is 503.
func statusCode(s Status) int {
	if s == StatusUnhealthy {
		return http.StatusServiceUnavailable
	}
	return http.StatusOK
}

// PublicLivenessHandler is the internet-facing liveness probe. It runs the same
// checks as HealthHandler to derive the status code but returns a status-only
// body, so the public port never exposes SystemInfo, per-check Metadata, or
// error strings.
// GET /health
func (h *Handler) PublicLivenessHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	status := h.manager.CheckHealthLiveness(ctx).Status
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(statusCode(status), gin.H{"status": status})
}

// PublicReadinessHandler is the internet-facing readiness probe — status-only
// body, same rationale as PublicLivenessHandler.
// GET /health/ready
func (h *Handler) PublicReadinessHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	status := h.manager.CheckHealthReadiness(ctx).Status
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Content-Type", "application/json")
	c.JSON(statusCode(status), gin.H{"status": status})
}
