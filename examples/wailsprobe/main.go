package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"runtime/trace"
	"time"

	"github.com/omniviewdev/wkperf/pkg/wailsprobe"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed assets/*
var assets embed.FS

const smokeDigest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

type TextService struct {
	probe   *wailsprobe.Probe
	autorun bool
}

// Fingerprint computes the SHA-256 digest of the user's UTF-8 text.
func (s *TextService) Fingerprint(ctx context.Context, tx, text string) string {
	if tx != "" {
		end := s.probe.Begin("bridge.go.handler", tx, map[string]any{
			"requestBytes":  len(text),
			"responseBytes": sha256.Size * 2,
		})
		defer func() {
			if err := end(); err != nil {
				log.Printf("record fingerprint: %v", err)
			}
		}()
		if trace.IsEnabled() {
			ctx, task := trace.NewTask(ctx, "fingerprint")
			defer task.End()
			trace.Log(ctx, "wkperf.tx", tx)
		}
	}

	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

// WaitForTrace gates the unattended smoke action on real Go trace activity.
// It is a fixture control, not part of a measured application call.
func (s *TextService) WaitForTrace(ctx context.Context) error {
	if !s.autorun {
		return errors.New("auto-run is disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	log.Print("wailsprobe smoke ready")
	for !trace.IsEnabled() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	trace.Log(ctx, "wkperf.smoke", "frontend released")
	return nil
}

// ReportSmokeDigest validates the result received by the WebView after flush.
func (s *TextService) ReportSmokeDigest(ctx context.Context, digest string, inspectorBounds int) error {
	if !s.autorun || digest != smokeDigest || (inspectorBounds != 0 && inspectorBounds != 2) {
		return errors.New("unexpected smoke result")
	}
	trace.Log(ctx, "wkperf.smoke", "frontend completed")
	log.Printf("wailsprobe smoke digest: %s", digest)
	log.Printf("wailsprobe smoke Inspector app-side bounds: %d", inspectorBounds)
	return nil
}

func run(autorun bool) (err error) {
	probe, err := wailsprobe.StartFromEnv()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, probe.Close()) }()

	assetServer := application.BundledAssetFileServer(assets)
	app := application.New(application.Options{
		Name: "Wails Probe Smoke",
		Services: []application.Service{
			application.NewService(probe.Service()),
			application.NewService(&TextService{probe: probe, autorun: autorun}),
		},
		Assets: application.AssetOptions{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/wailsprobe.js" {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				_, _ = io.WriteString(w, wailsprobe.ClientJS())
				return
			}
			assetServer.ServeHTTP(w, r)
		})},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		OnShutdown: func() {
			if err := probe.Close(); err != nil {
				log.Printf("close profiling session: %v", err)
			}
		},
	})
	url := "/"
	if autorun {
		url += "?autorun=1"
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:           "Wails Probe Smoke",
		URL:             url,
		Width:           550,
		Height:          300,
		DevToolsEnabled: true,
	})
	return app.Run()
}

func main() {
	autorun := flag.Bool("autorun", false, "run one fingerprint action once a Go execution trace is active")
	flag.Parse()
	if err := run(*autorun); err != nil {
		log.Fatal(err)
	}
}
