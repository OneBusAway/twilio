package health

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// Manager coordinates health checks and provides caching
type Manager struct {
	checkers      []HealthChecker
	config        HealthConfig
	cache         map[string]cacheEntry
	cacheMutex    sync.RWMutex
	startTime     time.Time
	checkCount    int64
	failureCount  int64
	totalDuration time.Duration
	mu            sync.RWMutex
}

type cacheEntry struct {
	result    CheckResult
	expiresAt time.Time
}

// NewManager creates a new health check manager
func NewManager(options ...HealthOption) *Manager {
	config := HealthConfig{
		Timeout:             5 * time.Second,
		CacheTTL:            30 * time.Second,
		MaxConcurrentChecks: 10,
		EnableSystemInfo:    true,
		EnableMetrics:       true,
	}

	for _, opt := range options {
		opt(&config)
	}

	return &Manager{
		checkers:  make([]HealthChecker, 0),
		config:    config,
		cache:     make(map[string]cacheEntry),
		startTime: time.Now(),
	}
}

// WithTimeout sets the timeout for health checks
func WithTimeout(timeout time.Duration) HealthOption {
	return func(c *HealthConfig) {
		c.Timeout = timeout
	}
}

// WithCacheTTL sets the cache TTL for health check results
func WithCacheTTL(ttl time.Duration) HealthOption {
	return func(c *HealthConfig) {
		c.CacheTTL = ttl
	}
}

// WithMaxConcurrentChecks sets the maximum number of concurrent health checks
func WithMaxConcurrentChecks(max int) HealthOption {
	return func(c *HealthConfig) {
		c.MaxConcurrentChecks = max
	}
}

// WithSystemInfo enables/disables system information collection
func WithSystemInfo(enabled bool) HealthOption {
	return func(c *HealthConfig) {
		c.EnableSystemInfo = enabled
	}
}

// WithMetrics enables/disables metrics collection
func WithMetrics(enabled bool) HealthOption {
	return func(c *HealthConfig) {
		c.EnableMetrics = enabled
	}
}

// AddChecker adds a health checker to the manager
func (m *Manager) AddChecker(checker HealthChecker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkers = append(m.checkers, checker)
}

// GetCheckers returns all registered health checkers
func (m *Manager) GetCheckers() []HealthChecker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	checkers := make([]HealthChecker, len(m.checkers))
	copy(checkers, m.checkers)
	return checkers
}

// CheckHealth performs all health checks
func (m *Manager) CheckHealth(ctx context.Context) HealthResponse {
	startTime := time.Now()

	results := m.runChecks(ctx)

	// Calculate overall status
	status := m.calculateOverallStatus(results)

	// Collect system information if enabled
	var systemInfo *SystemInfo
	if m.config.EnableSystemInfo {
		systemInfo = m.collectSystemInfo(results)
	}

	// Update metrics
	if m.config.EnableMetrics {
		m.updateMetrics(results, time.Since(startTime))
	}

	return HealthResponse{
		Status:     status,
		Version:    "1.0.0", // Could be injected at build time
		Timestamp:  time.Now(),
		Duration:   time.Since(startTime),
		Checks:     results,
		SystemInfo: systemInfo,
	}
}

// CheckHealthLiveness performs minimal health checks for liveness probes
func (m *Manager) CheckHealthLiveness(ctx context.Context) HealthResponse {
	startTime := time.Now()

	// Only run critical checks for liveness
	results := make(map[string]CheckResult)

	// Basic system check
	systemCheck := &SystemHealthChecker{}
	results[systemCheck.Name()] = systemCheck.Check()

	status := m.calculateOverallStatus(results)

	return HealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Duration:  time.Since(startTime),
		Checks:    results,
	}
}

// CheckHealthReadiness performs comprehensive health checks for readiness probes
func (m *Manager) CheckHealthReadiness(ctx context.Context) HealthResponse {
	return m.CheckHealth(ctx)
}

// CheckHealthDetailed performs all health checks with detailed system information
func (m *Manager) CheckHealthDetailed(ctx context.Context) HealthResponse {
	startTime := time.Now()

	results := m.runChecks(ctx)
	status := m.calculateOverallStatus(results)

	// Always collect detailed system info
	systemInfo := m.collectSystemInfo(results)

	// Collect dependency information
	dependencies := m.collectDependencyInfo()

	// Update metrics
	if m.config.EnableMetrics {
		m.updateMetrics(results, time.Since(startTime))
	}

	return HealthResponse{
		Status:       status,
		Version:      "1.0.0",
		Timestamp:    time.Now(),
		Duration:     time.Since(startTime),
		Checks:       results,
		SystemInfo:   systemInfo,
		Dependencies: dependencies,
	}
}

// runChecks executes all health checks with concurrency control and caching
func (m *Manager) runChecks(ctx context.Context) map[string]CheckResult {
	results := make(map[string]CheckResult)

	// Use semaphore to limit concurrent checks
	sem := make(chan struct{}, m.config.MaxConcurrentChecks)
	var wg sync.WaitGroup
	var resultsMutex sync.Mutex

	checkers := m.GetCheckers()

	for _, checker := range checkers {
		wg.Add(1)
		go func(c HealthChecker) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				resultsMutex.Lock()
				results[c.Name()] = CheckResult{
					Name:      c.Name(),
					Status:    StatusUnhealthy,
					Error:     "context cancelled",
					Duration:  0,
					Timestamp: time.Now(),
				}
				resultsMutex.Unlock()
				return
			}

			// Check cache first
			if cached, found := m.getCachedResult(c.Name()); found {
				resultsMutex.Lock()
				results[c.Name()] = cached
				resultsMutex.Unlock()
				return
			}

			// Run the check with timeout
			checkCtx, cancel := context.WithTimeout(ctx, m.config.Timeout)
			defer cancel()

			result := m.runSingleCheck(checkCtx, c)

			// Cache the result
			m.cacheResult(c.Name(), result)

			resultsMutex.Lock()
			results[c.Name()] = result
			resultsMutex.Unlock()
		}(checker)
	}

	wg.Wait()
	return results
}

// runSingleCheck executes a single health check with timeout and error handling
func (m *Manager) runSingleCheck(ctx context.Context, checker HealthChecker) CheckResult {
	startTime := time.Now()

	// Create a channel to receive the result
	resultChan := make(chan CheckResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultChan <- CheckResult{
					Name:      checker.Name(),
					Status:    StatusUnhealthy,
					Error:     fmt.Sprintf("panic during health check: %v", r),
					Duration:  time.Since(startTime),
					Timestamp: time.Now(),
				}
			}
		}()

		result := checker.Check()
		result.Duration = time.Since(startTime)
		result.Timestamp = time.Now()
		resultChan <- result
	}()

	select {
	case result := <-resultChan:
		return result
	case <-ctx.Done():
		return CheckResult{
			Name:      checker.Name(),
			Status:    StatusUnhealthy,
			Error:     "health check timeout",
			Duration:  time.Since(startTime),
			Timestamp: time.Now(),
		}
	}
}

// getCachedResult retrieves a cached health check result
func (m *Manager) getCachedResult(name string) (CheckResult, bool) {
	m.cacheMutex.RLock()
	defer m.cacheMutex.RUnlock()

	entry, exists := m.cache[name]
	if !exists || time.Now().After(entry.expiresAt) {
		return CheckResult{}, false
	}

	return entry.result, true
}

// cacheResult stores a health check result in the cache
func (m *Manager) cacheResult(name string, result CheckResult) {
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()

	m.cache[name] = cacheEntry{
		result:    result,
		expiresAt: time.Now().Add(m.config.CacheTTL),
	}
}

// calculateOverallStatus determines the overall health status
func (m *Manager) calculateOverallStatus(results map[string]CheckResult) Status {
	if len(results) == 0 {
		return StatusUnhealthy
	}

	healthy := 0
	degraded := 0
	unhealthy := 0

	for _, result := range results {
		switch result.Status {
		case StatusHealthy:
			healthy++
		case StatusDegraded:
			degraded++
		case StatusUnhealthy:
			unhealthy++
		}
	}

	// If any critical checks are unhealthy, overall status is unhealthy
	if unhealthy > 0 {
		return StatusUnhealthy
	}

	// If there are degraded checks, overall status is degraded
	if degraded > 0 {
		return StatusDegraded
	}

	return StatusHealthy
}

// collectSystemInfo gathers system-level health information
func (m *Manager) collectSystemInfo(results map[string]CheckResult) *SystemInfo {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	healthy := 0
	degraded := 0
	failed := 0

	for _, result := range results {
		switch result.Status {
		case StatusHealthy:
			healthy++
		case StatusDegraded:
			degraded++
		case StatusUnhealthy:
			failed++
		}
	}

	// Calculate memory usage percentage
	usagePercent := 0.0
	if memStats.HeapSys > 0 {
		usagePercent = float64(memStats.HeapInuse) / float64(memStats.HeapSys) * 100
	}

	return &SystemInfo{
		GoVersion:  runtime.Version(),
		Goroutines: runtime.NumGoroutine(),
		Memory: MemoryInfo{
			Alloc:        memStats.Alloc,
			TotalAlloc:   memStats.TotalAlloc,
			Sys:          memStats.Sys,
			NumGC:        memStats.NumGC,
			HeapAlloc:    memStats.HeapAlloc,
			HeapSys:      memStats.HeapSys,
			HeapInuse:    memStats.HeapInuse,
			HeapReleased: memStats.HeapReleased,
			UsagePercent: usagePercent,
		},
		Uptime:         time.Since(m.startTime),
		StartTime:      m.startTime,
		HealthyChecks:  healthy,
		DegradedChecks: degraded,
		FailedChecks:   failed,
	}
}

// collectDependencyInfo gathers information about external dependencies
func (m *Manager) collectDependencyInfo() map[string]DependencyInfo {
	dependencies := make(map[string]DependencyInfo)

	checkers := m.GetCheckers()
	for _, checker := range checkers {
		switch c := checker.(type) {
		case *OneBusAwayHealthChecker:
			if c.client != nil {
				metrics := c.client.GetMetrics()
				errorRate := 0.0
				if metrics.APICallCount > 0 {
					errorRate = float64(metrics.APIErrorCount) / float64(metrics.APICallCount)
				}

				avgResponseTime := time.Duration(0)
				if metrics.APICallCount > 0 {
					avgResponseTime = metrics.TotalResponseTime / time.Duration(metrics.APICallCount)
				}

				dependencies["onebusaway_api"] = DependencyInfo{
					Name:         "OneBusAway API",
					Status:       StatusHealthy, // Would be determined by thresholds
					ResponseTime: avgResponseTime,
					LastChecked:  time.Now(),
					SuccessRate:  (1.0 - errorRate) * 100,
					ErrorCount:   metrics.APIErrorCount,
					RequestCount: metrics.APICallCount,
					Metadata: map[string]interface{}{
						"cache_hit_rate":        float64(metrics.CacheHits) / float64(metrics.CacheHits+metrics.CacheMisses) * 100,
						"circuit_breaker_opens": metrics.CircuitBreakerOpen,
					},
				}
			}
		case *SessionStoreHealthChecker:
			if c.store != nil {
				metrics := c.store.GetMetrics()
				dependencies["session_store"] = DependencyInfo{
					Name:         "Session Store",
					Status:       StatusHealthy,
					LastChecked:  time.Now(),
					RequestCount: metrics.CacheHits + metrics.CacheMisses,
					Metadata: map[string]interface{}{
						"total_sessions": metrics.TotalSessions,
						"memory_usage":   metrics.MemoryUsage,
						"evictions":      metrics.Evictions,
					},
				}
			}
		}
	}

	return dependencies
}

// updateMetrics updates internal metrics
func (m *Manager) updateMetrics(results map[string]CheckResult, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.checkCount++
	m.totalDuration += duration

	for _, result := range results {
		if result.Status != StatusHealthy {
			m.failureCount++
		}
	}
}

// ClearCache clears all cached health check results
func (m *Manager) ClearCache() {
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()

	m.cache = make(map[string]cacheEntry)
}

// GetCacheSize returns the current cache size
func (m *Manager) GetCacheSize() int {
	m.cacheMutex.RLock()
	defer m.cacheMutex.RUnlock()

	return len(m.cache)
}

// GetStats returns basic health check statistics
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]interface{}{
		"total_checks":     m.checkCount,
		"total_failures":   m.failureCount,
		"average_duration": float64(0),
		"uptime":           time.Since(m.startTime).Seconds(),
		"cache_size":       m.GetCacheSize(),
		"checker_count":    len(m.checkers),
	}

	if m.checkCount > 0 {
		stats["average_duration"] = float64(m.totalDuration.Nanoseconds()) / float64(m.checkCount) / 1e9
		stats["success_rate"] = float64(m.checkCount-m.failureCount) / float64(m.checkCount) * 100
	}

	return stats
}
