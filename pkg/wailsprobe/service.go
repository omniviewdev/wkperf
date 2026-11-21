package wailsprobe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

const jsClock = "js.performance_now"

// Service is the opt-in binding for trusted, application-owned web content.
// Do not expose it to arbitrary navigation or apart from app development builds.
type Service struct{ probe *Probe }

// Service returns a binding suitable for registration with stock Wails.
func (p *Probe) Service() *Service { return &Service{probe: p} }

// Clock reads the host clock. A nonempty transaction ID measures the trusted
// binding handler for exact correlation with a browser calibration bracket.
func (s *Service) Clock(tx string) (*ClockReading, error) {
	if s == nil || s.probe == nil {
		return nil, nil
	}
	p := s.probe
	if tx == "" {
		reading := p.clock()
		return &reading, nil
	}
	if !label.MatchString(tx) {
		return nil, errors.New("invalid calibration transaction")
	}
	end := p.Begin("bridge.go.handler", tx)
	reading := p.clock()
	if err := end(); err != nil {
		return nil, err
	}
	return &reading, nil
}

// ClientEvent is one trusted observation in the document's performance clock.
// The host assigns clock, unit, and PID, rather than trusting caller values.
type ClientEvent struct {
	Stage      string          `json:"stage"`
	Phase      string          `json:"phase"`
	TS         float64         `json:"ts"`
	Tx         string          `json:"tx,omitempty"`
	Document   string          `json:"document"`
	TimeOrigin float64         `json:"timeOrigin"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

type anchorDetail struct {
	JSSend     *float64 `json:"jsSend"`
	JSReceive  *float64 `json:"jsReceive"`
	GoTS       *int64   `json:"goTs"`
	GoClock    string   `json:"goClock"`
	GoUnit     string   `json:"goUnit"`
	TimeOrigin *float64 `json:"timeOrigin"`
}

// RecordEvents validates all rows before writing a bounded batch. It accepts
// browser call spans, Go and Inspector clock brackets, and application marks.
func (s *Service) RecordEvents(rows []ClientEvent) error {
	if s == nil || s.probe == nil || len(rows) == 0 {
		return nil
	}
	if len(rows) > 128 {
		return errors.New("browser event batch exceeds 128 records")
	}
	p := s.probe
	now := p.elapsed()
	records := make([]event, len(rows))
	for i := range rows {
		r := &rows[i]
		if !r.valid(now) {
			return fmt.Errorf("invalid browser event at index %d", i)
		}
		var detail any
		if len(r.Detail) != 0 {
			detail = r.Detail
		}
		records[i] = event{Clock: jsClock, Unit: "ms", TS: r.TS, PID: p.pid, Stage: r.Stage, Phase: r.Phase,
			Tx: r.Tx, Document: r.Document, TimeOrigin: &r.TimeOrigin, Detail: detail}
	}
	return p.emit(records...)
}

func validBrowserTime(n float64) bool { return n >= 0 && n <= 1<<53 && !math.IsNaN(n) }

func (r *ClientEvent) valid(now int64) bool {
	if !validBrowserTime(r.TS) || !validBrowserTime(r.TimeOrigin) || r.TimeOrigin == 0 ||
		!label.MatchString(r.Document) || (r.Tx != "" && !label.MatchString(r.Tx)) || len(r.Detail) > 1024 {
		return false
	}
	switch r.Stage {
	case "bridge.js.call":
		if r.Tx == "" || (r.Phase != "begin" && r.Phase != "end") {
			return false
		}
		if len(r.Detail) == 0 {
			return true
		}
		var d map[string]int64
		if json.Unmarshal(r.Detail, &d) != nil || d == nil {
			return false
		}
		for key, value := range d {
			switch key {
			case "requestBytes", "responseBytes":
				if value < 0 || value > 1<<53 {
					return false
				}
			case "calibration", "failed":
				if value != 0 && value != 1 {
					return false
				}
			default:
				return false
			}
		}
		return true
	case "clock.anchor":
		var d anchorDetail
		return r.Phase == "mark" && r.Tx != "" && decodeDetail(r.Detail, &d) &&
			d.JSSend != nil && d.JSReceive != nil && d.TimeOrigin != nil && d.GoTS != nil &&
			validBrowserTime(*d.JSSend) && *d.JSSend <= r.TS && *d.JSReceive == r.TS && *d.TimeOrigin == r.TimeOrigin &&
			*d.GoTS >= 0 && *d.GoTS <= now && d.GoClock == hostClock && d.GoUnit == "ns"
	case "webkit.clock.anchor":
		var d struct {
			JSBefore *float64 `json:"jsBefore"`
			JSAfter  *float64 `json:"jsAfter"`
		}
		return r.Phase == "mark" && r.Tx != "" && decodeDetail(r.Detail, &d) &&
			d.JSBefore != nil && d.JSAfter != nil && validBrowserTime(*d.JSBefore) &&
			*d.JSBefore <= r.TS && *d.JSAfter == r.TS
	case "web.mark":
		if r.Phase != "mark" {
			return false
		}
		var d struct {
			Name string `json:"name"`
		}
		return len(r.Detail) == 0 || (decodeDetail(r.Detail, &d) && label.MatchString(d.Name))
	default:
		return false
	}
}

func decodeDetail(data []byte, dst any) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(dst) == nil && d.Decode(new(any)) == io.EOF
}
