package insights

import (
	"encoding/json"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

// ReportFields has no marshaling method; WireReport replaces only evidence fields.
type ReportFields Report

type WireReport struct {
	ReportFields
	EvidenceCatalog
	RootCause         *WireRootReport   `json:"rootCause"`
	Hotspots          []WireFunction    `json:"hotspots"`
	InclusiveHotspots []WireFunction    `json:"inclusiveHotspots"`
	CommonStacks      []WireCommonStack `json:"commonStacks"`
	Triggers          []WireTrigger     `json:"triggers"`
	Wakeups           WireWakeups       `json:"wakeups"`
}

type WireFunction struct {
	ID          string     `json:"id"`
	Frame       int        `json:"frame"`
	SelfMs      float64    `json:"selfMs"`
	TotalMs     float64    `json:"totalMs"`
	SelfSamples int        `json:"selfSamples"`
	Callers     []WireEdge `json:"callers,omitempty"`
	Callees     []WireEdge `json:"callees,omitempty"`
	StackID     string     `json:"stackId"`
}

type WireEdge struct {
	Function int     `json:"function"`
	WeightMs float64 `json:"weightMs"`
	Samples  int     `json:"samples"`
}

type WireCommonStack struct {
	StackID  string  `json:"stackId"`
	WeightMs float64 `json:"weightMs"`
	Samples  int     `json:"samples"`
}

type TriggerFields TriggerStat

type WireTrigger struct {
	TriggerFields
	TopSelf      []WireFunction `json:"topSelf"`
	TopInclusive []WireFunction `json:"topInclusive"`
}

type WireWakeups struct {
	Count     int           `json:"count"`
	PerSecond float64       `json:"perSecond"`
	Causes    []WireTrigger `json:"causes"`
}

type RootReportFields rootcause.Report

type WireRootReport struct {
	RootReportFields
	HotPath        WirePath          `json:"hotPath"`
	AppAttribution []WireAttribution `json:"appAttribution"`
	Findings       []WireRootFinding `json:"findings"`
	PeriodicRoots  []WirePeriodic    `json:"periodicRoots"`
	Windows        []WireWindow      `json:"windows"`
	LongTasks      []WireWindow      `json:"longTasks"`
	SlowFrames     []WireWindow      `json:"slowFrames"`
	InputStalls    []WireWindow      `json:"inputStalls"`
	EventBursts    []WireWindow      `json:"eventBursts"`
}

type PathFields rootcause.Path

type WirePath struct {
	PathFields
	Frames []int `json:"frames"`
}

type AttributionFields rootcause.Attribution

type WireAttribution struct {
	AttributionFields
	App int `json:"app"`
}

type RootFindingFields rootcause.Finding

type WireRootFinding struct {
	RootFindingFields
	StackIDs []string `json:"stackIds"`
}

type PeriodicFields rootcause.PeriodicRoot

type WirePeriodic struct {
	PeriodicFields
	Root int `json:"root"`
}

type WindowFields rootcause.Window

type WireWindow struct {
	WindowFields
	HotPath *WirePath `json:"hotPath"`
}

// ToWire serializes only selected evidence; full analysis stays available internally.
func ToWire(report *Report) *WireReport {
	r := SelectEvidence(report)
	if r == nil {
		return nil
	}
	c := newCatalog(r)
	w := &WireReport{ReportFields: ReportFields(*r), EvidenceCatalog: c.EvidenceCatalog,
		Hotspots: c.wireFunctions(r.Hotspots), InclusiveHotspots: c.wireFunctions(r.InclusiveHotspots),
		CommonStacks: make([]WireCommonStack, len(r.CommonStacks)), Triggers: c.wireTriggers(r.Triggers),
		Wakeups: WireWakeups{Count: r.Wakeups.Count, PerSecond: r.Wakeups.PerSecond, Causes: c.wireTriggers(r.Wakeups.Causes)}}
	for i, stack := range r.CommonStacks {
		w.CommonStacks[i] = WireCommonStack{StackID: stack.ID, WeightMs: stack.WeightMs, Samples: stack.Samples}
	}
	if root := r.RootCause; root != nil {
		w.RootCause = &WireRootReport{RootReportFields: RootReportFields(*root), HotPath: c.wirePath(root.HotPath),
			AppAttribution: make([]WireAttribution, len(root.AppAttribution)), Findings: make([]WireRootFinding, len(root.Findings)),
			PeriodicRoots: make([]WirePeriodic, len(root.PeriodicRoots)), Windows: c.wireWindows(root.Windows),
			LongTasks: c.wireWindows(root.LongTasks), SlowFrames: c.wireWindows(root.SlowFrames),
			InputStalls: c.wireWindows(root.InputStalls), EventBursts: c.wireWindows(root.EventBursts)}
		for i, attribution := range root.AppAttribution {
			w.RootCause.AppAttribution[i] = WireAttribution{AttributionFields: AttributionFields(attribution), App: c.indices[attribution.App.ID]}
		}
		for i, finding := range root.Findings {
			w.RootCause.Findings[i] = WireRootFinding{RootFindingFields: RootFindingFields(finding), StackIDs: stackIDs(finding.Evidence)}
		}
		for i, periodic := range root.PeriodicRoots {
			w.RootCause.PeriodicRoots[i] = WirePeriodic{PeriodicFields: PeriodicFields(periodic), Root: c.indices[periodic.Root.ID]}
		}
	}
	return w
}

func (r *Report) MarshalJSON() ([]byte, error) {
	return json.Marshal(ToWire(r))
}

func (c *catalogBuilder) wirePath(path rootcause.Path) WirePath {
	return WirePath{PathFields: PathFields(path), Frames: c.frameIndices(path.Frames)}
}

func (c *catalogBuilder) wireFunctions(functions []metrics.FuncStat) []WireFunction {
	out := make([]WireFunction, len(functions))
	for i, f := range functions {
		out[i] = WireFunction{ID: f.ID, Frame: c.indices[f.ID], SelfMs: f.SelfMs, TotalMs: f.TotalMs, SelfSamples: f.SelfSamples, StackID: f.CommonStackID,
			Callers: c.wireEdges(f.Callers), Callees: c.wireEdges(f.Callees)}
	}
	return out
}

func (c *catalogBuilder) wireEdges(edges []metrics.CallEdge) []WireEdge {
	out := make([]WireEdge, len(edges))
	for i, edge := range edges {
		out[i] = WireEdge{Function: c.indices[edge.Function.ID], WeightMs: edge.WeightMs, Samples: edge.Samples}
	}
	return out
}

func (c *catalogBuilder) wireTriggers(triggers []TriggerStat) []WireTrigger {
	out := make([]WireTrigger, len(triggers))
	for i, trigger := range triggers {
		out[i] = WireTrigger{TriggerFields: TriggerFields(trigger), TopSelf: c.wireFunctions(trigger.TopSelf), TopInclusive: c.wireFunctions(trigger.TopInclusive)}
	}
	return out
}

func (c *catalogBuilder) wireWindows(windows []rootcause.Window) []WireWindow {
	out := make([]WireWindow, len(windows))
	for i, window := range windows {
		out[i] = WireWindow{WindowFields: WindowFields(window)}
		if window.HotPath != nil {
			path := c.wirePath(*window.HotPath)
			out[i].HotPath = &path
		}
	}
	return out
}
