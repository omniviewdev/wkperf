package cmd

import (
	"strings"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

func analyzeMarkdownDocuments(reports []*insights.Report, cards []*scorecard.Scorecard, brief bool) ([]insights.MarkdownDocument, error) {
	documents := make([]insights.MarkdownDocument, len(reports))
	for i, report := range reports {
		documents[i].Report = report
		if brief || i >= len(cards) || cards[i] == nil {
			continue
		}
		var suffix strings.Builder
		if err := scorecard.RenderMarkdown(&suffix, cards[i]); err != nil {
			return nil, err
		}
		documents[i].Suffix = suffix.String()
	}
	return documents, nil
}
