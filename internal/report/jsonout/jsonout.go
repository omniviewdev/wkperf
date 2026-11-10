// Package jsonout renders a compare.Result as indented JSON.
package jsonout

import (
	"encoding/json"
	"io"

	"github.com/omniviewdev/wkperf/internal/compare"
)

// Schema identifies the JSON layout version.
const Schema = "wkperf/v1"

type document struct {
	Schema string `json:"schema"`
	*compare.Result
}

// Render writes r as indented JSON with a top-level schema field.
func Render(w io.Writer, r *compare.Result) error {
	if r == nil {
		r = &compare.Result{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(document{Schema: Schema, Result: r})
}
