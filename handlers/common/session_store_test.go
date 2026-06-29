package common

import (
	"fmt"
	"oba-twilio/models"
	"sync"
	"testing"
	"time"
)

func TestSessionStore_NewSessionStore(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	if store == nil {
		t.Fatal("NewSessionStore returned nil")
		return
	}

	if store.sessions == nil || store.lru == nil {
		t.Error("Session storage not initialized")
	}

	if store.GetSessionCount() != 0 {
		t.Error("New store should have 0 sessions")
	}
}

func TestSessionStore_DisambiguationSession_BasicOperations(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	phoneNumber := "+15551234567"
	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_75403", AgencyName: "Metro", StopName: "Test Stop"},
		},
	}

	// Test Set
	err := store.SetDisambiguationSession(phoneNumber, session)
	if err != nil {
		t.Fatalf("Failed to set session: %v", err)
	}

	// Test Get
	retrieved := store.GetDisambiguationSession(phoneNumber)
	if retrieved == nil {
		t.Fatal("Failed to get session")
		return
	}

	if len(retrieved.StopOptions) != 1 {
		t.Error("Session data corrupted")
	}

	// Test Clear
	store.ClearDisambiguationSession(phoneNumber)
	if store.GetDisambiguationSession(phoneNumber) != nil {
		t.Error("Session not cleared")
	}
}

func TestSessionStore_VoiceSession_BasicOperations(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	phoneNumber := "+15551234567"
	session := &models.VoiceSession{
		StopID:       "1_75403",
		MinutesAfter: 10,
	}

	// Test Set
	err := store.SetVoiceSession(phoneNumber, session)
	if err != nil {
		t.Fatalf("Failed to set voice session: %v", err)
	}

	// Test Get
	retrieved := store.GetVoiceSession(phoneNumber)
	if retrieved == nil {
		t.Fatal("Failed to get voice session")
		return
	}

	if retrieved.StopID != "1_75403" {
		t.Error("Voice session data corrupted")
	}

	// Test Clear
	store.ClearVoiceSession(phoneNumber)
	if store.GetVoiceSession(phoneNumber) != nil {
		t.Error("Voice session not cleared")
	}
}

func TestSessionStore_SMSSession_BasicOperations(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	phoneNumber := "+15551234567"
	session := &models.SMSSession{
		LastStopID:    "1_75403",
		Language:      "en-US",
		WindowMinutes: 30,
	}

	// Test Set
	err := store.SetSMSSession(phoneNumber, session)
	if err != nil {
		t.Fatalf("Failed to set SMS session: %v", err)
	}

	// Test Get
	retrieved := store.GetSMSSession(phoneNumber)
	if retrieved == nil {
		t.Fatal("Failed to get SMS session")
		return
	}

	if retrieved.LastStopID != "1_75403" {
		t.Error("SMS session data corrupted")
	}

	// Test Clear
	store.ClearSMSSession(phoneNumber)
	if store.GetSMSSession(phoneNumber) != nil {
		t.Error("SMS session not cleared")
	}
}

func TestSessionStore_InvalidPhoneNumber(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	invalidPhones := []string{
		"invalid",
		"1234567890",
		"+1555123",          // too short (fewer than 8 digits after +)
		"+1555123456789012", // too long (more than 15 digits after +)
		"",
	}

	session := &models.DisambiguationSession{}

	for _, phone := range invalidPhones {
		err := store.SetDisambiguationSession(phone, session)
		if err == nil {
			t.Errorf("Expected error for invalid phone number: %s", phone)
		}

		if store.GetDisambiguationSession(phone) != nil {
			t.Errorf("Should not retrieve session for invalid phone: %s", phone)
		}
	}
}

func TestSessionStore_AcceptsNonUSPhoneNumberInE164(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	phoneNumber := "+48500100200"
	session := &models.VoiceSession{
		StopID:       "1_12345",
		MinutesAfter: 30,
	}

	err := store.SetVoiceSession(phoneNumber, session)
	if err != nil {
		t.Fatalf("Failed to set voice session for E.164 phone number: %v", err)
	}

	retrieved := store.GetVoiceSession(phoneNumber)
	if retrieved == nil {
		t.Fatal("Failed to get voice session for E.164 phone number")
	}
}

func TestSessionStore_SessionExpiration(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	phoneNumber := "+15551234567"

	// Create expired disambiguation session
	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_75403", AgencyName: "Metro", StopName: "Test Stop"},
		},
		CreatedAt: time.Now().Unix() - (SessionTimeoutMinutes+1)*60,
	}

	// Set the session normally first
	err := store.SetDisambiguationSession(phoneNumber, session)
	if err != nil {
		t.Fatalf("Failed to set session: %v", err)
	}

	// Verify session was set
	if store.GetSessionCount() != 1 {
		t.Error("Session should be set")
	}

	// Now expire the session
	store.ExpireSession(phoneNumber)

	// Should return nil for expired session
	retrieved := store.GetDisambiguationSession(phoneNumber)
	if retrieved != nil {
		t.Error("Expired session should not be returned")
	}

	// Session should be cleaned up after accessing expired session
	if store.GetSessionCount() != 0 {
		t.Error("Expired session should be cleaned up")
	}
}

func TestSessionStore_ConcurrentAccessImproved(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	const numGoroutines = 100
	const numOperations = 10

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*numOperations)

	// Test concurrent disambiguation sessions
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			phoneNumber := fmt.Sprintf("+1555123%04d", id)

			for j := 0; j < numOperations; j++ {
				session := &models.DisambiguationSession{
					StopOptions: []models.StopOption{
						{FullStopID: fmt.Sprintf("1_%d", id*1000+j), AgencyName: "Metro", StopName: "Test Stop"},
					},
				}

				// Set session
				if err := store.SetDisambiguationSession(phoneNumber, session); err != nil {
					errors <- fmt.Errorf("set error: %v", err)
					return
				}

				// Get session
				retrieved := store.GetDisambiguationSession(phoneNumber)
				if retrieved == nil {
					errors <- fmt.Errorf("get returned nil for phone %s", phoneNumber)
					return
				}

				// Clear session
				store.ClearDisambiguationSession(phoneNumber)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestSessionStore_ConcurrentMixedSessionTypes(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	const numGoroutines = 50
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*3)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(3)
		phoneNumber := fmt.Sprintf("+1555123%04d", i)

		// Concurrent disambiguation session operations
		go func(phone string, id int) {
			defer wg.Done()
			session := &models.DisambiguationSession{
				StopOptions: []models.StopOption{
					{FullStopID: fmt.Sprintf("1_%d", id), AgencyName: "Metro", StopName: "Test Stop"},
				},
			}
			if err := store.SetDisambiguationSession(phone, session); err != nil {
				errors <- err
			}
		}(phoneNumber, i)

		// Concurrent voice session operations
		go func(phone string, id int) {
			defer wg.Done()
			session := &models.VoiceSession{
				StopID:       fmt.Sprintf("1_%d", id),
				MinutesAfter: 10,
			}
			if err := store.SetVoiceSession(phone, session); err != nil {
				errors <- err
			}
		}(phoneNumber, i)

		// Concurrent SMS session operations
		go func(phone string, id int) {
			defer wg.Done()
			session := &models.SMSSession{
				LastStopID:    fmt.Sprintf("1_%d", id),
				Language:      "en-US",
				WindowMinutes: 30,
			}
			if err := store.SetSMSSession(phone, session); err != nil {
				errors <- err
			}
		}(phoneNumber, i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestSessionStore_LRUEviction(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	// Fill up to maxSessions
	for i := 0; i < maxSessions; i++ {
		phoneNumber := fmt.Sprintf("+1555123%04d", i)
		session := &models.DisambiguationSession{
			StopOptions: []models.StopOption{
				{FullStopID: fmt.Sprintf("1_%d", i), AgencyName: "Metro", StopName: "Test Stop"},
			},
		}

		err := store.SetDisambiguationSession(phoneNumber, session)
		if err != nil {
			t.Fatalf("Failed to set session %d: %v", i, err)
		}

		// Add small delay to ensure different access times
		time.Sleep(time.Microsecond)
	}

	if store.GetSessionCount() != maxSessions {
		t.Errorf("Expected %d sessions, got %d", maxSessions, store.GetSessionCount())
	}

	// Access first session to make it more recent
	firstPhone := "+15551230000"
	firstSession := store.GetDisambiguationSession(firstPhone)
	if firstSession == nil {
		t.Fatal("First session should exist")
	}

	// Add one more session to trigger LRU eviction
	newPhone := "+15559999999"
	newSession := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_9999", AgencyName: "Metro", StopName: "New Stop"},
		},
	}

	err := store.SetDisambiguationSession(newPhone, newSession)
	if err != nil {
		t.Fatalf("Failed to set new session: %v", err)
	}

	// Should have maxSessions or fewer (due to batch eviction)
	finalCount := store.GetSessionCount()
	if finalCount > maxSessions {
		t.Errorf("Expected at most %d sessions after eviction, got %d", maxSessions, finalCount)
	}
	if finalCount < maxSessions-lruCleanupBatchSize {
		t.Errorf("Expected at least %d sessions after eviction, got %d", maxSessions-lruCleanupBatchSize, finalCount)
	}

	// First session should still exist (was accessed recently)
	if store.GetDisambiguationSession(firstPhone) == nil {
		t.Error("Recently accessed session should not be evicted")
	}

	// New session should exist
	if store.GetDisambiguationSession(newPhone) == nil {
		t.Error("New session should exist")
	}
}

func TestSessionStore_CleanupExpiredSessions(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	phoneNumber := "+15551234567"

	// Set normal sessions first, then expire them
	err1 := store.SetDisambiguationSession(phoneNumber, &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_75403", AgencyName: "Metro", StopName: "Test Stop"},
		},
	})
	err2 := store.SetVoiceSession("+15551234568", &models.VoiceSession{
		StopID:       "1_75403",
		MinutesAfter: 10,
	})
	err3 := store.SetSMSSession("+15551234569", &models.SMSSession{
		LastStopID:    "1_75403",
		Language:      "en-US",
		WindowMinutes: 30,
	})

	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("Failed to set sessions: %v, %v, %v", err1, err2, err3)
	}

	initialCount := store.GetSessionCount()
	if initialCount != 3 {
		t.Errorf("Expected 3 sessions, got %d", initialCount)
	}

	// Now expire all sessions
	store.ExpireSession(phoneNumber)
	store.ExpireSession("+15551234568")
	store.ExpireSession("+15551234569")

	// Try to get expired sessions - they should be automatically cleaned up
	retrievedDisambig := store.GetDisambiguationSession(phoneNumber)
	retrievedVoice := store.GetVoiceSession("+15551234568")
	retrievedSMS := store.GetSMSSession("+15551234569")

	if retrievedDisambig != nil || retrievedVoice != nil || retrievedSMS != nil {
		t.Error("Expired sessions should not be retrievable")
	}

	// Manually trigger cleanup to ensure it works
	store.cleanupExpiredSessions()

	finalCount := store.GetSessionCount()
	if finalCount != 0 {
		t.Errorf("Expected 0 sessions after cleanup, got %d", finalCount)
	}
}

func TestSessionStore_SessionMetrics(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	// Test that we can get metrics
	metrics := store.GetMetrics()
	if metrics == nil {
		t.Error("GetMetrics should not return nil")
		return
	}

	// Initially should have zero stats
	if metrics.TotalSessions != 0 {
		t.Error("Initial total sessions should be 0")
	}

	// Add a session and check metrics
	phoneNumber := "+15551234567"
	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_75403", AgencyName: "Metro", StopName: "Test Stop"},
		},
	}

	err := store.SetDisambiguationSession(phoneNumber, session)
	if err != nil {
		t.Fatalf("Failed to set session: %v", err)
	}

	metrics = store.GetMetrics()
	if metrics.TotalSessions != 1 {
		t.Errorf("Expected 1 total session, got %d", metrics.TotalSessions)
	}

	// Test session hit
	retrieved := store.GetDisambiguationSession(phoneNumber)
	if retrieved == nil {
		t.Error("Should retrieve session")
	}

	metrics = store.GetMetrics()
	if metrics.CacheHits == 0 {
		t.Error("Should have cache hits")
	}

	// Test session miss
	store.GetDisambiguationSession("+15559999999")
	metrics = store.GetMetrics()
	if metrics.CacheMisses == 0 {
		t.Error("Should have cache misses")
	}
}

func BenchmarkSessionStore_SetGet(b *testing.B) {
	store := NewSessionStore()
	defer store.Close()

	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_75403", AgencyName: "Metro", StopName: "Test Stop"},
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		phoneNumber := fmt.Sprintf("+1555123%04d", i%10000)

		_ = store.SetDisambiguationSession(phoneNumber, session)
		store.GetDisambiguationSession(phoneNumber)
	}
}

func BenchmarkSessionStore_ConcurrentAccess(b *testing.B) {
	store := NewSessionStore()
	defer store.Close()

	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_75403", AgencyName: "Metro", StopName: "Test Stop"},
		},
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			phoneNumber := fmt.Sprintf("+1555123%04d", i%10000)

			_ = store.SetDisambiguationSession(phoneNumber, session)
			store.GetDisambiguationSession(phoneNumber)
			i++
		}
	})
}

func BenchmarkSessionStore_LRUEviction(b *testing.B) {
	store := NewSessionStore()
	defer store.Close()

	// Fill up the store
	for i := 0; i < maxSessions; i++ {
		phoneNumber := fmt.Sprintf("+1555123%04d", i)
		session := &models.DisambiguationSession{
			StopOptions: []models.StopOption{
				{FullStopID: fmt.Sprintf("1_%d", i), AgencyName: "Metro", StopName: "Test Stop"},
			},
		}
		_ = store.SetDisambiguationSession(phoneNumber, session)
	}

	session := &models.DisambiguationSession{
		StopOptions: []models.StopOption{
			{FullStopID: "1_benchmark", AgencyName: "Metro", StopName: "Benchmark Stop"},
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		phoneNumber := fmt.Sprintf("+1555999%04d", i%1000)
		_ = store.SetDisambiguationSession(phoneNumber, session)
	}
}

func TestSessionStore_SessionCountAccuracy(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	// Add some sessions
	for i := 0; i < 10; i++ {
		phoneNumber := fmt.Sprintf("+1555123%04d", i)
		session := &models.DisambiguationSession{
			StopOptions: []models.StopOption{
				{FullStopID: fmt.Sprintf("1_%d", i), AgencyName: "Metro", StopName: "Test Stop"},
			},
		}
		err := store.SetDisambiguationSession(phoneNumber, session)
		if err != nil {
			t.Fatalf("Failed to set session %d: %v", i, err)
		}
	}

	// Check that both count methods return the same value
	fastCount := store.GetSessionCount()
	accurateCount := store.GetSessionCountAccurate()

	if fastCount != accurateCount {
		t.Errorf("Session count mismatch: fast=%d, accurate=%d", fastCount, accurateCount)
	}

	if fastCount != 10 {
		t.Errorf("Expected 10 sessions, got %d", fastCount)
	}
}

func TestSessionStore_MetricsCounterSync(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	// Add exactly 1000 sessions to trigger the sync logic
	for i := 0; i < 1000; i++ {
		phoneNumber := fmt.Sprintf("+1555123%04d", i)
		session := &models.DisambiguationSession{
			StopOptions: []models.StopOption{
				{FullStopID: fmt.Sprintf("1_%d", i), AgencyName: "Metro", StopName: "Test Stop"},
			},
		}
		err := store.SetDisambiguationSession(phoneNumber, session)
		if err != nil {
			t.Fatalf("Failed to set session %d: %v", i, err)
		}
	}

	// Get metrics should trigger sync
	metrics := store.GetMetrics()
	if metrics.TotalSessions != 1000 {
		t.Errorf("Expected 1000 sessions in metrics, got %d", metrics.TotalSessions)
	}

	// Verify accuracy with direct count
	accurateCount := store.GetSessionCountAccurate()
	if accurateCount != 1000 {
		t.Errorf("Expected 1000 accurate sessions, got %d", accurateCount)
	}
}
