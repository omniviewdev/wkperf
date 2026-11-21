package wailsprobe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDisabled(t *testing.T) {
	t.Setenv("OMNIVIEW_WKPERF_TRACE", "")
	t.Setenv("OMNIVIEW_WKPERF_READY", "")
	p, err := StartFromEnv()
	if p != nil || err != nil {
		t.Fatalf("disabled: %v, %v", p, err)
	}
	clock, err := p.Service().Clock("")
	if clock != nil || err != nil {
		t.Fatalf("disabled clock: %v, %v", clock, err)
	}
	if err := errors.Join(p.Mark("ignored", nil), p.Begin("ignored", "")(), p.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestExclusiveFiles(t *testing.T) {
	dir := t.TempDir()
	path, ready := filepath.Join(dir, "trace"), filepath.Join(dir, "ready")
	if err := os.WriteFile(ready, []byte("existing ready"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := start(path, ready)
	if p != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing ready: %v, %v", p, err)
	}
	data, err := os.ReadFile(ready)
	if err != nil || string(data) != "existing ready" {
		t.Fatalf("ready changed: %s, %v", data, err)
	}
	p, err = start(path, "")
	if p != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing trace: %v, %v", p, err)
	}
}

func TestCorrelatedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	p, err := start(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	s := p.Service()
	reading, err := s.Clock("cal:1")
	if err != nil {
		t.Fatal(err)
	}
	rows := []ClientEvent{
		{Stage: "bridge.js.call", Phase: "begin", TS: 1, Tx: "cal:1", Document: "doc:1", TimeOrigin: 1500, Detail: json.RawMessage(`{"calibration":1}`)},
		{Stage: "bridge.js.call", Phase: "end", TS: 2, Tx: "cal:1", Document: "doc:1", TimeOrigin: 1500},
		{Stage: "clock.anchor", Phase: "mark", TS: 2, Tx: "cal:1", Document: "doc:1", TimeOrigin: 1500, Detail: json.RawMessage(fmt.Sprintf(`{"jsSend":1,"jsReceive":2,"goTs":%d,"goClock":%q,"goUnit":"ns","timeOrigin":1500}`, reading.TS, hostClock))},
	}
	bad := append(append([]ClientEvent{}, rows...), ClientEvent{Stage: "bridge.go.handler"})
	if err := s.RecordEvents(bad); err == nil {
		t.Fatal("accepted forged host event")
	}
	if err := s.RecordEvents(rows); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if err := p.Mark("test.mark", nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	end := p.Begin("bridge.go.handler", "call:1", map[string]any{"requestBytes": 5, "responseBytes": 1})
	if err := errors.Join(end(), end(), p.Close()); err != nil {
		t.Fatal(err)
	}
	if err := p.Mark("test.mark", nil); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("mark after close: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type recordedEvent struct {
		Stage, Phase, Clock, Unit, Tx, Document string
		TS, TimeOrigin                          float64
		PID                                     int
		Detail                                  json.RawMessage
	}
	var events []recordedEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row recordedEvent
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		events = append(events, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 25 {
		t.Fatalf("got %d rows, want 25", len(events))
	}
	begin, stop := events[2], events[3]
	if begin.Phase != "begin" || stop.Phase != "end" || begin.Tx != "cal:1" || stop.Tx != begin.Tx ||
		begin.TS > float64(reading.TS) || stop.TS < float64(reading.TS) || begin.Clock != hostClock {
		t.Fatalf("calibration bounds: %+v, %+v, %+v", begin, stop, reading)
	}
	for i, row := range events[4:7] {
		if row.Clock != jsClock || row.Unit != "ms" || row.PID != os.Getpid() || row.TS != rows[i].TS ||
			row.Document != "doc:1" || row.TimeOrigin != 1500 || row.Tx != "cal:1" {
			t.Fatalf("bad browser clock: %+v", row)
		}
	}
	if events[23].Tx != "call:1" || string(events[23].Detail) != `{"requestBytes":5,"responseBytes":1}` || events[24].Phase != "end" {
		t.Fatalf("host handler rows: %+v", events[23:])
	}
}

func TestProfileAndCaptureState(t *testing.T) {
	dir := t.TempDir()
	path, readyPath := filepath.Join(dir, "events.ndjson"), filepath.Join(dir, "ready.json")
	t.Setenv("OMNIVIEW_WKPERF_TRACE", path)
	t.Setenv("OMNIVIEW_WKPERF_READY", readyPath)
	p, err := StartFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, f := range []string{path, readyPath} {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %v", f, info.Mode())
		}
	}
	data, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatal(err)
	}
	var ready readiness
	if err := json.Unmarshal(data, &ready); err != nil || ready.PID != os.Getpid() || !strings.HasPrefix(ready.URL, "http://127.0.0.1:") || ready.Token == "" {
		t.Fatalf("invalid readiness: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	get := func(path, token, origin, id string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ready.URL+path, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-WKPerf-Capture-ID", id)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return resp.StatusCode, body, err
	}
	status := func(path, token, origin string, want int) []byte {
		t.Helper()
		code, body, err := get(path, token, origin, "")
		if err != nil || code != want {
			t.Fatalf("%s: status %d, err %v; want %d", path, code, err, want)
		}
		return body
	}
	status("/debug/pprof/heap", "wrong", "", http.StatusUnauthorized)
	status("/debug/pprof/heap", ready.Token, "https://example.test", http.StatusForbidden)
	if body := status("/debug/pprof/heap", ready.Token, "", http.StatusOK); len(body) == 0 {
		t.Fatal("empty heap profile")
	}
	traceDone := make(chan error, 1)
	go func() {
		code, body, err := get("/debug/pprof/trace?seconds=2", ready.Token, "", "test-trace")
		if err == nil && (code != http.StatusOK || !bytes.HasPrefix(body, []byte("go ")) ||
			!bytes.Contains(body, []byte("wkperf.clock.begin")) || !bytes.Contains(body, []byte("wkperf.clock.end"))) {
			err = fmt.Errorf("invalid trace response: status %d, length %d", code, len(body))
		}
		traceDone <- err
	}()
	capturePath := "/debug/wkperf/capture?id=test-trace"
	state := func() captureState {
		t.Helper()
		var s captureState
		if err := json.Unmarshal(status(capturePath, ready.Token, "", http.StatusOK), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for {
		s := state()
		if s.Active && s.Start != nil && s.Stop == nil && s.Kind == "trace" {
			break
		}
		select {
		case err := <-traceDone:
			t.Fatalf("capture finished before active state was observed: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	var clock ClockReading
	if err := json.Unmarshal(status("/debug/wkperf/clock", ready.Token, "", http.StatusOK), &clock); err != nil || clock.Clock != hostClock || clock.PID != os.Getpid() {
		t.Fatalf("invalid clock: %+v, %v", clock, err)
	}
	if err := <-traceDone; err != nil {
		t.Fatal(err)
	}
	if s := state(); s.Active || s.Start == nil || s.Stop == nil || s.Start.TS > clock.TS || clock.TS > s.Stop.TS {
		t.Fatalf("invalid stopped state: %+v", s)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ready file after close: %v", err)
	}
}

func TestEventSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	p, err := start(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	fields := map[string]any{"value": strings.Repeat("x", maxEventBytes)}
	if err := p.Mark("oversized.mark", fields); !errors.Is(err, errEventTooLarge) {
		t.Fatalf("oversized Mark: %v", err)
	}
	if err := p.Begin("oversized.span", "test", fields)(); !errors.Is(err, errEventTooLarge) {
		t.Fatalf("oversized Begin: %v", err)
	}

	boundary := p.hostEvent(0, "boundary", "mark", "", map[string]string{"value": ""})
	encoded, err := json.Marshal(boundary)
	if err != nil {
		t.Fatal(err)
	}
	valueLen := maxEventBytes - len(encoded) - 1
	boundary.Detail = map[string]string{"value": strings.Repeat("x", valueLen+1)}
	if err := p.emit(p.hostEvent(0, "not.written", "mark", "", nil), boundary); !errors.Is(err, errEventTooLarge) {
		t.Fatalf("oversized batch: %v", err)
	}
	boundary.Detail = map[string]string{"value": strings.Repeat("x", valueLen)}
	if err := p.emit(boundary); err != nil {
		t.Fatalf("boundary event: %v", err)
	}
	if err := errors.Join(p.Mark("kept", nil), p.Close()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) != 5 || len(lines[2])+1 != maxEventBytes || !bytes.Contains(lines[3], []byte(`"stage":"kept"`)) {
		t.Fatalf("expected only startup, boundary, and kept events; got %d lines", len(lines)-1)
	}
}

func TestInspectorClockAnchor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	p, err := start(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	s := p.Service()
	anchor := ClientEvent{
		Stage: "webkit.clock.anchor", Phase: "mark", Tx: "doc:1-2", Document: "doc:1",
		TS: 12.5, TimeOrigin: 1500, Detail: json.RawMessage(`{"jsBefore":12.25,"jsAfter":12.5}`),
	}
	for _, detail := range []string{
		`{"jsBefore":13,"jsAfter":12.5}`,
		`{"jsBefore":12,"jsAfter":13}`,
		`{"jsBefore":-1,"jsAfter":12.5}`,
		`{"jsBefore":null,"jsAfter":12.5}`,
		`{"jsBefore":12,"jsAfter":12.5,"extra":1}`,
	} {
		bad := anchor
		bad.Detail = json.RawMessage(detail)
		if err := s.RecordEvents([]ClientEvent{bad}); err == nil {
			t.Errorf("accepted invalid Inspector anchor: %s", detail)
		}
	}
	bad := anchor
	bad.Tx = ""
	if err := s.RecordEvents([]ClientEvent{bad}); err == nil {
		t.Error("accepted Inspector anchor without transaction ID")
	}
	if err := errors.Join(s.RecordEvents([]ClientEvent{anchor}), p.Close()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("expected two startup events and one Inspector anchor; got %d events", len(lines))
	}
	var row struct {
		ClientEvent
		Clock string
		Unit  string
		PID   int
	}
	if err := json.Unmarshal(lines[2], &row); err != nil {
		t.Fatal(err)
	}
	if row.Clock != jsClock || row.Unit != "ms" || row.PID != os.Getpid() || row.Stage != anchor.Stage ||
		row.Phase != anchor.Phase || row.Tx != anchor.Tx || row.Document != anchor.Document ||
		row.TS != anchor.TS || row.TimeOrigin != anchor.TimeOrigin || !bytes.Equal(row.Detail, anchor.Detail) {
		t.Fatalf("Inspector anchor changed: %s", lines[2])
	}
}
