package flame

import (
	"fmt"
	"math"
)

// DiffMode controls after-minus-before weighted-time comparisons.
type DiffMode string

const (
	DiffAuto      DiffMode = "auto"
	DiffNone      DiffMode = "none"
	DiffPerSecond DiffMode = "per-second"
	DiffShare     DiffMode = "share"
)

// DiffWarning describes a limit on comparison, using the same duration-mismatch
// (>10% against baseline) and sample-sparse (<200) rules as compare.
type DiffWarning struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DiffNormalization is the reusable contract for flame and query differences.
// Raw input values remain milliseconds. Delta converts only their difference:
// none uses ms, per-second uses ms/s of active duration, and share uses percentage
// points of each input's selected sampled weight. Auto selects per-second only
// when both active durations are known and differ by more than 10% of baseline.
// Missing denominators cause an explicit warning and raw (none) comparison.
type DiffNormalization struct {
	RequestedMode  DiffMode      `json:"requestedMode"`
	Mode           DiffMode      `json:"mode"`
	Unit           string        `json:"unit"`
	BeforeTotalMs  float64       `json:"beforeTotalMs"`
	AfterTotalMs   float64       `json:"afterTotalMs"`
	BeforeActiveMs float64       `json:"beforeActiveMs"`
	AfterActiveMs  float64       `json:"afterActiveMs"`
	Warnings       []DiffWarning `json:"warnings"`
}

// ParseDiffMode validates the CLI and shared-engine normalization vocabulary.
// The empty value means auto, as does omitting --normalize.
func ParseDiffMode(value string) (DiffMode, error) {
	mode := DiffMode(value)
	if mode == "" {
		mode = DiffAuto
	}
	switch mode {
	case DiffAuto, DiffNone, DiffPerSecond, DiffShare:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown normalization %q (want auto|none|per-second|share)", value)
	}
}

// ResolveDiffNormalization retains comparison evidence once, for reuse across
// all function/context deltas. It does not mutate or scale either input tree.
func ResolveDiffNormalization(before, after *Tree, mode DiffMode) (DiffNormalization, error) {
	requested, err := ParseDiffMode(string(mode))
	if err != nil {
		return DiffNormalization{}, err
	}
	out := DiffNormalization{RequestedMode: requested, Mode: requested, Unit: "ms", Warnings: make([]DiffWarning, 0)}
	if before != nil {
		out.BeforeActiveMs = before.ActiveDurationMs
		if before.Root != nil {
			out.BeforeTotalMs = before.Root.TotalMs
		}
	}
	if after != nil {
		out.AfterActiveMs = after.ActiveDurationMs
		if after.Root != nil {
			out.AfterTotalMs = after.Root.TotalMs
		}
	}
	positive := func(value float64) bool { return finite(value) && value > 0 }
	knownDuration := positive(out.BeforeActiveMs) && positive(out.AfterActiveMs)
	mismatch := knownDuration && math.Abs(out.AfterActiveMs-out.BeforeActiveMs)/out.BeforeActiveMs > .10
	if out.Mode == DiffAuto {
		out.Mode = DiffNone
		if mismatch {
			out.Mode = DiffPerSecond
		}
	}
	switch out.Mode {
	case DiffPerSecond:
		if knownDuration {
			out.Unit = "ms/s"
		} else {
			out.Mode = DiffNone
			out.Warnings = append(out.Warnings, DiffWarning{Level: "warn", Code: "normalization-unavailable", Message: "per-second normalization requires positive active durations on both inputs; raw milliseconds were retained"})
		}
	case DiffShare:
		if positive(out.BeforeTotalMs) && positive(out.AfterTotalMs) {
			out.Unit = "percentage-points"
		} else {
			out.Mode = DiffNone
			out.Warnings = append(out.Warnings, DiffWarning{Level: "warn", Code: "normalization-unavailable", Message: "share normalization requires positive sampled totals on both inputs; raw milliseconds were retained"})
		}
	}
	if !knownDuration && requested == DiffAuto {
		out.Warnings = append(out.Warnings, DiffWarning{Level: "warn", Code: "duration-unavailable", Message: "active duration is unavailable on one or both inputs; auto retained raw milliseconds"})
	}
	if mismatch {
		level := "warn"
		message := fmt.Sprintf("active durations differ: baseline %.3fs vs candidate %.3fs (%+.0f%%)", out.BeforeActiveMs/1000, out.AfterActiveMs/1000, 100*(out.AfterActiveMs-out.BeforeActiveMs)/out.BeforeActiveMs)
		switch out.Mode {
		case DiffPerSecond:
			level, message = "info", message+"; weighted-time deltas were compared per second"
		case DiffShare:
			level, message = "info", message+"; weighted-time deltas compare shares of sampled weight"
		default:
			message += "; raw totals are not duration-comparable (use per-second normalization)"
		}
		out.Warnings = append(out.Warnings, DiffWarning{Level: level, Code: "duration-mismatch", Message: message})
	}
	for _, input := range []struct {
		name string
		tree *Tree
	}{{"baseline", before}, {"candidate", after}} {
		if count := input.tree.SampleCount(); count < 200 {
			out.Warnings = append(out.Warnings, DiffWarning{Level: "warn", Code: "sample-sparse", Message: fmt.Sprintf("%s has only %d profiler samples (<200); function-level deltas are unreliable", input.name, count)})
		}
	}
	return out, nil
}

// Delta returns the normalized after-minus-before value in Unit. Both arguments
// are unmodified raw milliseconds from the same trees used to resolve the mode.
func (normalization DiffNormalization) Delta(beforeMs, afterMs float64) float64 {
	switch normalization.Mode {
	case DiffPerSecond:
		return afterMs/(normalization.AfterActiveMs/1000) - beforeMs/(normalization.BeforeActiveMs/1000)
	case DiffShare:
		return 100 * (afterMs/normalization.AfterTotalMs - beforeMs/normalization.BeforeTotalMs)
	default:
		return afterMs - beforeMs
	}
}

// DeltaUnit labels normalized deltas without changing raw baseline/current ms.
func (comparison *Comparison) DeltaUnit() string {
	if comparison == nil || comparison.Normalization.Unit == "" {
		return "ms"
	}
	return comparison.Normalization.Unit
}
