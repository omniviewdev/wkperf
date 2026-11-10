package insights

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

// CatalogStack retains one exact stack as indices into the report's frame table.
type CatalogStack struct {
	Frames []int `json:"frames"`
	Depth  int   `json:"depth"`
}

// EvidenceCatalog stores generated function identities and exact stacks once.
// Stack keys are the same n_ selectors accepted by the stacks query operation.
type EvidenceCatalog struct {
	Frames []flame.Frame           `json:"frames"`
	Stacks map[string]CatalogStack `json:"stacks"`
}

// SelectEvidence gives every serializer and renderer the same top-N evidence.
// The returned shallow copy retains the full Summary and numeric observations.
func SelectEvidence(report *Report) *Report {
	if report == nil {
		return nil
	}
	r := *report
	top := r.Top
	if top <= 0 {
		top = 10
	}
	if r.MoreEvidenceCommand == "" {
		r.MoreEvidenceCommand = moreEvidenceCommand(report, top)
	}
	r.RootCause = rootcause.SelectEvidence(r.RootCause, top)
	r.Findings = append([]Finding{}, r.Findings...)
	for i := range r.Findings {
		f := &r.Findings[i]
		f.StacksTotal = max(f.StacksTotal, max(len(f.Stacks), len(f.StackIDs)))
		f.Stacks = f.Stacks[:min(len(f.Stacks), top)]
		if len(f.Stacks) > 0 {
			f.StackIDs = stackIDs(f.Stacks)
		} else {
			f.StackIDs = f.StackIDs[:min(len(f.StackIDs), top)]
			if f.StackIDs == nil {
				f.StackIDs = []string{}
			}
		}
	}
	return &r
}

// Catalog returns the selected report's deterministic, exact evidence tables.
func Catalog(report *Report) EvidenceCatalog {
	return newCatalog(SelectEvidence(report)).EvidenceCatalog
}

type catalogBuilder struct {
	EvidenceCatalog
	indices     map[string]int
	paths       map[string][]flame.Frame
	metricPaths map[string][]metrics.FunctionFrame
	frames      map[string]flame.Frame
}

func newCatalog(r *Report) *catalogBuilder {
	c := &catalogBuilder{EvidenceCatalog: EvidenceCatalog{Stacks: map[string]CatalogStack{}}, indices: map[string]int{}, paths: map[string][]flame.Frame{}, metricPaths: map[string][]metrics.FunctionFrame{}, frames: map[string]flame.Frame{}}
	if r != nil {
		c.functions(r.Hotspots)
		c.functions(r.InclusiveHotspots)
		for _, stack := range r.CommonStacks {
			c.metricStack(stack.ID, stack.Frames)
		}
		for _, group := range [][]TriggerStat{r.Triggers, r.Wakeups.Causes} {
			for _, trigger := range group {
				c.functions(trigger.TopSelf)
				c.functions(trigger.TopInclusive)
			}
		}
		for _, f := range r.Findings {
			c.stacks(f.Stacks)
		}
		if root := r.RootCause; root != nil {
			c.path(root.HotPath.Frames)
			for _, attribution := range root.AppAttribution {
				c.frame(attribution.App)
				c.stacks(attribution.Stacks)
			}
			for _, f := range root.Findings {
				c.stacks(f.Evidence)
			}
			for _, periodic := range root.PeriodicRoots {
				c.frame(periodic.Root)
				c.stacks(periodic.Evidence)
			}
			for _, outlier := range root.Depth.Outliers {
				c.stacks([]rootcause.Stack{outlier.Stack})
			}
			for _, windows := range [][]rootcause.Window{root.Windows, root.LongTasks, root.SlowFrames, root.EventBursts} {
				for _, window := range windows {
					if window.HotPath != nil {
						c.path(window.HotPath.Frames)
					}
					if window.TopStack != nil {
						c.stacks([]rootcause.Stack{*window.TopStack})
					}
				}
			}
		}
	}
	c.Frames = make([]flame.Frame, 0, len(c.frames))
	for _, frame := range c.frames {
		c.Frames = append(c.Frames, frame)
	}
	slices.SortFunc(c.Frames, compareCatalogFrame)
	for i, frame := range c.Frames {
		c.indices[frame.ID] = i
	}
	for id, frames := range c.paths {
		c.Stacks[id] = CatalogStack{Frames: c.frameIndices(frames), Depth: len(frames)}
	}
	for id, frames := range c.metricPaths {
		indices := make([]int, len(frames))
		for i, frame := range frames {
			indices[i] = c.indices[flame.FunctionID(metricFrame(frame))]
		}
		c.Stacks[id] = CatalogStack{Frames: indices, Depth: len(frames)}
	}
	return c
}

func (c *catalogBuilder) frame(frame flame.Frame) {
	frame.ID = flame.FunctionID(frame)
	if current, exists := c.frames[frame.ID]; !exists || compareCatalogFrame(frame, current) < 0 {
		c.frames[frame.ID] = frame
	}
}

func (c *catalogBuilder) path(frames []flame.Frame) {
	for _, frame := range frames {
		c.frame(frame)
	}
}

func (c *catalogBuilder) stack(id string, frames []flame.Frame) {
	if len(frames) == 0 {
		return
	}
	if id == "" {
		id = flame.StackID(frames)
	}
	c.path(frames)
	if _, exists := c.metricPaths[id]; exists {
		return
	}
	if _, exists := c.paths[id]; exists {
		return
	}
	c.paths[id] = frames
}

func (c *catalogBuilder) stacks(stacks []rootcause.Stack) {
	for _, stack := range stacks {
		c.stack(stack.StackID, stack.Frames)
	}
}

func (c *catalogBuilder) functions(functions []metrics.FuncStat) {
	for _, function := range functions {
		c.frame(metricFrame(function.Frame()))
		c.metricStack(function.CommonStackID, function.CommonStack)
		for _, group := range [][]metrics.CallEdge{function.Callers, function.Callees} {
			for _, edge := range group {
				c.frame(metricFrame(edge.Function))
			}
		}
	}
}

func (c *catalogBuilder) frameIndices(frames []flame.Frame) []int {
	indices := make([]int, len(frames))
	for i, frame := range frames {
		indices[i] = c.indices[flame.FunctionID(frame)]
	}
	return indices
}

func metricFrame(frame metrics.FunctionFrame) flame.Frame {
	f := flame.Frame{ID: frame.ID, Name: frame.Name, URL: frame.URL, Line: frame.Line, Column: frame.Column, SourceID: frame.SourceID, Native: frame.URL == "", Anonymous: frame.Name == ""}
	if frame.Original != nil {
		f.Original = (*flame.Location)(frame.Original)
	}
	f.Category = flame.Categorize(f)
	return f
}

func (c *catalogBuilder) metricStack(id string, frames []metrics.FunctionFrame) {
	if len(frames) == 0 {
		return
	}
	if id == "" {
		id = MetricStackID(frames)
	}
	for _, frame := range frames {
		c.frame(metricFrame(frame))
	}
	if _, exists := c.paths[id]; exists {
		return
	}
	if _, exists := c.metricPaths[id]; !exists {
		c.metricPaths[id] = frames
	}
}

func stackIDs(stacks []rootcause.Stack) []string {
	out := make([]string, len(stacks))
	for i, stack := range stacks {
		out[i] = stack.StackID
		if out[i] == "" {
			out[i] = flame.StackID(stack.Frames)
		}
	}
	return out
}

func compareCatalogFrame(a, b flame.Frame) int {
	for _, order := range [...]int{cmp.Compare(a.Name, b.Name), cmp.Compare(a.URL, b.URL), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column), cmp.Compare(a.SourceID, b.SourceID)} {
		if order != 0 {
			return order
		}
	}
	if a.Original == nil && b.Original != nil {
		return -1
	}
	if a.Original != nil && b.Original == nil {
		return 1
	}
	if a.Original != nil {
		for _, order := range [...]int{cmp.Compare(a.Original.Name, b.Original.Name), cmp.Compare(a.Original.URL, b.Original.URL), cmp.Compare(a.Original.Line, b.Original.Line), cmp.Compare(a.Original.Column, b.Original.Column)} {
			if order != 0 {
				return order
			}
		}
	}
	if order := cmp.Compare(a.Category, b.Category); order != 0 {
		return order
	}
	return cmp.Compare(a.ID, b.ID)
}

func moreEvidenceCommand(r *Report, top int) string {
	if r.Source == "" {
		return ""
	}
	required := top
	for _, finding := range r.Findings {
		required = max(required, finding.StacksTotal, len(finding.Stacks), len(finding.StackIDs))
	}
	if root := r.RootCause; root != nil {
		required = max(required, root.AppAttributionTotal, len(root.AppAttribution),
			root.PeriodicRootsTotal, len(root.PeriodicRoots), root.GCOverlapsTotal, len(root.GCOverlaps),
			root.WindowsTotal, len(root.Windows), root.LongTasksTotal, len(root.LongTasks),
			root.SlowFramesTotal, len(root.SlowFrames), root.EventBurstsTotal, len(root.EventBursts),
			root.Depth.OutliersTotal, len(root.Depth.Outliers))
		required = max(required, max(root.LongTasksTotal, len(root.LongTasks))+max(root.SlowFramesTotal, len(root.SlowFrames))+max(root.EventBurstsTotal, len(root.EventBursts)))
		for _, attribution := range root.AppAttribution {
			required = max(required, attribution.StacksTotal, len(attribution.Stacks))
		}
		for _, finding := range root.Findings {
			required = max(required, finding.EvidenceTotal, len(finding.Evidence), finding.WindowsTotal, len(finding.Windows))
		}
		for _, periodic := range root.PeriodicRoots {
			required = max(required, periodic.FiresTotal, len(periodic.Fires), periodic.EvidenceTotal, len(periodic.Evidence))
		}
		for _, gc := range root.GCOverlaps {
			required = max(required, gc.EventsTotal, len(gc.Events))
		}
	}
	if required <= top {
		return ""
	}
	command := "wkperf analyze " + shellQuote(r.Source) + " --top " + strconv.Itoa(required) + " --format json" + queryFlags(r)
	if r.JankMs > 0 && r.JankMs != 16.7 {
		command += " --jank-ms " + seconds(r.JankMs)
	}
	if r.LongTaskMs > 0 && r.LongTaskMs != 50 {
		command += " --long-task-ms " + seconds(r.LongTaskMs)
	}
	if r.ModuleGrouping != "" && r.ModuleGrouping != "file" {
		command += " --module-grouping " + shellQuote(r.ModuleGrouping)
	}
	return command
}
