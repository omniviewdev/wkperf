// Package wailsprobe provides opt-in local profiling for application hosts.
package wailsprobe

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const hostClock = "go.process_monotonic"

const maxEventBytes = 1 << 20

var errEventTooLarge = errors.New("profiling event exceeds 1 MiB including newline")

var label = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

// ClockReading identifies a point on the host process clock.
type ClockReading struct {
	Clock string `json:"clock"`
	Unit  string `json:"unit"`
	TS    int64  `json:"ts"`
	PID   int    `json:"pid"`
}

type event struct {
	Clock      string   `json:"clock"`
	Unit       string   `json:"unit"`
	TS         any      `json:"ts"`
	PID        int      `json:"pid"`
	Stage      string   `json:"stage"`
	Phase      string   `json:"phase"`
	Tx         string   `json:"tx,omitempty"`
	Document   string   `json:"document,omitempty"`
	TimeOrigin *float64 `json:"timeOrigin,omitempty"`
	Detail     any      `json:"detail,omitempty"`
}

// Probe owns one opt-in profiling session. A nil *Probe is disabled.
type Probe struct {
	zero           time.Time
	pid            int
	mu             sync.Mutex
	file           *os.File
	writeErr       error
	closeOnce      sync.Once
	closeErr       error
	server         *http.Server
	readyPath      string
	readyInfo      os.FileInfo
	samplerMu      sync.Mutex
	samplerWG      sync.WaitGroup
	closing        bool
	cpu, execution captureState
	nonce          atomic.Uint64
}

// StartFromEnv opens an exclusive event file and optionally publishes an
// authenticated loopback address to an exclusive readiness file. With neither
// OMNIVIEW_WKPERF_TRACE nor OMNIVIEW_WKPERF_READY set it returns nil, nil.
func StartFromEnv() (*Probe, error) {
	return start(os.Getenv("OMNIVIEW_WKPERF_TRACE"), os.Getenv("OMNIVIEW_WKPERF_READY"))
}

func start(path, ready string) (*Probe, error) {
	if path == "" && ready == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) || (ready != "" && (!filepath.IsAbs(ready) || filepath.Clean(path) == filepath.Clean(ready))) {
		return nil, errors.New("OMNIVIEW_WKPERF_TRACE must be absolute; OMNIVIEW_WKPERF_READY must be absolute and distinct if set")
	}
	zero := time.Now()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	p := &Probe{zero: zero, pid: os.Getpid(), file: f}
	if err := p.emit(p.hostEvent(0, "host.main.entry", "mark", "", nil), p.hostEvent(p.elapsed(), "process.host", "mark", "", processEvidence())); err != nil {
		return nil, errors.Join(err, p.Close())
	}
	if ready != "" {
		if err := p.serve(ready); err != nil {
			return nil, errors.Join(err, p.Close())
		}
	}
	return p, nil
}

func processEvidence() map[string]any {
	return map[string]any{"parentPID": os.Getppid(), "error": "portable host probe has no kernel start identity", "attribution": "host-self; webcontent/gpu unverified"}
}

func (p *Probe) elapsed() int64 { return time.Since(p.zero).Nanoseconds() }

func (p *Probe) clock() ClockReading {
	return ClockReading{Clock: hostClock, Unit: "ns", TS: p.elapsed(), PID: p.pid}
}

func (p *Probe) hostEvent(ts int64, stage, phase, tx string, detail any) event {
	return event{Clock: hostClock, Unit: "ns", TS: ts, PID: p.pid, Stage: stage, Phase: phase, Tx: tx, Detail: detail}
}

type eventBatch struct {
	bytes.Buffer
}

// json.Encoder writes one complete encoded event, including newline, per call.
func (b *eventBatch) Write(data []byte) (int, error) {
	if len(data) > maxEventBytes {
		return 0, errEventTooLarge
	}
	return b.Buffer.Write(data)
}

func (p *Probe) emit(rows ...event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return os.ErrClosed
	}
	if p.writeErr != nil {
		return p.writeErr
	}
	var batch eventBatch
	encoder := json.NewEncoder(&batch)
	for i := range rows {
		if err := encoder.Encode(&rows[i]); err != nil {
			return err
		}
	}
	_, p.writeErr = p.file.Write(batch.Bytes())
	return p.writeErr
}

// Mark records a host observation. Callers must not mutate fields during Mark.
func (p *Probe) Mark(name string, fields map[string]any) error {
	if p == nil {
		return nil
	}
	if !label.MatchString(name) {
		return errors.New("invalid mark name")
	}
	var detail any
	if len(fields) != 0 {
		detail = fields
	}
	return p.emit(p.hostEvent(p.elapsed(), name, "mark", "", detail))
}

// Begin measures work on the host clock. Call the returned function once when
// work completes. Pass an explicit transaction ID to correlate with browser rows.
// An optional field map becomes begin detail; do not mutate it during the call
// to the returned function.
func (p *Probe) Begin(stage, tx string, fields ...map[string]any) func() error {
	if p == nil {
		return func() error { return nil }
	}
	if !label.MatchString(stage) || (tx != "" && !label.MatchString(tx)) || len(fields) > 1 {
		return func() error { return errors.New("invalid span stage, transaction, or fields") }
	}
	begin := p.elapsed()
	var detail any
	if len(fields) == 1 && len(fields[0]) != 0 {
		detail = fields[0]
	}
	return sync.OnceValue(func() error {
		end := p.elapsed()
		return p.emit(p.hostEvent(begin, stage, "begin", tx, detail), p.hostEvent(end, stage, "end", tx, nil))
	})
}

// Close stops the HTTP endpoint and closes the event stream. It is idempotent.
func (p *Probe) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.server != nil {
			p.samplerMu.Lock()
			p.closing = true
			p.samplerMu.Unlock()
			p.closeErr = p.server.Close()
			p.samplerWG.Wait()
		}
		if p.readyInfo != nil {
			info, err := os.Lstat(p.readyPath)
			if err == nil && os.SameFile(p.readyInfo, info) {
				err = os.Remove(p.readyPath)
			}
			if !errors.Is(err, os.ErrNotExist) {
				p.closeErr = errors.Join(p.closeErr, err)
			}
		}
		p.mu.Lock()
		if p.file != nil {
			p.closeErr = errors.Join(p.closeErr, p.writeErr, p.file.Close())
			p.file = nil
		}
		p.mu.Unlock()
	})
	return p.closeErr
}
