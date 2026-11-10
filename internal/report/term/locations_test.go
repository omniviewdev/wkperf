package term

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/compare"
)

func TestRenderPreservesGeneratedAndOriginalLocations(t *testing.T) {
	var r compare.Result
	if err := json.Unmarshal([]byte(`{"funcRegressed":[{"name":"f","url":"https://example.test/assets/bundle.js","line":1,"column":12345,"sourceID":"7","original":{"url":"src/Component.tsx","line":60,"column":12},"baseSelfMs":1,"candSelfMs":20,"deltaSelfMs":19,"verdict":"regressed"}]}`), &r); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Render(&b, &r, Options{Width: 60}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://example.test/assets/bundle.js:1:12345", "src/Component.tsx:60:12"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
