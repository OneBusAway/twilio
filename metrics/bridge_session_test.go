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
