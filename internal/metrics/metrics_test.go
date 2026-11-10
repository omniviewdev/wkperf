package metrics

import (
	"strings"
	"testing"
)

func TestFunctionIdentityIncludesColumnAndFallbackSource(t *testing.T) {
	a := FuncStat{Name: "a", URL: "bundle.js", Line: 1, Column: 10, SourceID: "1"}
	b := a
	b.Column = 20
	if a.Key() == b.Key() {
		t.Fatal("same-line minified functions merged")
	}
	b = a
	b.SourceID = "2"
	if a.Key() != b.Key() {
		t.Fatal("sourceID must not split URL-backed functions")
	}
	a.URL, b.URL = "", ""
	if a.Key() == b.Key() {
		t.Fatal("URL-less sourceIDs merged")
	}
	a.Original = &SourceLocation{Name: "render", URL: "src/view.ts", Line: 8, Column: 4}
	if got := a.Location(); !strings.Contains(got, "1:10") || !strings.Contains(got, "src/view.ts:8:4") {
		t.Fatalf("location lost generated or original: %q", got)
	}
}
