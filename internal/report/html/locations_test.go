package html

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/compare"
)

func TestRenderFullSourceLocationsEscaped(t *testing.T) {
	var r compare.Result
	if err := json.Unmarshal([]byte(`{"funcRegressed":[{"name":"f<script>","url":"bundle<unsafe>.js","line":1,"column":12345,"original":{"url":"src/App<unsafe>.tsx","line":60,"column":12},"baseSelfMs":1,"candSelfMs":20,"deltaSelfMs":19,"verdict":"regressed"}]}`), &r); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Render(&b, &r, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bundle&lt;unsafe&gt;.js:1:12345", "src/App&lt;unsafe&gt;.tsx:60:12", "f&lt;script&gt;"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(b.String(), "<unsafe>") || strings.Contains(b.String(), "f<script>") {
		t.Fatal("unescaped location")
	}
}
