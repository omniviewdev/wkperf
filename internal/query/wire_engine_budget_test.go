package query

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func deepBudgetRecording(reverse bool) *model.Recording {
	traces := make([]model.StackTrace, 120)
	durations := make([]float64, len(traces))
	for i := range traces {
		frames := make([]model.StackFrame, 400)
		for j := range frames {
			position := len(frames) - j - 1
			frames[j] = model.StackFrame{Name: fmt.Sprintf("render%03d", position), URL: "wails://app/assets/index.js", Line: position + 1, Column: 1}
		}
		frames[0].Name = fmt.Sprintf("leaf%03d", i)
		traces[i] = model.StackTrace{Timestamp: .01 + float64(i)*.05, StackFrames: frames}
		durations[i] = .001
	}
	if reverse {
		for i, j := 0, len(traces)-1; i < j; i, j = i+1, j-1 {
			traces[i], traces[j] = traces[j], traces[i]
			durations[i], durations[j] = durations[j], durations[i]
		}
	}
	return &model.Recording{StartTime: 0, EndTime: 12, Samples: []model.TargetSamples{{StackTraces: traces, Durations: durations}}}
}

func TestV2EngineDeepDefaultAndExplicitExact(t *testing.T) {
	engine := engineFor(deepBudgetRecording(false))
	result := run(t, engine, Request{Operation: "stacks"})
	if result.TotalCount != 120 || result.ReturnedCount == 0 || result.ReturnedCount >= DefaultLimit || !hasReason(result.TruncatedBy, "bytes") || !hasReason(result.TruncatedBy, "stack_frames") {
		t.Fatalf("deep default not useful and bounded: rows=%d total=%d reasons=%v", result.ReturnedCount, result.TotalCount, result.TruncatedBy)
	}
	_, encoded := wireObject(t, result, "json")
	if len(encoded) > DefaultMaxBytes {
		t.Fatalf("default actual bytes=%d", len(encoded))
	}
	shuffled := run(t, engineFor(deepBudgetRecording(true)), Request{Operation: "stacks"})
	_, shuffledBytes := wireObject(t, shuffled, "json")
	if !bytes.Equal(encoded, shuffledBytes) {
		t.Fatal("deep byte prefix changed after shuffling samples")
	}
	exact := run(t, engine, Request{Operation: "stacks", Selector: result.Rows[0].NodeID, MaxStackFramesSet: true, MaxBytes: MaxBytes})
	document, _ := wireObject(t, exact, "json")
	rows := document["rows"].([]any)
	if len(rows) != 1 || len(rows[0].(map[string]any)["stack"].([]any)) != 400 || exact.Truncated {
		t.Fatalf("explicit exact stack lost detail: rows=%d reasons=%v", len(rows), exact.TruncatedBy)
	}
}
