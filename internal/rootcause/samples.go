package rootcause

import (
	"fmt"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
)

// UntimedSampleStatus labels overlapping export-order spans, never sample times.
func UntimedSampleStatus(spans []flame.UntimedSpan, from, to, offset float64) string {
	var labels []string
	for _, span := range spans {
		if span.FromSec >= to || span.ToSec <= from {
			continue
		}
		labels = append(labels, fmt.Sprintf("%d untimed samples (%.6g ms), captured between +%s s and +%s s by export order, cannot be placed in time", span.Samples, span.Ms, decimal(span.FromSec+offset), decimal(span.ToSec+offset)))
	}
	if len(labels) == 0 {
		return ""
	}
	return "No timed samples in this interval; " + strings.Join(labels, "; ") + "."
}

// UntimedAttributionNote describes missing clocks when at least 10% of weight is untimed.
// sampledMs includes untimed weight even when the selected window excludes it.
func UntimedAttributionNote(untimedMs, sampledMs float64) string {
	if sampledMs <= 0 || untimedMs <= 0 || untimedMs/sampledMs < 0.1 {
		return ""
	}
	share := untimedMs / sampledMs * 100
	return fmt.Sprintf("%.0f%% of sampled weight has no timestamp; time-based attribution uses the remaining %.0f%%.", share, 100-share)
}
