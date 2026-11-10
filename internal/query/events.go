package query

import (
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

func triggerRows(rec *model.Recording, functionCount int, req Request) []Row {
	rows := []Row{}
	classifier := flame.NewClassifier(req.ownership())
	for _, trigger := range insights.TriggerStats(rec, functionCount+1) {
		if req.Selector != "" && req.Selector != trigger.Name && req.Selector != trigger.Kind && req.Selector != trigger.Kind+"/"+trigger.Name {
			continue
		}
		row := Row{Function: Function{Name: trigger.Name}, Kind: trigger.Kind, Count: trigger.Count, TotalMS: clean(trigger.TotalMs), MeanMS: clean(trigger.MeanMs), P95MS: clean(trigger.P95Ms), MaxMS: clean(trigger.MaxMs), TopSelfTotalCount: len(trigger.TopSelf), TopInclusiveTotalCount: len(trigger.TopInclusive)}
		self, inclusive := trigger.TopSelf, trigger.TopInclusive
		if len(self) > req.Limit {
			self = self[:req.Limit]
			row.FunctionsTruncated = true
		}
		if len(inclusive) > req.Limit {
			inclusive = inclusive[:req.Limit]
			row.FunctionsTruncated = true
		}
		row.TopSelf, row.TopInclusive = costs(self, classifier), costs(inclusive, classifier)
		rows = append(rows, row)
	}
	sortRows(rows, "total")
	return rows
}

func costs(functions []metrics.FuncStat, classifier *flame.Classifier) []FunctionCost {
	out := make([]FunctionCost, len(functions))
	for i, f := range functions {
		frame := flame.Frame{Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Native: f.URL == "", Anonymous: f.Name == ""}
		if f.Original != nil {
			frame.Original = &flame.Location{Name: f.Original.Name, URL: f.Original.URL, Line: f.Original.Line, Column: f.Original.Column}
		}
		frame.Category = classifier.Categorize(frame)
		out[i] = FunctionCost{Function: function(frame), SelfMS: clean(f.SelfMs), TotalMS: clean(f.TotalMs), SelfSamples: f.SelfSamples}
	}
	return out
}
