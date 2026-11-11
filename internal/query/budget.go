package query

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
)

// Finalize bounds the already sorted, offset and row-limited result using its
// actual compact JSON encoding. Other transports must also bound their own
// representation with Prepare or Bound rather than estimating JSON overhead.
func Finalize(result *Result, req Request) (*Result, error) {
	copy := *result
	copy.MaxBytes = req.MaxBytes
	copy.MaxStackFrames, copy.MaxStackFramesSet = req.MaxStackFrames, req.MaxStackFramesSet
	bounded, _, err := Bound(&copy, req.MaxBytes, jsonBytes)
	return bounded, err
}

// Prepare returns a bounded copy and exactly the bytes Render writes. JSON,
// Markdown and TSV include all metadata, escaping and the final newline.
func Prepare(result *Result, format string) (*Result, []byte, error) {
	if format != "json" && format != "md" && format != "tsv" {
		return nil, nil, diagnostic.Wrap(diagnostic.UsageError, "", fmt.Errorf("unknown format %q (want json|md|tsv)", format))
	}
	return Bound(result, result.MaxBytes, func(value *Result) ([]byte, error) {
		var out bytes.Buffer
		if err := renderUnbounded(&out, value, format); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	})
}

// Bound selects the largest fitting row prefix using binary search. encode
// must return the complete transport representation, including any envelope or
// escaped duplicate. No input rows, frames, diagnostics or errors are mutated.
// Mandatory metadata that cannot fit receives actionable, typed guidance.
func Bound(result *Result, maxBytes int, encode func(*Result) ([]byte, error)) (*Result, []byte, error) {
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxBytes < 1 || maxBytes > MaxBytes {
		return nil, nil, diagnostic.Wrap(diagnostic.UsageError, "", fmt.Errorf("max_bytes must be between 1 and %d (0 uses %d)", MaxBytes, DefaultMaxBytes))
	}
	base := *result
	if base.MaxBytes == 0 {
		base.MaxBytes = maxBytes
	}
	if base.Limit == 0 {
		base.Limit = DefaultLimit
	}
	frames := stackLimit(&base)
	if frames < 0 {
		return nil, nil, diagnostic.Wrap(diagnostic.UsageError, "", fmt.Errorf("max_stack_frames must not be negative"))
	}
	count := len(base.Rows)
	if base.Status == "ambiguous" {
		count = len(base.Candidates)
	}
	details := len(base.HotPathBranches)
	maxDepth := len(base.DeepestStack)
	for _, row := range base.Rows {
		details = max(details, len(row.TopSelf), len(row.TopInclusive))
		maxDepth = max(maxDepth, len(row.Stack))
	}
	if frames == 0 || frames > maxDepth {
		frames = maxDepth
	}
	makeResult := func(n, frameLimit, detailLimit int, byteTrimmed bool) *Result {
		copy := base
		copy.MaxStackFrames, copy.MaxStackFramesSet = frameLimit, true
		copy.TruncatedBy = nil
		for _, reason := range base.TruncatedBy {
			if reason != "stack_frames" {
				copy.TruncatedBy = addReason(copy.TruncatedBy, reason)
			}
		}
		if copy.Status == "ambiguous" {
			copy.Candidates = base.Candidates[:n]
		} else {
			copy.Rows = base.Rows[:n]
		}
		copy.ReturnedCount = n
		copy.NextOffset = nil
		if copy.Offset+n < copy.TotalCount {
			next := copy.Offset + n
			copy.NextOffset = &next
		}
		if len(copy.HotPathBranches) > detailLimit {
			copy.HotPathBranches = copy.HotPathBranches[:detailLimit]
		}
		copiedRows := false
		for i, row := range copy.Rows {
			if len(row.TopSelf) > detailLimit || len(row.TopInclusive) > detailLimit {
				if !copiedRows {
					copy.Rows = append([]Row(nil), copy.Rows...)
					copiedRows = true
				}
				copy.Rows[i].TopSelf = row.TopSelf[:min(len(row.TopSelf), detailLimit)]
				copy.Rows[i].TopInclusive = row.TopInclusive[:min(len(row.TopInclusive), detailLimit)]
				copy.Rows[i].FunctionsTruncated = true
			}
			if row.FunctionsTruncated && (!hasReason(base.TruncatedBy, "bytes") || hasReason(base.TruncatedBy, "limit")) {
				copy.TruncatedBy = addReason(copy.TruncatedBy, "limit")
			}
			if frameLimit > 0 && len(row.Stack) > frameLimit {
				copy.TruncatedBy = addReason(copy.TruncatedBy, "stack_frames")
			}
		}
		if base.HotPathBranchesTotal > len(base.HotPathBranches) && (!hasReason(base.TruncatedBy, "bytes") || hasReason(base.TruncatedBy, "limit")) {
			copy.TruncatedBy = addReason(copy.TruncatedBy, "limit")
		}
		if frameLimit > 0 && len(copy.DeepestStack) > frameLimit {
			copy.TruncatedBy = addReason(copy.TruncatedBy, "stack_frames")
		}
		if copy.Offset+n < copy.TotalCount && n == count {
			copy.TruncatedBy = addReason(copy.TruncatedBy, "limit")
		}
		if byteTrimmed {
			copy.TruncatedBy = addReason(copy.TruncatedBy, "bytes")
		}
		copy.Truncated = len(copy.TruncatedBy) > 0
		return &copy
	}
	try := func(n, frameLimit, detailLimit int, trimmed bool) (*Result, []byte, error) {
		candidate := makeResult(n, frameLimit, detailLimit, trimmed)
		encoded, err := encode(candidate)
		return candidate, encoded, err
	}
	full, encoded, err := try(count, frames, details, false)
	if err != nil || len(encoded) <= maxBytes {
		return full, encoded, err
	}

	// Keep at least one useful row. Only reduce stack/detail previews when the
	// ordinary first row cannot fit; an exact explicit stack stays exact whenever
	// the requested budget allows it.
	minimumRows := min(1, count)
	_, smallest, err := try(minimumRows, frames, 0, true)
	if err != nil {
		return nil, nil, err
	}
	if len(smallest) > maxBytes {
		minimumFrames := min(1, maxDepth)
		_, smallest, err = try(minimumRows, minimumFrames, 0, true)
		if err != nil {
			return nil, nil, err
		}
		if len(smallest) > maxBytes {
			return nil, nil, diagnostic.Wrap(diagnostic.UsageError, "", fmt.Errorf("max_bytes=%d cannot fit query metadata and %d useful row (need at least %d bytes); increase max_bytes up to %d, narrow the selector/window, or reduce max_stack_frames", maxBytes, minimumRows, len(smallest), MaxBytes))
		}
		low, high := minimumFrames, frames
		for low < high {
			middle := low + (high-low+1)/2
			_, data, err := try(minimumRows, middle, 0, true)
			if err != nil {
				return nil, nil, err
			}
			if len(data) <= maxBytes {
				low = middle
			} else {
				high = middle - 1
			}
		}
		frames = low
	}
	lowDetails, highDetails := 0, details
	for lowDetails < highDetails {
		middle := lowDetails + (highDetails-lowDetails+1)/2
		_, data, err := try(minimumRows, frames, middle, true)
		if err != nil {
			return nil, nil, err
		}
		if len(data) <= maxBytes {
			lowDetails = middle
		} else {
			highDetails = middle - 1
		}
	}
	details = lowDetails
	low, high := minimumRows, count
	for low < high {
		middle := low + (high-low+1)/2
		_, data, err := try(middle, frames, details, true)
		if err != nil {
			return nil, nil, err
		}
		if len(data) <= maxBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return try(low, frames, details, true)
}

func addReason(reasons []string, reason string) []string {
	rank := func(value string) int {
		switch value {
		case "limit":
			return 0
		case "bytes":
			return 1
		default:
			return 2
		}
	}
	for i, existing := range reasons {
		if existing == reason {
			return reasons
		}
		if rank(existing) > rank(reason) {
			reasons = append(reasons, "")
			copy(reasons[i+1:], reasons[i:])
			reasons[i] = reason
			return reasons
		}
	}
	return append(reasons, reason)
}

func hasReason(reasons []string, reason string) bool {
	for _, existing := range reasons {
		if existing == reason {
			return true
		}
	}
	return false
}

func jsonBytes(result *Result) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(Wire(result)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
