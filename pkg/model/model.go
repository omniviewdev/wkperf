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

// Discontinuity is an interval between capture stop and resume.
type Discontinuity = model.Discontinuity

// MemoryPressure is a timestamped memory pressure notification.
type MemoryPressure = model.MemoryPressure

// Target identifies an inspected page or worker.
type Target = model.Target

// Thread contains one thread's CPU usage in a resource sample.
type Thread = model.Thread

// MemoryCategory holds the sampled byte count for one memory category.
type MemoryCategory = model.MemoryCategory

// SampleEvidence preserves samples in their original export order.
type SampleEvidence = model.SampleEvidence

// TargetSamples holds weighted stack traces for one target.
type TargetSamples = model.TargetSamples

// StackTrace is a sampled stack, ordered from the executing leaf to the root.
type StackTrace = model.StackTrace

// StackFrame is one frame in a sampled stack.
type StackFrame = model.StackFrame

// SourceLocation identifies a source-mapped frame location.
type SourceLocation = model.SourceLocation

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
