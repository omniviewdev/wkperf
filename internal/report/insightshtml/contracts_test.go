package insightshtml

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestRichAnalysisHTMLContracts(t *testing.T) {
	var r insights.Report
	if err := json.Unmarshal([]byte(`{"name":"Profile <script>","durationSec":2,"hotspots":[{"name":"leaf<script>","url":"bundle.js","line":1,"column":91,"sourceID":"7","original":{"url":"src/Leaf.tsx","line":12,"column":3},"selfMs":8,"totalMs":8,"callers":[{"function":{"name":"parent<script>","url":"bundle.js","line":1,"column":30},"weightMs":8,"samples":4}]}],"inclusiveHotspots":[{"name":"parent<script>","url":"bundle.js","line":1,"column":30,"selfMs":0,"totalMs":20,"callees":[{"function":{"name":"leaf<script>","url":"bundle.js","line":1,"column":91},"weightMs":8,"samples":4}]}],"triggers":[{"kind":"timer","name":"setInterval <unsafe>","count":2,"totalMs":14,"meanMs":7,"p95Ms":8,"maxMs":8,"topSelf":[{"name":"leaf<script>","url":"bundle.js","line":1,"column":91,"selfMs":8,"totalMs":8}],"topInclusive":[{"name":"parent<script>","url":"bundle.js","line":1,"column":30,"totalMs":14}],"sampleMessage":"Sampled overlap is attribution, not causation"}],"findings":[{"id":"timer-cost","title":"Timer work","severity":"medium","category":"script","confidence":"medium","confidenceReasons":["sparse samples <unsafe>"],"metricKeys":["script.timer.total_ms"],"numericEvidence":[{"metricKey":"script.timer.total_ms","value":14,"unit":"ms","description":"measured handler work"}],"verify":[{"metricKey":"script.timer.total_ms","direction":"decrease","target":7,"unit":"ms","description":"same scenario"}],"suggestedWindows":[{"fromSec":0.25,"toSec":0.75,"reason":"inspect <unsafe>","flags":["--from","--to"]}]}]}`), &r); err != nil {
		t.Fatal(err)
	}
	var summary metrics.Summary
	if err := json.Unmarshal([]byte(`{"commonStacks":[{"frames":[{"name":"parent<script>","url":"bundle.js","line":1,"column":30},{"name":"leaf<script>","url":"bundle.js","line":1,"column":91}],"weightMs":8,"samples":4}]}`), &summary); err != nil {
		t.Fatal(err)
	}
	r.Summary = &summary
	var b bytes.Buffer
	if err := Render(&b, []*insights.Report{&r}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bundle.js:1:91", "src/Leaf.tsx:12:3", "Inclusive", "Callers", "Callees", "Common stacks", "Trigger timing", "Trigger code attribution", "setInterval &lt;unsafe&gt;", "script.timer.total_ms", "Confidence", "medium", "Verify", "decrease", "Suggested windows", "0.250", "0.750", "sparse samples &lt;unsafe&gt;", "parent&lt;script&gt;"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(b.String(), "<script>") || strings.Contains(b.String(), "<unsafe>") {
		t.Fatal("unescaped report data")
	}
}
