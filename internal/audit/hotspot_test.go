package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
)

func TestAuditHotspotIdentityAndExactStack(t *testing.T) {
	var profile insights.Report
	if err := json.Unmarshal([]byte(`{"hotspots":[{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90,"selfMs":20,"commonStack":[{"id":"parent-stable","name":"owner","url":"bundle.js","line":1,"column":30},{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90}]}]}`), &profile); err != nil {
		t.Fatal(err)
	}
	r := Build([]Scene{{Name: "edit", Runs: []Run{{Report: &profile}}}}, Options{})
	payload, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hotspots []struct {
			ID               string
			CommonStack      []any
			CommonStackScene string
		}
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hotspots) != 1 || doc.Hotspots[0].ID != "function-stable" || len(doc.Hotspots[0].CommonStack) != 2 || doc.Hotspots[0].CommonStackScene != "edit" {
		t.Fatal("audit lost hotspot evidence")
	}
	for _, render := range []func(*bytes.Buffer) error{func(b *bytes.Buffer) error { return RenderTerm(b, r, false) }, func(b *bytes.Buffer) error { return RenderMarkdown(b, r) }} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"function-stable", "Common stack", "edit", "owner", "bundle.js:1:30"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("audit text missing %q", want)
			}
		}
	}
}
