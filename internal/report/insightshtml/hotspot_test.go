package insightshtml

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
)

func TestHotspotHTMLIncludesStableIDAndFullStack(t *testing.T) {
	var profile insights.Report
	if err := json.Unmarshal([]byte(`{"hotspots":[{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90,"selfMs":20,"commonStack":[{"id":"parent-stable","name":"owner<unsafe>","url":"bundle.js","line":1,"column":30},{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90}]}]}`), &profile); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Render(&b, []*insights.Report{&profile}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"function-stable", "Common stack", "owner&lt;unsafe&gt;", "bundle.js:1:30"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("hotspot HTML missing %q", want)
		}
	}
	if strings.Contains(b.String(), "owner<unsafe>") {
		t.Fatal("unescaped stack frame")
	}
}
