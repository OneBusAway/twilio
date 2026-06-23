package common

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"oba-twilio/models"
)

const (
	SessionTimeoutMinutes    = 10
	smsSessionTimeoutMinutes = 15
	cleanupIntervalMinutes   = 5
	maxSessions              = 10000
	lruCleanupBatchSize      = 100 // Clean up this many LRU entries at once when limit exceeded
)

// Accept E.164 numbers used by Twilio (e.g. +14445556666, +48500100200).
var phoneRegex = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)

type sessionCategory int

func (s sessionCategory) String() string {
	switch s {
	case disambiguation:
		return "disambiguation"
	case voice:
		return "voice"
	case sms:
		return "sms"
	}
	return "unknown"
}

const (
	disambiguation sessionCategory = iota
	voice
	sms
)

// SessionMetrics provides insights into session store performance
type SessionMetrics struct {
	TotalSessions   int64
	CacheHits       int64
	CacheMisses     int64
	Evictions       int64
	ExpiredSessions int64
	CreatedSessions int64
	MemoryUsage     int64 // Approximate memory usage in bytes
	LastCleanupTime int64
}

// SessionEntry represents a session with metadata for efficient LRU management
type SessionEntry struct {
	sessionType sessionCategory
	data        interface{}
	createdAt   int64
	accessedAt  int64
	checksum    [32]byte // For integrity validation
}

// LRUNode represents a node in the doubly-linked list for efficient LRU operations
type LRUNode struct {
	phoneNumber string
	timestamp   int64
	prev        *LRUNode
	next        *LRUNode
}

// LRUList manages the least-recently-used order efficiently
type LRUList struct {
	head  *LRUNode
	tail  *LRUNode
	nodes map[string]*LRUNode // For O(1) access to nodes
}

// NewLRUList creates a new LRU list
func NewLRUList() *LRUList {
	head := &LRUNode{}
	tail := &LRUNode{}
	head.next = tail
	tail.prev = head

	return &LRUList{
		head:  head,
		tail:  tail,
		nodes: make(map[string]*LRUNode),
	}
}

// MoveToFront moves a node to the front of the LRU list (most recent)
func (l *LRUList) MoveToFront(phoneNumber string) {
	if node, exists := l.nodes[phoneNumber]; exists {
		l.removeNode(node)
		l.addToFront(node)
		node.timestamp = time.Now().Unix()
	}
}

// AddToFront adds a new node to the front of the LRU list
func (l *LRUList) AddToFront(phoneNumber string) {
	if _, exists := l.nodes[phoneNumber]; exists {
		l.MoveToFront(phoneNumber)
		return
	}

	node := &LRUNode{
		phoneNumber: phoneNumber,
		timestamp:   time.Now().Unix(),
	}

	l.addToFront(node)
	l.nodes[phoneNumber] = node
}

// RemoveLRU removes and returns the least recently used phone numbers
func (l *LRUList) RemoveLRU(count int) []string {
	var removed []string

	for i := 0; i < count && l.tail.prev != l.head; i++ {
		node := l.tail.prev
		removed = append(removed, node.phoneNumber)
		l.removeNode(node)
		delete(l.nodes, node.phoneNumber)
	}

	return removed
}

// Remove removes a specific node from the LRU list
func (l *LRUList) Remove(phoneNumber string) {
	if node, exists := l.nodes[phoneNumber]; exists {
		l.removeNode(node)
		delete(l.nodes, phoneNumber)
	}
}

// addToFront adds a node right after head
func (l *LRUList) addToFront(node *LRUNode) {
	node.prev = l.head
	node.next = l.head.next
	l.head.next.prev = node
	l.head.next = node
}

// removeNode removes a node from the list
func (l *LRUList) removeNode(node *LRUNode) {
	node.prev.next = node.next
	node.next.prev = node.prev
}

// Size returns the number of nodes in the LRU list
func (l *LRUList) Size() int {
	return len(l.nodes)
}

// ImprovedSessionStore provides thread-safe session management with efficient LRU eviction
type ImprovedSessionStore struct {
	// Core session storage
	sessions map[string]*SessionEntry
	lru      *LRUList

	// Synchronization
	mutex sync.RWMutex

	// Metrics (using atomic operations for thread safety)
	metrics struct {
		totalSessions   int64
		cacheHits       int64
		cacheMisses     int64
		evictions       int64
		expiredSessions int64
		createdSessions int64
		lastCleanupTime int64
	}

	// Background cleanup
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	// Configuration
	maxSessions   int
	cleanupTicker *time.Ticker
}

// NewImprovedSessionStore creates a new improved session store
func NewImprovedSessionStore() *ImprovedSessionStore {
	ctx, cancel := context.WithCancel(context.Background())

	store := &ImprovedSessionStore{
		sessions:      make(map[string]*SessionEntry),
		lru:           NewLRUList(),
		ctx:           ctx,
		cancel:        cancel,
		maxSessions:   maxSessions,
		cleanupTicker: time.NewTicker(cleanupIntervalMinutes * time.Minute),
	}

	// Start background cleanup
	go store.backgroundCleanup()

	return store
}

// calculateChecksum calculates a checksum for session integrity validation
func (s *ImprovedSessionStore) calculateChecksum(phoneNumber string, data interface{}) [32]byte {
	hasher := sha256.New()
	hasher.Write([]byte(phoneNumber))

	// Add type-specific data to checksum - exclude timestamps to avoid timing issues
	switch v := data.(type) {
	case *models.DisambiguationSession:
		_, _ = fmt.Fprintf(hasher, "disambiguation_%d", len(v.StopOptions))
		for _, opt := range v.StopOptions {
			_, _ = fmt.Fprintf(hasher, "_%s_%s", opt.FullStopID, opt.DisplayText)
		}
	case *models.VoiceSession:
		_, _ = fmt.Fprintf(hasher, "voice_%s_%d", v.StopID, v.MinutesAfter)
	case *models.SMSSession:
		_, _ = fmt.Fprintf(hasher, "sms_%s_%s_%d_%d", v.LastStopID, v.Language, v.WindowMinutes, v.ArrivalHorizonShownMinutes)
	}

	return sha256.Sum256(hasher.Sum(nil))
}

// validateSession validates session integrity and expiration
func (s *ImprovedSessionStore) validateSession(phoneNumber string, entry *SessionEntry) bool {
	// Check expiration based on session type first (most common failure case)
	now := time.Now().Unix()
	var timeout int64

	switch entry.sessionType {
	case disambiguation, voice:
		timeout = SessionTimeoutMinutes * 60
	case sms:
		timeout = smsSessionTimeoutMinutes * 60
	default:
		return false
	}

	// Performance optimization: check expiration first since it's the most common
	// failure case and avoids the expensive checksum calculation
	if now-entry.createdAt > timeout {
		return false // Session expired
	}

	// Only compute expensive checksum if session hasn't expired
	expectedChecksum := s.calculateChecksum(phoneNumber, entry.data)
	if expectedChecksum != entry.checksum {
		return false // Session has been tampered with
	}

	return true
}

// evictLRUSessions evicts the least recently used sessions
func (s *ImprovedSessionStore) evictLRUSessions(count int) {
	toRemove := s.lru.RemoveLRU(count)

	evicted := 0
	for _, phoneNumber := range toRemove {
		if _, exists := s.sessions[phoneNumber]; exists {
			delete(s.sessions, phoneNumber)
			evicted++
		}
	}

	atomic.AddInt64(&s.metrics.evictions, int64(evicted))
	atomic.AddInt64(&s.metrics.totalSessions, int64(-evicted))
}

// setSession is a generic method to set any type of session
func (s *ImprovedSessionStore) setSession(phoneNumber string, data interface{}, sessionType sessionCategory) error {
	if !phoneRegex.MatchString(phoneNumber) {
		return fmt.Errorf("invalid phone number format: %s", phoneNumber)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Check if we need to evict sessions
	if len(s.sessions) >= s.maxSessions {
		s.evictLRUSessions(lruCleanupBatchSize)
	}

	// Create session entry with integrity checksum
	now := time.Now().Unix()
	entry := &SessionEntry{
		sessionType: sessionType,
		data:        data,
		createdAt:   now,
		accessedAt:  now,
	}
	entry.checksum = s.calculateChecksum(phoneNumber, data)

	// Set timestamps based on session type
	switch sessionType {
	case disambiguation:
		if ds, ok := data.(*models.DisambiguationSession); ok {
			ds.CreatedAt = now
		}
	case voice:
		if vs, ok := data.(*models.VoiceSession); ok {
			vs.CreatedAt = now
		}
	case sms:
		if ss, ok := data.(*models.SMSSession); ok {
			ss.CreatedAt = now
			ss.LastQueryTime = now
		}
	}

	// Store session and update LRU
	s.sessions[phoneNumber] = entry
	s.lru.AddToFront(phoneNumber)

	atomic.AddInt64(&s.metrics.totalSessions, 1)
	atomic.AddInt64(&s.metrics.createdSessions, 1)

	return nil
}

// getSession is a generic method to get any type of session
func (s *ImprovedSessionStore) getSession(phoneNumber string, sessionType sessionCategory) interface{} {
	if !phoneRegex.MatchString(phoneNumber) {
		return nil
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	entry, exists := s.sessions[phoneNumber]
	if !exists || entry.sessionType != sessionType {
		atomic.AddInt64(&s.metrics.cacheMisses, 1)
		return nil
	}

	// Validate session integrity and expiration
	if !s.validateSession(phoneNumber, entry) {
		// Remove invalid/expired session
		delete(s.sessions, phoneNumber)
		s.lru.Remove(phoneNumber)
		atomic.AddInt64(&s.metrics.totalSessions, -1)
		atomic.AddInt64(&s.metrics.expiredSessions, 1)
		atomic.AddInt64(&s.metrics.cacheMisses, 1)
		return nil
	}

	// Update access time and LRU position
	entry.accessedAt = time.Now().Unix()
	s.lru.MoveToFront(phoneNumber)

	atomic.AddInt64(&s.metrics.cacheHits, 1)
	return entry.data
}

// clearSession is a generic method to clear any type of session
func (s *ImprovedSessionStore) clearSession(phoneNumber string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.sessions[phoneNumber]; exists {
		delete(s.sessions, phoneNumber)
		s.lru.Remove(phoneNumber)
		atomic.AddInt64(&s.metrics.totalSessions, -1)
	}
}

// Public API methods for disambiguation sessions
func (s *ImprovedSessionStore) SetDisambiguationSession(phoneNumber string, session *models.DisambiguationSession) error {
	return s.setSession(phoneNumber, session, disambiguation)
}

func (s *ImprovedSessionStore) GetDisambiguationSession(phoneNumber string) *models.DisambiguationSession {
	if data := s.getSession(phoneNumber, disambiguation); data != nil {
		return data.(*models.DisambiguationSession)
	}
	return nil
}

func (s *ImprovedSessionStore) ClearDisambiguationSession(phoneNumber string) {
	s.clearSession(phoneNumber)
}

// Public API methods for voice sessions
func (s *ImprovedSessionStore) SetVoiceSession(phoneNumber string, session *models.VoiceSession) error {
	return s.setSession(phoneNumber, session, voice)
}

func (s *ImprovedSessionStore) GetVoiceSession(phoneNumber string) *models.VoiceSession {
	if data := s.getSession(phoneNumber, voice); data != nil {
		return data.(*models.VoiceSession)
	}
	return nil
}

func (s *ImprovedSessionStore) ClearVoiceSession(phoneNumber string) {
	s.clearSession(phoneNumber)
}

// Public API methods for SMS sessions
func (s *ImprovedSessionStore) SetSMSSession(phoneNumber string, session *models.SMSSession) error {
	return s.setSession(phoneNumber, session, sms)
}

func (s *ImprovedSessionStore) GetSMSSession(phoneNumber string) *models.SMSSession {
	if data := s.getSession(phoneNumber, sms); data != nil {
		return data.(*models.SMSSession)
	}
	return nil
}

func (s *ImprovedSessionStore) ClearSMSSession(phoneNumber string) {
	s.clearSession(phoneNumber)
}

// GetSessionCount returns the current number of active sessions
func (s *ImprovedSessionStore) GetSessionCount() int {
	return int(atomic.LoadInt64(&s.metrics.totalSessions))
}

// GetSessionCountAccurate returns the accurate session count by checking the actual map size
// This is slightly more expensive but guarantees accuracy
func (s *ImprovedSessionStore) GetSessionCountAccurate() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.sessions)
}

// GetMetrics returns current session store metrics
func (s *ImprovedSessionStore) GetMetrics() *SessionMetrics {
	// Use atomic counter for performance, with occasional sync for accuracy
	totalSessions := atomic.LoadInt64(&s.metrics.totalSessions)

	// Periodically sync the atomic counter with actual map size
	// This helps catch any potential drift between atomic counter and actual sessions
	if totalSessions%1000 == 0 {
		s.mutex.RLock()
		actualCount := int64(len(s.sessions))
		s.mutex.RUnlock()
		if actualCount != totalSessions {
			atomic.StoreInt64(&s.metrics.totalSessions, actualCount)
			totalSessions = actualCount
		}
	}

	return &SessionMetrics{
		TotalSessions:   totalSessions,
		CacheHits:       atomic.LoadInt64(&s.metrics.cacheHits),
		CacheMisses:     atomic.LoadInt64(&s.metrics.cacheMisses),
		Evictions:       atomic.LoadInt64(&s.metrics.evictions),
		ExpiredSessions: atomic.LoadInt64(&s.metrics.expiredSessions),
		CreatedSessions: atomic.LoadInt64(&s.metrics.createdSessions),
		MemoryUsage:     s.estimateMemoryUsage(),
		LastCleanupTime: atomic.LoadInt64(&s.metrics.lastCleanupTime),
	}
}

// estimateMemoryUsage provides an approximate memory usage calculation
func (s *ImprovedSessionStore) estimateMemoryUsage() int64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Rough estimation: phone number (15 bytes) + SessionEntry (64 bytes) + LRU node (32 bytes)
	// Plus additional overhead for maps and data structures
	baseSize := int64(len(s.sessions) * (15 + 64 + 32 + 50)) // 50 bytes overhead

	// Add estimated size of session data
	for _, entry := range s.sessions {
		switch entry.sessionType {
		case disambiguation:
			if ds, ok := entry.data.(*models.DisambiguationSession); ok {
				baseSize += int64(len(ds.StopOptions) * 100) // rough estimate per stop option
			}
		case voice:
			baseSize += 100 // voice sessions are small
		case sms:
			baseSize += 200 // SMS sessions with language strings
		}
	}

	return baseSize
}

// backgroundCleanup runs periodic cleanup of expired sessions
func (s *ImprovedSessionStore) backgroundCleanup() {
	defer s.cleanupTicker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.cleanupTicker.C:
			s.cleanupExpiredSessions()
		}
	}
}

// cleanupExpiredSessions removes expired sessions
func (s *ImprovedSessionStore) cleanupExpiredSessions() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := time.Now().Unix()
	var toRemove []string
	expiredCount := int64(0)

	// Find expired sessions
	for phoneNumber, entry := range s.sessions {
		if !s.validateSession(phoneNumber, entry) {
			toRemove = append(toRemove, phoneNumber)
			expiredCount++
		}
	}

	// Remove expired sessions
	for _, phoneNumber := range toRemove {
		delete(s.sessions, phoneNumber)
		s.lru.Remove(phoneNumber)
	}

	// Update metrics
	atomic.AddInt64(&s.metrics.totalSessions, -expiredCount)
	atomic.AddInt64(&s.metrics.expiredSessions, expiredCount)
	atomic.StoreInt64(&s.metrics.lastCleanupTime, now)
}

// ResetMetrics resets all metrics counters (useful for testing)
func (s *ImprovedSessionStore) ResetMetrics() {
	atomic.StoreInt64(&s.metrics.cacheHits, 0)
	atomic.StoreInt64(&s.metrics.cacheMisses, 0)
	atomic.StoreInt64(&s.metrics.evictions, 0)
	atomic.StoreInt64(&s.metrics.expiredSessions, 0)
	atomic.StoreInt64(&s.metrics.createdSessions, 0)
	atomic.StoreInt64(&s.metrics.lastCleanupTime, 0)
}

// Close gracefully shuts down the session store
func (s *ImprovedSessionStore) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.cleanupTicker.Stop()
	})
}

// NewSessionStore creates a new session store (backward compatibility alias)
func NewSessionStore() *ImprovedSessionStore {
	return NewImprovedSessionStore()
}

// SessionStore is an alias for backward compatibility
type SessionStore = ImprovedSessionStore

// Helper methods for testing compatibility
func (s *ImprovedSessionStore) ExpireSession(phoneNumber string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if entry, exists := s.sessions[phoneNumber]; exists {
		// Set the creation time to an expired timestamp based on session type
		var timeout int64
		switch entry.sessionType {
		case disambiguation, voice:
			timeout = (SessionTimeoutMinutes + 1) * 60
		case sms:
			timeout = (smsSessionTimeoutMinutes + 1) * 60
		}
		entry.createdAt = time.Now().Unix() - timeout

		// Update the session data timestamp as well
		switch entry.sessionType {
		case disambiguation:
			if ds, ok := entry.data.(*models.DisambiguationSession); ok {
				ds.CreatedAt = entry.createdAt
			}
		case voice:
			if vs, ok := entry.data.(*models.VoiceSession); ok {
				vs.CreatedAt = entry.createdAt
			}
		case sms:
			if ss, ok := entry.data.(*models.SMSSession); ok {
				ss.CreatedAt = entry.createdAt
			}
		}

		// Recalculate checksum with the new timestamp
		entry.checksum = s.calculateChecksum(phoneNumber, entry.data)
	}
}

// SetExpiredSMSSession manually inserts an expired SMS session for testing
func (s *ImprovedSessionStore) SetExpiredSMSSession(phoneNumber string, session *models.SMSSession) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Create session entry with expired timestamp
	expiredTime := time.Now().Unix() - (smsSessionTimeoutMinutes+1)*60
	session.CreatedAt = expiredTime

	entry := &SessionEntry{
		sessionType: sms,
		data:        session,
		createdAt:   expiredTime,
		accessedAt:  expiredTime,
	}
	entry.checksum = s.calculateChecksum(phoneNumber, session)

	s.sessions[phoneNumber] = entry
	s.lru.AddToFront(phoneNumber)
}

// GetRawSessionCount returns the actual map size for testing
func (s *ImprovedSessionStore) GetRawSessionCount() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.sessions)
}
