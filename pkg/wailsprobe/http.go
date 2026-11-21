package wailsprobe

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"time"
)

type readiness struct {
	URL   string `json:"url"`
	PID   int    `json:"pid"`
	Token string `json:"token"`
}

type captureState struct {
	ID     string        `json:"id"`
	Kind   string        `json:"kind,omitempty"`
	Active bool          `json:"active"`
	Start  *ClockReading `json:"start,omitempty"`
	Stop   *ClockReading `json:"stop,omitempty"`
}

const writeBudget = 5 * time.Second

func (p *Probe) serve(path string) error {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := ln.Addr().String()
	token := rand.Text()
	auth := []byte("Bearer " + token)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/profile", p.capture("cpu", "profile", 30, pprof.StartCPUProfile, pprof.StopCPUProfile))
	mux.HandleFunc("GET /debug/pprof/trace", p.capture("trace", "trace", 1, trace.Start, trace.Stop))
	mux.HandleFunc("GET /debug/pprof/{name}", snapshot)
	mux.HandleFunc("GET /debug/pprof/{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, profile := range pprof.Profiles() {
			fmt.Fprintf(w, "%s\n", profile.Name())
		}
	})
	mux.HandleFunc("GET /debug/wkperf/processes", func(w http.ResponseWriter, _ *http.Request) { jsonResponse(w, processEvidence()) })
	mux.HandleFunc("GET /debug/wkperf/clock", p.serveClock)
	mux.HandleFunc("GET /debug/wkperf/capture", p.captureStatus)
	mux.HandleFunc("GET /debug/wkperf/captures", func(w http.ResponseWriter, _ *http.Request) {
		cpu, execution := p.states()
		jsonResponse(w, map[string]captureState{"cpu": cpu, "trace": execution})
	})
	server := &http.Server{ReadHeaderTimeout: writeBudget, WriteTimeout: writeBudget, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != addr || req.Header.Get("Origin") != "" || req.Header.Get("Sec-Fetch-Site") != "" {
			http.Error(w, "local profiler clients only", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare(auth, []byte(req.Header.Get("Authorization"))) != 1 {
			http.Error(w, "invalid profiler credentials", http.StatusUnauthorized)
			return
		}
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, req)
	})}
	info, err := publishReady(path, readiness{URL: "http://" + addr, PID: p.pid, Token: token})
	if err != nil {
		return errors.Join(err, ln.Close())
	}
	p.server, p.readyPath, p.readyInfo = server, path, info
	go server.Serve(ln)
	return nil
}

// Link a complete private file into place without replacing an existing path.
func publishReady(path string, ready readiness) (os.FileInfo, error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".wkperf-ready-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	encodeErr := json.NewEncoder(f).Encode(ready)
	info, statErr := f.Stat()
	if err := errors.Join(encodeErr, statErr, f.Close()); err != nil {
		return nil, err
	}
	if err := os.Link(f.Name(), path); err != nil {
		return nil, err
	}
	return info, nil
}

func jsonResponse(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func snapshot(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	if q.Has("seconds") {
		http.Error(w, "seconds is only supported for CPU and execution trace", http.StatusBadRequest)
		return
	}
	profile := pprof.Lookup(req.PathValue("name"))
	if profile == nil {
		http.NotFound(w, req)
		return
	}
	if profile.Name() == "heap" && q.Get("gc") == "1" {
		runtime.GC()
	}
	debug, _ := strconv.Atoi(q.Get("debug"))
	if debug > 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+profile.Name()+`"`)
	}
	_ = profile.WriteTo(w, debug)
}

func (p *Probe) serveClock(w http.ResponseWriter, req *http.Request) {
	if !trace.IsEnabled() {
		jsonResponse(w, p.clock())
		return
	}
	id := fmt.Sprintf("%d %d", p.pid, p.nonce.Add(1))
	trace.Log(req.Context(), "wkperf.clock.begin", id)
	reading := p.clock()
	trace.Log(req.Context(), "wkperf.clock.end", id+" "+strconv.FormatInt(reading.TS, 10))
	jsonResponse(w, reading)
}

func (p *Probe) states() (captureState, captureState) {
	p.samplerMu.Lock()
	defer p.samplerMu.Unlock()
	return p.cpu, p.execution
}

func (p *Probe) captureStatus(w http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("id")
	cpu, execution := p.states()
	state := captureState{ID: id}
	if id != "" {
		switch id {
		case cpu.ID:
			state = cpu
		case execution.ID:
			state = execution
		}
	}
	jsonResponse(w, state)
}

func (p *Probe) capture(kind, filename string, fallback int, start func(io.Writer) error, stop func()) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := req.Header.Get("X-WKPerf-Capture-ID")
		if id != "" && !label.MatchString(id) {
			http.Error(w, "invalid capture ID", http.StatusBadRequest)
			return
		}
		seconds := float64(fallback)
		if raw := req.URL.Query().Get("seconds"); raw != "" {
			var err error
			seconds, err = strconv.ParseFloat(raw, 64)
			if err != nil || !(seconds >= 1e-9 && seconds <= 300) {
				http.Error(w, "capture seconds must be in (0, 300]", http.StatusBadRequest)
				return
			}
		}
		duration := time.Duration(seconds * float64(time.Second))
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(duration + writeBudget)); err != nil {
			http.Error(w, "cannot set capture write deadline", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		p.samplerMu.Lock()
		slot := &p.cpu
		if kind == "trace" {
			slot = &p.execution
		}
		var err error
		switch {
		case p.closing:
			err = os.ErrClosed
		case id != "" && (p.cpu.ID == id || p.execution.ID == id):
			err = errors.New("duplicate capture ID")
		default:
			err = start(w)
		}
		if err != nil {
			p.samplerMu.Unlock()
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		started := p.clock()
		*slot = captureState{ID: id, Kind: kind, Active: true, Start: &started}
		p.samplerWG.Add(1)
		p.samplerMu.Unlock()
		defer p.samplerWG.Done()
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-req.Context().Done():
		}
		p.samplerMu.Lock()
		stopped := p.clock()
		slot.Active, slot.Stop = false, &stopped
		p.samplerMu.Unlock()
		stop()
	}
}
