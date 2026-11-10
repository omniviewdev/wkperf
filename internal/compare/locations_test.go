package compare

import (
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestCompareKeepsColumnAndSourceIdentity(t *testing.T) {
	var base, candidate metrics.Summary
	if err := json.Unmarshal([]byte(`{"functions":[{"name":"f","url":"bundle.js","line":1,"column":20,"sourceID":"one","selfMs":10},{"name":"f","url":"bundle.js","line":1,"column":30,"sourceID":"one","selfMs":10},{"name":"f","url":"","line":1,"column":20,"sourceID":"two","selfMs":10}]}`), &base); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"functions":[{"name":"f","url":"bundle.js","line":1,"column":20,"sourceID":"one","original":{"url":"src/F.tsx","line":4,"column":2},"selfMs":20},{"name":"f","url":"bundle.js","line":1,"column":30,"sourceID":"one","selfMs":5},{"name":"f","url":"","line":1,"column":20,"sourceID":"two","selfMs":30}]}`), &candidate); err != nil {
		t.Fatal(err)
	}
	r := Compare([]*metrics.Summary{&base}, []*metrics.Summary{&candidate}, DefaultOptions())
	if len(r.FuncRegressed) != 2 || len(r.FuncImproved) != 1 {
		t.Fatalf("distinct minified functions merged: %+v %+v", r.FuncRegressed, r.FuncImproved)
	}
	payload, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Functions []struct {
			Column   int
			SourceID string
			Original *struct{ URL string }
		} `json:"funcRegressed"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Functions[0].Column != 20 || doc.Functions[0].SourceID != "two" || doc.Functions[1].Original == nil {
		t.Fatalf("identity or source metadata lost: %s", payload)
	}
}
