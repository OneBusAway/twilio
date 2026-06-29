package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"oba-twilio/handlers/common"
)

type fakeSessionSource struct{ sm common.SessionMetrics }

func (f fakeSessionSource) GetMetrics() *common.SessionMetrics { return &f.sm }

type nilSessionSource struct{}

func (nilSessionSource) GetMetrics() *common.SessionMetrics { return nil }

func TestSessionBridgeEmitsLabelledSeries(t *testing.T) {
	src := fakeSessionSource{sm: common.SessionMetrics{
		TotalSessions: 5, CacheHits: 9, CacheMisses: 1,
		Evictions: 2, ExpiredSessions: 3, CreatedSessions: 8,
	}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(newSessionCollector("sms", src))

	expected := `
# HELP session_store_active_sessions Active sessions in the store.
# TYPE session_store_active_sessions gauge
session_store_active_sessions{store="sms"} 5
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"session_store_active_sessions"); err != nil {
		t.Error(err)
	}
}

func TestSessionBridgeEmitsAllCounters(t *testing.T) {
	src := fakeSessionSource{sm: common.SessionMetrics{
		TotalSessions: 5, CacheHits: 9, CacheMisses: 1,
		Evictions: 2, ExpiredSessions: 3, CreatedSessions: 8,
	}}
	reg := prometheus.NewRegistry()
	reg.MustRegister(newSessionCollector("sms", src))

	expected := `
# HELP session_store_cache_hits_total Session-store cache hits.
# TYPE session_store_cache_hits_total counter
session_store_cache_hits_total{store="sms"} 9
# HELP session_store_cache_misses_total Session-store cache misses.
# TYPE session_store_cache_misses_total counter
session_store_cache_misses_total{store="sms"} 1
# HELP session_store_created_total Sessions created.
# TYPE session_store_created_total counter
session_store_created_total{store="sms"} 8
# HELP session_store_evictions_total Sessions evicted.
# TYPE session_store_evictions_total counter
session_store_evictions_total{store="sms"} 2
# HELP session_store_expired_total Sessions expired.
# TYPE session_store_expired_total counter
session_store_expired_total{store="sms"} 3
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"session_store_cache_hits_total",
		"session_store_cache_misses_total",
		"session_store_evictions_total",
		"session_store_expired_total",
		"session_store_created_total",
	); err != nil {
		t.Error(err)
	}
}

func TestSessionBridgeDualRegistration(t *testing.T) {
	smsSrc := fakeSessionSource{sm: common.SessionMetrics{TotalSessions: 3, CacheHits: 5}}
	voiceSrc := fakeSessionSource{sm: common.SessionMetrics{TotalSessions: 7, CacheHits: 2}}

	reg := prometheus.NewRegistry()
	reg.MustRegister(newSessionCollector("sms", smsSrc))
	reg.MustRegister(newSessionCollector("voice", voiceSrc))

	expected := `
# HELP session_store_active_sessions Active sessions in the store.
# TYPE session_store_active_sessions gauge
session_store_active_sessions{store="sms"} 3
session_store_active_sessions{store="voice"} 7
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		"session_store_active_sessions"); err != nil {
		t.Error(err)
	}
}

func TestSessionBridgeNilSourceNoPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(newSessionCollector("sms", nilSessionSource{}))

	// Scrape must not panic and must produce no series.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "session_store") {
			t.Errorf("expected no session_store series, got %s", mf.GetName())
		}
	}
}
