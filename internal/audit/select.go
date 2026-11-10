package audit

import (
	"encoding/json"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
)

// SelectEvidence uses each evidence scene's analysis cap for audit references.
// Full numeric rollups and in-memory scene analyses remain unchanged.
func SelectEvidence(report *Report) *Report {
	if report == nil {
		return nil
	}
	r := *report
	r.Scenes = append([]SceneResult{}, r.Scenes...)
	limits := make([]int, len(r.Scenes))
	for i := range r.Scenes {
		scene := &r.Scenes[i]
		scene.Rep = insights.SelectEvidence(scene.Rep)
		top := 10
		if scene.Rep != nil && scene.Rep.Top > 0 {
			top = scene.Rep.Top
		}
		limits[i] = top
	}
	r.Priorities = append([]Priority{}, r.Priorities...)
	for i := range r.Priorities {
		p := &r.Priorities[i]
		top := 10
		if p.EvidenceSceneIndex >= 0 && p.EvidenceSceneIndex < len(limits) {
			top = limits[p.EvidenceSceneIndex]
		}
		p.StacksTotal = max(p.StacksTotal, max(len(p.Stacks), len(p.StackIDs)))
		p.Stacks = p.Stacks[:min(len(p.Stacks), top)]
		if len(p.Stacks) > 0 {
			p.StackIDs = make([]string, len(p.Stacks))
			for j, stack := range p.Stacks {
				p.StackIDs[j] = stack.StackID
				if p.StackIDs[j] == "" {
					p.StackIDs[j] = flame.StackID(stack.Frames)
				}
			}
		} else if len(p.StackIDs) > 0 {
			p.StackIDs = p.StackIDs[:min(len(p.StackIDs), top)]
		} else {
			p.StackIDs = []string{}
		}
	}
	return &r
}

type reportFields Report

func (r *Report) MarshalJSON() ([]byte, error) {
	selected := SelectEvidence(r)
	return json.Marshal((*reportFields)(selected))
}
