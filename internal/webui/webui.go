// Package webui serves a local, upload-only recording investigation UI.
package webui

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

//go:embed page.html
var pageHTML string

//go:embed flame.js
var flameJS string

var page = template.Must(template.New("ui").Funcs(template.FuncMap{
	"boundedText":       boundedText,
	"categoryLabel":     categoryLabel,
	"frameName":         frameName,
	"generatedName":     generatedName,
	"generatedLocation": generatedLocation,
	"originalLocation":  originalLocation,
	"addOne":            func(n int) int { return n + 1 },
	"subtractOne":       func(n int) int { return n - 1 },
	"percent":           func(n float64) float64 { return 100 * n },
	"leaf": func(frames []metrics.FunctionFrame) string {
		if len(frames) == 0 {
			return ""
		}
		return frames[len(frames)-1].Name
	},
	"number": func(n float64) string {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "—"
		}
		return fmt.Sprintf("%.4g", n)
	},
	"name": func(s string) string {
		if s == "" {
			return "(anonymous)"
		}
		return boundedText(s, 160)
	},
}).Parse(pageHTML))

type recording struct {
	Role, Filename string
	Graph          flameGraph
	*insights.Report
}

func (r recording) HasTaskEvidence() bool {
	_, ok := r.Summary.Lookup("script.tasks.count")
	return ok
}

type view struct {
	Token          string
	LimitMiB       int
	Error          string
	Recordings     []recording
	Comparison     *compare.Result
	CompareSamples bool
}

var errByteLimit = errors.New("upload byte limit exceeded")

// New creates the handler for the exact numeric loopback host:port used by the server.
func New(host string, maxFileMiB int) http.Handler {
	maxFile := int64(maxFileMiB) << 20
	token := rand.Text()
	busy := make(chan struct{}, 1)
	origin := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if r.Host != host {
			http.Error(w, "Use the loopback URL printed by wkperf ui.", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/flame.js" {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				h.Set("Allow", "GET, HEAD")
				http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
				return
			}
			h.Set("Content-Type", "text/javascript; charset=utf-8")
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, flameJS)
			}
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data := view{Token: token, LimitMiB: maxFileMiB}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			render(w, http.StatusOK, data)
			return
		}
		if r.Method != http.MethodPost {
			h.Set("Allow", "GET, HEAD, POST")
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		if err := origin.Check(r); err != nil {
			http.Error(w, "Cross-origin uploads are not allowed.", http.StatusForbidden)
			return
		}
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			data.Error = "Another recording is being analyzed. Choose your files and try again when it finishes."
			render(w, http.StatusServiceUnavailable, data)
			return
		}
		files, status, err := upload(w, r, token, maxFile)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) || errors.Is(err, errByteLimit) {
				status = http.StatusRequestEntityTooLarge
				data.Error = fmt.Sprintf("Upload too large. Each recording must be at most %d MiB.", maxFileMiB)
			} else {
				data.Error = err.Error()
			}
			render(w, status, data)
			return
		}
		for _, f := range files {
			if r.Context().Err() != nil {
				return
			}
			report := insights.Analyze(f.rec, f.name, insights.Options{})
			data.Recordings = append(data.Recordings, recording{Role: f.role, Filename: f.name, Graph: layoutFlame(report.FlameTree), Report: report})
		}
		if len(data.Recordings) == 2 {
			base, cand := data.Recordings[0].Summary, data.Recordings[1].Summary
			data.Comparison = compare.Compare([]*metrics.Summary{base}, []*metrics.Summary{cand}, compare.DefaultOptions())
			data.CompareSamples = data.Recordings[0].SampleCoverage.SampledMs > 0 &&
				data.Recordings[1].SampleCoverage.SampledMs > 0
		}
		render(w, http.StatusOK, data)
	})
}

func render(w http.ResponseWriter, status int, data view) {
	var output bytes.Buffer
	if err := page.Execute(&output, data); err != nil {
		http.Error(w, "Could not render the recording report.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}

type file struct {
	role, name string
	rec        *model.Recording
}

func upload(w http.ResponseWriter, r *http.Request, token string, maxFile int64) ([]file, int, error) {
	maxRequest := 2*maxFile + (64 << 10)
	if r.ContentLength > maxRequest {
		return nil, http.StatusRequestEntityTooLarge, errByteLimit
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequest)
	parts, err := r.MultipartReader()
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("Choose files using the recording upload form.")
	}
	part, err := parts.NextRawPart()
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("Could not read the upload: %w", err)
	}
	if part.FormName() != "token" || part.FileName() != "" {
		return nil, http.StatusForbidden, errors.New("Reload the UI before uploading recordings.")
	}
	value, err := io.ReadAll(io.LimitReader(part, int64(len(token)+1)))
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("Could not read the upload: %w", err)
	}
	if subtle.ConstantTimeCompare(value, []byte(token)) != 1 {
		return nil, http.StatusForbidden, errors.New("Reload the UI before uploading recordings.")
	}

	var files [2]file
	var seen [2]bool
	for {
		part, err := parts.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("Could not read the upload: %w", err)
		}
		var i int
		var role string
		switch part.FormName() {
		case "baseline":
			i, role = 0, "Baseline"
		case "candidate":
			i, role = 1, "Candidate"
		default:
			return nil, http.StatusBadRequest, errors.New("Upload only a baseline and an optional candidate recording.")
		}
		if seen[i] {
			return nil, http.StatusBadRequest, fmt.Errorf("Choose only one %s recording.", strings.ToLower(role))
		}
		seen[i] = true
		_, params, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		filename, ok := params["filename"]
		if !ok {
			return nil, http.StatusBadRequest, fmt.Errorf("%s must be a file upload, not a file path.", role)
		}
		if filename == "" && i == 1 {
			var probe [1]byte
			if _, err := io.ReadFull(part, probe[:]); err == io.EOF {
				continue
			}
		}
		if filename == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("Choose a %s recording file.", strings.ToLower(role))
		}
		name := fmt.Sprintf("%.255s", path.Base(strings.ReplaceAll(filename, `\`, "/")))
		// Parse directly from the capped stream; do not write uploads to disk.
		limit := &io.LimitedReader{R: part, N: maxFile + 1}
		budget := &jsonBudget{r: limit}
		rec, err := model.Parse(budget)
		if budget.err != nil {
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("%s %q: %w", role, name, budget.err)
		}
		if limit.N == 0 {
			return nil, http.StatusRequestEntityTooLarge, errByteLimit
		}
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, http.StatusBadRequest, fmt.Errorf("%s %q: incomplete recording or upload.", role, name)
			}
			return nil, http.StatusBadRequest, fmt.Errorf("%s %q: %.400s", role, name, err)
		}
		files[i] = file{role: role, name: name, rec: rec}
	}
	if files[0].rec == nil {
		return nil, http.StatusBadRequest, errors.New("Choose a baseline recording first.")
	}
	if files[1].rec != nil {
		return files[:], http.StatusOK, nil
	}
	return files[:1], http.StatusOK, nil
}
