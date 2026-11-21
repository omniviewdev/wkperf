package inspector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket"
)

type message struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *protocolError  `json:"error"`
}

type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *protocolError) Error() string {
	return fmt.Sprintf("protocol error %d: %s", e.Code, e.Message)
}

type request struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type targetEnvelope struct {
	TargetID string `json:"targetId"`
	Message  string `json:"message"`
}

type incoming struct {
	message
	err error
}

type session struct {
	ws        *websocket.Conn
	messages  <-chan incoming
	id        int
	target    string
	candidate string
	capture   evidence
	stops     stopInstrument
}

type stopInstrument uint8

const (
	stopTimeline stopInstrument = 1 << iota
	stopScript
	stopCPU
	stopMemory
)

func readMessages(ctx context.Context, ws *websocket.Conn) <-chan incoming {
	ch := make(chan incoming, 16)
	go func() {
		defer close(ch)
		for {
			kind, data, err := ws.Read(ctx)
			var m message
			if err == nil {
				if kind != websocket.MessageText {
					err = fmt.Errorf("expected a WebKit protocol text message")
				} else {
					err = json.Unmarshal(data, &m)
				}
			}
			select {
			case ch <- incoming{m, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// accept unwraps the page target and drops events from all other targets.
func (s *session) accept(in incoming) (message, error) {
	m := in.message
	if in.err != nil {
		return m, fmt.Errorf("read WebKit protocol: %w", in.err)
	}
	switch m.Method {
	case "Target.targetCreated":
		var p struct {
			Info struct {
				ID          string `json:"targetId"`
				Type        string `json:"type"`
				Provisional bool   `json:"isProvisional"`
			} `json:"targetInfo"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return message{}, err
		}
		if p.Info.Type == "page" && !p.Info.Provisional {
			s.candidate = p.Info.ID
		}
	case "Target.targetDestroyed", "Target.didCommitProvisionalTarget":
		var p struct {
			TargetID string `json:"targetId"`
			OldID    string `json:"oldTargetId"`
			NewID    string `json:"newTargetId"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return message{}, err
		}
		if s.target != "" && (s.target == p.TargetID || s.target == p.OldID) {
			return message{}, fmt.Errorf("WebKit page target %q changed during recording", s.target)
		}
		if s.candidate != "" && (s.candidate == p.TargetID || s.candidate == p.OldID) {
			s.candidate = p.NewID
		}
	case "Target.dispatchMessageFromTarget":
		var p targetEnvelope
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return message{}, err
		}
		if s.target == "" || p.TargetID != s.target {
			return message{}, nil
		}
		m = message{}
		if err := json.Unmarshal([]byte(p.Message), &m); err != nil {
			return message{}, err
		}
	}
	var stopped stopInstrument
	switch m.Method {
	case "Timeline.recordingStopped":
		stopped = stopTimeline
	case "ScriptProfiler.trackingComplete":
		stopped = stopScript
	case "CPUProfiler.trackingComplete":
		stopped = stopCPU
	case "Memory.trackingComplete":
		stopped = stopMemory
	}
	if stopped != 0 && s.stops&stopped == 0 {
		return message{}, fmt.Errorf("WebKit stopped recording before wkperf requested it (%s)", m.Method)
	}
	return m, s.capture.event(m)
}

func (s *session) next(ctx context.Context) (message, error) {
	select {
	case <-ctx.Done():
		return message{}, ctx.Err()
	case in, ok := <-s.messages:
		if !ok {
			return message{}, fmt.Errorf("WebKit inspector disconnected")
		}
		return s.accept(in)
	}
}

func (s *session) command(ctx context.Context, method string, params any) error {
	_, err := s.exchange(ctx, method, params)
	return err
}

func (s *session) exchange(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.id++
	inner := request{ID: s.id, Method: method, Params: params}
	outer := inner
	if s.target != "" {
		data, err := json.Marshal(inner)
		if err != nil {
			return nil, err
		}
		s.id++
		outer = request{ID: s.id, Method: "Target.sendMessageToTarget", Params: targetEnvelope{s.target, string(data)}}
	}
	data, err := json.Marshal(outer)
	if err != nil {
		return nil, err
	}
	var stopping stopInstrument
	switch method {
	case "Timeline.stop":
		stopping = stopTimeline
	case "ScriptProfiler.stopTracking":
		stopping = stopScript
	case "CPUProfiler.stopTracking":
		stopping = stopCPU
	case "Memory.stopTracking":
		stopping = stopMemory
	}
	if stopping != 0 {
		// Validate notifications already queued before sending this stop.
		for range len(s.messages) {
			if _, err := s.next(ctx); err != nil {
				return nil, err
			}
		}
	}
	if err := s.ws.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, err
	}
	s.stops |= stopping
	awaitInner, awaitOuter := true, outer.ID != inner.ID
	var result json.RawMessage
	for awaitInner || awaitOuter {
		m, err := s.next(ctx)
		if err != nil {
			return nil, err
		}
		switch m.ID {
		case inner.ID:
			awaitInner = false
			result = m.Result
		case outer.ID:
			awaitOuter = false
		default:
			continue
		}
		if m.Error != nil {
			return nil, m.Error
		}
	}
	return result, nil
}

func (s *session) enableTimeline(ctx context.Context) error {
	err := s.enableDomain(ctx, "Timeline")
	if protocol, ok := err.(*protocolError); !ok || protocol.Code != -32601 {
		return err
	}
	// A WebKit page proxy exposes Target instead of the page's Timeline.
	for s.candidate == "" {
		if _, waitErr := s.next(ctx); waitErr != nil {
			return fmt.Errorf("no WebKit page target announced after %w: %w", err, waitErr)
		}
	}
	s.target = s.candidate
	return s.enableDomain(ctx, "Timeline")
}

func (s *session) enableDomain(ctx context.Context, domain string) error {
	err := s.command(ctx, domain+".enable", nil)
	if protocol, ok := err.(*protocolError); ok && protocol.Code == -32000 && protocol.Message == domain+" domain already enabled" {
		return nil
	}
	return err
}
