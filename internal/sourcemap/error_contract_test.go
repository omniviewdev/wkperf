package sourcemap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestExplicitMapTypedError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.js.map")
	if err := os.WriteFile(bad, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{bad, filepath.Join(dir, "missing.js.map")} {
		_, err := Resolve(&model.Recording{}, []string{file}, "profile.json")
		var typed *diagnostic.Error
		if !errors.As(err, &typed) || typed.Code != diagnostic.SourceMapError || typed.File != file {
			t.Fatalf("want typed source_map_error at map path; got %v", err)
		}
		if strings.Count(err.Error(), file) != 1 || strings.Contains(typed.Message(), file) {
			t.Fatalf("map path must occur once: %v", err)
		}
	}
}
