package compare

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestCompareHotspotIdentityAndExactStack(t *testing.T) {
	var candidate metrics.Summary
	if err := json.Unmarshal([]byte(`{"functions":[{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90,"selfMs":20,"commonStack":[{"id":"parent-stable","name":"owner","url":"bundle.js","line":1,"column":30},{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90}]}]}`), &candidate); err != nil {
		t.Fatal(err)
	}
	r := Compare([]*metrics.Summary{{}}, []*metrics.Summary{&candidate}, DefaultOptions())
	payload, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"function-stable"`, `"commonStack"`, `"id":"parent-stable"`} {
		if !bytes.Contains(payload, []byte(want)) {
			t.Errorf("comparison lost %s", want)
		}
	}
	var b bytes.Buffer
	if err := RenderMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"function-stable", "Common stack", "owner", "bundle.js:1:30", "bundle.js:1:90"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("Markdown missing %q", want)
		}
	}
}
