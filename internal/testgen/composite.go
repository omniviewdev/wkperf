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
