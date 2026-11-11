package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestAuditMinimumRejectsUnavailableGrade(t *testing.T) {
	p := testgen.Baseline()
	p.DurationSec, p.FrameMeanMs, p.FrameJitterMs = .1, 1000.0/60, 0
	p.LongTaskRate, p.ForcedLayoutRate = 0, 0
	f := writeRec(t, t.TempDir(), "sparse.json", 1, p)
	out, err := run(t, "audit", f, "--format", "json", "--min-score", "1")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != exitMinScore || !strings.Contains(exit.Msg, "unavailable") {
		t.Fatalf("unsupported grade must fail minimum via exit 4: %v", err)
	}
	var doc struct {
		Report struct {
			Overall struct {
				Grade   string `json:"grade"`
				Partial bool   `json:"partial"`
			} `json:"overall"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Report.Overall.Grade != "n/a" || !doc.Report.Overall.Partial {
		t.Fatalf("output must expose unsupported partial score: %s", out)
	}
}

func TestAuditMinimumAcceptsObservedHealthyCapture(t *testing.T) {
	p := testgen.Baseline()
	p.DurationSec, p.FrameMeanMs, p.FrameJitterMs = 2, 1000.0/60, 0
	p.LongTaskRate, p.ForcedLayoutRate = 0, 0
	f := writeRec(t, t.TempDir(), "healthy.json", 1, p)
	if _, err := run(t, "audit", f, "--format", "json", "--min-score", "70"); err != nil {
		t.Fatalf("supported healthy grade must pass minimum: %v", err)
	}
}

func TestAuditMinimumCannotBypassUnavailableGradeWithPrompt(t *testing.T) {
	p := testgen.Baseline()
	p.DurationSec = .1
	f := writeRec(t, t.TempDir(), "sparse.json", 1, p)
	_, err := run(t, "audit", f, "--emit-prompt=-", "--min-score", "1")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != exitMinScore {
		t.Fatalf("prompt output must not bypass minimum-score gate: %v", err)
	}
}

type auditMeasurementCard struct {
	Grade    string  `json:"grade"`
	Partial  bool    `json:"partial"`
	Coverage float64 `json:"coverage"`
}

type auditMeasurementOutput struct {
	Report struct {
		Overall auditMeasurementCard `json:"overall"`
		Scenes  []struct {
			Runs int                  `json:"runs"`
			Card auditMeasurementCard `json:"card"`
		} `json:"scenes"`
	} `json:"report"`
	Errors []json.RawMessage `json:"errors"`
}

func TestAuditFailedRunCannotCertifySurvivingRun(t *testing.T) {
	for _, failure := range []string{"parse", "window"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			p := testgen.Baseline()
			p.DurationSec, p.FrameMeanMs, p.FrameJitterMs = 2, 1000.0/60, 0
			p.LongTaskRate, p.ForcedLayoutRate = 0, 0
			writeRec(t, dir, "scene-1.json", 1, p)
			args := []string{"audit", dir, "--group-by-prefix", "--format", "json"}
			if failure == "parse" {
				if err := os.WriteFile(filepath.Join(dir, "scene-2.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				p.DurationSec = .1
				writeRec(t, dir, "scene-2.json", 1, p)
				args = append(args, "--from", ".5", "--to", "1.6")
			}
			out, err := run(t, args...)
			if err == nil {
				t.Fatal("failed run must preserve input error")
			}
			var doc auditMeasurementOutput
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Errors) != 1 || len(doc.Report.Scenes) != 1 {
				t.Fatalf("preserve surviving results and failure: %s", out)
			}
			scene := doc.Report.Scenes[0]
			if scene.Runs != 1 {
				t.Errorf("failed runs are not parsed runs: %d", scene.Runs)
			}
			for _, c := range []auditMeasurementCard{scene.Card, doc.Report.Overall} {
				if c.Grade != "n/a" || !c.Partial || c.Coverage > .5 {
					t.Errorf("failed requested run cannot earn healthy grade or full coverage: %+v", c)
				}
			}
		})
	}
}

func TestAuditDiscoveryFailureCannotCertifySurvivingRuns(t *testing.T) {
	dir := t.TempDir()
	p := testgen.Baseline()
	p.DurationSec, p.FrameMeanMs, p.FrameJitterMs = 2, 1000.0/60, 0
	p.LongTaskRate, p.ForcedLayoutRate = 0, 0
	good := writeRec(t, dir, "healthy.json", 1, p)
	out, err := run(t, "audit", good, filepath.Join(dir, "missing.json"), "--format", "json")
	if err == nil {
		t.Fatal("missing path must preserve discovery error")
	}
	var doc auditMeasurementOutput
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Errors) != 1 || doc.Report.Overall.Grade != "n/a" || !doc.Report.Overall.Partial {
		t.Fatalf("unknown missing evidence cannot support overall grade: %s", out)
	}
}

func TestAuditAllFailedRunsRemainUnobserved(t *testing.T) {
	file := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "audit", file, "--format", "json")
	if err == nil {
		t.Fatal("failed run must preserve input error")
	}
	var doc auditMeasurementOutput
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Errors) != 1 || len(doc.Report.Scenes) != 1 {
		t.Fatalf("requested failed scene must remain visible: %s", out)
	}
	if s := doc.Report.Scenes[0]; s.Runs != 0 || s.Card.Grade != "n/a" || s.Card.Coverage != 0 || !s.Card.Partial {
		t.Fatalf("failed scene must not impute parsed runs or observed standards: %+v", s)
	}
}
