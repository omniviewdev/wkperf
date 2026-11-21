// Package model reads WebKit Inspector Timelines recording exports.
package model

import (
	"io"

	"github.com/omniviewdev/wkperf/internal/model"
)

// Recording is the decoded Timelines recording payload.
type Recording = model.Recording

// Record is a Timelines event or resource sample.
type Record = model.Record

// Marker is a timestamped recording annotation.
type Marker = model.Marker

// Record type identifiers used in Timelines exports.
const (
	TypeCPU            = model.TypeCPU
	TypeLayout         = model.TypeLayout
	TypeMemory         = model.TypeMemory
	TypeRenderingFrame = model.TypeRenderingFrame
	TypeScript         = model.TypeScript
)

// Parse reads a flat or versioned WebKit Inspector Timelines export.
func Parse(r io.Reader) (*Recording, error) {
	return model.Parse(r)
}
