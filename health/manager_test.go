package health

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// MockHealthChecker is a mock health checker for testing
type MockHealthChecker struct {
	name        string
	result      CheckResult
	checkDelay  time.Duration
	shouldPanic bool
}

func (m *MockHealthChecker) Name() string {
	return m.name
}

func (m *MockHealthChecker) Check() CheckResult {
	if m.checkDelay > 0 {
		time.Sleep(m.checkDelay)
	}

	if m.shouldPanic {
		panic("test panic")
	}

	return m.result
}

func TestNewManager(t *testing.T) {
	manager := NewManager()

	if manager == nil {
		t.Fatal("Expected manager to be created")
		return
	}

	if manager.config.Timeout != 5*time.Second {
		t.Errorf("Expected default timeout to be 5s, got %v", manager.config.Timeout)
	}

	if manager.config.CacheTTL != 30*time.Second {
		t.Errorf("Expected default cache TTL to be 30s, got %v", manager.config.CacheTTL)
	}

	if manager.config.MaxConcurrentChecks != 10 {
		t.Errorf("Expected default max concurrent checks to be 10, got %d", manager.config.MaxConcurrentChecks)
	}
}

func TestManagerWithOptions(t *testing.T) {
	manager := NewManager(
		WithTimeout(2*time.Second),
		WithCacheTTL(10*time.Second),
		WithMaxConcurrentChecks(5),
		WithSystemInfo(false),
		WithMetrics(false),
	)

	if manager.config.Timeout != 2*time.Second {
		t.Errorf("Expected timeout to be 2s, got %v", manager.config.Timeout)
	}

	if manager.config.CacheTTL != 10*time.Second {
		t.Errorf("Expected cache TTL to be 10s, got %v", manager.config.CacheTTL)
	}

	if manager.config.MaxConcurrentChecks != 5 {
		t.Errorf("Expected max concurrent checks to be 5, got %d", manager.config.MaxConcurrentChecks)
	}

	if manager.config.EnableSystemInfo {
		t.Error("Expected system info to be disabled")
	}

	if manager.config.EnableMetrics {
		t.Error("Expected metrics to be disabled")
	}
}

func TestAddChecker(t *testing.T) {
	manager := NewManager()

	checker := &MockHealthChecker{
		name: "test-checker",
		result: CheckResult{
			Status: StatusHealthy,
		},
	}

	manager.AddChecker(checker)

	checkers := manager.GetCheckers()
	if len(checkers) != 1 {
		t.Errorf("Expected 1 checker, got %d", len(checkers))
	}

	if checkers[0].Name() != "test-checker" {
		t.Errorf("Expected checker name to be 'test-checker', got %s", checkers[0].Name())
	}
}

func TestCheckHealthLiveness(t *testing.T) {
	manager := NewManager()

	ctx := context.Background()
	response := manager.CheckHealthLiveness(ctx)

	if response.Status != StatusHealthy {
		t.Errorf("Expected status to be healthy, got %s", response.Status)
	}

	if response.Checks == nil {
		t.Error("Expected checks to be present")
	}

	if len(response.Checks) == 0 {
		t.Error("Expected at least one check (system check)")
	}

	if response.Duration < 0 {
		t.Error("Expected duration to be non-negative")
	}
}

func TestCheckHealthWithHealthyCheckers(t *testing.T) {
	manager := NewManager()

	// Add healthy checkers
	manager.AddChecker(&MockHealthChecker{
		name: "checker1",
		result: CheckResult{
			Status:  StatusHealthy,
			Message: "All good",
		},
	})

	manager.AddChecker(&MockHealthChecker{
		name: "checker2",
		result: CheckResult{
			Status:  StatusHealthy,
			Message: "Also good",
		},
	})

	ctx := context.Background()
	response := manager.CheckHealth(ctx)

	if response.Status != StatusHealthy {
		t.Errorf("Expected overall status to be healthy, got %s", response.Status)
	}

	if len(response.Checks) < 2 {
		t.Errorf("Expected at least 2 checks, got %d", len(response.Checks))
	}

	if response.SystemInfo == nil {
		t.Error("Expected system info to be present")
	}
}

func TestCheckHealthWithDegradedCheckers(t *testing.T) {
	manager := NewManager()

	manager.AddChecker(&MockHealthChecker{
		name: "healthy-checker",
		result: CheckResult{
			Status: StatusHealthy,
		},
	})

	manager.AddChecker(&MockHealthChecker{
		name: "degraded-checker",
		result: CheckResult{
			Status:  StatusDegraded,
			Message: "Performance degraded",
		},
	})

	ctx := context.Background()
	response := manager.CheckHealth(ctx)

	if response.Status != StatusDegraded {
		t.Errorf("Expected overall status to be degraded, got %s", response.Status)
	}
}

func TestCheckHealthWithUnhealthyCheckers(t *testing.T) {
	manager := NewManager()

	manager.AddChecker(&MockHealthChecker{
		name: "healthy-checker",
		result: CheckResult{
			Status: StatusHealthy,
		},
	})

	manager.AddChecker(&MockHealthChecker{
		name: "unhealthy-checker",
		result: CheckResult{
			Status: StatusUnhealthy,
			Error:  "Something is broken",
		},
	})

	ctx := context.Background()
	response := manager.CheckHealth(ctx)

	if response.Status != StatusUnhealthy {
		t.Errorf("Expected overall status to be unhealthy, got %s", response.Status)
	}
}

func TestCheckHealthWithTimeout(t *testing.T) {
	manager := NewManager(WithTimeout(100 * time.Millisecond))

	// Add a slow checker that exceeds timeout
	manager.AddChecker(&MockHealthChecker{
		name:       "slow-checker",
		checkDelay: 200 * time.Millisecond,
		result: CheckResult{
			Status: StatusHealthy,
		},
	})

	ctx := context.Background()
	response := manager.CheckHealth(ctx)

	// The slow checker should timeout and be marked unhealthy
	if response.Status == StatusHealthy {
		t.Error("Expected status to not be healthy due to timeout")
	}

	slowCheck, exists := response.Checks["slow-checker"]
	if !exists {
		t.Fatal("Expected slow-checker to be in results")
	}

	if slowCheck.Status != StatusUnhealthy {
		t.Errorf("Expected slow checker to be unhealthy, got %s", slowCheck.Status)
	}

	if slowCheck.Error != "health check timeout" {
		t.Errorf("Expected timeout error, got %s", slowCheck.Error)
	}
}

func TestCheckHealthWithPanic(t *testing.T) {
	manager := NewManager()

	manager.AddChecker(&MockHealthChecker{
		name:        "panic-checker",
		shouldPanic: true,
		result: CheckResult{
			Status: StatusHealthy, // This won't be used due to panic
		},
	})

	ctx := context.Background()
	response := manager.CheckHealth(ctx)

	if response.Status == StatusHealthy {
		t.Error("Expected status to not be healthy due to panic")
	}

	panicCheck, exists := response.Checks["panic-checker"]
	if !exists {
		t.Fatal("Expected panic-checker to be in results")
	}

	if panicCheck.Status != StatusUnhealthy {
		t.Errorf("Expected panic checker to be unhealthy, got %s", panicCheck.Status)
	}

	if panicCheck.Error == "" {
		t.Error("Expected panic error message")
	}
}

func TestHealthCheckCaching(t *testing.T) {
	manager := NewManager(WithCacheTTL(1 * time.Second))

	callCount := 0
	manager.AddChecker(&MockHealthChecker{
		name: "cached-checker",
		result: CheckResult{
			Status: StatusHealthy,
		},
	})

	// Override the checker to count calls
	originalChecker := manager.checkers[0]
	manager.checkers[0] = &CountingChecker{
		HealthChecker: originalChecker,
		callCount:     &callCount,
	}

	ctx := context.Background()

	// First call
	manager.CheckHealth(ctx)
	if callCount != 1 {
		t.Errorf("Expected call count to be 1, got %d", callCount)
	}

	// Second call should use cache
	manager.CheckHealth(ctx)
	if callCount != 1 {
		t.Errorf("Expected call count to still be 1 (cached), got %d", callCount)
	}

	// Wait for cache to expire
	time.Sleep(1100 * time.Millisecond)

	// Third call should hit the checker again
	manager.CheckHealth(ctx)
	if callCount != 2 {
		t.Errorf("Expected call count to be 2 after cache expiry, got %d", callCount)
	}
}

func TestClearCache(t *testing.T) {
	manager := NewManager()

	manager.AddChecker(&MockHealthChecker{
		name: "test-checker",
		result: CheckResult{
			Status: StatusHealthy,
		},
	})

	// Run a check to populate cache
	ctx := context.Background()
	manager.CheckHealth(ctx)

	if manager.GetCacheSize() == 0 {
		t.Error("Expected cache to have entries after health check")
	}

	manager.ClearCache()

	if manager.GetCacheSize() != 0 {
		t.Errorf("Expected cache to be empty after clear, got size %d", manager.GetCacheSize())
	}
}

func TestGetStats(t *testing.T) {
	manager := NewManager()

	manager.AddChecker(&MockHealthChecker{
		name: "test-checker",
		result: CheckResult{
			Status: StatusHealthy,
		},
	})

	ctx := context.Background()
	manager.CheckHealth(ctx)

	stats := manager.GetStats()

	if stats["total_checks"] == nil {
		t.Error("Expected total_checks in stats")
	}

	if stats["uptime"] == nil {
		t.Error("Expected uptime in stats")
	}

	if stats["checker_count"] == nil {
		t.Error("Expected checker_count in stats")
	}

	checkerCount := stats["checker_count"].(int)
	if checkerCount != 1 {
		t.Errorf("Expected checker_count to be 1, got %d", checkerCount)
	}
}

func TestConcurrentHealthChecks(t *testing.T) {
	manager := NewManager(WithMaxConcurrentChecks(2))

	// Add multiple checkers
	for i := 0; i < 5; i++ {
		manager.AddChecker(&MockHealthChecker{
			name: fmt.Sprintf("checker-%d", i),
			result: CheckResult{
				Status: StatusHealthy,
			},
			checkDelay: 50 * time.Millisecond, // Small delay to test concurrency
		})
	}

	ctx := context.Background()
	start := time.Now()
	response := manager.CheckHealth(ctx)
	duration := time.Since(start)

	if response.Status != StatusHealthy {
		t.Errorf("Expected status to be healthy, got %s", response.Status)
	}

	// With max 2 concurrent checks and 5 checkers with 50ms delay each,
	// it should take at least 150ms (3 batches) but less than 250ms (sequential)
	if duration < 100*time.Millisecond {
		t.Errorf("Expected duration to be at least 100ms, got %v", duration)
	}

	if duration > 300*time.Millisecond {
		t.Errorf("Expected duration to be less than 300ms, got %v", duration)
	}
}

func TestContextCancellation(t *testing.T) {
	manager := NewManager()

	manager.AddChecker(&MockHealthChecker{
		name:       "slow-checker",
		checkDelay: 500 * time.Millisecond,
		result: CheckResult{
			Status: StatusHealthy,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context immediately
	cancel()

	response := manager.CheckHealth(ctx)

	// Should still return a response, but the checker should be marked as cancelled
	slowCheck, exists := response.Checks["slow-checker"]
	if !exists {
		t.Fatal("Expected slow-checker to be in results")
	}

	if slowCheck.Status != StatusUnhealthy {
		t.Errorf("Expected cancelled checker to be unhealthy, got %s", slowCheck.Status)
	}

	// The error could be either "context cancelled" or "health check timeout" depending on timing
	if slowCheck.Error != "context cancelled" && slowCheck.Error != "health check timeout" {
		t.Errorf("Expected context cancelled or timeout error, got %s", slowCheck.Error)
	}
}

// CountingChecker wraps a health checker to count calls
type CountingChecker struct {
	HealthChecker
	callCount *int
}

func (c *CountingChecker) Check() CheckResult {
	*c.callCount++
	return c.HealthChecker.Check()
}
