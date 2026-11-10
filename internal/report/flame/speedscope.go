package flame

import (
	"encoding/json"
	"io"

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
	trees := make([]*core.Tree, len(inputs))
	for i, in := range inputs {
		trees[i] = in.tree
	}
	frames := core.FrameCatalog(trees...)
	doc := Speedscope{Schema: "https://www.speedscope.app/file-format-schema.json", Name: "wkperf flame", Exporter: "wkperf", Shared: SpeedscopeShared{Frames: make([]SpeedscopeFrame, 0, len(frames))}, Profiles: make([]SpeedscopeProfile, 0, len(inputs))}
	indices := make(map[string]int, len(frames))
	for i, frame := range frames {
		file, line, column := frame.URL, frame.Line, frame.Column
		name := core.FrameLabel(frame)
		if frame.Original != nil {
			file, line, column = frame.Original.URL, frame.Original.Line, frame.Original.Column
		}
		doc.Shared.Frames = append(doc.Shared.Frames, SpeedscopeFrame{Name: name, File: file, Line: line, Column: column})
		indices[frame.ID] = i
	}
	for _, in := range inputs {
		p := SpeedscopeProfile{Type: "sampled", Name: in.name, Unit: "milliseconds", Samples: make([][]int, 0, in.tree.SampleCount()), Weights: make([]float64, 0, in.tree.SampleCount())}
		in.tree.EachSample(func(sample core.Sample) {
			stack := make([]int, len(sample.Frames))
			for i, frame := range sample.Frames {
				stack[i] = indices[frame.ID]
			}
			p.Samples = append(p.Samples, stack)
			p.Weights = append(p.Weights, sample.WeightMs)
			p.EndValue += sample.WeightMs
		})
		doc.Profiles = append(doc.Profiles, p)
	}
	enc := json.NewEncoder(w)
	return enc.Encode(doc)
}
