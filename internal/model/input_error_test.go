package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
)

func TestLoadTypedInputErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		code diagnostic.Code
	}{
		{filepath.Join(dir, "missing.json"), diagnostic.LoadError},
		{dir, diagnostic.InputError},
		{bad, diagnostic.LoadError},
	} {
		_, err := Load(tc.path)
		var typed *diagnostic.Error
		if !errors.As(err, &typed) || typed.Code != tc.code || typed.File != tc.path {
			t.Fatalf("want typed %s for %s; got %v", tc.code, tc.path, err)
		}
		if strings.Count(err.Error(), tc.path) != 1 || strings.Contains(typed.Message(), tc.path) {
			t.Fatalf("file path must occur once in error, separately from message: %v", err)
		}
	}
}
