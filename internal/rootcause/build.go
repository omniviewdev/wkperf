package rootcause

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

const sampledBasis = "duration-weighted sampler estimate"
const timelineBasis = "instrumented timeline interval"

// Build uses a supplied mapped tree, or builds a canonical tree when tree is nil.
func Build(rec *model.Recording, tree *flame.Tree, o Options) *Report {
	r := &Report{SchemaVersion: "2", Source: o.Source}
	if rec == nil {
		return r
	}
	o = defaults(o)
	r.Top = o.Top
	if rec.SampleEvidence != nil {
		o.OffsetSec = rec.StartTime - rec.SampleEvidence.StartTime
	}
	from := math.Max(0, math.Max(o.FromSec, o.TrimStartSec))
	to := math.Max(0, rec.EndTime-rec.StartTime-math.Max(0, o.TrimEndSec))
	if o.ToSec > 0 {
		to = math.Min(to, o.ToSec)
	}
	from = math.Min(from, math.Max(0, rec.EndTime-rec.StartTime))
	if to < from {
		to = from
	}
	from, to = seconds(from), seconds(to)
	if tree == nil {
		tree = flame.Build(rec, flame.Options{Ownership: o.Ownership})
	}
	o.Ownership = tree.Ownership
	r.Ownership = flame.OwnershipRules{
		AppURLs:     append([]string(nil), o.Ownership.AppURLs...),
		LibraryURLs: append([]string(nil), o.Ownership.LibraryURLs...),
	}
	from = seconds(math.Max(from, tree.StartTime))
	to = seconds(math.Min(to, tree.EndTime))
	if to < from {
		to = from
	}
	if !profile.CoversRecording(rec, from, to) {
		tree = tree.Window(from, to)
	}
	b := builder{rec: rec, tree: tree, o: o, from: from, to: to}
	r.FromSec, r.ToSec = b.exportTime(from), b.exportTime(to)
	r.SourceMapPaths = append([]string(nil), o.Sourcemaps...)
	b.pathIDs = make(map[string]string)
	b.pathKeys = make(map[string]string)
	b.pathByID = make(map[string]flame.Path)
	b.paths = tree.Paths()
	for _, p := range b.paths {
		key := stackKey(p.Frames)
		b.pathIDs[key] = p.ID
		b.pathKeys[p.ID] = key
		b.pathByID[p.ID] = p
	}
	r.Commands = Commands{
		HotPath: b.query("hot-path --min-share 0.05", from, to), Window: b.query("hot-path --min-share 0.05", from, to),
		Depth: b.query("depth", from, to), Stacks: b.query("stacks", from, to),
		Owners: b.query("rootcause", from, to), GC: b.query("rootcause", from, to),
	}
	functions := tree.Functions()
	if len(functions) > 0 {
		r.Commands.Paths = b.query("paths "+quote(flame.FunctionID(functions[0].Frame)), from, to)
	} else {
		r.Commands.Paths = b.reproduce("samples.count")
		r.Commands.Depth = b.reproduce("samples.count")
		r.Commands.Stacks = b.reproduce("samples.count")
	}
	r.HotPath = b.hotPath(tree, "hot_path", from, to)
	r.Commands.HotPath = r.HotPath.Command
	r.Commands.Window = r.HotPath.Command
	r.Owners, r.ContextAttribution, r.ContextUnavailable = b.ownership()
	b.indexSamples()
	r.Findings = b.stackFindings()
	r.PeriodicRoots = b.periodicRoots()
	totalMs := tree.Root.TotalMs
	if !tree.UntimedIncluded {
		totalMs += tree.UntimedTotal.Ms
	}
	note := UntimedAttributionNote(tree.UntimedTotal.Ms, totalMs)
	for i := range r.Findings {
		if r.Findings[i].ID == "react-sync-in-trigger" || r.Findings[i].ID == "react-sync" || r.Findings[i].ID == "forced-style-or-layout-read" {
			if note != "" {
				r.Findings[i].Explanation += " " + note
			}
		}
	}
	for i := range r.PeriodicRoots {
		r.PeriodicRoots[i].SampleStatus = note
	}
	r.Depth = b.depth()
	r.Findings = append(r.Findings, b.depthFindings(r.Depth)...)
	r.GCOverlaps = b.gcOverlaps()
	r.LongTasks, r.SlowFrames, r.InputStalls, r.EventBursts, r.Windows = b.windows()
	sampledMs := tree.Root.TotalMs
	excludedMs := 0.0
	if !tree.UntimedIncluded {
		excludedMs = tree.UntimedTotal.Ms
	}
	r.Stats = []Stat{
		b.stat("samples.self_ms", "Sampled self time", sampledMs, "ms", sampledBasis),
		b.stat("samples.count", "Retained profiler samples", float64(tree.SampleCount()), "count", sampledBasis),
		b.stat("samples.untimed_count", "Profiler samples without timestamps", float64(tree.UntimedTotal.Count), "count", sampledBasis),
		b.stat("samples.untimed_ms", "Sample weight without timestamps", tree.UntimedTotal.Ms, "ms", sampledBasis),
		b.stat("samples.untimed_excluded_ms", "Untimed sample weight excluded from this window", excludedMs, "ms", sampledBasis),
		b.stat("window.duration_ms", "Selected wall duration", (to-from)*1000, "ms", timelineBasis),
		b.stat("window.active_ms", "Active wall duration", profile.ActiveDuration(rec, rec.StartTime+from, rec.StartTime+to)*1000, "ms", timelineBasis),
		b.stat("window.from_sec", "Selected window start", r.FromSec, "s", timelineBasis),
		b.stat("window.to_sec", "Selected window end", r.ToSec, "s", timelineBasis),
		b.stat("triage.long_tasks", "Retained long tasks", float64(len(r.LongTasks)), "count", timelineBasis),
		b.stat("triage.slow_frames", "Active slow frames", float64(len(r.SlowFrames)), "count", timelineBasis),
		b.stat("triage.input_stalls", "Input-driven rendering stalls with little measured work", float64(len(r.InputStalls)), "count", timelineBasis),
		b.stat("triage.event_bursts", "Clustered script-event bursts", float64(len(r.EventBursts)), "count", timelineBasis),
	}
	return r
}

type builder struct {
	rec            *model.Recording
	tree           *flame.Tree
	o              Options
	from, to       float64
	pathIDs        map[string]string
	paths          []flame.Path
	pathKeys       map[string]string
	pathByID       map[string]flame.Path
	sampleEvidence map[sampleRef][]string
}

func defaults(o Options) Options {
	if o.Top <= 0 {
		o.Top = 10
	}
	if o.LongTaskMs <= 0 {
		o.LongTaskMs = 50
	}
	if o.JankMs <= 0 {
		o.JankMs = profile.FrameBudgetMs
	}
	o.Sourcemaps = append([]string(nil), o.Sourcemaps...)
	maps := o.Sourcemaps[:0]
	seen := make(map[string]bool, len(o.Sourcemaps))
	for _, path := range o.Sourcemaps {
		if !seen[path] {
			seen[path] = true
			maps = append(maps, path)
		}
	}
	o.Sourcemaps = maps
	return o
}

func number(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*1e6) / 1e6
}

func seconds(v float64) float64 {
	v = math.Round(v*1e9) / 1e9
	if v == 0 {
		return 0
	}
	return v
}
func decimal(v float64) string                 { return strconv.FormatFloat(seconds(v), 'f', -1, 64) }
func quote(s string) string                    { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (b builder) exportTime(t float64) float64 { return seconds(t + b.o.OffsetSec) }

func (b builder) query(op string, from, to float64) string {
	if b.o.Source == "" {
		return ""
	}
	flags := ""
	if strings.HasPrefix(op, "rootcause") {
		if b.o.LongTaskMs != 50 {
			flags += " --long-task-ms " + decimal(b.o.LongTaskMs)
		}
		if b.o.JankMs != 16.7 {
			flags += " --jank-ms " + decimal(b.o.JankMs)
		}
		if b.o.Top != 10 {
			flags += " --top " + strconv.Itoa(b.o.Top)
		}
	}
	for _, path := range b.o.Sourcemaps {
		flags += " --sourcemap " + quote(path)
	}
	for _, pattern := range b.o.Ownership.AppURLs {
		flags += " --app-url " + quote(pattern)
	}
	for _, pattern := range b.o.Ownership.LibraryURLs {
		flags += " --library-url " + quote(pattern)
	}
	window := ""
	if b.o.OffsetSec > 1e-9 || b.rec.UntimedExcluded || !profile.CoversRecording(b.rec, from, to) {
		window = " --from " + decimal(b.exportTime(from)) + " --to " + decimal(b.exportTime(to))
	}
	return "wkperf query " + quote(b.o.Source) + " " + op + window + flags + " --format json"
}

func (b builder) reproduce(key string) string { return b.query("rootcause "+quote(key), b.from, b.to) }
func (b builder) stat(key, label string, value float64, unit, basis string) Stat {
	if unit == "s" {
		value = seconds(value)
	} else {
		value = number(value)
	}
	return Stat{Key: key, Label: label, Value: value, Unit: unit, Basis: basis, Command: b.reproduce(key)}
}

func stackKey(frames []flame.Frame) string {
	h := sha256.New()
	for _, f := range frames {
		h.Write([]byte(flame.FunctionID(f)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

func frameLess(a, b flame.Frame) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.URL != b.URL {
		return a.URL < b.URL
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Column != b.Column {
		return a.Column < b.Column
	}
	return a.SourceID < b.SourceID
}

func framesLess(a, b []flame.Frame) bool {
	for i := range min(len(a), len(b)) {
		if frameLess(a[i], b[i]) {
			return true
		}
		if frameLess(b[i], a[i]) {
			return false
		}
	}
	return len(a) < len(b)
}

func (b builder) stacks(samples []flame.Sample, prefix string) []Stack {
	agg := map[string]*Stack{}
	for _, s := range samples {
		if len(s.Frames) == 0 || s.WeightMs <= 0 {
			continue
		}
		key := stackKey(s.Frames)
		v := agg[key]
		if v == nil {
			v = &Stack{Frames: s.Frames}
			agg[key] = v
		}
		v.SelfMs += s.WeightMs
		v.Samples++
		if s.Untimed {
			v.UntimedSamples++
			continue
		}
		at := b.exportTime(s.Timestamp)
		if v.FirstSec == nil {
			v.FirstSec, v.LastSec = new(float64), new(float64)
			*v.FirstSec, *v.LastSec = at, at
		} else {
			*v.FirstSec = math.Min(*v.FirstSec, at)
			*v.LastSec = math.Max(*v.LastSec, at)
		}
	}
	out := make([]Stack, 0, len(agg))
	for key, v := range agg {
		out = append(out, b.finishStack(*v, prefix, key))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelfMs != out[j].SelfMs {
			return out[i].SelfMs > out[j].SelfMs
		}
		return framesLess(out[i].Frames, out[j].Frames)
	})
	return out
}

func (b builder) finishStack(v Stack, prefix, key string) Stack {
	v.SelfMs = number(v.SelfMs)
	v.StackID = b.pathIDs[key]
	id := prefix + ".stack." + key
	v.Command = b.query("stacks "+quote(b.pathIDs[key]), b.from, b.to)
	v.Stats = []Stat{
		b.stat(id+".self_ms", "Exact stack self time", v.SelfMs, "ms", sampledBasis),
		b.stat(id+".samples", "Exact stack samples", float64(v.Samples), "count", sampledBasis),
		b.stat(id+".untimed_samples", "Exact stack samples without timestamps", float64(v.UntimedSamples), "count", sampledBasis),
	}
	if v.FirstSec != nil && v.LastSec != nil {
		v.Stats = append(v.Stats,
			b.stat(id+".first_sec", "First retained timed sample", *v.FirstSec, "s", sampledBasis),
			b.stat(id+".last_sec", "Last retained timed sample", *v.LastSec, "s", sampledBasis))
	}
	return v
}

func (b builder) hotPath(tree *flame.Tree, key string, from, to float64) Path {
	p := Path{Command: b.query("hot-path --min-share 0.05", from, to)}
	selected := tree.SupportedHotPath(.05, 5, .8)
	p.StopReason = selected.StopReason
	p.BranchesTotal = len(selected.Branches)
	describe := func(node *flame.Node, kind string) PathNode {
		share := 0.0
		if tree.Root.TotalMs > 0 {
			share = node.TotalMs / tree.Root.TotalMs
		}
		id := key + "." + kind + "." + node.ID
		return PathNode{Frame: node.Frame, TotalMs: number(node.TotalMs), SelfMs: number(node.SelfMs), Share: number(share), Samples: node.Samples, Stats: []Stat{
			b.stat(id+".total_ms", "Calling-context inclusive sampled time", node.TotalMs, "ms", sampledBasis),
			b.stat(id+".self_ms", "Calling-context exclusive sampled self time", node.SelfMs, "ms", sampledBasis),
			b.stat(id+".share", "Calling-context inclusive share of selected samples", share, "ratio", sampledBasis),
			b.stat(id+".samples", "Samples supporting this calling context", float64(node.Samples), "count", sampledBasis),
		}}
	}
	for _, node := range selected.Nodes {
		p.Frames = append(p.Frames, node.Frame)
		p.Nodes = append(p.Nodes, describe(node, "node"))
		p.SampledMs = number(node.TotalMs)
		p.Samples = node.Samples
	}
	for _, node := range selected.Branches {
		p.Branches = append(p.Branches, describe(node, "branch"))
	}
	p.Stats = []Stat{
		b.stat(key+".total_ms", "Hot path terminal inclusive sampled time", p.SampledMs, "ms", sampledBasis),
		b.stat(key+".samples", "Hot path terminal supporting samples", float64(p.Samples), "count", sampledBasis),
		b.stat(key+".depth", "Hot path depth", float64(len(p.Frames)), "frames", sampledBasis),
		b.stat(key+".branches", "Competing calling-context branches", float64(len(p.Branches)), "count", sampledBasis),
	}
	if len(p.Nodes) == 0 {
		p.Command = b.reproduce(key + ".total_ms")
	}
	return p
}

func sampleEnd(s flame.Sample) float64 { return s.Timestamp }

func clipSample(s flame.Sample, intervals []flame.Interval) (flame.Sample, bool) {
	for _, w := range intervals {
		if s.Timestamp >= w.Start && s.Timestamp < w.End {
			return s, true
		}
	}
	return flame.Sample{}, false
}

func (b builder) active(from, to float64) []flame.Interval {
	parts := profile.ActiveIntervals(b.rec, b.rec.StartTime+math.Max(from, b.from), b.rec.StartTime+math.Min(to, b.to))
	out := make([]flame.Interval, 0, len(parts))
	for _, p := range parts {
		out = append(out, flame.Interval{Start: seconds(p.Start - b.rec.StartTime), End: seconds(p.End - b.rec.StartTime)})
	}
	return out
}

func eventName(r model.Record) string {
	if len(r.Details) == 0 {
		return r.EventType
	}
	var v any
	if json.Unmarshal(r.Details, &v) != nil {
		return r.EventType
	}
	if s, ok := v.(string); ok {
		if s == "" {
			return r.EventType
		}
		return r.EventType + " " + s
	}
	data, _ := json.Marshal(v)
	return r.EventType + " " + string(data)
}
