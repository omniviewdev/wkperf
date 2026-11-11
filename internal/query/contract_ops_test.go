package query

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

func TestKeyDiscoverySelectorsAndPagination(t *testing.T) {
	rec := fixture()
	rec.Records = []model.Record{{Type: model.TypeScript, EventType: "timer-fired", StartTime: 10, EndTime: 11}}
	engine := engineFor(rec)
	metrics := run(t, engine, Request{Operation: "metrics", Limit: MaxLimit})
	root := run(t, engine, Request{Operation: "rootcause", Limit: MaxLimit})
	keys := run(t, engine, Request{Operation: "keys", Limit: MaxLimit})
	if keys.TotalCount != metrics.TotalCount+root.TotalCount {
		t.Fatalf("discovery count %d does not cover metrics %d and rootcause %d", keys.TotalCount, metrics.TotalCount, root.TotalCount)
	}
	for _, row := range keys.Rows {
		if row.Key == "" || row.Operation != "metrics" && row.Operation != "rootcause" || row.Unit == "" || row.Description == "" || row.Basis == "" || strings.Contains(row.Key, "%00") {
			t.Fatalf("incomplete key metadata: %+v", row)
		}
	}
	var paged []Row
	for offset := 0; ; {
		page := run(t, engine, Request{Operation: "keys", Limit: 7, Offset: offset})
		paged = append(paged, page.Rows...)
		if page.Offset != offset || page.ReturnedCount != len(page.Rows) || page.TotalCount != keys.TotalCount {
			t.Fatalf("incorrect page metadata: %+v", page)
		}
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset <= offset {
			t.Fatal("pagination did not advance")
		}
		offset = *page.NextOffset
	}
	if !reflect.DeepEqual(keys.Rows, paged) {
		t.Fatal("paging changed key order or content")
	}
	prefix := run(t, engine, Request{Operation: "keys", Selector: "wakeups.", Limit: MaxLimit})
	if len(prefix.Rows) == 0 || prefix.Rows[0].Key != "wakeups.count" {
		t.Fatalf("unreachable key family: %+v", prefix.Rows)
	}
	for _, operation := range []string{"metrics", "rootcause"} {
		prefix := run(t, engine, Request{Operation: operation, Selector: "samples.*", Limit: MaxLimit})
		regex := run(t, engine, Request{Operation: operation, Selector: "/^samples\\./", Limit: MaxLimit})
		if len(prefix.Rows) == 0 || !reflect.DeepEqual(prefix.Rows, regex.Rows) {
			t.Fatalf("%s prefix/regex differ: %+v / %+v", operation, prefix.Rows, regex.Rows)
		}
	}
}

func TestUnknownKeyCandidatesAreBoundedAndDeterministic(t *testing.T) {
	engine := engineFor(fixture())
	var first []string
	for i := range 3 {
		_, err := engine.Run(context.Background(), Request{File: "sample.json", Operation: "metrics", Selector: "samples.total_ms"})
		var detail *diagnostic.Error
		if !errors.As(err, &detail) || detail.Code != diagnostic.UnknownKey || len(detail.Candidates) == 0 || len(detail.Candidates) > 10 {
			t.Fatalf("missing bounded typed candidates: %v", err)
		}
		if i == 0 {
			first = detail.Candidates
		} else if !reflect.DeepEqual(first, detail.Candidates) {
			t.Fatal("unknown-key candidates changed")
		}
	}
	if !strings.Contains(strings.Join(first, " "), "samples.sampled_ms") {
		t.Fatalf("missing nearest key: %v", first)
	}
}

func TestScalarAliasesAndCanonicalFunctionKeys(t *testing.T) {
	rec := fixture()
	rec.Samples[0].Durations[0] = .040000000123456
	engine := engineFor(rec)
	value := func(operation, key string) float64 {
		return run(t, engine, Request{Operation: operation, Selector: key}).Rows[0].Value
	}
	if value("metrics", "overview.duration_s") != value("metrics", "recording.duration_sec") {
		t.Fatal("duration alias changed value")
	}
	if value("metrics", "samples.sampled_ms") != value("rootcause", "samples.self_ms") {
		t.Fatal("sampled-weight alias changed rounding")
	}
	top := run(t, engine, Request{Operation: "top"})
	for _, row := range top.Rows {
		key := "functions." + row.ID + ".self_ms"
		if value("metrics", key) != row.SelfMS {
			t.Fatalf("canonical function key %s changed sampled self time", key)
		}
	}
}

func TestEveryRowOperationSupportsOffset(t *testing.T) {
	rec := fixture()
	rec.Records = []model.Record{{Type: model.TypeScript, EventType: "timer-fired", StartTime: 10, EndTime: 11}, {Type: model.TypeScript, EventType: "event-dispatched", Details: json.RawMessage(`"click"`), StartTime: 11, EndTime: 12}}
	engine := engineFor(rec)
	for _, operation := range Operations {
		if operation == "window" {
			continue
		}
		t.Run(operation, func(t *testing.T) {
			req := Request{Operation: operation, Limit: MaxLimit}
			switch operation {
			case "paths", "callers", "callees", "subtree":
				req.Selector = "render"
			case "diff":
				req.Baseline, req.Candidate = "before.json", "after.json"
			}
			all := run(t, engine, req)
			req.Offset, req.Limit = 1, 2
			if operation == "events" {
				// The row limit also controls event function previews. Keep those
				// fixed while checking offset independently of nested truncation.
				req.Limit = MaxLimit
			}
			page := run(t, engine, req)
			want := all.Rows[min(1, len(all.Rows)):min(1+req.Limit, len(all.Rows))]
			if page.TotalCount != all.TotalCount || page.ReturnedCount != len(want) || !reflect.DeepEqual(page.Rows, want) {
				t.Fatalf("offset changed operation: %+v; want %+v", page, want)
			}
			req.Offset = all.TotalCount + 5
			empty := run(t, engine, req)
			if len(empty.Rows) != 0 || empty.NextOffset != nil || empty.TotalCount != all.TotalCount {
				t.Fatalf("past-end page changed totals: %+v", empty)
			}
		})
	}
}

func TestDiffNormalizationKeepsRawContextAndZeroRateChange(t *testing.T) {
	before, after := fixture(), fixture()
	after.EndTime = 14
	for _, trace := range before.Samples[0].StackTraces {
		trace.Timestamp += 2
		after.Samples[0].StackTraces = append(after.Samples[0].StackTraces, trace)
	}
	after.Samples[0].Durations = append(after.Samples[0].Durations, before.Samples[0].Durations...)
	engine := &Engine{Loader: func(_ context.Context, file string, _ SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		if file == "before.json" {
			return before, sourcemap.Diagnostics{}, nil
		}
		return after, sourcemap.Diagnostics{}, nil
	}}
	for _, mode := range []string{"", "auto", "per-second", "share", "none"} {
		result := run(t, engine, Request{Operation: "diff", Baseline: "before.json", Candidate: "after.json", Normalize: mode})
		normalization := result.DiffNormalization
		if normalization == nil || normalization.BeforeTotalMs != 100 || normalization.AfterTotalMs != 200 || normalization.BeforeActiveMs != 2000 || normalization.AfterActiveMs != 4000 {
			t.Fatalf("raw context lost: %+v", normalization)
		}
		if len(normalization.Warnings) == 0 {
			t.Fatal("comparison warnings lost")
		}
		for _, row := range result.Rows {
			if row.CandidateTotalMS != 2*row.BaselineTotalMS || row.CandidateSelfMS != 2*row.BaselineSelfMS {
				t.Fatalf("raw function weights changed: %+v", row)
			}
			if mode != "none" && (math.Abs(row.DeltaSelfMS) > 1e-9 || math.Abs(row.DeltaTotalMS) > 1e-9) {
				t.Fatalf("same rate appears changed: %+v", row)
			}
		}
		if mode != "none" && mode != "share" && normalization.Mode != flame.DiffPerSecond {
			t.Fatalf("auto did not resolve duration mismatch: %+v", normalization)
		}
	}
}

func TestDefaultLoaderPreservesMapCoverageAndHints(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "x", URL: "https://example.test/app.min.js", Line: 1, Column: 1001}}}},
		Durations:   []float64{.01},
	}}}
	dir := t.TempDir()
	file := filepath.Join(dir, "recording.json")
	payload, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	result := run(t, &Engine{}, Request{File: file, Operation: "top"})
	diagnostics := result.Sources[0].SourceMaps
	if diagnostics.UnmappedFrames != 1 || len(diagnostics.MissingMaps) != 1 {
		t.Fatalf("unmapped source hint lost: %+v", diagnostics)
	}
	mapPath := filepath.Join(dir, "app.min.js.map")
	if err := os.WriteFile(mapPath, []byte(`{"version":3,"file":"app.min.js","sources":["app.ts"],"sourcesContent":["function render() {}"],"names":["render"],"mappings":"AAAAA"}`), 0600); err != nil {
		t.Fatal(err)
	}
	mapped := run(t, &Engine{}, Request{File: file, Operation: "top", Sourcemaps: []string{mapPath}})
	if got := mapped.Sources[0].SourceMaps; got.MappedFrames != 1 || got.UnmappedFrames != 0 || len(got.MissingMaps) != 0 {
		t.Fatalf("mapped source coverage lost: %+v", got)
	}
}

func TestQueryFailuresKeepTypedCodeAndFile(t *testing.T) {
	for _, test := range []struct {
		request Request
		code    diagnostic.Code
		file    string
	}{
		{Request{Operation: "top", Offset: -1}, diagnostic.UsageError, ""},
		{Request{Operation: "top", MaxBytes: MaxBytes + 1}, diagnostic.UsageError, ""},
		{Request{Operation: "top", MaxStackFrames: -1}, diagnostic.UsageError, ""},
		{Request{Operation: "diff", Normalize: "invalid"}, diagnostic.UsageError, ""},
		{Request{Operation: "top", From: 9}, diagnostic.WindowError, "sample.json"},
		{Request{Operation: "paths", Selector: "missing"}, diagnostic.SelectorError, "sample.json"},
		{Request{Operation: "metrics", Selector: "/[/"}, diagnostic.SelectorError, "sample.json"},
	} {
		test.request.File = "sample.json"
		_, err := engineFor(fixture()).Run(context.Background(), test.request)
		var detail *diagnostic.Error
		if !errors.As(err, &detail) || detail.Code != test.code || detail.File != test.file {
			t.Errorf("request %+v: got %v, want %s with file %q", test.request, err, test.code, test.file)
		}
	}
}
