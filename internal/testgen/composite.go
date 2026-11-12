package testgen

import "github.com/omniviewdev/wkperf/internal/model"

// compositeRecord is a composite layout record starting at startSec that WebKit
// reports as ending durSec later.
func compositeRecord(startSec, durSec float64) model.Record {
	return model.Record{Type: model.TypeLayout, EventType: "composite", StartTime: startSec, EndTime: startSec + durSec}
}

// StalledComposites is a 30-second idle capture whose compositor reports two
// composite records ending 12.5 and 13.5 seconds late. A 49 ms composite and a
// 6 ms composite are ordinary work, and a 100 ms timer runs inside the first
// stalled span. The fixture lists rendering-frame instrumentation but, like real
// WKWebView exports, contains no rendering-frame records.
func StalledComposites() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Stalled composite records"
	rec.EndTime = 30
	rec.Records = []model.Record{
		compositeRecord(1, 12.5),
		compositeRecord(2, .049),
		scriptRecord("timer-fired", 3, 3.1),
		compositeRecord(14, 13.5),
		compositeRecord(28, .006),
	}
	return rec
}

// CompositeCadenceRuns is a capture without rendering-frame records whose
// composite starts form two active runs 250 ms apart plus one isolated start.
// Within the runs the 20 start intervals, in milliseconds, are ten of 16, five
// of 18, two of 30 and one each of 40, 60 and 90: p50 17, p95 61.5, max 90,
// five intervals over 1.5 frame budgets (25.05 ms), two over 50 ms and half a
// second of run time. The fourth start belongs to a composite WebKit reports as
// ending three seconds late; its start still counts.
func CompositeCadenceRuns() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Composite cadence runs"
	runs := [][]float64{
		{16, 16, 16, 16, 16, 18, 18, 18, 30, 60},
		{16, 16, 16, 16, 16, 18, 18, 30, 40, 90},
	}
	at := 1.0
	starts := 0
	for _, run := range runs {
		for i := 0; i <= len(run); i++ {
			durSec := .002
			if starts == 3 {
				durSec = 3
			}
			rec.Records = append(rec.Records, compositeRecord(at, durSec))
			starts++
			if i < len(run) {
				at += run[i] / 1000
			}
		}
		at += .25
	}
	rec.Records = append(rec.Records, compositeRecord(at+.05, .002))
	return rec
}
