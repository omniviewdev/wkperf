package inspector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestCapture(t *testing.T) {
	for _, tc := range []struct {
		name                string
		routed, omitSamples bool
	}{
		{name: "direct"},
		{name: "routed", routed: true},
		{name: "routed without sampling", routed: true, omitSamples: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := fixtureEndpoint(t, tc.routed, "", tc.omitSamples)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var out bytes.Buffer
			if err := Capture(ctx, Options{Endpoint: endpoint, Duration: time.Millisecond}, &out); err != nil {
				t.Fatal(err)
			}
			r, err := model.Parse(&out)
			if err != nil {
				t.Fatal(err)
			}
			wantRecords, wantStatus := 3, "unsupported"
			if tc.routed {
				wantRecords, wantStatus = 5, "captured"
			}
			if r.CaptureCapabilities["CPUProfiler"] != wantStatus || r.CaptureCapabilities["Memory"] != wantStatus {
				t.Fatalf("unexpected resource capabilities: %+v", r.CaptureCapabilities)
			}
			if r.StartTime != 2 || r.EndTime != 3 || len(r.Records) != wantRecords || r.Records[0].Type != model.TypeRenderingFrame {
				t.Fatalf("unexpected recording: %+v", r)
			}
			if r.Records[1].EventType != "event-dispatched" || string(r.Records[1].Details) != `"click"` || r.Records[2].EventType != "paint" {
				t.Fatalf("unexpected records: %+v", r.Records)
			}
			if tc.routed && (r.Records[3].Usage != 17 || r.Records[4].Categories[0].Size != 1024 || len(r.MemoryPressureEvents) != 1) {
				t.Fatalf("unexpected resource evidence: %+v", r)
			}
			if len(r.Markers) != 1 || r.Markers[0].Type != "timestamp" || r.Markers[0].Time != 2.625 || string(r.Markers[0].Details) != `"wkperf.clock.fixture"` {
				t.Fatalf("unexpected clock markers: %+v", r.Markers)
			}
			if tc.omitSamples {
				if r.Samples == nil || len(r.Samples) != 0 {
					t.Fatalf("unexpected samples: %+v", r.Samples)
				}
				return
			}
			samples := r.Samples[0]
			if samples.StackTraces[1].StackFrames[0].Name != "leaf" || samples.Durations[0] != 0.001 || samples.Durations[1] != 0.25 {
				t.Fatalf("unexpected samples: %+v", samples)
			}
		})
	}
}

func TestCaptureWorkload(t *testing.T) {
	endpoint := fixtureEndpoint(t, false, "", false)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, logs bytes.Buffer
	if err := Capture(ctx, Options{
		Endpoint: endpoint,
		Workload: []string{binary, "-test.run=^TestFixtureWorkload$", "--", "one argument"},
		Output:   &logs,
	}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "workload received one argument") {
		t.Fatalf("workload did not run or its output was lost: %q", logs.String())
	}
	if _, err := model.Parse(&out); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureWorkload(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) && os.Args[i+1] == "one argument" {
			fmt.Println("workload received one argument")
			return
		}
	}
}

func TestCaptureCancellation(t *testing.T) {
	endpoint := fixtureEndpoint(t, false, "", false) + "/private-target?token=private-query"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := Capture(ctx, Options{Endpoint: endpoint, Duration: time.Hour}, &out)
	if !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatalf("canceled capture: err=%v output=%q", err, out.Bytes())
	}
	if strings.Contains(err.Error(), "private-target") || strings.Contains(err.Error(), "private-query") {
		t.Fatalf("canceled dial exposed the endpoint: %v", err)
	}
}

func TestCaptureRejectsRedirect(t *testing.T) {
	reached := make(chan struct{}, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached <- struct{}{}
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	var out bytes.Buffer
	endpoint := "ws" + strings.TrimPrefix(source.URL, "http") + "/private-target?token=private-query"
	err := Capture(context.Background(), Options{Endpoint: endpoint, Duration: time.Millisecond}, &out)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") || out.Len() != 0 {
		t.Fatalf("redirected capture: err=%v output=%q", err, out.Bytes())
	}
	select {
	case <-reached:
		t.Fatal("capture contacted the redirect destination")
	default:
	}
}

func TestCaptureRedactsHandshakeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		status       int
	}{
		{"unauthorized", "HTTP 401", http.StatusUnauthorized},
		{"invalid upgrade", "Upgrade header", http.StatusSwitchingProtocols},
		{"invalid redirect", "Location header", http.StatusFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch tc.status {
				case http.StatusSwitchingProtocols:
					conn, rw, err := http.NewResponseController(w).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: ws://%s%s\r\n\r\n", r.Host, r.URL.RequestURI())
					if err := rw.Flush(); err != nil {
						t.Error(err)
					}
				case http.StatusFound:
					w.Header().Set("Location", "ws://user:private-redirect@example.invalid/%bad%")
					w.WriteHeader(tc.status)
				default:
					w.WriteHeader(tc.status)
					fmt.Fprint(w, r.URL.RequestURI())
				}
			}))
			defer srv.Close()
			endpoint := "ws" + strings.TrimPrefix(srv.URL, "http") + "/private-target?token=private-query"
			var out bytes.Buffer
			err := Capture(context.Background(), Options{Endpoint: endpoint}, &out)
			if err == nil || !strings.Contains(err.Error(), tc.reason) || out.Len() != 0 {
				t.Fatalf("handshake error: err=%v output=%q", err, out.Bytes())
			}
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				for _, secret := range []string{"private-target", "private-query", "private-redirect", srv.URL} {
					if strings.Contains(cause.Error(), secret) {
						t.Fatalf("handshake error exposed the endpoint or redirect: %v", cause)
					}
				}
			}
		})
	}
}

func TestCaptureScript(t *testing.T) {
	for _, mode := range []string{"resolve", "reject", "Timeline.recordingStopped", "ScriptProfiler.trackingComplete", "CPUProfiler.trackingComplete", "Memory.trackingComplete"} {
		t.Run(mode, func(t *testing.T) {
			endpoint := fixtureEndpoint(t, true, mode, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "workload.js")
			if err := os.WriteFile(path, []byte("await trustedWorkload();"), 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err := Capture(ctx, Options{Endpoint: endpoint, Script: path}, &out)
			if mode == "reject" {
				if err == nil || !strings.Contains(err.Error(), "sentinel exception") || out.Len() != 0 {
					t.Fatalf("rejected capture: err=%v output=%q", err, out.Bytes())
				}
				return
			}
			if mode != "resolve" {
				if err == nil || !strings.Contains(err.Error(), "before wkperf requested it ("+mode+")") || out.Len() != 0 {
					t.Fatalf("premature stop: err=%v output=%q", err, out.Bytes())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "trustedWorkload") || strings.Contains(out.String(), "private value") {
				t.Fatal("recording persisted workload source or evaluation result")
			}
			if _, err := model.Parse(&out); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func fixtureEndpoint(t *testing.T, routed bool, scriptMode string, omitSamples bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		write := func(msg string) bool {
			if err := ws.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
				t.Error(err)
				return false
			}
			return true
		}
		if routed && !write(`{"method":"Target.targetCreated","params":{"targetInfo":{"targetId":"page-id","type":"page"}}}`) {
			return
		}
		methods := []string{"Timeline.enable", "Page.enable", "CPUProfiler.startTracking", "Memory.enable"}
		if routed {
			methods = append(methods, "Memory.startTracking")
		}
		methods = append(methods, "ScriptProfiler.startTracking", "Timeline.start")
		if scriptMode != "" {
			methods = append(methods, "Runtime.evaluate", "Runtime.awaitPromise")
		}
		if scriptMode == "" || scriptMode == "resolve" {
			methods = append(methods, "Timeline.stop", "ScriptProfiler.stopTracking")
			if routed {
				methods = append(methods, "CPUProfiler.stopTracking", "Memory.stopTracking")
			}
		}
		for _, want := range methods {
			if routed && want == "Timeline.enable" {
				_, raw, err := ws.Read(ctx)
				var root request
				if err != nil || json.Unmarshal(raw, &root) != nil || root.Method != want {
					t.Errorf("unexpected root Timeline probe: %s (%v)", raw, err)
					return
				}
				if !write(fmt.Sprintf(`{"id":%d,"error":{"code":-32601,"message":"unknown domain"}}`, root.ID)) {
					return
				}
			}
			_, raw, err := ws.Read(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			if routed {
				var outer struct {
					ID     int            `json:"id"`
					Method string         `json:"method"`
					Params targetEnvelope `json:"params"`
				}
				if err := json.Unmarshal(raw, &outer); err != nil || outer.Method != "Target.sendMessageToTarget" || outer.Params.TargetID != "page-id" {
					t.Errorf("invalid target wrapper: %s (%v)", raw, err)
					return
				}
				if !write(fmt.Sprintf(`{"id":%d,"result":{}}`, outer.ID)) {
					return
				}
				raw = []byte(outer.Params.Message)
			}
			var req request
			if err := json.Unmarshal(raw, &req); err != nil || req.Method != want {
				t.Errorf("expected %s: got %s (%v)", want, raw, err)
				return
			}
			send := func(msg string) bool {
				if routed {
					inner, _ := json.Marshal(targetEnvelope{"page-id", msg})
					msg = `{"method":"Target.dispatchMessageFromTarget","params":` + string(inner) + `}`
				}
				return write(msg)
			}
			switch want {
			case "CPUProfiler.startTracking", "Memory.enable":
				if !routed {
					if !send(fmt.Sprintf(`{"id":%d,"error":{"code":-32601,"message":"unknown domain"}}`, req.ID)) {
						return
					}
					continue
				}
			case "CPUProfiler.stopTracking":
				if !send(`{"method":"CPUProfiler.trackingUpdate","params":{"event":{"timestamp":2.7,"usage":17,"threads":[{"name":"main","usage":12}]}}}`) ||
					!send(`{"method":"CPUProfiler.trackingComplete","params":{"timestamp":3}}`) {
					return
				}
			case "Memory.stopTracking":
				if !send(`{"method":"Memory.trackingUpdate","params":{"event":{"timestamp":2.8,"categories":[{"type":"javascript","size":1024}]}}}`) ||
					!send(`{"method":"Memory.memoryPressure","params":{"timestamp":2.9,"severity":"non-critical"}}`) ||
					!send(`{"method":"Memory.trackingComplete","params":{"timestamp":3}}`) {
					return
				}
			case "ScriptProfiler.startTracking":
				if !send(`{"method":"ScriptProfiler.trackingStart","params":{"timestamp":2}}`) {
					return
				}
			case "Timeline.start":
				if !send(`{"method":"Timeline.recordingStarted","params":{"startTime":2}}`) {
					return
				}
			case "Runtime.evaluate":
				params, _ := req.Params.(map[string]any)
				if params["expression"] != "(async () => {\nawait trustedWorkload();\n})()" {
					t.Errorf("unexpected script expression: %v", params)
					return
				}
				if !send(fmt.Sprintf(`{"id":%d,"result":{"result":{"type":"object","objectId":"promise"}}}`, req.ID)) {
					return
				}
				continue
			case "Runtime.awaitPromise":
				params, _ := req.Params.(map[string]any)
				if params["promiseObjectId"] != "promise" {
					t.Errorf("unexpected promise ID: %v", params)
					return
				}
				if scriptMode == "reject" {
					send(fmt.Sprintf(`{"id":%d,"result":{"wasThrown":true,"result":{"type":"object","description":"Error: sentinel exception"}}}`, req.ID))
					return
				}
				if scriptMode != "resolve" {
					send(fmt.Sprintf(`{"method":%q,"params":{"endTime":3,"timestamp":3}}`, scriptMode))
					return
				}
				if !send(fmt.Sprintf(`{"id":%d,"result":{"result":{"type":"string","value":"private value"}}}`, req.ID)) {
					return
				}
				continue
			case "Timeline.stop":
				if !send(`{"method":"Timeline.eventRecorded","params":{"record":{"type":"RenderingFrame","startTime":2.1,"endTime":2.9,"children":[{"type":"EventDispatch","data":{"type":"click"},"children":[{"type":"FunctionCall","startTime":2.25,"endTime":2.5}]},{"type":"Paint","startTime":2.5,"endTime":2.6}]}}}`) ||
					!send(`{"method":"Timeline.eventRecorded","params":{"record":{"type":"TimeStamp","startTime":2.625,"data":{"message":"wkperf.clock.fixture"}}}}`) ||
					!send(`{"method":"Timeline.eventRecorded","params":{"record":{"type":"TimeStamp","startTime":2.75,"data":{"message":"non-calibration timestamp"}}}}`) ||
					!send(`{"method":"Timeline.recordingStopped","params":{"endTime":3}}`) {
					return
				}
			case "ScriptProfiler.stopTracking":
				completion := `{"method":"ScriptProfiler.trackingComplete","params":{"timestamp":3}}`
				if !omitSamples {
					completion = `{"method":"ScriptProfiler.trackingComplete","params":{"timestamp":3,"samples":{"stackTraces":[{"timestamp":0,"stackFrames":[]},{"timestamp":2.375,"stackFrames":[{"name":"leaf"},{"name":"caller"}]}]}}}`
				}
				if !send(`{"method":"ScriptProfiler.trackingUpdate","params":{"event":{"type":"Other","startTime":2.25,"endTime":2.5}}}`) ||
					!send(completion) {
					return
				}
			}
			if !send(fmt.Sprintf(`{"id":%d,"result":{}}`, req.ID)) {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}
