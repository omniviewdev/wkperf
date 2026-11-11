package audithtml

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
)

func TestPartialScoreHTMLLabelsCoverage(t *testing.T) {
	var report audit.Report
	if err := json.Unmarshal([]byte(`{"overall":{"score":97,"grade":"n/a","partial":true,"coverage":0.33},"scenes":[{"name":"idle","runs":1,"durationSec":2,"card":{"score":97,"grade":"n/a","partial":true,"coverage":0.33}}]}`), &report); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Render(&out, &report, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "Partial score") < 2 || !strings.Contains(out.String(), "33% coverage") || !strings.Contains(out.String(), "insufficient data") {
		t.Fatalf("partial numeric scores look fully graded: %.1600s", out.String())
	}
}
