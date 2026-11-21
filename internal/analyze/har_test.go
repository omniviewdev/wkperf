package analyze

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

func TestHARWindowUsesRecordingClock(t *testing.T) {
	rec, err := model.Parse(strings.NewReader(`{
		"startTime": 10, "endTime": 17,
		"discontinuities": [{"startTime":12,"endTime":14}],
		"records": [
			{"type":"timeline-record-type-network","archiveStartTime":1794494972,"entry":{"startedDateTime":"2026-11-12T14:49:43.000Z","time":1000,"response":{"_transferSize":0,"bodySize":500}}},
			{"type":"timeline-record-type-network","archiveStartTime":1794494972,"entry":{"startedDateTime":"2026-11-12T14:49:44.000Z","time":500,"response":{"_transferSize":500}}},
			{"type":"timeline-record-type-network","archiveStartTime":1794494972,"entry":{"startedDateTime":"2026-11-12T14:49:46.000Z","time":250,"response":{"headersSize":25,"bodySize":75}}},
			{"type":"timeline-record-type-network","archiveStartTime":1794494972,"entry":{"startedDateTime":"2026-11-12T14:49:47.000Z","time":0,"response":{"_transferSize":400}}},
			{"type":"timeline-record-type-network","entry":{"startedDateTime":"2026-11-12T14:49:46.500Z","time":50,"response":{"bodySize":500}}},
			{"type":"timeline-record-type-network","startTime":14.5,"endTime":14.5,"archiveStartTime":1794494972,"entry":{"startedDateTime":"2026-11-12T14:49:44.000Z","time":-1,"response":{"_transferSize":0}}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	full := Summarize(rec, "")
	if val(t, full, "network.requests") != 5 || val(t, full, "network.transfer_unknown_requests") != 1 {
		t.Fatalf("whole-recording metrics: %+v", full.Metrics)
	}
	if _, ok := full.Lookup("network.transfer_bytes"); ok {
		t.Fatal("incomplete transfer total emitted")
	}
	direct := SummarizeWith(rec, "", Options{FromSec: 1.5, ToSec: 5})
	clipped := profile.Clip(rec, 11.5, 15)
	if len(clipped.Records) != 3 {
		t.Fatalf("clip retained %d records, want 3", len(clipped.Records))
	}
	for _, summary := range []*metrics.Summary{direct, Summarize(clipped, "")} {
		for key, want := range map[string]float64{
			"overview.records": 3, "network.requests": 3, "network.transfer_bytes": 100,
			"network.transfer_unknown_requests": 0, "network.mean_ms": 625, "network.p95_ms": 962.5,
		} {
			if got := val(t, summary, key); !approx(got, want) {
				t.Errorf("%s = %g, want %g", key, got, want)
			}
		}
	}
	zero := SummarizeWith(rec, "", Options{FromSec: 1.5, ToSec: 2})
	if val(t, zero, "network.transfer_bytes") != 0 || val(t, zero, "network.transfer_unknown_requests") != 0 {
		t.Fatalf("measured zero not retained: %+v", zero.Metrics)
	}
}
