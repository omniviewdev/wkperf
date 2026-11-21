package inspector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/omniviewdev/wkperf/internal/model"
)

type resourceTracker struct {
	kind     string
	domain   string
	enabled  bool
	complete bool
	samples  int
}

func (s *session) startResources(ctx context.Context) error {
	s.capture.resources = [2]resourceTracker{
		{kind: model.TypeCPU, domain: "CPUProfiler"},
		{kind: model.TypeMemory, domain: "Memory"},
	}
	for i := range s.capture.resources {
		r := &s.capture.resources[i]
		if r.kind == model.TypeMemory {
			if err := s.enableDomain(ctx, "Memory"); err != nil {
				if methodUnavailable(err) {
					continue
				}
				return fmt.Errorf("enable WebKit Memory: %w", err)
			}
		}
		if err := s.command(ctx, r.domain+".startTracking", nil); err != nil {
			if methodUnavailable(err) {
				continue
			}
			return fmt.Errorf("start WebKit %s: %w", r.domain, err)
		}
		r.enabled = true
	}
	return nil
}

func (s *session) stopResources(ctx context.Context) error {
	for i := range s.capture.resources {
		r := &s.capture.resources[i]
		if !r.enabled {
			continue
		}
		if err := s.command(ctx, r.domain+".stopTracking", nil); err != nil {
			return fmt.Errorf("stop WebKit %s: %w", r.domain, err)
		}
		for !r.complete {
			if _, err := s.next(ctx); err != nil {
				return fmt.Errorf("flush WebKit %s: %w", r.domain, err)
			}
		}
	}
	return nil
}

func methodUnavailable(err error) bool {
	var protocol *protocolError
	return errors.As(err, &protocol) && protocol.Code == -32601
}

func (e *evidence) resourceEvent(m message) error {
	switch m.Method {
	case "CPUProfiler.trackingComplete":
		e.resources[0].complete = true
	case "Memory.trackingComplete":
		e.resources[1].complete = true
	case "CPUProfiler.trackingUpdate", "Memory.trackingUpdate":
		var p struct {
			Event struct {
				Timestamp  *float64               `json:"timestamp"`
				Usage      *float64               `json:"usage"`
				Threads    []model.Thread         `json:"threads"`
				Categories []model.MemoryCategory `json:"categories"`
			} `json:"event"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return fmt.Errorf("decode %s: %w", m.Method, err)
		}
		if p.Event.Timestamp == nil {
			return fmt.Errorf("decode %s: missing event timestamp", m.Method)
		}
		if m.Method == "CPUProfiler.trackingUpdate" {
			if p.Event.Usage == nil {
				return fmt.Errorf("decode %s: missing CPU usage", m.Method)
			}
			e.resourceRecords = append(e.resourceRecords, model.Record{Type: model.TypeCPU, Timestamp: *p.Event.Timestamp, Usage: *p.Event.Usage, Threads: p.Event.Threads})
		} else if p.Event.Categories != nil {
			e.resourceRecords = append(e.resourceRecords, model.Record{Type: model.TypeMemory, Timestamp: *p.Event.Timestamp, Categories: p.Event.Categories})
		} else {
			return fmt.Errorf("decode %s: missing memory categories", m.Method)
		}
	case "Memory.memoryPressure":
		var pressure model.MemoryPressure
		if err := json.Unmarshal(m.Params, &pressure); err != nil {
			return fmt.Errorf("decode %s: %w", m.Method, err)
		}
		e.pressure = append(e.pressure, pressure)
	}
	return nil
}

func (r resourceTracker) status() string {
	switch {
	case !r.enabled:
		return "unsupported"
	case r.samples == 0:
		return "enabled-no-samples"
	default:
		return "captured"
	}
}
