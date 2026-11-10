package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
)

func TestAuditFunctionIdentityAndFindingEvidence(t *testing.T) {
	var r insights.Report
	if err := json.Unmarshal([]byte(`{"durationSec":1,"hotspots":[{"name":"f","url":"bundle.js","line":1,"column":20,"sourceID":"one","original":{"url":"src/F.tsx","line":4,"column":2},"selfMs":3},{"name":"f","url":"bundle.js","line":1,"column":30,"sourceID":"one","selfMs":4},{"name":"f","url":"","line":1,"column":20,"sourceID":"two","selfMs":5}],"findings":[{"id":"work","title":"Work","severity":"medium","category":"script","impactMs":12,"impactPct":1.2,"confidence":"medium","confidenceReasons":["limited samples"],"metricKeys":["script.total_ms"],"numericEvidence":[{"metricKey":"script.total_ms","value":12,"unit":"ms","description":"measured work"}],"verify":[{"metricKey":"script.total_ms","direction":"decrease","target":8,"unit":"ms"}],"suggestedWindows":[{"fromSec":0.2,"toSec":0.4,"reason":"work burst","flags":["--from","--to"]}]}]}`), &r); err != nil {
		t.Fatal(err)
	}
	r.SchemaVersion = "wkperf-insights/v2"
	a := Build([]Scene{{Name: "edit", Runs: []Run{{Report: &r}}}}, Options{})
	if len(a.Hotspots) != 3 {
		t.Fatalf("merged distinct minified functions: %+v", a.Hotspots)
	}
	payload, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"column":20`, `"sourceID":"two"`, `"original"`, `"numericEvidence"`, `"metricKeys"`, `"verify"`, `"confidence"`, `"suggestedWindows"`, `"rating"`, `"analysis"`, `"schemaVersion":"wkperf-insights/v2"`} {
		if !bytes.Contains(payload, []byte(want)) {
			t.Errorf("JSON missing %s", want)
		}
	}
	for _, render := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return RenderTerm(b, a, false) },
		func(b *bytes.Buffer) error { return RenderMarkdown(b, a) },
	} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"bundle.js:1:20", "bundle.js:1:30", "src/F.tsx:4:2", "script.total_ms", "medium", "decrease", "0.200", "0.400"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("report missing %q", want)
			}
		}
	}
}
