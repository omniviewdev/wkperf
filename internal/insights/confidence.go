package insights

import (
	"fmt"

	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func findingEvidenceBasis(id string) EvidenceBasis {
	switch id {
	case "jank", "input-stall", "long-tasks", "forced-layout", "layout-after-script",
		"layout-heavy", "style-recalc-heavy", "paint-heavy", "raf-load", "timer-storm",
		"event-handler-heavy", "microtask-heavy", "observer-callback-heavy", "gc-pressure", "network":
		return EvidenceMeasured
	case "hot-function", "hot-module", "low-sample-coverage", "cpu-high",
		"react-sync", "react-sync-in-trigger", "deep-stack", "deep-commit", "deep-recursion":
		return EvidenceSampled
	default:
		return EvidenceHypothesis
	}
}

// SetFindingConfidence derives support from actual analyses containing f.ID.
// Callers pass each matching report once, all attempted runs, and the number of
// parsed analyses. Missing inputs stay in the prevalence denominator. Sample
// counts and coverage are checked per capture, never pooled across runs.
func SetFindingConfidence(f *Finding, reports []*Report, runs, parsedRuns int) {
	if f.EvidenceBasis == "" {
		f.EvidenceBasis = findingEvidenceBasis(f.ID)
	}
	f.Replication = Replication{Runs: runs, ParsedRuns: parsedRuns, RunsWithFinding: len(reports)}
	f.Confidence = ConfidenceLow
	f.ConfidenceReasons = []string{fmt.Sprintf("Finding seen in %d of %d runs; %d parsed captures. Presence matches the finding ID, not an established shared cause.", len(reports), runs, parsedRuns)}
	if runs == 1 && parsedRuns == 1 {
		f.ConfidenceReasons = append(f.ConfidenceReasons, "Single-run observation; repeat the same scene to estimate variance.")
	}
	if parsedRuns < runs {
		f.ConfidenceReasons = append(f.ConfidenceReasons, fmt.Sprintf("Evidence is incomplete: %d of %d attempted captures were parsed; failed or unavailable inputs are not negative findings.", parsedRuns, runs))
	}

	short, sparse, lowCoverage, unavailableCoverage := 0, 0, 0, 0
	for _, report := range reports {
		if report.DurationSec < 10 {
			short++
		}
		coverage := report.SampleCoverage
		if coverage.SampleCount < 100 {
			sparse++
		}
		if !coverage.Available {
			unavailableCoverage++
		} else if coverage.Ratio < 0.5 {
			lowCoverage++
		}
		if temporalFinding(f.ID) {
			sampledMs := coverage.SampledMs
			if coverage.UntimedExcluded {
				sampledMs += coverage.UntimedMs
			}
			if note := rootcause.UntimedAttributionNote(coverage.UntimedMs, sampledMs); note != "" {
				f.ConfidenceReasons = appendUnique(f.ConfidenceReasons, note)
			}
		}
	}
	if short > 0 {
		f.ConfidenceReasons = append(f.ConfidenceReasons, fmt.Sprintf("Short capture: fewer than 10 active seconds in %d finding run(s).", short))
	}
	if sparse > 0 {
		f.ConfidenceReasons = append(f.ConfidenceReasons, fmt.Sprintf("Sparse JavaScript samples: fewer than 100 weighted samples in %d finding run(s).", sparse))
	}
	if lowCoverage > 0 {
		f.ConfidenceReasons = append(f.ConfidenceReasons, fmt.Sprintf("Sample weights cover less than half the measured script time in %d finding run(s).", lowCoverage))
	}
	if unavailableCoverage > 0 {
		f.ConfidenceReasons = append(f.ConfidenceReasons, fmt.Sprintf("Sample coverage unavailable: no measured script denominator in %d finding run(s).", unavailableCoverage))
	}

	prevalent := runs > 0 && len(reports)*3 >= runs*2
	switch f.EvidenceBasis {
	case EvidenceMeasured:
		switch {
		case parsedRuns >= 3 && prevalent:
			f.Confidence = ConfidenceHigh
			f.ConfidenceReasons = append(f.ConfidenceReasons, "Instrumented evidence recurs in at least two thirds of attempted runs, with at least three parsed captures.")
		case runs == 1 && len(reports) == 1 && short == 0:
			f.Confidence = ConfidenceMedium
			f.ConfidenceReasons = append(f.ConfidenceReasons, "Instrumented evidence spans at least 10 active seconds; a single capture cannot establish replication.")
		default:
			if parsedRuns < 3 {
				f.ConfidenceReasons = append(f.ConfidenceReasons, "High measured confidence requires at least three parsed captures.")
			}
			if !prevalent {
				f.ConfidenceReasons = append(f.ConfidenceReasons, "The finding is present in fewer than two thirds of attempted runs.")
			}
		}
	case EvidenceSampled:
		if len(reports) > 0 && prevalent && short == 0 && sparse == 0 && lowCoverage == 0 && unavailableCoverage == 0 {
			f.Confidence = ConfidenceMedium
			f.ConfidenceReasons = append(f.ConfidenceReasons, "Each finding capture has at least 10 active seconds, 100 samples and 50% sample coverage; sampled confidence is capped at medium.")
		}
		if !prevalent {
			f.ConfidenceReasons = append(f.ConfidenceReasons, "The finding is present in fewer than two thirds of attempted runs.")
		}
		f.ConfidenceReasons = append(f.ConfidenceReasons, "Sample weights estimate cost; repeated finding IDs do not prove the same function or timeline cause.")
	case EvidenceHypothesis:
		f.ConfidenceReasons = append(f.ConfidenceReasons, "Pattern evidence supports a hypothesis, not proof of the suspected cause; repetition alone cannot raise confidence.")
	}
}
