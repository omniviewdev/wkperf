package flame

import (
	"encoding/json"
	"io"
	"sort"

	core "github.com/omniviewdev/wkperf/internal/flame"
)

// Speedscope follows the sampled-profile schema. All weights use milliseconds.
type Speedscope struct {
	Schema             string              `json:"$schema"`
	Name               string              `json:"name"`
	Exporter           string              `json:"exporter"`
	ActiveProfileIndex int                 `json:"activeProfileIndex"`
	Shared             SpeedscopeShared    `json:"shared"`
	Profiles           []SpeedscopeProfile `json:"profiles"`
}

type SpeedscopeShared struct {
	Frames []SpeedscopeFrame `json:"frames"`
}

type SpeedscopeFrame struct {
	Name   string `json:"name"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"col,omitempty"`
}

type SpeedscopeProfile struct {
	Type       string    `json:"type"`
	Name       string    `json:"name"`
	Unit       string    `json:"unit"`
	StartValue float64   `json:"startValue"`
	EndValue   float64   `json:"endValue"`
	Samples    [][]int   `json:"samples"`
	Weights    []float64 `json:"weights"`
}

func renderSpeedscope(w io.Writer, charts []Chart) error {
	type input struct {
		name string
		tree *core.Tree
	}
	inputs := make([]input, 0, len(charts))
	for _, chart := range charts {
		if chart.Comparison != nil {
			inputs = append(inputs, input{chart.Label + " · baseline", chart.Before}, input{chart.Label + " · current", chart.After})
		} else {
			inputs = append(inputs, input{chart.Label, chart.Tree})
		}
	}
	frames := make(map[string]core.Frame)
	for _, in := range inputs {
		for _, sample := range in.tree.Samples {
			for _, frame := range sample.Frames {
				frames[core.FunctionID(frame)] = frame
			}
		}
	}
	keys := make([]string, 0, len(frames))
	for id := range frames {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := frames[keys[i]], frames[keys[j]]
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
		return keys[i] < keys[j]
	})
	doc := Speedscope{Schema: "https://www.speedscope.app/file-format-schema.json", Name: "wkperf flame", Exporter: "wkperf", Shared: SpeedscopeShared{Frames: make([]SpeedscopeFrame, 0, len(keys))}, Profiles: make([]SpeedscopeProfile, 0, len(inputs))}
	indices := make(map[string]int, len(keys))
	for i, id := range keys {
		frame := frames[id]
		file, line, column := frame.URL, frame.Line, frame.Column
		name := core.FrameLabel(frame)
		if frame.Original != nil {
			file, line, column = frame.Original.URL, frame.Original.Line, frame.Original.Column
		}
		doc.Shared.Frames = append(doc.Shared.Frames, SpeedscopeFrame{Name: name, File: file, Line: line, Column: column})
		indices[id] = i
	}
	for _, in := range inputs {
		p := SpeedscopeProfile{Type: "sampled", Name: in.name, Unit: "milliseconds", Samples: make([][]int, 0, len(in.tree.Samples)), Weights: make([]float64, 0, len(in.tree.Samples))}
		for _, sample := range in.tree.Samples {
			stack := make([]int, len(sample.Frames))
			for i, frame := range sample.Frames {
				stack[i] = indices[core.FunctionID(frame)]
			}
			p.Samples = append(p.Samples, stack)
			p.Weights = append(p.Weights, sample.WeightMs)
			p.EndValue += sample.WeightMs
		}
		doc.Profiles = append(doc.Profiles, p)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
