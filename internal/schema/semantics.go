package schema

import "github.com/omniviewdev/wkperf/internal/diagnostic"

// The catalog describes serialized meaning, not Go field spelling. Shared entries
// apply only where the same concept is reused; context-specific entries override
// them for normalized deltas, timeline durations, indices and aggregate reports.
// x-unit names a physical unit, a dimensionless scale, or the field supplying it.
type leafMeaning struct {
	description string
	unit        string
}

var stringVocabularies = map[string][]string{
	"github.com/omniviewdev/wkperf/internal/diagnostic.Code": {
		string(diagnostic.InputError), string(diagnostic.LoadError), string(diagnostic.SourceMapError),
		string(diagnostic.WindowError), string(diagnostic.SelectorError), string(diagnostic.AmbiguousSelector),
		string(diagnostic.UnknownKey), string(diagnostic.UsageError), string(diagnostic.Regression),
		string(diagnostic.FixtureMismatch), string(diagnostic.ScoreBelowMinimum),
	},
	"github.com/omniviewdev/wkperf/internal/insights.Severity":      {"high", "medium", "low", "info"},
	"github.com/omniviewdev/wkperf/internal/insights.Confidence":    {"low", "medium", "high"},
	"github.com/omniviewdev/wkperf/internal/insights.EvidenceBasis": {"measured", "sampled", "hypothesis"},
	"github.com/omniviewdev/wkperf/internal/scorecard.Rating":       {"good", "needs-improvement", "poor", "n/a"},
	"github.com/omniviewdev/wkperf/internal/compare.Verdict":        {"improved", "regressed", "unchanged", "noisy", "info", "new", "removed"},
	"github.com/omniviewdev/wkperf/internal/flame.DiffMode":         {"auto", "none", "per-second", "share"},
	"github.com/omniviewdev/wkperf/internal/flame.Category":         {"app", "framework", "native", "gc", "unknown-bundled", "injected"},
}

var sharedMeanings = map[string]leafMeaning{
	"schema":                  {"Versioned discriminator selecting this command output contract.", ""},
	"schemaVersion":           {"Version of the analysis evidence contract embedded in this report.", ""},
	"source":                  {"Local recording path used to load this analysis and reproduce its evidence.", ""},
	"sources":                 {"Local recording paths included in this successful analysis group.", ""},
	"sourceMapPaths":          {"Configured source-map file or directory paths used for reproduction; these are not resolution diagnostics.", ""},
	"errors":                  {"Typed failures retained beside any successfully produced evidence.", ""},
	"ai":                      {"Optional model-generated narrative; structured measured evidence remains authoritative.", ""},
	"command":                 {"Shell-quoted wkperf command reproducing this evidence with the same source, window and options.", ""},
	"metricKey":               {"Stable scalar query key reproducing this exact numeric observation.", ""},
	"metricKeys":              {"Stable scalar query keys supporting this finding or priority.", ""},
	"unit":                    {"Physical unit or dimensionless scale used by the associated numeric observation.", ""},
	"description":             {"Human-readable interpretation and limitations of the associated numeric observation.", ""},
	"selfMs":                  {"Statistical weight of samples ending at this function or exact calling context; not elapsed occupancy.", "ms"},
	"totalMs":                 {"Inclusive statistical sample weight through this function or context; recursion counts once per sample for functions.", "ms"},
	"sampledMs":               {"Included statistical sample weight; sampler weights estimate cost rather than occupied clock time.", "ms"},
	"weightMs":                {"Statistical sample weight supporting this stack or adjacent call relation; not elapsed wall time.", "ms"},
	"samples":                 {"Number of positive-weight profiler observations supporting this aggregate; sample weights do not increase this count.", "count"},
	"selfSamples":             {"Number of retained profiler observations whose terminal frame is this function or module.", "count"},
	"fromSec":                 {"Inclusive window start in seconds relative to the original recording start.", "s"},
	"toSec":                   {"Exclusive window end in seconds relative to the original recording start.", "s"},
	"from_sec":                {"Export-order inferred span start relative to recording start; a label, not a usable timestamp.", "s"},
	"to_sec":                  {"Export-order inferred span end relative to recording start; a label, not a usable timestamp.", "s"},
	"stackId":                 {"Canonical n_ exact root-to-leaf selector resolving in the enclosing report's stacks table.", ""},
	"stackIds":                {"Selected canonical n_ exact stack selectors; resolve against the explicitly scoped analysis catalog.", ""},
	"stacksTotal":             {"Complete supporting exact stack count before the shared top-N evidence limit.", "count"},
	"evidenceTotal":           {"Complete supporting evidence stack count before the shared top-N evidence limit.", "count"},
	"windowsTotal":            {"Complete supporting timeline window count before the shared top-N evidence limit.", "count"},
	"nativeReadsTotal":        {"Complete supported native read API count before the shared top-N evidence limit.", "count"},
	"relatedFindings":         {"IDs of related causal facets; these are not additional independent costs to sum.", ""},
	"severity":                {"Impact-priority label, distinct from confidence in the evidence or proposed explanation.", ""},
	"evidence":                {"Human-readable observations supporting this finding; causal explanations may remain hypotheses.", ""},
	"suggestion":              {"Testable optimization guidance, not proof that the proposed cause produced the measured cost.", ""},
	"confidenceReasons":       {"Evidence-quality criteria, replication prevalence and incomplete-run caveats supporting the confidence label.", ""},
	"confidence":              {"Conservative evidence confidence, not severity or a causal probability. High requires measured evidence in at least three attempted runs with prevalence at least two thirds; medium requires sufficiently long measured or strongly supported sampled evidence. Missing runs remain in the denominator.", ""},
	"evidenceBasis":           {"Primary evidence origin: instrumented measurement, statistical sampling, or an unconfirmed causal hypothesis.", ""},
	"functions":               {"Generated or mapped function labels implicated by this finding; exact identities remain in the frame catalog.", ""},
	"triggers":                {"Observed trigger labels associated with this finding; association alone does not prove causation.", ""},
	"durationMs":              {"Observed active timeline interval duration, distinct from statistical sample weight.", "ms"},
	"activeMs":                {"Exclusive instrumented main-thread work within this interval, excluding the GC collector overlay.", "ms"},
	"workMs":                  {"Exclusive instrumented main-thread work within this interval, excluding the GC collector overlay.", "ms"},
	"frameworkMs":             {"Framework self-sample weight charged to the nearest captured non-framework context.", "ms"},
	"nativeMs":                {"Native self-sample weight charged to the nearest captured non-framework context.", "ms"},
	"untimedSamples":          {"Number of positive-weight profiler observations without a usable recording timestamp.", "count"},
	"untimedMs":               {"Whole-recording statistical weight of samples without usable timestamps, including those excluded by a window.", "ms"},
	"untimedExcluded":         {"True when the selected aggregate excludes whole-recording sample weight lacking usable timestamps.", ""},
	"untimedSpans":            {"Export-order inferred spans used only as labels; never use them for time-based attribution.", ""},
	"sampleStatus":            {"Evidence availability explanation; untimed samples cannot establish attribution to a timeline window.", ""},
	"stats":                   {"Exact numeric observations with query keys, physical units, evidence basis and reproduction commands.", ""},
	"beforeSelfMs":            {"Unmodified baseline statistical sample weight ending at this function or context.", "ms"},
	"afterSelfMs":             {"Unmodified candidate statistical sample weight ending at this function or context.", "ms"},
	"beforeTotalMs":           {"Unmodified baseline inclusive statistical sample weight, before normalization.", "ms"},
	"afterTotalMs":            {"Unmodified candidate inclusive statistical sample weight, before normalization.", "ms"},
	"beforeSamples":           {"Unnormalized baseline profiler observation count supporting this function or context.", "count"},
	"afterSamples":            {"Unnormalized candidate profiler observation count supporting this function or context.", "count"},
	"beforeActiveMs":          {"Baseline selected active clock duration, excluding recorder pauses.", "ms"},
	"afterActiveMs":           {"Candidate selected active clock duration, excluding recorder pauses.", "ms"},
	"warnings":                {"Explicit limitations or comparability problems retained alongside the successful evidence.", ""},
	"branchesTotal":           {"Complete competing-child count at the hot-path stopping point, before the evidence limit.", "count"},
	"contextAttributionTotal": {"Complete nearest non-framework context count before the shared evidence limit.", "count"},
	"periodicRootsTotal":      {"Complete qualifying periodic root count before the shared evidence limit.", "count"},
	"gcOverlapsTotal":         {"Complete observed GC collector interval count before the shared evidence limit.", "count"},
	"longTasksTotal":          {"Complete maximal overlapping script-task count above the configured threshold, before the evidence limit.", "count"},
	"slowFramesTotal":         {"Complete work-bound slow-frame count above the configured threshold, before the evidence limit.", "count"},
	"inputStallsTotal":        {"Complete input-driven low-work stall count before the shared evidence limit.", "count"},
	"eventBurstsTotal":        {"Complete clustered script-event burst count before the shared evidence limit.", "count"},
	"eventsTotal":             {"Complete distinct overlapping script-event count before the shared evidence limit.", "count"},
	"firesTotal":              {"Complete deduplicated periodic fire count before the shared evidence limit.", "count"},
	"outliersTotal":           {"Complete exact stack depth outlier count before the shared evidence limit.", "count"},
	"childrenTotal":           {"Complete unique nested event-type count before the shared evidence limit; nested records are not separate tasks.", "count"},
	"maxDepth":                {"Maximum complete root-to-leaf frame count supporting this depth observation.", "frames"},
	"commitFrames":            {"Captured React commit-related frame count supporting this depth facet, not measured component nesting.", "frames"},
	"reactFiberLevels":        {"Repeated React fiber traversal level count; ordinary traversal is not classified as recursion.", "count"},
}

type meaningGroup struct {
	definitions []string
	fields      map[string]leafMeaning
}

var meaningGroups = []meaningGroup{
	{[]string{"schema_Error"}, map[string]leafMeaning{
		"file":       {"Failed recording path, or an empty string for command-level failures; the message does not repeat this path.", ""},
		"message":    {"Human-readable failure reason without a duplicate prefix of the separately recorded file path.", ""},
		"code":       {"Stable failure classification telling callers whether to fix input, loading, source maps, selection, usage or an explicit quality gate.", ""},
		"candidates": {"At most ten deterministic nearest scalar-key suggestions for an unknown_key failure.", ""},
	}},
	{[]string{"schema_SourceMapMetadata"}, map[string]leafMeaning{
		"file": {"Recording path whose source-map resolution coverage is summarized by diagnostics.", ""},
	}},
	{[]string{"schema_SummaryDocument"}, map[string]leafMeaning{
		"runs": {"Number of successfully loaded recordings contributing to the summary metric medians.", "count"},
	}},
	{[]string{"schema_Analysis", "insights_Report"}, map[string]leafMeaning{
		"name":                {"Recording display name from the capture; source retains the distinct local file identity.", ""},
		"durationSec":         {"Selected active clock duration in seconds, excluding recorder pauses; not the sum of sampler weights.", "s"},
		"windowStartSec":      {"Inclusive selected window start relative to the original recording clock.", "s"},
		"windowEndSec":        {"Exclusive selected window end relative to the original recording clock.", "s"},
		"moduleGrouping":      {"Sampled module grouping policy: normalized source file or its parent directory.", ""},
		"jankMs":              {"Configured slow-frame and finding threshold in milliseconds; fixed scorecard standards remain at 16.7 ms.", "ms"},
		"longTaskMs":          {"Configured maximal script-task finding threshold in milliseconds; fixed scorecard standards remain at 50 ms.", "ms"},
		"gcMs":                {"Union of observed GC collector intervals; not a measured blocking pause or exclusive main-thread work.", "ms"},
		"gcOverlapWithWorkMs": {"GC collector interval intersection with instrumented main-thread work; overlap does not establish blocking.", "ms"},
		"moreEvidenceCommand": {"Explicit analyze rerun with a larger top limit while preserving source, maps, window and analysis options.", ""},
	}},
	{[]string{"flame_Frame", "metrics_FunctionFrame", "compare_FuncDiff"}, map[string]leafMeaning{
		"id":        {"Stable f_ selector derived from generated function identity; source maps and ownership rules do not change it.", ""},
		"name":      {"Captured generated function name, preserved separately from any mapped original name.", ""},
		"url":       {"Captured generated source URL; may be empty for URL-less or native functions and is not replaced by mapped paths.", ""},
		"line":      {"Captured generated 1-based source line; 0 or -1 can represent native or unknown locations.", "line"},
		"column":    {"Captured generated 1-based source column; 0 or -1 can represent native or unknown locations.", "column"},
		"sourceID":  {"Captured ScriptProfiler source identifier; distinguishes generated identity only when the captured URL is empty.", ""},
		"native":    {"True when this frame represents a native browser or runtime function rather than captured JavaScript.", ""},
		"anonymous": {"True when the captured generated function lacks a usable name; mapped metadata does not change generated identity.", ""},
		"category":  {"Effective ownership category of this frame, not its caller: library rules win app overlap; unknown bundled or injected code is not confirmed application code.", ""},
	}},
	{[]string{"flame_Location", "model_SourceLocation"}, map[string]leafMeaning{
		"name":   {"Source-map-resolved original function name when a declaration name is supported; never a guessed parameter name.", ""},
		"url":    {"Resolved original source URL or honest unresolved relative map source; never the captured generated identity.", ""},
		"line":   {"Mapped original 1-based source line; 0 or -1 can represent an unavailable location.", "line"},
		"column": {"Mapped original 1-based source column; 0 or -1 can represent an unavailable location.", "column"},
	}},
	{[]string{"flame_OwnershipRules"}, map[string]leafMeaning{
		"appURLs":     {"Effective whole-URL application ownership globs over generated or mapped sources; library matches take precedence.", ""},
		"libraryURLs": {"Effective whole-URL library ownership globs; matching library rules win overlap with application rules.", ""},
	}},
	{[]string{"flame_Interval"}, map[string]leafMeaning{
		"start": {"Inclusive half-open interval start in recording-relative seconds.", "s"},
		"end":   {"Exclusive half-open interval end in recording-relative seconds.", "s"},
	}},
	{[]string{"flame_SampleTotal"}, map[string]leafMeaning{
		"count": {"Whole-recording positive-weight untimed sample count, including evidence excluded by a window.", "count"},
		"ms":    {"Whole-recording untimed statistical sample weight, not an elapsed clock interval.", "ms"},
	}},
	{[]string{"flame_UntimedSpan"}, map[string]leafMeaning{
		"samples": {"Number of untimed observations in this export-order inferred span; no usable timestamps are implied.", "count"},
		"ms":      {"Statistical sample weight in this inferred export-order span; never time-attributed from the inferred bounds.", "ms"},
	}},
	{[]string{"sourcemap_Diagnostics"}, map[string]leafMeaning{
		"mappedFrames":   {"Captured frame occurrences with resolved original source metadata; repeated occurrences contribute separately.", "count"},
		"unmappedFrames": {"Captured frame occurrences without original source metadata, including native or unknown frames.", "count"},
		"missingMaps":    {"Distinct generated bundle URLs lacking matching source-map coverage, not source-map input paths.", ""},
		"minified":       {"Heuristic hint that generated JavaScript may be minified; not proof that every short name came from a minifier.", ""},
	}},
	{[]string{"insights_CatalogStack"}, map[string]leafMeaning{
		"frames": {"Zero-based report frame-catalog indices in complete exact root-to-leaf order, without stack depth truncation.", "index"},
		"depth":  {"Complete number of frames in this exact root-to-leaf sampled stack.", "frames"},
	}},
	{[]string{"insights_WireFunction"}, map[string]leafMeaning{
		"id":    {"Stable generated f_ function selector shared with the corresponding report frame-catalog entry.", ""},
		"frame": {"Zero-based index into the enclosing analysis frames catalog for this generated function.", "index"},
	}},
	{[]string{"insights_WireEdge"}, map[string]leafMeaning{
		"function": {"Zero-based report frame index identifying the adjacent caller or callee.", "index"},
	}},
	{[]string{"insights_Slice"}, map[string]leafMeaning{
		"name": {"Exclusive active-time work category; GC collector occupancy is an overlay rather than a work slice.", ""},
		"ms":   {"Exclusive active wall time assigned to this category; idle includes unoccupied time, not measured work.", "ms"},
		"pct":  {"Percentage of selected active recording duration assigned to this exclusive slice.", "%"},
	}},
	{[]string{"insights_ModuleStat"}, map[string]leafMeaning{
		"name": {"Normalized original source file when mapped, otherwise generated source file; parent-directory grouping is explicit.", ""},
		"pct":  {"Percentage of all included statistical self-sample weight charged to this module.", "%"},
	}},
	{[]string{"insights_SampleCoverage"}, map[string]leafMeaning{
		"sampleCount":        {"Included positive-weight profiler observation count supporting aggregate sample statistics.", "count"},
		"timedSampleCount":   {"Included positive-weight profiler observations with usable recording timestamps.", "count"},
		"untimedSampleCount": {"Whole-recording positive-weight observation count without timestamps, including samples excluded by this window.", "count"},
		"scriptMs":           {"Exclusive instrumented script work used as the sampled-coverage denominator; excludes collector intervals.", "ms"},
		"ratio":              {"Included statistical sample weight divided by exclusive measured script time; estimates may exceed one.", "ratio"},
		"available":          {"True when positive measured script time supplies a meaningful sample-coverage denominator.", ""},
		"interpretation":     {"Human-readable sample-support limitation; sampler estimates do not prove complete elapsed-time coverage.", ""},
	}},
	{[]string{"insights_SlowFrame", "insights_LongTask", "insights_InputStall"}, map[string]leafMeaning{
		"t":                   {"Observed interval start in seconds relative to the analyzed recording start.", "s"},
		"ms":                  {"Observed active timeline interval length, not an inferred statistical sample duration.", "ms"},
		"topFunc":             {"Hottest sampled generated or original function label when timed samples overlap this interval.", ""},
		"topEvent":            {"Dominant observed script event label within this interval, not proof of a causal scheduling owner.", ""},
		"scriptMs":            {"Exclusive instrumented script work within the rendering interval, excluding collector intervals.", "ms"},
		"layoutMs":            {"Exclusive layout and style work within the active rendering interval.", "ms"},
		"paintMs":             {"Exclusive paint and compositing work within the active rendering interval.", "ms"},
		"gcMs":                {"Observed GC collector interval union overlapping this frame, not a measured blocking pause.", "ms"},
		"gcOverlapWithWorkMs": {"GC collector interval intersection with instrumented main-thread work within this frame.", "ms"},
		"eventType":           {"Outer script record event type retained for this maximal overlapping script task.", ""},
		"children":            {"Selected sorted unique nested script-event types; nested records are not counted as independent tasks.", ""},
		"detail":              {"Outer task's captured event name, timer identifier, URL or other descriptive payload.", ""},
	}},
	{[]string{"insights_Evidence"}, map[string]leafMeaning{
		"value": {"Exact numeric observation in the sibling unit; sample-weight estimates remain distinct from instrumented durations.", "unit"},
		"basis": {"Origin and limitations of this numeric observation; may describe measured, estimated or explicitly instrumented evidence.", ""},
	}},
	{[]string{"insights_Verification"}, map[string]leafMeaning{
		"direction": {"Desired change of the associated scalar in repeated equivalent recordings, not a guaranteed improvement.", ""},
		"target":    {"Suggested verification threshold in the sibling unit; compare equivalent scenes rather than treating this as a guarantee.", "unit"},
	}},
	{[]string{"insights_SuggestedWindow"}, map[string]leafMeaning{
		"reason": {"Evidence-based explanation for inspecting this recording-relative interval.", ""},
		"flags":  {"CLI argument tokens reproducing this exact suggested window with relevant analysis options.", ""},
	}},
	{[]string{"insights_NodeClue"}, map[string]leafMeaning{
		"cssPath":     {"Captured CSS path identifying a DOM node implicated by the event, when available.", ""},
		"displayName": {"Captured human-readable DOM node label, when the recording provides one.", ""},
		"eventType":   {"Captured timeline event type associated with this DOM node clue.", ""},
	}},
	{[]string{"insights_WireFinding", "audit_Priority"}, map[string]leafMeaning{
		"id":        {"Stable detector finding ID used to match evidence and replication across recordings.", ""},
		"title":     {"Human-readable finding title summarizing the observed performance issue or unconfirmed hypothesis.", ""},
		"category":  {"Performance domain grouping for the finding, not generated-frame ownership.", ""},
		"impactMs":  {"Measured or statistically estimated cost supporting this finding; overlapping causal facets are not additive.", "ms"},
		"impactPct": {"Finding cost as a percentage of selected active recording duration; not a confidence probability.", "%"},
	}},
	{[]string{"insights_Replication"}, map[string]leafMeaning{
		"runs":            {"Attempted run denominator, including captures that failed loading or analysis; missing runs are not positive evidence.", "count"},
		"parsedRuns":      {"Attempted runs with an available parsed analysis report; may be smaller than the attempted denominator.", "count"},
		"runsWithFinding": {"Parsed runs whose actual analysis contains this finding ID; never copied from a representative run.", "count"},
	}},
	{[]string{"insights_WireTrigger"}, map[string]leafMeaning{
		"kind":          {"Observed script callback class used to group dispatch, timer, animation-frame and microtask records.", ""},
		"name":          {"Captured event name or normalized callback label within this trigger class.", ""},
		"count":         {"Number of observed script callback records in this trigger group.", "count"},
		"totalMs":       {"Sum of observed callback record durations; overlapping records may contribute separately.", "ms"},
		"meanMs":        {"Arithmetic mean observed callback duration in this trigger group.", "ms"},
		"p95Ms":         {"95th percentile observed callback duration in this trigger group.", "ms"},
		"maxMs":         {"Maximum observed callback duration in this trigger group.", "ms"},
		"sampleMessage": {"Timed profiler evidence availability within the merged observed trigger spans.", ""},
	}},
	{[]string{"insights_WireWakeups"}, map[string]leafMeaning{
		"count":     {"Number of observed low-occupancy rendering intervals classified as idle wakeups.", "count"},
		"perSecond": {"Observed idle wakeup count divided by selected active recording seconds.", "count/s"},
	}},
	{[]string{"rootcause_Stat"}, map[string]leafMeaning{
		"key":   {"Stable rootcause scalar key resolving to this exact observation with the same source and options.", ""},
		"label": {"Human-readable name for this measured interval, sample estimate or count.", ""},
		"value": {"Exact numeric observation in the sibling unit; basis distinguishes instrumented intervals from sampler estimates.", "unit"},
		"basis": {"Specific evidence origin, such as duration-weighted sampler estimate or instrumented timeline interval; not a causal claim.", ""},
	}},
	{[]string{"rootcause_Commands"}, map[string]leafMeaning{
		"hotPath": {"Shell-quoted query command inspecting the supported hot path with the effective analysis context.", ""},
		"window":  {"Shell-quoted query command inspecting an explicitly selected recording-relative window.", ""},
		"paths":   {"Shell-quoted query command inspecting complete exact paths for a stable generated function selector.", ""},
		"depth":   {"Shell-quoted query command inspecting sampled stack depth evidence.", ""},
		"owners":  {"Shell-quoted query command inspecting exclusive sample ownership totals.", ""},
		"stacks":  {"Shell-quoted query command listing complete exact sampled stack selectors.", ""},
		"gc":      {"Shell-quoted rootcause query command inspecting observed GC collector intervals and temporal overlaps.", ""},
	}},
	{[]string{"rootcause_Stack"}, map[string]leafMeaning{
		"firstSec": {"First usable timed observation relative to recording start, or null when the exact stack is untimed-only.", "s"},
		"lastSec":  {"Last usable timed observation relative to recording start, or null when the exact stack is untimed-only.", "s"},
	}},
	{[]string{"insights_WirePath"}, map[string]leafMeaning{
		"frames":     {"Zero-based report frame indices for the supported complete hot-path prefix in root-to-leaf order.", "index"},
		"stopReason": {"Evidence gate ending hot-path descent: no samples, insufficient share/support, near tie, or leaf.", ""},
	}},
	{[]string{"insights_WirePathNode"}, map[string]leafMeaning{
		"frame": {"Zero-based report frame index for this exact calling-context prefix.", "index"},
		"share": {"This child's inclusive sample weight divided by its parent context's inclusive sample weight.", "ratio"},
	}},
	{[]string{"rootcause_Owner"}, map[string]leafMeaning{
		"category": {"Effective owner category partitioning exclusive self-sample weight; inclusive caller costs are not added.", ""},
		"pct":      {"Percentage of all included statistical self-sample weight assigned to this owner category.", "%"},
	}},
	{[]string{"insights_WireAttribution"}, map[string]leafMeaning{
		"frame":   {"Zero-based report frame index for the nearest captured non-framework context, not necessarily confirmed app code.", "index"},
		"selfMs":  {"Own non-framework context self-sample weight, excluding framework and native weight charged to this context.", "ms"},
		"totalMs": {"Sum of this captured context's own self weight and framework/native self weight charged to its nearest context.", "ms"},
	}},
	{[]string{"rootcause_AttributionUnavailable"}, map[string]leafMeaning{
		"reason": {"Explanation for framework or native samples lacking a captured non-framework context; no scheduling owner is inferred.", ""},
	}},
	{[]string{"insights_WireRootFinding"}, map[string]leafMeaning{
		"id":          {"Stable root-cause detector finding ID, shared with its bridged analysis finding.", ""},
		"title":       {"Human-readable root-cause observation or suspected explanation supported by the attached evidence.", ""},
		"explanation": {"Interpretation of the detector evidence; sample patterns do not by themselves prove causality.", ""},
	}},
	{[]string{"rootcause_NativeRead"}, map[string]leafMeaning{
		"api":           {"Captured native browser read API name associated with a possible style or layout flush.", ""},
		"flush":         {"Potential API flush mechanism, not proof that this invocation forced work.", ""},
		"styleSamples":  {"Timed API samples overlapping instrumented recalculate-styles intervals.", "count"},
		"layoutSamples": {"Timed API samples overlapping instrumented layout intervals.", "count"},
		"styleMs":       {"Union of instrumented style intervals covering the API samples; not sampler self weight.", "ms"},
		"layoutMs":      {"Union of instrumented layout intervals covering the API samples; not sampler self weight.", "ms"},
	}},
	{[]string{"insights_WireRepeatedFrame"}, map[string]leafMeaning{
		"frame": {"Zero-based report frame index of the exact generated function counted in this depth facet.", "index"},
		"count": {"Occurrences of the same exact generated function in the supporting complete sampled stack.", "count"},
	}},
	{[]string{"rootcause_WindowRef"}, map[string]leafMeaning{
		"event": {"Observed event label associated with this suggested evidence window, when available.", ""},
	}},
	{[]string{"rootcause_Fire"}, map[string]leafMeaning{
		"event": {"Observed callback or contiguous sample-burst label associated with this deduplicated periodic fire.", ""},
	}},
	{[]string{"insights_WirePeriodic"}, map[string]leafMeaning{
		"root":       {"Zero-based report frame index of the repeated sampled root; generated identity remains unchanged by mapping.", "index"},
		"targetID":   {"Captured target identifier used to keep periodic evidence from distinct targets separate.", ""},
		"fireCount":  {"Deduplicated periodic event or contiguous sample-burst count before evidence truncation.", "count"},
		"periodMs":   {"Median elapsed spacing between successive qualifying periodic fires.", "ms"},
		"jitterMs":   {"95th percentile absolute deviation of elapsed inter-fire spacing from the median period.", "ms"},
		"regularity": {"One minus mean absolute period deviation divided by median period, clamped at zero; a timing pattern, not a causal probability.", "ratio"},
		"meanCostMs": {"Mean statistical sample weight per deduplicated periodic fire, not measured callback occupancy.", "ms"},
		"kind":       {"Observed periodic context classification: animation-frame-driven, input-driven, timer-background, or unknown.", ""},
		"rafFires":   {"Deduplicated fires enclosed by observed animation-frame callbacks.", "count"},
		"inputFires": {"Deduplicated fires overlapping observed input-driven frames or input dispatches.", "count"},
	}},
	{[]string{"rootcause_EventOverlap"}, map[string]leafMeaning{
		"event":     {"Observed script event label intersecting the collector interval; temporal overlap alone is not causality.", ""},
		"overlapMs": {"Temporal intersection of the script event with the GC collector interval, not a measured GC pause.", "ms"},
	}},
	{[]string{"rootcause_GCOverlap"}, map[string]leafMeaning{
		"attribution": {"Collector-overlap limitation explaining that no main-thread pause, allocation owner or application cause is established.", ""},
	}},
	{[]string{"insights_WireWindow"}, map[string]leafMeaning{
		"kind":  {"Observed timeline window class separating maximal long tasks, work-bound slow frames, input stalls and clustered event bursts.", ""},
		"label": {"Human-readable event or interval label for this observed timeline window.", ""},
	}},
	{[]string{"rootcause_DepthOutlier"}, map[string]leafMeaning{
		"depth":              {"Complete root-to-leaf frame count of this exact sampled stack.", "frames"},
		"repeatedFrameDepth": {"Largest occurrence count of any exact generated function in this complete stack.", "count"},
		"commitDepth":        {"React commit-related frame count in this exact stack, not measured component nesting.", "frames"},
	}},
	{[]string{"rootcause_DepthStats"}, map[string]leafMeaning{
		"median":                {"Interpolated median complete root-to-leaf depth across sample observations; each observation counts once regardless of its weight.", "frames"},
		"p95":                   {"Interpolated 95th percentile complete stack depth across observations, not weighted by statistical sample duration.", "frames"},
		"p99":                   {"Interpolated 99th percentile complete stack depth across observations, not weighted by statistical sample duration.", "frames"},
		"max":                   {"Maximum complete root-to-leaf frame count among included weighted samples.", "frames"},
		"outlierThreshold":      {"Complete stack depth threshold used to select unusually deep exact sampled stacks.", "frames"},
		"maxRepeatedFrameDepth": {"Maximum occurrence count of one exact generated function in any included stack.", "count"},
		"maxCommitDepth":        {"Maximum captured React commit-related frame count in any included stack.", "frames"},
	}},
	{[]string{"audit_SceneResult"}, map[string]leafMeaning{
		"name":          {"Scene identifier grouping repeated captures of the same interaction or workload.", ""},
		"runs":          {"Successfully parsed analysis runs contributing to this scene's median metrics; excludes missing runs.", "count"},
		"attemptedRuns": {"Requested scene run count, including failed or unavailable analysis reports.", "count"},
		"missingRuns":   {"Requested scene runs lacking a usable analysis report; preserve incomplete evidence explicitly.", "count"},
		"durationSec":   {"Median selected active clock duration across this scene's parsed runs, excluding recorder pauses.", "s"},
		"topFinding":    {"Highest-impact finding title from the scene's representative analysis, not a replication count.", ""},
	}},
	{[]string{"audit_Priority"}, map[string]leafMeaning{
		"scenes":             {"Scene identifiers whose actual analyses include this detector finding.", ""},
		"totalImpactMs":      {"Sum of per-scene representative finding cost; related causal facets can overlap and must not be added.", "ms"},
		"avgImpactPct":       {"Mean finding impact percentage over every scene, counting absent findings as zero.", "%"},
		"rank":               {"Cross-scene breadth-weighted impact sort score; not a physical duration, confidence or probability.", "score"},
		"evidenceScene":      {"Scene identifier supplying the selected highest-impact representative evidence.", ""},
		"evidenceSceneIndex": {"Zero-based audit scenes index scoping this priority's frame and exact stack references.", "index"},
	}},
	{[]string{"audit_WireRepeatedFrame"}, map[string]leafMeaning{
		"id":    {"Stable generated f_ selector resolving in scenes[evidenceSceneIndex].analysis.frames.", ""},
		"count": {"Occurrences of the same exact generated function in the representative supporting stack.", "count"},
	}},
	{[]string{"audit_HotFunction"}, map[string]leafMeaning{
		"id":                 {"Stable generated f_ function selector shared across scene analyses and resolved in the evidence scene catalog.", ""},
		"commonStackScene":   {"Scene identifier whose representative exact stack supplies this hotspot evidence.", ""},
		"evidenceSceneIndex": {"Zero-based audit scenes index scoping this hotspot's function and exact stack references.", "index"},
		"selfMs":             {"Sum of per-scene median self-sample weights for this generated function.", "ms"},
		"pct":                {"Percentage of total included statistical self-sample weight across all scenes charged to this function.", "%"},
		"sceneCount":         {"Number of scenes whose parsed evidence includes this generated function.", "count"},
		"scenes":             {"Scene identifiers whose parsed evidence includes this generated function.", ""},
	}},
	{[]string{"audit_Report"}, map[string]leafMeaning{
		"worstByStandard": {"Map from scorecard standard ID to the scene identifier with the worst available value.", ""},
	}},
	{[]string{"metrics_Metric"}, map[string]leafMeaning{
		"key":       {"Stable dotted scalar key used to reproduce this exact metric.", ""},
		"group":     {"Performance domain grouping the flat scalar metric, such as frames, script, layout, CPU or memory.", ""},
		"label":     {"Human-readable name for the scalar metric, including any metric-specific caveat.", ""},
		"dir":       {"Improvement direction: zero means lower is better, one means higher is better, two means neutral information.", "enum"},
		"value":     {"Exact scalar magnitude expressed in the sibling unit; duration normalization applies only where the comparison declares it.", "unit"},
		"extensive": {"True when this total or count scales with capture activity and may be normalized per active second in comparisons.", ""},
	}},
	{[]string{"compare_Side"}, map[string]leafMeaning{
		"label": {"Human-readable comparison side label, normally baseline or candidate.", ""},
		"runs":  {"Successfully summarized capture count used to estimate this side's metric median and noise.", "count"},
	}},
	{[]string{"compare_MetricDiff"}, map[string]leafMeaning{
		"base":       {"Baseline run median in metric.unit; extensive metrics use a per-active-second rate when comparability.normalized is true.", "metric.unit"},
		"cand":       {"Candidate run median in metric.unit; extensive metrics use a per-active-second rate when comparability.normalized is true.", "metric.unit"},
		"baseRuns":   {"Baseline per-run values used for the median and noise check, with the same normalization as base.", "metric.unit"},
		"candRuns":   {"Candidate per-run values used for the median and noise check, with the same normalization as cand.", "metric.unit"},
		"delta":      {"Candidate median minus baseline median, using the same metric unit and declared per-second normalization.", "metric.unit"},
		"pct":        {"Relative median delta divided by baseline median, or zero when the baseline is zero; 0.1 means ten percent.", "ratio"},
		"verdict":    {"Directional threshold and run-noise classification for this scalar change.", ""},
		"confidence": {"Comparison noise confidence, not finding evidence confidence: single-run means variance unavailable; low means baseline and candidate run ranges overlap; high means ranges separate.", ""},
	}},
	{[]string{"compare_FuncDiff"}, map[string]leafMeaning{
		"commonStackID":     {"Canonical n_ selector for this function's representative exact sampled stack.", ""},
		"commonStackSource": {"Recording path containing the representative exact stack; selectors are scoped to this source.", ""},
		"baseSelfMs":        {"Baseline median function self-sample weight; extensive normalization uses a per-active-second rate when enabled.", "ms or ms/s when normalized"},
		"candSelfMs":        {"Candidate median function self-sample weight; extensive normalization uses a per-active-second rate when enabled.", "ms or ms/s when normalized"},
		"baseTotalMs":       {"Baseline median inclusive function sample weight, counting recursion once per sample; per-second when normalized.", "ms or ms/s when normalized"},
		"candTotalMs":       {"Candidate median inclusive function sample weight, counting recursion once per sample; per-second when normalized.", "ms or ms/s when normalized"},
		"deltaSelfMs":       {"Candidate minus baseline median self-sample weight, using the same declared normalization as both sides.", "ms or ms/s when normalized"},
		"pct":               {"Relative self-time delta divided by baseline self time, or zero when baseline is zero; 0.1 means ten percent.", "ratio"},
		"verdict":           {"Directional threshold classification of this function's median self-time change.", ""},
	}},
	{[]string{"compare_Counts"}, map[string]leafMeaning{
		"improved":  {"Number of scalar metric comparisons classified as improved; excludes function-only change lists.", "count"},
		"regressed": {"Number of scalar metric comparisons classified as regressed; excludes function-only change lists.", "count"},
		"unchanged": {"Number of scalar metric comparisons below change thresholds; excludes informational and newly present metrics.", "count"},
		"noisy":     {"Number of scalar metric comparisons whose observed change lies within repeated-run noise.", "count"},
	}},
	{[]string{"compare_Comparability"}, map[string]leafMeaning{
		"score":      {"Fixture similarity score from zero through one after explicit comparability penalties; not performance quality or finding confidence.", "ratio"},
		"normalized": {"True when extensive metric totals and counts were compared as rates per active second.", ""},
	}},
	{[]string{"compare_Warning", "flame_DiffWarning"}, map[string]leafMeaning{
		"level":   {"Comparability notice severity: warning indicates a substantive limitation; info records normalization context.", ""},
		"code":    {"Stable comparability condition identifier, separate from command failure diagnostic codes.", ""},
		"message": {"Human-readable comparison limitation and any normalization performed or unavailable.", ""},
	}},
	{[]string{"scorecard_Result"}, map[string]leafMeaning{
		"id":           {"Stable performance standard identifier used to match results and gap targets.", ""},
		"name":         {"Human-readable performance standard name, not a recording or function identity.", ""},
		"source":       {"Published benchmark or explicit wkperf heuristic from which this standard is adapted.", ""},
		"good":         {"Good-rating threshold in the sibling unit; interpretation reverses when higherBetter is true.", "unit"},
		"poor":         {"Poor-rating threshold in the sibling unit; interpretation reverses when higherBetter is true.", "unit"},
		"higherBetter": {"True when larger observed values improve this standard, such as frames per second.", ""},
		"weight":       {"Relative contribution of this standard to available-weight score and total standard coverage.", "weight"},
		"note":         {"Derivation, observation requirements and differences from the original published performance standard.", ""},
		"value":        {"Observed or aggregated standard magnitude in the sibling unit; rating n/a means evidence is unavailable.", "unit"},
		"rating":       {"Threshold-based rating, with n/a explicitly retaining missing or insufficient observation.", ""},
		"score":        {"Numeric score from zero through one hundred derived from this standard's thresholds; unavailable results do not contribute.", "score/100"},
		"detail":       {"Specific observation limitation or derivation explanation for this evaluated standard.", ""},
	}},
	{[]string{"scorecard_Scorecard"}, map[string]leafMeaning{
		"score":    {"Available-weight mean numeric score from zero through one hundred; partial scores are not overall health grades.", "score/100"},
		"grade":    {"Overall letter grade A, B, C, D or F; n/a when coverage or aggregate evidence cannot support a grade.", ""},
		"coverage": {"Available standard weight divided by total standard weight; aggregates average child coverage.", "ratio"},
		"partial":  {"True when observation coverage, missing frame standards or an ungraded aggregate child prevents an overall grade.", ""},
	}},
	{[]string{"scorecard_GapItem"}, map[string]leafMeaning{
		"id":     {"Performance standard ID identifying this gap's unit and good threshold.", ""},
		"name":   {"Human-readable performance standard name for this available non-good result.", ""},
		"value":  {"Observed value in the unit of the standard identified by id.", "standard(id).unit"},
		"target": {"Good threshold in the unit of the standard identified by id.", "standard(id).unit"},
		"rating": {"Current threshold rating for this available result that has not reached good.", ""},
		"factor": {"Distance as value divided by target, reversed for higher-better standards; dimensionless and not a percentage.", "ratio"},
	}},
	{[]string{"report_flame_Document"}, map[string]leafMeaning{
		"mode": {"Profile contains raw sampled trees; diff also contains baseline, candidate and normalized comparison trees.", ""},
	}},
	{[]string{"report_flame_Options"}, map[string]leafMeaning{
		"title":  {"Optional presentation title; it does not change captured evidence or function identity.", ""},
		"invert": {"Visual leaf-first layout option only; exported nodes and samples remain canonical root-to-leaf evidence.", ""},
		"minMs":  {"Minimum visual context weight to draw; exported evidence is never filtered by this display threshold.", "ms"},
	}},
	{[]string{"report_flame_WireChart"}, map[string]leafMeaning{
		"label":       {"Human-readable chart source or comparison label; chart frame indices resolve in the document catalog.", ""},
		"empty":       {"True when selected chart inputs contain no included positive statistical sample weight.", ""},
		"emptyReason": {"Explanation of valid selection lacking sampled evidence, distinct from proof of no CPU work.", ""},
	}},
	{[]string{"report_flame_WireTree"}, map[string]leafMeaning{
		"root":             {"Zero-based index of the synthetic root in this tree's flat nodes array.", "index"},
		"startTime":        {"Inclusive selected wall-clock window start in recording-relative seconds.", "s"},
		"endTime":          {"Exclusive selected wall-clock window end in recording-relative seconds.", "s"},
		"activeDurationMs": {"Selected active clock duration excluding recorder pauses; not the sum of statistical sample weights.", "ms"},
		"untimedIncluded":  {"True when whole-recording sample observations without timestamps contribute to this aggregate.", ""},
	}},
	{[]string{"report_flame_WireNode", "report_flame_WireNodeDelta"}, map[string]leafMeaning{
		"id":     {"Stable n_ exact calling-context selector, or root for the synthetic context.", ""},
		"parent": {"Zero-based parent index in this tree's nodes array; -1 identifies only the synthetic root.", "index"},
		"frame":  {"Zero-based document frame index; -1 identifies only the synthetic root, not a generated function.", "index"},
	}},
	{[]string{"report_flame_WireSample", "report_flame_WireSampleRef"}, map[string]leafMeaning{
		"path":     {"Terminal n_ context ID referencing one complete exact canonical sampled stack.", ""},
		"w":        {"Original positive statistical sample weight in milliseconds, not an occupied elapsed-time span.", "ms"},
		"t":        {"Usable recording-relative timestamp in seconds; untimed observations never receive this field.", "s"},
		"targetID": {"Captured target identifier attached to this profiler observation, when available.", ""},
	}},
	{[]string{"report_flame_WireNodeDelta", "report_flame_WireFunctionDelta"}, map[string]leafMeaning{
		"selfMs":  {"Normalized candidate-minus-baseline self-sample delta in comparison.normalization.unit; raw side weights remain milliseconds.", "comparison.normalization.unit"},
		"totalMs": {"Normalized candidate-minus-baseline inclusive sample delta in comparison.normalization.unit; raw side weights remain milliseconds.", "comparison.normalization.unit"},
		"samples": {"Unnormalized candidate-minus-baseline profiler observation count; negative counts represent fewer candidate samples.", "count"},
	}},
	{[]string{"report_flame_WireFunctionDelta"}, map[string]leafMeaning{
		"id":    {"Stable generated f_ selector shared by this comparison and the document frame catalog.", ""},
		"frame": {"Zero-based document frame index for the function compared across all its calling contexts.", "index"},
	}},
	{[]string{"report_flame_WireComparison"}, map[string]leafMeaning{
		"root":    {"Zero-based synthetic root index in this comparison's flat nodes array.", "index"},
		"totalMs": {"Normalized candidate-minus-baseline root sample-weight delta in normalization.unit; raw totals remain milliseconds.", "normalization.unit"},
	}},
	{[]string{"flame_DiffNormalization"}, map[string]leafMeaning{
		"requestedMode": {"Requested normalization policy: auto chooses per-second only for an active-duration mismatch above ten percent.", ""},
		"mode":          {"Resolved delta normalization policy after checking required positive duration or sample-weight denominators.", ""},
		"unit":          {"Resolved delta unit: milliseconds for none, milliseconds per active second, or percentage-point sample-share difference.", ""},
	}},
	{[]string{"report_flame_Speedscope"}, map[string]leafMeaning{
		"name":               {"Human-readable title of the complete speedscope export.", ""},
		"exporter":           {"Name of the program producing this speedscope document.", ""},
		"activeProfileIndex": {"Zero-based profiles index initially selected by a speedscope viewer.", "index"},
	}},
	{[]string{"report_flame_SpeedscopeFrame"}, map[string]leafMeaning{
		"name": {"Display label retaining original and generated identity where mapping exists.", ""},
		"file": {"Original mapped source URL when available, otherwise the captured generated URL.", ""},
		"line": {"Displayed 1-based source line; native or unknown locations may retain zero or -1 sentinels.", "line"},
		"col":  {"Displayed 1-based source column; native or unknown locations may retain zero or -1 sentinels.", "column"},
	}},
	{[]string{"report_flame_SpeedscopeProfile"}, map[string]leafMeaning{
		"type":       {"Speedscope sampled-profile representation; every sample references the shared frame catalog.", ""},
		"name":       {"Recording or comparison-side label for this individual speedscope profile.", ""},
		"unit":       {"Speedscope sample weight unit; this exporter always uses milliseconds.", ""},
		"startValue": {"Initial cumulative statistical sample weight, always zero for this exporter.", "ms"},
		"endValue":   {"Total included statistical sample weight, not selected elapsed recording duration.", "ms"},
		"samples":    {"Exact root-to-leaf stacks of zero-based shared frame indices; statistical observations may be timed or untimed.", "index"},
		"weights":    {"Original statistical millisecond weight for each corresponding sample stack.", "ms"},
	}},
}

var contextMeanings = buildContextMeanings()

func buildContextMeanings() map[string]map[string]leafMeaning {
	contexts := make(map[string]map[string]leafMeaning)
	for _, group := range meaningGroups {
		for _, name := range group.definitions {
			if contexts[name] == nil {
				contexts[name] = make(map[string]leafMeaning)
			}
			for field, meaning := range group.fields {
				contexts[name][field] = meaning
			}
		}
	}
	return contexts
}

func applySemantics(definitions map[string]any) {
	for name, raw := range definitions {
		properties, _ := raw.(map[string]any)["properties"].(map[string]any)
		for field, rawProperty := range properties {
			property := rawProperty.(map[string]any)
			meaning := sharedMeanings[field]
			if specific, ok := contextMeanings[name][field]; ok {
				meaning = specific
			}
			if description, ok := property["description"].(string); ok {
				meaning.description = description
			}
			applyMeaning(property, meaning)
		}
	}
	applyFieldVocabularies(definitions)
}

// Nullable wrappers and primitive array/map elements carry the field's exact
// semantics too. Referenced objects retain their own context-specific catalog.
func applyMeaning(spec map[string]any, meaning leafMeaning) {
	if meaning.description != "" {
		spec["description"] = meaning.description
	}
	if meaning.unit != "" {
		spec["x-unit"] = meaning.unit
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if child, ok := spec[key].(map[string]any); ok {
			applyMeaning(child, meaning)
		}
	}
	if alternatives, ok := spec["anyOf"].([]any); ok {
		for _, raw := range alternatives {
			child := raw.(map[string]any)
			if child["type"] != "null" {
				applyMeaning(child, meaning)
			}
		}
	}
}

func applyFieldVocabularies(definitions map[string]any) {
	for _, entry := range []struct {
		definitions []string
		field       string
		values      []string
	}{
		{[]string{"schema_Analysis", "insights_Report"}, "moduleGrouping", []string{"file", "parent-directory"}},
		{[]string{"flame_Frame", "rootcause_Owner"}, "category", []string{"app", "framework", "native", "gc", "unknown-bundled", "injected"}},
		{[]string{"insights_WireFinding", "audit_Priority"}, "category", []string{"frames", "script", "layout", "paint", "gc", "memory", "cpu", "network"}},
		{[]string{"insights_WireFinding", "audit_Priority"}, "confidence", []string{"low", "medium", "high"}},
		{[]string{"insights_WireFinding", "audit_Priority"}, "evidenceBasis", []string{"measured", "sampled", "hypothesis"}},
		{[]string{"compare_MetricDiff"}, "confidence", []string{"single-run", "low", "high"}},
		{[]string{"insights_WireRootFinding"}, "severity", []string{"high", "medium", "low", "info"}},
		{[]string{"insights_WireWindow"}, "kind", []string{"long-task", "slow-frame", "input-stall", "event-burst"}},
		{[]string{"insights_WirePeriodic"}, "kind", []string{"raf", "input", "background", "unknown"}},
		{[]string{"insights_WireTrigger"}, "kind", []string{"dispatch", "timer", "rAF", "microtask", "observer", "rendering"}},
		{[]string{"insights_WirePath"}, "stopReason", []string{"no-samples", "min-share", "sample-support", "near-tie", "leaf"}},
		{[]string{"insights_Verification"}, "direction", []string{"increase", "decrease"}},
		{[]string{"rootcause_NativeRead"}, "flush", []string{"style", "layout"}},
		{[]string{"scorecard_Scorecard"}, "grade", []string{"A", "B", "C", "D", "F", "n/a"}},
		{[]string{"compare_Warning", "flame_DiffWarning"}, "level", []string{"warn", "info"}},
		{[]string{"compare_Warning"}, "code", []string{"window-empty", "duration-mismatch", "frame-count-mismatch", "url-mismatch", "instrument-mismatch", "workload-mismatch", "sample-sparse", "run-variance"}},
		{[]string{"flame_DiffWarning"}, "code", []string{"normalization-unavailable", "duration-unavailable", "duration-mismatch", "sample-sparse"}},
		{[]string{"report_flame_SpeedscopeProfile"}, "type", []string{"sampled"}},
		{[]string{"report_flame_SpeedscopeProfile"}, "unit", []string{"milliseconds"}},
	} {
		for _, name := range entry.definitions {
			if raw, ok := definitions[name]; ok {
				properties := raw.(map[string]any)["properties"].(map[string]any)
				if property, ok := properties[entry.field].(map[string]any); ok {
					property["enum"] = entry.values
				}
			}
		}
	}
	metric := definitions["metrics_Metric"].(map[string]any)["properties"].(map[string]any)
	metric["dir"].(map[string]any)["enum"] = []int{0, 1, 2}
}
