package health

import (
	"time"
)

// Status represents the overall health status
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusUnhealthy Status = "unhealthy"
)

// CheckResult represents the result of a health check
type CheckResult struct {
	Name        string            `json:"name"`
	Status      Status            `json:"status"`
	Message     string            `json:"message,omitempty"`
	Error       string            `json:"error,omitempty"`
	Duration    time.Duration     `json:"duration"`
	Timestamp   time.Time         `json:"timestamp"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	LastSuccess *time.Time        `json:"last_success,omitempty"`
	LastFailure *time.Time        `json:"last_failure,omitempty"`
}

// HealthResponse represents the overall health check response
type HealthResponse struct {
	Status       Status                    `json:"status"`
	Version      string                    `json:"version,omitempty"`
	Timestamp    time.Time                 `json:"timestamp"`
	Duration     time.Duration             `json:"duration"`
	Checks       map[string]CheckResult    `json:"checks,omitempty"`
	SystemInfo   *SystemInfo               `json:"system_info,omitempty"`
	Dependencies map[string]DependencyInfo `json:"dependencies,omitempty"`
}

// SystemInfo contains system-level health information
type SystemInfo struct {
	GoVersion      string        `json:"go_version"`
	Goroutines     int           `json:"goroutines"`
	Memory         MemoryInfo    `json:"memory"`
	Uptime         time.Duration `json:"uptime"`
	CPUUsage       float64       `json:"cpu_usage,omitempty"`
	StartTime      time.Time     `json:"start_time"`
	HealthyChecks  int           `json:"healthy_checks"`
	DegradedChecks int           `json:"degraded_checks"`
	FailedChecks   int           `json:"failed_checks"`
}

// MemoryInfo contains memory usage information
type MemoryInfo struct {
	Alloc        uint64  `json:"alloc"`         // bytes allocated and not yet freed
	TotalAlloc   uint64  `json:"total_alloc"`   // bytes allocated (even if freed)
	Sys          uint64  `json:"sys"`           // bytes obtained from system
	NumGC        uint32  `json:"num_gc"`        // number of garbage collections
	HeapAlloc    uint64  `json:"heap_alloc"`    // bytes allocated and not yet freed (same as Alloc)
	HeapSys      uint64  `json:"heap_sys"`      // bytes obtained from system for heap
	HeapInuse    uint64  `json:"heap_inuse"`    // bytes in in-use spans
	HeapReleased uint64  `json:"heap_released"` // bytes released to the OS
	UsagePercent float64 `json:"usage_percent"` // heap usage percentage
}

// DependencyInfo contains information about external dependencies
type DependencyInfo struct {
	Name           string                 `json:"name"`
	Status         Status                 `json:"status"`
	URL            string                 `json:"url,omitempty"`
	Version        string                 `json:"version,omitempty"`
	ResponseTime   time.Duration          `json:"response_time"`
	LastChecked    time.Time              `json:"last_checked"`
	SuccessRate    float64                `json:"success_rate"`
	CircuitBreaker string                 `json:"circuit_breaker,omitempty"`
	ErrorCount     int64                  `json:"error_count"`
	RequestCount   int64                  `json:"request_count"`
	CacheHitRate   float64                `json:"cache_hit_rate,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// HealthChecker interface for implementing health checks
type HealthChecker interface {
	Name() string
	Check() CheckResult
}

// HealthOption configures health check behavior
type HealthOption func(*HealthConfig)

// HealthConfig contains health check configuration
type HealthConfig struct {
	Timeout             time.Duration
	CacheTTL            time.Duration
	MaxConcurrentChecks int
	EnableSystemInfo    bool
	EnableMetrics       bool
}
