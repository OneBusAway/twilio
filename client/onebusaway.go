package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"oba-twilio/models"
)

const (
	// apiTimeoutSeconds defines the maximum time to wait for API operations
	// Set to 30 seconds to balance responsiveness with reliability for mobile users
	apiTimeoutSeconds = 30

	// maxConcurrentRequests limits parallel API calls to prevent overwhelming the server
	// Set to 10 to balance performance with server resource conservation
	maxConcurrentRequests = 10

	// cacheTTLMinutes defines how long to cache API responses
	cacheTTLMinutes = 5

	// arrivalsCacheTTLMinutes defines how long to cache arrival data (shorter due to time-sensitive nature)
	arrivalsCacheTTLMinutes = 1

	// coverageCacheTTLMinutes defines how long to cache coverage data (longer as it changes infrequently)
	coverageCacheTTLMinutes = 60

	// agenciesCacheTTLMinutes defines how long to cache agency IDs used for stop searching
	// (we refresh once per day as requested).
	agenciesCacheTTLMinutes = 24 * 60

	// maxCacheEntries limits the number of cached responses
	maxCacheEntries = 1000

	// Circuit breaker constants
	circuitBreakerFailureThreshold = 5
	circuitBreakerTimeout          = 60 * time.Second
	circuitBreakerRetryTimeout     = 30 * time.Second
)

const (
	coverageAgencyIDsCacheKey = "coverage_agency_ids"
)

// ClientConfig holds configuration for the OneBusAway client
type ClientConfig struct {
	AgencyPriority  []string      `json:"agency_priority"`
	DefaultAgencies []string      `json:"default_agencies"`
	APITimeout      time.Duration `json:"api_timeout,omitempty"` // Override default API timeout for testing
}

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// CircuitBreaker implements the circuit breaker pattern for external API protection
type CircuitBreaker struct {
	mutex           sync.RWMutex
	failureCount    int
	lastFailureTime time.Time
	state           CircuitState
}

// NewCircuitBreaker creates a new circuit breaker instance
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state: CircuitClosed,
	}
}

// Call executes a function with circuit breaker protection
func (cb *CircuitBreaker) Call(fn func() error) error {
	if !cb.canAttempt() {
		return fmt.Errorf("circuit breaker is open")
	}

	err := fn()
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

// canAttempt determines if a call can be attempted based on circuit breaker state
func (cb *CircuitBreaker) canAttempt() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		return time.Since(cb.lastFailureTime) >= circuitBreakerRetryTimeout
	case CircuitHalfOpen:
		return true
	default:
		return false
	}
}

// recordSuccess records a successful call and potentially closes the circuit
func (cb *CircuitBreaker) recordSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount = 0
	cb.state = CircuitClosed
}

// recordFailure records a failed call and potentially opens the circuit
func (cb *CircuitBreaker) recordFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	if cb.failureCount >= circuitBreakerFailureThreshold {
		cb.state = CircuitOpen
	} else if cb.state == CircuitHalfOpen {
		cb.state = CircuitOpen
	}
}

// getDefaultConfig returns the default configuration with standard agency priorities
func getDefaultConfig() *ClientConfig {
	return &ClientConfig{
		// Include common Puget Sound agency IDs first (Metro, Sound Transit, Pierce Transit),
		// then fall back to the broader default list.
		AgencyPriority:  []string{"1", "40", "29", "2", "4", "5", "6", "7", "3", "9"},
		DefaultAgencies: []string{"1", "40", "29", "2", "4", "5", "6", "7", "3", "9"},
	}
}

// getEffectiveTimeout returns the configured timeout or the default if not set
func (c *OneBusAwayClient) getEffectiveTimeout() time.Duration {
	if c.config != nil && c.config.APITimeout > 0 {
		return c.config.APITimeout
	}
	return time.Duration(apiTimeoutSeconds) * time.Second
}

// validateConfig validates the client configuration for required fields and data integrity
func validateConfig(config *ClientConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	if len(config.DefaultAgencies) == 0 {
		return fmt.Errorf("DefaultAgencies cannot be empty")
	}

	// Validate that all agency IDs are non-empty strings
	for i, agency := range config.DefaultAgencies {
		if strings.TrimSpace(agency) == "" {
			return fmt.Errorf("DefaultAgencies[%d] cannot be empty", i)
		}
	}

	// If AgencyPriority is provided, validate it as well
	if config.AgencyPriority != nil {
		for i, agency := range config.AgencyPriority {
			if strings.TrimSpace(agency) == "" {
				return fmt.Errorf("AgencyPriority[%d] cannot be empty", i)
			}
		}
	}

	return nil
}

// Metrics holds performance and operational metrics for the OneBusAway client
type Metrics struct {
	mutex              sync.RWMutex
	CacheHits          int64         `json:"cache_hits"`
	CacheMisses        int64         `json:"cache_misses"`
	APICallCount       int64         `json:"api_call_count"`
	APIErrorCount      int64         `json:"api_error_count"`
	TotalResponseTime  time.Duration `json:"total_response_time"`
	CircuitBreakerOpen int64         `json:"circuit_breaker_open"`
	ValidationErrors   int64         `json:"validation_errors"`
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{}
}

// IncrementCacheHits increments the cache hit counter
func (m *Metrics) IncrementCacheHits() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.CacheHits++
}

// IncrementCacheMisses increments the cache miss counter
func (m *Metrics) IncrementCacheMisses() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.CacheMisses++
}

// IncrementAPICall increments the API call counter and records response time
func (m *Metrics) IncrementAPICall(responseTime time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.APICallCount++
	m.TotalResponseTime += responseTime
}

// IncrementAPIError increments the API error counter
func (m *Metrics) IncrementAPIError() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.APIErrorCount++
}

// IncrementCircuitBreakerOpen increments the circuit breaker open counter
func (m *Metrics) IncrementCircuitBreakerOpen() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.CircuitBreakerOpen++
}

// IncrementValidationErrors increments the validation error counter
func (m *Metrics) IncrementValidationErrors() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.ValidationErrors++
}

// GetMetrics returns a copy of the current metrics
func (m *Metrics) GetMetrics() Metrics {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return Metrics{
		CacheHits:          m.CacheHits,
		CacheMisses:        m.CacheMisses,
		APICallCount:       m.APICallCount,
		APIErrorCount:      m.APIErrorCount,
		TotalResponseTime:  m.TotalResponseTime,
		CircuitBreakerOpen: m.CircuitBreakerOpen,
		ValidationErrors:   m.ValidationErrors,
	}
}

// GetCacheHitRate returns the cache hit rate as a percentage
func (m *Metrics) GetCacheHitRate() float64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	total := m.CacheHits + m.CacheMisses
	if total == 0 {
		return 0.0
	}
	return float64(m.CacheHits) / float64(total) * 100.0
}

// GetAverageResponseTime returns the average API response time
func (m *Metrics) GetAverageResponseTime() time.Duration {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.APICallCount == 0 {
		return 0
	}
	return m.TotalResponseTime / time.Duration(m.APICallCount)
}

// GetErrorRate returns the API error rate as a percentage
func (m *Metrics) GetErrorRate() float64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.APICallCount == 0 {
		return 0.0
	}
	return float64(m.APIErrorCount) / float64(m.APICallCount) * 100.0
}

// CacheEntry represents a cached API response with expiration
type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

// APICache provides thread-safe caching with TTL
type APICache struct {
	mutex   sync.RWMutex
	entries map[string]CacheEntry
}

// NewAPICache creates a new cache instance
func NewAPICache() *APICache {
	return &APICache{
		entries: make(map[string]CacheEntry),
	}
}

// Get retrieves a cached entry if it exists and hasn't expired
func (c *APICache) Get(key string) (interface{}, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	entry, exists := c.entries[key]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry.Data, true
}

// GetExpired retrieves a cached entry even if it has expired (for graceful degradation)
func (c *APICache) GetExpired(key string) (interface{}, bool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return nil, false, false
	}

	expired := time.Now().After(entry.ExpiresAt)
	return entry.Data, true, expired
}

// Set stores a value in the cache with TTL
func (c *APICache) Set(key string, value interface{}, ttl time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Evict oldest entries if cache is full
	if len(c.entries) >= maxCacheEntries {
		c.evictOldest()
	}

	c.entries[key] = CacheEntry{
		Data:      value,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// evictOldest removes the oldest entry from the cache
func (c *APICache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.entries {
		if oldestKey == "" || entry.ExpiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.ExpiresAt
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

type OneBusAwayClient struct {
	BaseURL        string
	APIKey         string
	Client         *http.Client
	coverageArea   *models.CoverageArea
	cache          *APICache
	config         *ClientConfig
	circuitBreaker *CircuitBreaker
	metrics        *Metrics
	// agencyMu guards config.AgencyPriority, which is read by getAgencyList (from
	// goroutines spawned by FindAllMatchingStops) and written by
	// ensureAgencyIDsForSearch, InitializeCoverage, and SetAgencyPriority.
	agencyMu sync.RWMutex
}

func NewOneBusAwayClient(baseURL, apiKey string) *OneBusAwayClient {
	return &OneBusAwayClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache:          NewAPICache(),
		config:         getDefaultConfig(),
		circuitBreaker: NewCircuitBreaker(),
		metrics:        NewMetrics(),
	}
}

// NewOneBusAwayClientWithConfig creates a client with custom configuration
func NewOneBusAwayClientWithConfig(baseURL, apiKey string, config *ClientConfig) (*OneBusAwayClient, error) {
	if config == nil {
		config = getDefaultConfig()
	}

	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &OneBusAwayClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache:          NewAPICache(),
		config:         config,
		circuitBreaker: NewCircuitBreaker(),
		metrics:        NewMetrics(),
	}, nil
}

// getAgencyList returns the configured agency list with priority ordering.
//
// Writers always replace the AgencyPriority slice (never mutate it in place), so
// the returned slice header is safe to use after the read lock is released.
func (c *OneBusAwayClient) getAgencyList() []string {
	c.agencyMu.RLock()
	defer c.agencyMu.RUnlock()
	if len(c.config.AgencyPriority) > 0 {
		return c.config.AgencyPriority
	}
	return c.config.DefaultAgencies
}

// storeAgencyPriority replaces the agency priority list under agencyMu.
func (c *OneBusAwayClient) storeAgencyPriority(ids []string) {
	c.agencyMu.Lock()
	defer c.agencyMu.Unlock()
	c.config.AgencyPriority = ids
}

// logAgencyIDsForSearch writes the agency ID list used for stop lookup (from cache or API).
func logAgencyIDsForSearch(source string, ids []string) {
	if len(ids) == 0 {
		log.Printf("[OBA] agency search list (%s): <empty>", source)
		return
	}
	log.Printf("[OBA] agency search list (%s, %d): %s", source, len(ids), strings.Join(ids, ", "))
}

// extractAgencyIDsFromCoverageList returns unique agency IDs from agencies-with-coverage data.list, preserving API order.
func extractAgencyIDsFromCoverageList(list []models.AgencyCoverageRow) []string {
	ids := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, a := range list {
		id := strings.TrimSpace(a.AgencyID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// ensureAgencyIDsForSearch refreshes the agency ID list used by FindAllMatchingStops.
// It is cached for ~1 day and sourced from the OneBusAway agencies-with-coverage endpoint.
func (c *OneBusAwayClient) ensureAgencyIDsForSearch() error {
	// 1) Fast path: valid cache
	if cached, found := c.cache.Get(coverageAgencyIDsCacheKey); found {
		if ids, ok := cached.([]string); ok && len(ids) > 0 {
			c.storeAgencyPriority(ids)
			logAgencyIDsForSearch("cache hit (coverage_agency_ids)", ids)
			return nil
		}
	}

	// 2) Capture expired cached data as a fallback, but do NOT short-circuit: we
	// still attempt a refresh below so the daily refresh actually happens. Stale
	// IDs are only applied if that refresh fails (graceful degradation).
	var staleIDs []string
	if cachedData, found, _ := c.cache.GetExpired(coverageAgencyIDsCacheKey); found {
		if ids, ok := cachedData.([]string); ok && len(ids) > 0 {
			staleIDs = ids
		}
	}

	// degradeToStale applies stale agency IDs when a refresh fails so stop search
	// keeps working; it returns nil if stale IDs exist, otherwise the refresh error.
	degradeToStale := func(reason string, refreshErr error) error {
		if len(staleIDs) > 0 {
			c.storeAgencyPriority(staleIDs)
			logAgencyIDsForSearch("stale fallback (coverage_agency_ids)", staleIDs)
			log.Printf("[OBA] agencies-with-coverage refresh failed (%s); continuing with stale agency IDs: %v", reason, refreshErr)
			return nil
		}
		return refreshErr
	}

	// 3) Fetch from API
	endpoint := fmt.Sprintf("%s/api/where/agencies-with-coverage.json", c.BaseURL)

	var coverageResp models.AgenciesWithCoverageResponse
	startTime := time.Now()

	err := c.circuitBreaker.Call(func() error {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Add cache-control headers
		req.Header.Set("Cache-Control", "max-age=3600")
		req.Header.Set("User-Agent", "oba-twilio/1.0")

		q := req.URL.Query()
		q.Add("key", c.APIKey)
		req.URL.RawQuery = q.Encode()

		resp, err := c.Client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to make request: %w", err)
		}

		defer func() {
			_ = resp.Body.Close()
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&coverageResp); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	})

	responseTime := time.Since(startTime)

	if err != nil {
		c.metrics.IncrementAPIError()
		if strings.Contains(err.Error(), "circuit breaker is open") {
			c.metrics.IncrementCircuitBreakerOpen()
		}
		return degradeToStale("request error", err)
	}

	c.metrics.IncrementAPICall(responseTime)

	if coverageResp.Code != 200 {
		return degradeToStale("api error", fmt.Errorf("API error: %s (code %d)", coverageResp.Text, coverageResp.Code))
	}
	if len(coverageResp.Data.List) == 0 {
		return degradeToStale("empty agency list", fmt.Errorf("no agencies-with-coverage agencies found"))
	}

	// Extract unique agency IDs preserving API order.
	ids := extractAgencyIDsFromCoverageList(coverageResp.Data.List)
	if len(ids) == 0 {
		return degradeToStale("no valid agency IDs", fmt.Errorf("agencies-with-coverage returned no valid agency IDs"))
	}

	// Apply to search ordering.
	c.storeAgencyPriority(ids)

	// Cache for ~1 day.
	c.cache.Set(coverageAgencyIDsCacheKey, ids, agenciesCacheTTLMinutes*time.Minute)
	logAgencyIDsForSearch("API agencies-with-coverage (ensureAgencyIDsForSearch)", ids)

	return nil
}

// SetAgencyPriority allows runtime configuration of agency priority
func (c *OneBusAwayClient) SetAgencyPriority(agencies []string) error {
	if c.config == nil {
		c.config = getDefaultConfig()
	}

	// Validate the provided agencies
	if len(agencies) == 0 {
		c.metrics.IncrementValidationErrors()
		return fmt.Errorf("agencies list cannot be empty")
	}

	for i, agency := range agencies {
		if strings.TrimSpace(agency) == "" {
			c.metrics.IncrementValidationErrors()
			return fmt.Errorf("agency[%d] cannot be empty", i)
		}
	}

	c.storeAgencyPriority(agencies)
	return nil
}

// GetMetrics returns the current performance metrics
func (c *OneBusAwayClient) GetMetrics() Metrics {
	return c.metrics.GetMetrics()
}

func (c *OneBusAwayClient) InitializeCoverage() error {
	// Check cache first
	cacheKey := "coverage_areas"
	if cached, found := c.cache.Get(cacheKey); found {
		if coverageArea, ok := cached.(*models.CoverageArea); ok {
			c.metrics.IncrementCacheHits()
			c.coverageArea = coverageArea
			// Even if coverage areas are cached, we still need to refresh
			// agency IDs used for stop searching once per day. Log on failure so
			// that silently falling back to the hardcoded agency defaults (e.g.
			// API down, circuit breaker open, no stale cache) is diagnosable.
			if err := c.ensureAgencyIDsForSearch(); err != nil {
				log.Printf("Failed to refresh agency IDs for search (using existing agency priority): %v", err)
			}
			return nil
		}
	}
	c.metrics.IncrementCacheMisses()

	endpoint := fmt.Sprintf("%s/api/where/agencies-with-coverage.json", c.BaseURL)

	var coverageResp models.AgenciesWithCoverageResponse
	startTime := time.Now()
	err := c.circuitBreaker.Call(func() error {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Add cache-control headers
		req.Header.Set("Cache-Control", "max-age=3600")
		req.Header.Set("User-Agent", "oba-twilio/1.0")

		q := req.URL.Query()
		q.Add("key", c.APIKey)
		req.URL.RawQuery = q.Encode()

		resp, err := c.Client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to make request: %w", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				// Log error but don't fail the request
				fmt.Printf("Error closing response body: %v\n", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&coverageResp); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	})
	responseTime := time.Since(startTime)

	if err != nil {
		c.metrics.IncrementAPIError()
		if strings.Contains(err.Error(), "circuit breaker is open") {
			c.metrics.IncrementCircuitBreakerOpen()
		}

		// Graceful degradation: try to use expired cached data
		if cachedData, found, _ := c.cache.GetExpired(cacheKey); found {
			if coverageArea, ok := cachedData.(*models.CoverageArea); ok {
				c.coverageArea = coverageArea
				return fmt.Errorf("using cached coverage data due to API failure: %w", err)
			}
		}

		return err
	}

	c.metrics.IncrementAPICall(responseTime)

	// Validate API response structure
	if coverageResp.Code != 200 {
		return fmt.Errorf("API error: %s (code %d)", coverageResp.Text, coverageResp.Code)
	}
	// Additional validation for coverage response
	for i, agency := range coverageResp.Data.List {
		if agency.AgencyID == "" {
			return fmt.Errorf("invalid response: agency %d missing ID", i)
		}
		if agency.Lat < -90 || agency.Lat > 90 {
			return fmt.Errorf("invalid response: agency %s has invalid latitude %f", agency.AgencyID, agency.Lat)
		}
		if agency.Lon < -180 || agency.Lon > 180 {
			return fmt.Errorf("invalid response: agency %s has invalid longitude %f", agency.AgencyID, agency.Lon)
		}
	}

	if len(coverageResp.Data.List) == 0 {
		return fmt.Errorf("no coverage areas found")
	}

	// Apply agency IDs to stop searching and cache them for ~1 day.
	// (This reuses the agencies-with-coverage response we already fetched.)
	agencyIDs := extractAgencyIDsFromCoverageList(coverageResp.Data.List)

	if len(agencyIDs) > 0 {
		c.storeAgencyPriority(agencyIDs)
		c.cache.Set(coverageAgencyIDsCacheKey, agencyIDs, agenciesCacheTTLMinutes*time.Minute)
		logAgencyIDsForSearch("InitializeCoverage (agencies-with-coverage response)", agencyIDs)
	}

	c.coverageArea = c.calculateCoverageArea(coverageResp.Data.List)

	// Cache the result
	c.cache.Set(cacheKey, c.coverageArea, coverageCacheTTLMinutes*time.Minute)

	return nil
}

func (c *OneBusAwayClient) GetCoverageArea() *models.CoverageArea {
	return c.coverageArea
}

func (c *OneBusAwayClient) calculateCoverageArea(agencies []models.AgencyCoverageRow) *models.CoverageArea {
	if len(agencies) == 0 {
		return &models.CoverageArea{
			CenterLat: 47.6062,
			CenterLon: -122.3321,
			Radius:    25000,
		}
	}

	var minLat, maxLat, minLon, maxLon float64
	first := true

	for _, agency := range agencies {
		agencyMinLat := agency.Lat - agency.LatSpan/2
		agencyMaxLat := agency.Lat + agency.LatSpan/2
		agencyMinLon := agency.Lon - agency.LonSpan/2
		agencyMaxLon := agency.Lon + agency.LonSpan/2

		if first {
			minLat, maxLat = agencyMinLat, agencyMaxLat
			minLon, maxLon = agencyMinLon, agencyMaxLon
			first = false
		} else {
			if agencyMinLat < minLat {
				minLat = agencyMinLat
			}
			if agencyMaxLat > maxLat {
				maxLat = agencyMaxLat
			}
			if agencyMinLon < minLon {
				minLon = agencyMinLon
			}
			if agencyMaxLon > maxLon {
				maxLon = agencyMaxLon
			}
		}
	}

	centerLat := (minLat + maxLat) / 2
	centerLon := (minLon + maxLon) / 2

	latSpan := maxLat - minLat
	lonSpan := maxLon - minLon

	radius := c.calculateRadius(latSpan, lonSpan, centerLat)

	return &models.CoverageArea{
		CenterLat: centerLat,
		CenterLon: centerLon,
		Radius:    radius,
	}
}

func (c *OneBusAwayClient) calculateRadius(latSpan, lonSpan, centerLat float64) float64 {
	const earthRadiusMeters = 6371000

	latRadians := latSpan * math.Pi / 180
	lonRadians := lonSpan * math.Pi / 180
	centerLatRadians := centerLat * math.Pi / 180

	latDistanceMeters := latRadians * earthRadiusMeters
	lonDistanceMeters := lonRadians * earthRadiusMeters * math.Cos(centerLatRadians)

	maxDistance := math.Max(latDistanceMeters, lonDistanceMeters)

	radius := maxDistance / 2

	const minRadius = 5000
	const maxRadius = 100000

	if radius < minRadius {
		return minRadius
	}
	if radius > maxRadius {
		return maxRadius
	}

	return radius
}

func (c *OneBusAwayClient) GetArrivalsAndDepartures(stopID string) (*models.OneBusAwayResponse, error) {
	fullStopID, err := c.resolveStopID(stopID)
	if err != nil {
		// Pass through the already structured error
		return nil, err
	}

	// Check cache first
	cacheKey := fmt.Sprintf("arrivals:%s", fullStopID)
	if cached, found := c.cache.Get(cacheKey); found {
		if obaResp, ok := cached.(*models.OneBusAwayResponse); ok {
			c.metrics.IncrementCacheHits()
			return obaResp, nil
		}
	}
	c.metrics.IncrementCacheMisses()

	endpoint := fmt.Sprintf("%s/api/where/arrivals-and-departures-for-stop/%s.json", c.BaseURL, url.QueryEscape(fullStopID))

	var obaResp models.OneBusAwayResponse
	startTime := time.Now()
	err = c.circuitBreaker.Call(func() error {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Add cache-control headers for arrivals (shorter cache due to time-sensitive data)
		req.Header.Set("Cache-Control", "max-age=60")
		req.Header.Set("User-Agent", "oba-twilio/1.0")

		q := req.URL.Query()
		q.Add("key", c.APIKey)
		q.Add("minutesBefore", "0")
		q.Add("minutesAfter", "30")
		req.URL.RawQuery = q.Encode()

		resp, err := c.Client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to make request: %w", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				// Log error but don't fail the request
				fmt.Printf("Error closing response body: %v\n", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&obaResp); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	})
	responseTime := time.Since(startTime)

	if err != nil {
		c.metrics.IncrementAPIError()
		if strings.Contains(err.Error(), "circuit breaker is open") {
			c.metrics.IncrementCircuitBreakerOpen()
		}

		// Graceful degradation: try to use expired cached data
		if cachedData, found, _ := c.cache.GetExpired(cacheKey); found {
			if obaResp, ok := cachedData.(*models.OneBusAwayResponse); ok {
				return obaResp, fmt.Errorf("using cached arrival data due to API failure: %w", err)
			}
		}

		return nil, err
	}

	c.metrics.IncrementAPICall(responseTime)

	// Validate API response structure
	if obaResp.Code != 200 {
		return nil, fmt.Errorf("API error: %s (code %d)", obaResp.Text, obaResp.Code)
	}

	if obaResp.Data.Entry.StopId == "" {
		return nil, fmt.Errorf("invalid response: missing stop information")
	}

	// Cache the result with shorter TTL for time-sensitive data
	c.cache.Set(cacheKey, &obaResp, arrivalsCacheTTLMinutes*time.Minute)

	return &obaResp, nil
}

// GetArrivalsAndDeparturesWithWindow fetches arrivals and departures with a custom minutesAfter window
func (c *OneBusAwayClient) GetArrivalsAndDeparturesWithWindow(stopID string, minutesAfter int) (*models.OneBusAwayResponse, error) {
	fullStopID, err := c.resolveStopID(stopID)
	if err != nil {
		// Pass through the already structured error
		return nil, err
	}

	// Use different cache key to include the minutesAfter parameter
	cacheKey := fmt.Sprintf("arrivals:%s:%d", fullStopID, minutesAfter)
	if cached, found := c.cache.Get(cacheKey); found {
		if obaResp, ok := cached.(*models.OneBusAwayResponse); ok {
			c.metrics.IncrementCacheHits()
			return obaResp, nil
		}
	}
	c.metrics.IncrementCacheMisses()

	endpoint := fmt.Sprintf("%s/api/where/arrivals-and-departures-for-stop/%s.json", c.BaseURL, url.QueryEscape(fullStopID))

	var obaResp models.OneBusAwayResponse
	startTime := time.Now()
	err = c.circuitBreaker.Call(func() error {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Add cache-control headers for arrivals (shorter cache due to time-sensitive data)
		req.Header.Set("Cache-Control", "max-age=60")
		req.Header.Set("User-Agent", "oba-twilio/1.0")

		q := req.URL.Query()
		q.Add("key", c.APIKey)
		q.Add("minutesBefore", "0")
		q.Add("minutesAfter", fmt.Sprintf("%d", minutesAfter))
		req.URL.RawQuery = q.Encode()

		resp, err := c.Client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to make request: %w", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				// Log error but don't fail the request
				fmt.Printf("Error closing response body: %v\n", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&obaResp); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	})
	responseTime := time.Since(startTime)

	if err != nil {
		c.metrics.IncrementAPIError()
		if strings.Contains(err.Error(), "circuit breaker is open") {
			c.metrics.IncrementCircuitBreakerOpen()
		}

		// Graceful degradation: try to use expired cached data
		if cachedData, found, _ := c.cache.GetExpired(cacheKey); found {
			if obaResp, ok := cachedData.(*models.OneBusAwayResponse); ok {
				return obaResp, fmt.Errorf("using cached arrival data due to API failure: %w", err)
			}
		}

		return nil, err
	}

	c.metrics.IncrementAPICall(responseTime)

	// Validate API response structure
	if obaResp.Code != 200 {
		return nil, fmt.Errorf("API error: %s (code %d)", obaResp.Text, obaResp.Code)
	}

	if obaResp.Data.Entry.StopId == "" {
		return nil, fmt.Errorf("invalid response: missing stop information")
	}

	// Cache the result with shorter TTL for time-sensitive data
	c.cache.Set(cacheKey, &obaResp, arrivalsCacheTTLMinutes*time.Minute)

	return &obaResp, nil
}

func (c *OneBusAwayClient) resolveStopID(stopID string) (string, error) {
	stopID = strings.TrimSpace(stopID)
	if stopID == "" {
		return "", models.NewInvalidStopIDError("", fmt.Errorf("stop ID cannot be empty"))
	}

	if strings.Contains(stopID, "_") {
		return stopID, nil
	}

	agencies := c.getAgencyList()

	for _, agency := range agencies {
		fullStopID := fmt.Sprintf("%s_%s", agency, stopID)
		if c.stopExists(fullStopID) {
			return fullStopID, nil
		}
	}

	return fmt.Sprintf("1_%s", stopID), nil
}

func (c *OneBusAwayClient) FindAllMatchingStops(stopID string) ([]models.StopOption, error) {
	stopID = strings.TrimSpace(stopID)
	if stopID == "" {
		return nil, models.NewInvalidStopIDError("", fmt.Errorf("stop ID cannot be empty"))
	}

	// Check cache first
	cacheKey := fmt.Sprintf("matching_stops:%s", stopID)
	if cached, found := c.cache.Get(cacheKey); found {
		if stops, ok := cached.([]models.StopOption); ok {
			c.metrics.IncrementCacheHits()
			return stops, nil
		}
	}
	c.metrics.IncrementCacheMisses()

	if strings.Contains(stopID, "_") {
		stopOption, err := c.GetStopInfo(stopID)
		if err != nil {
			return nil, err
		}
		if stopOption != nil {
			result := []models.StopOption{*stopOption}
			c.cache.Set(cacheKey, result, cacheTTLMinutes*time.Minute)
			return result, nil
		}
		return []models.StopOption{}, nil
	}

	// Order agencies by likelihood (most common first based on typical transit systems)
	agencies := c.getAgencyList()

	// Use context for proper timeout and cancellation
	timeout := c.getEffectiveTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Use a channel to collect results and limit concurrency
	semaphore := make(chan struct{}, maxConcurrentRequests)
	resultChan := make(chan *models.StopOption, len(agencies)*2) // Buffer for all potential results
	var wg sync.WaitGroup

	for _, agency := range agencies {
		wg.Add(1)
		go func(agencyID string) {
			defer wg.Done()

			// Check if context is cancelled
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Acquire semaphore with panic recovery
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}

			defer func() {
				if r := recover(); r != nil {
					<-semaphore
					panic(r)
				}
				<-semaphore
			}()

			fullStopID := fmt.Sprintf("%s_%s", agencyID, stopID)
			stopOption, err := c.GetStopInfoWithContext(ctx, fullStopID)

			// Always check if context is done before sending
			select {
			case <-ctx.Done():
				return
			default:
			}

			if err == nil && stopOption != nil {
				select {
				case resultChan <- stopOption:
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case resultChan <- nil:
				case <-ctx.Done():
					return
				}
			}
		}(agency)
	}

	// Close result channel when all goroutines complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results with timeout
	var matchingStops []models.StopOption

CollectLoop:
	for {
		select {
		case stopOption, ok := <-resultChan:
			if !ok {
				// Channel closed, all goroutines finished
				break CollectLoop
			}
			if stopOption != nil {
				matchingStops = append(matchingStops, *stopOption)
			}
		case <-ctx.Done():
			// Timeout occurred, stop collecting but let goroutines finish
			break CollectLoop
		}
	}

	// Cache the result
	c.cache.Set(cacheKey, matchingStops, cacheTTLMinutes*time.Minute)

	return matchingStops, nil
}

func (c *OneBusAwayClient) GetStopInfo(fullStopID string) (*models.StopOption, error) {
	return c.GetStopInfoWithContext(context.Background(), fullStopID)
}

// GetStopInfoWithContext fetches stop information with context for timeout and cancellation
func (c *OneBusAwayClient) GetStopInfoWithContext(ctx context.Context, fullStopID string) (*models.StopOption, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("stop_info:%s", fullStopID)
	if cached, found := c.cache.Get(cacheKey); found {
		if stopOption, ok := cached.(*models.StopOption); ok {
			c.metrics.IncrementCacheHits()
			return stopOption, nil
		}
	}
	c.metrics.IncrementCacheMisses()

	endpoint := fmt.Sprintf("%s/api/where/stop/%s.json", c.BaseURL, url.QueryEscape(fullStopID))

	var stopResp struct {
		Data struct {
			Entry struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"entry"`
			References struct {
				Agencies []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"agencies"`
			} `json:"references"`
		} `json:"data"`
		Code int    `json:"code"`
		Text string `json:"text"`
	}

	startTime := time.Now()
	err := c.circuitBreaker.Call(func() error {
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return models.NewInternalError("failed to create HTTP request", err)
		}

		// Add cache-control headers
		req.Header.Set("Cache-Control", "max-age=300")
		req.Header.Set("User-Agent", "oba-twilio/1.0")

		q := req.URL.Query()
		q.Add("key", c.APIKey)
		req.URL.RawQuery = q.Encode()

		resp, err := c.Client.Do(req)
		if err != nil {
			return models.NewNetworkError("failed to communicate with OneBusAway API", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				// Log error but don't fail the request
				fmt.Printf("Error closing response body: %v\n", err)
			}
		}()

		if resp.StatusCode == http.StatusNotFound {
			return models.NewStopNotFoundError(fullStopID, nil)
		}
		if resp.StatusCode >= 500 {
			return models.NewServiceUnavailableError(fmt.Sprintf("API returned status %d", resp.StatusCode), nil)
		}
		if resp.StatusCode != http.StatusOK {
			return models.NewInvalidResponseError(fmt.Sprintf("unexpected status code %d", resp.StatusCode), nil)
		}

		if err := json.NewDecoder(resp.Body).Decode(&stopResp); err != nil {
			return models.NewInvalidResponseError("failed to decode JSON response", err)
		}

		return nil
	})
	responseTime := time.Since(startTime)

	if err != nil {
		c.metrics.IncrementAPIError()
		if strings.Contains(err.Error(), "circuit breaker is open") {
			c.metrics.IncrementCircuitBreakerOpen()
		}

		// Graceful degradation: try to use expired cached data
		if cachedData, found, _ := c.cache.GetExpired(cacheKey); found {
			if stopOption, ok := cachedData.(*models.StopOption); ok {
				return stopOption, fmt.Errorf("using cached stop info due to API failure: %w", err)
			}
		}

		return nil, err
	}

	c.metrics.IncrementAPICall(responseTime)

	// Validate API response structure
	if stopResp.Code != 200 {
		if stopResp.Code == 404 {
			return nil, models.NewStopNotFoundError(fullStopID, nil)
		}
		return nil, models.NewServiceUnavailableError(fmt.Sprintf("API error: %s (code %d)", stopResp.Text, stopResp.Code), nil)
	}
	if stopResp.Data.Entry.ID == "" {
		return nil, models.NewInvalidResponseError("missing stop ID in API response", nil)
	}
	if stopResp.Data.Entry.Name == "" {
		return nil, models.NewInvalidResponseError("missing stop name in API response", nil)
	}

	agencyName := c.getAgencyNameFromID(fullStopID, stopResp.Data.References.Agencies)

	stopOption := &models.StopOption{
		FullStopID:  fullStopID,
		AgencyName:  agencyName,
		StopName:    stopResp.Data.Entry.Name,
		DisplayText: fmt.Sprintf("%s: %s", agencyName, stopResp.Data.Entry.Name),
	}

	// Cache the result
	c.cache.Set(cacheKey, stopOption, cacheTTLMinutes*time.Minute)

	return stopOption, nil
}

func (c *OneBusAwayClient) getAgencyNameFromID(stopID string, agencies []struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}) string {
	parts := strings.Split(stopID, "_")
	if len(parts) < 2 {
		return "Unknown"
	}

	agencyID := parts[0]

	for _, agency := range agencies {
		if agency.ID == agencyID {
			return agency.Name
		}
	}

	switch agencyID {
	case "1":
		return "King County Metro"
	case "40":
		return "Sound Transit"
	case "29":
		return "Pierce Transit"
	case "95":
		return "Community Transit"
	case "97":
		return "Kitsap Transit"
	case "98":
		return "Everett Transit"
	case "3":
		return "Washington State Ferries"
	case "23":
		return "Other Agency"
	default:
		return fmt.Sprintf("Agency %s", agencyID)
	}
}

func (c *OneBusAwayClient) stopExists(stopID string) bool {
	// Check cache first
	cacheKey := fmt.Sprintf("stop_exists:%s", stopID)
	if cached, found := c.cache.Get(cacheKey); found {
		if exists, ok := cached.(bool); ok {
			c.metrics.IncrementCacheHits()
			return exists
		}
	}
	c.metrics.IncrementCacheMisses()

	endpoint := fmt.Sprintf("%s/api/where/stop/%s.json", c.BaseURL, url.QueryEscape(stopID))

	var exists bool
	startTime := time.Now()
	err := c.circuitBreaker.Call(func() error {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return err
		}

		// Add cache-control headers
		req.Header.Set("Cache-Control", "max-age=300")
		req.Header.Set("User-Agent", "oba-twilio/1.0")

		q := req.URL.Query()
		q.Add("key", c.APIKey)
		req.URL.RawQuery = q.Encode()

		resp, err := c.Client.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				// Log error but don't fail the request
				fmt.Printf("Error closing response body: %v\n", err)
			}
		}()

		exists = resp.StatusCode == http.StatusOK
		return nil
	})
	responseTime := time.Since(startTime)

	if err != nil {
		c.metrics.IncrementAPIError()
		if strings.Contains(err.Error(), "circuit breaker is open") {
			c.metrics.IncrementCircuitBreakerOpen()
		}

		// Graceful degradation: try to use expired cached data
		if cachedData, found, _ := c.cache.GetExpired(cacheKey); found {
			if cachedExists, ok := cachedData.(bool); ok {
				return cachedExists
			}
		}

		return false
	}

	c.metrics.IncrementAPICall(responseTime)

	// Cache the result
	c.cache.Set(cacheKey, exists, cacheTTLMinutes*time.Minute)

	return exists
}

func (c *OneBusAwayClient) SearchStops(query string) ([]models.Stop, error) {
	coverage := c.GetCoverageArea()
	if coverage == nil {
		return nil, fmt.Errorf("coverage area not initialized - call InitializeCoverage() first")
	}

	// Check cache first
	cacheKey := fmt.Sprintf("search_stops:%s", query)
	if cached, found := c.cache.Get(cacheKey); found {
		if stops, ok := cached.([]models.Stop); ok {
			c.metrics.IncrementCacheHits()
			return stops, nil
		}
	}
	c.metrics.IncrementCacheMisses()

	endpoint := fmt.Sprintf("%s/api/where/stops-for-location.json", c.BaseURL)

	var stopData models.StopData
	startTime := time.Now()
	err := c.circuitBreaker.Call(func() error {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Add cache-control headers
		req.Header.Set("Cache-Control", "max-age=300")
		req.Header.Set("User-Agent", "oba-twilio/1.0")

		q := req.URL.Query()
		q.Add("key", c.APIKey)
		q.Add("lat", fmt.Sprintf("%.6f", coverage.CenterLat))
		q.Add("lon", fmt.Sprintf("%.6f", coverage.CenterLon))
		q.Add("radius", fmt.Sprintf("%.0f", coverage.Radius))
		q.Add("query", query)
		req.URL.RawQuery = q.Encode()

		resp, err := c.Client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to make request: %w", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				// Log error but don't fail the request
				fmt.Printf("Error closing response body: %v\n", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&stopData); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	})
	responseTime := time.Since(startTime)

	if err != nil {
		c.metrics.IncrementAPIError()
		if strings.Contains(err.Error(), "circuit breaker is open") {
			c.metrics.IncrementCircuitBreakerOpen()
		}

		// Graceful degradation: try to use expired cached data
		if cachedData, found, _ := c.cache.GetExpired(cacheKey); found {
			if stops, ok := cachedData.([]models.Stop); ok {
				return stops, fmt.Errorf("using cached search results due to API failure: %w", err)
			}
		}

		return nil, err
	}

	c.metrics.IncrementAPICall(responseTime)

	// Validate API response structure
	if stopData.Code != 200 {
		return nil, fmt.Errorf("API error: %s (code %d)", stopData.Text, stopData.Code)
	}
	// Validate stop data
	for i, stop := range stopData.Data.List {
		if stop.ID == "" {
			return nil, fmt.Errorf("invalid response: stop %d missing ID", i)
		}
		if stop.Lat < -90 || stop.Lat > 90 {
			return nil, fmt.Errorf("invalid response: stop %s has invalid latitude %f", stop.ID, stop.Lat)
		}
		if stop.Lon < -180 || stop.Lon > 180 {
			return nil, fmt.Errorf("invalid response: stop %s has invalid longitude %f", stop.ID, stop.Lon)
		}
	}

	stops := make([]models.Stop, len(stopData.Data.List))
	for i, s := range stopData.Data.List {
		stops[i] = models.Stop{
			ID:        s.ID,
			Name:      s.Name,
			Latitude:  s.Lat,
			Longitude: s.Lon,
		}
	}

	// Cache the result
	c.cache.Set(cacheKey, stops, cacheTTLMinutes*time.Minute)

	return stops, nil
}

func (c *OneBusAwayClient) ProcessArrivals(obaResp *models.OneBusAwayResponse, maxMinutesOut int) []models.Arrival {
	if maxMinutesOut <= 0 {
		maxMinutesOut = 120
	}

	arrivals := make([]models.Arrival, 0)
	now := time.Now().Unix() * 1000

	for _, ad := range obaResp.Data.Entry.ArrivalsAndDepartures {
		arrivalTime := ad.PredictedArrivalTime
		if arrivalTime == 0 {
			arrivalTime = ad.ScheduledArrivalTime
		}

		if arrivalTime <= now {
			continue
		}

		minutesUntil := int((arrivalTime - now) / (1000 * 60))
		if minutesUntil > maxMinutesOut {
			continue
		}

		arrival := models.Arrival{
			RouteShortName:       ad.RouteShortName,
			TripHeadsign:         ad.TripHeadsign,
			PredictedArrivalTime: ad.PredictedArrivalTime,
			ScheduledArrivalTime: ad.ScheduledArrivalTime,
			MinutesUntilArrival:  minutesUntil,
			Status:               ad.Status,
		}

		arrivals = append(arrivals, arrival)
	}

	// Keep arrivals deterministic and user-friendly: nearest departures first.
	sort.SliceStable(arrivals, func(i, j int) bool {
		if arrivals[i].MinutesUntilArrival == arrivals[j].MinutesUntilArrival {
			if arrivals[i].RouteShortName == arrivals[j].RouteShortName {
				return arrivals[i].TripHeadsign < arrivals[j].TripHeadsign
			}
			return arrivals[i].RouteShortName < arrivals[j].RouteShortName
		}
		return arrivals[i].MinutesUntilArrival < arrivals[j].MinutesUntilArrival
	})

	return arrivals
}
