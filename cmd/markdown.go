package cmd

import (
	"fmt"
	"strings"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/schema"
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

func failureMarkdown(failures []schema.Error) string {
	if len(failures) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString("## Errors\n\n")
	for _, failure := range failures {
		fmt.Fprintf(&text, "- [%s] ", failure.Code)
		if failure.File != "" {
			text.WriteString(strings.ReplaceAll(failure.File, "\n", " "))
			text.WriteString(": ")
		}
		text.WriteString(strings.ReplaceAll(failure.Message, "\n", " "))
		text.WriteByte('\n')
	}
	text.WriteByte('\n')
	return text.String()
}
