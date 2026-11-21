// Package inspector records a WebKit Inspector Protocol page target.
package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/workload"
)

const (
	defaultDuration = 10 * time.Second
	protocolTimeout = 15 * time.Second
)

// Options selects an explicit page endpoint and a duration, command, or script.
// Output receives both workload stdout and stderr. A nil Output discards both.
type Options struct {
	Endpoint string
	Duration time.Duration
	Workload []string
	Script   string
	Output   io.Writer
	// Workload hooks run after profiler start and before profiler flush.
	BeforeWorkload func(context.Context) error
	AfterWorkload  func()
}

// Capture writes one wkperf-compatible Timelines recording on success.
// Canceling ctx stops the command workload and detaches without exporting a partial recording.
func Capture(ctx context.Context, options Options, output io.Writer) error {
	u, err := url.Parse(options.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "ws" && u.Scheme != "wss") {
		return fmt.Errorf("endpoint must be an explicit ws:// or wss:// WebKit Inspector target URL without userinfo or fragment")
	}
	if options.Duration < 0 ||
		(options.Duration != 0 && (len(options.Workload) != 0 || options.Script != "")) ||
		(len(options.Workload) != 0 && options.Script != "") {
		return fmt.Errorf("specify at most one of duration, workload command, or workload script")
	}
	if options.Duration == 0 && len(options.Workload) == 0 && options.Script == "" {
		options.Duration = defaultDuration
	}
	var script []byte
	if options.Script != "" {
		script, err = os.ReadFile(options.Script)
		if err != nil {
			return fmt.Errorf("read workload script: %w", err)
		}
	}

	ctx, closeReader := context.WithCancel(ctx)
	defer closeReader()
	setup, cancelSetup := context.WithTimeout(ctx, protocolTimeout)
	defer cancelSetup()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ws, _, err := websocket.Dial(setup, options.Endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return fmt.Errorf("connect to WebKit inspector: %w", err)
	}
	defer ws.CloseNow()
	// ScriptProfiler sends the entire sample buffer in one trackingComplete message.
	ws.SetReadLimit(128 << 20)
	s := session{ws: ws, messages: readMessages(ctx, ws)}
	if err := s.enableTimeline(setup); err != nil {
		return fmt.Errorf("enable WebKit Timeline: %w", err)
	}
	// Page.enable starts the execution stopwatch on legacy WebKit targets.
	if err := s.enableDomain(setup, "Page"); err != nil && !methodUnavailable(err) {
		return fmt.Errorf("enable WebKit Page clock: %w", err)
	}
	if err := s.startResources(setup); err != nil {
		return err
	}
	if err := s.command(setup, "ScriptProfiler.startTracking", struct {
		IncludeSamples bool `json:"includeSamples"`
	}{true}); err != nil {
		return fmt.Errorf("start WebKit ScriptProfiler: %w", err)
	}
	if err := s.command(setup, "Timeline.start", nil); err != nil {
		return fmt.Errorf("start WebKit Timeline: %w", err)
	}
	for s.capture.timelineStart == nil || s.capture.scriptStart == nil {
		if _, err := s.next(setup); err != nil {
			return fmt.Errorf("wait for WebKit recording start: %w", err)
		}
	}
	cancelSetup()
	if options.BeforeWorkload != nil {
		if err := options.BeforeWorkload(ctx); err != nil {
			return err
		}
	}

	if options.Script != "" {
		err = s.runScript(ctx, script)
	} else {
		err = s.runWorkload(ctx, options)
	}
	if options.AfterWorkload != nil {
		options.AfterWorkload()
	}
	if err != nil {
		return err
	}
	flush, cancelFlush := context.WithTimeout(ctx, protocolTimeout)
	defer cancelFlush()
	if err := s.command(flush, "Timeline.stop", nil); err != nil {
		return fmt.Errorf("stop WebKit Timeline: %w", err)
	}
	if err := s.command(flush, "ScriptProfiler.stopTracking", nil); err != nil {
		return fmt.Errorf("stop WebKit ScriptProfiler: %w", err)
	}
	for s.capture.timelineEnd == nil || s.capture.scriptEnd == nil {
		if _, err := s.next(flush); err != nil {
			return fmt.Errorf("flush WebKit recording: %w", err)
		}
	}
	if err := s.stopResources(flush); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	recording, err := s.capture.recording(s.target)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(struct {
		*model.Recording
		Capabilities map[string]string `json:"captureCapabilities"`
	}{recording, s.capture.resourceCapabilities()}); err != nil {
		return fmt.Errorf("write WebKit recording: %w", err)
	}
	return nil
}

func (s *session) runWorkload(ctx context.Context, options Options) error {
	var done <-chan error
	var elapsed <-chan time.Time
	if len(options.Workload) != 0 {
		workCtx, cancel := context.WithCancel(ctx)
		cmd := exec.CommandContext(workCtx, options.Workload[0], options.Workload[1:]...)
		cmd.Stdout, cmd.Stderr = options.Output, options.Output
		cmd.WaitDelay = time.Second
		cleanup := workload.Own(cmd)
		if err := cmd.Start(); err != nil {
			cancel()
			return fmt.Errorf("start workload: %w", err)
		}
		stopCleanup := context.AfterFunc(workCtx, cleanup)
		finished := make(chan error, 1)
		go func() {
			finished <- cmd.Wait()
			close(finished)
		}()
		defer func() {
			stopCleanup()
			cancel()
			cleanup()
			<-finished
		}()
		done = finished
	} else {
		timer := time.NewTimer(options.Duration)
		defer timer.Stop()
		elapsed = timer.C
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-elapsed:
			return nil
		case err := <-done:
			if err != nil {
				return fmt.Errorf("run workload: %w", err)
			}
			return nil
		case in, ok := <-s.messages:
			if !ok {
				return fmt.Errorf("WebKit inspector disconnected")
			}
			if _, err := s.accept(in); err != nil {
				return err
			}
		}
	}
}
