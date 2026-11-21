package query

import (
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestHARWindowScalarAndFullMetricParity(t *testing.T) {
	rec := &model.Recording{StartTime: 10, EndTime: 16, Records: []model.Record{
		{Type: model.TypeNetwork, ArchiveStartTime: 1794494972, Entry: json.RawMessage(`{"startedDateTime":"2026-11-12T14:49:44Z","time":500}`)},
		{Type: model.TypeNetwork, ArchiveStartTime: 1794494972, Entry: json.RawMessage(`{"startedDateTime":"2026-11-12T14:49:46Z","time":500}`)},
		{Type: model.TypeNetwork, Entry: json.RawMessage(`{"time":50}`)},
	}}
	for _, tc := range []struct{ from, to, want float64 }{{0, 0, 3}, {3, 5, 1}} {
		engine := engineFor(rec)
		for _, key := range []string{"overview.records", "network.requests"} {
			result := run(t, engine, Request{Operation: "metrics", Selector: key, From: tc.from, To: tc.to})
			if len(result.Rows) != 1 || result.Rows[0].Value != tc.want {
				t.Errorf("%s window [%v, %v]: %+v, want %v", key, tc.from, tc.to, result.Rows, tc.want)
			}
		}
	}
}
