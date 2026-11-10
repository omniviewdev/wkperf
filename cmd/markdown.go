package cmd

import (
	"io"
	"strings"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/insights"
)

func writeBoundedMarkdown(w io.Writer, text string, maxBytes int) error {
	if maxBytes <= 0 || len(text) <= maxBytes {
		_, err := io.WriteString(w, text)
		return err
	}
	marker := insights.MarkdownTruncationMarker + "\n"
	text = strings.ReplaceAll(text, insights.MarkdownTruncationMarker, "")
	limit := maxBytes - len(marker) - 1
	if limit < 0 {
		limit = 0
	}
	if limit > len(text) {
		limit = len(text)
	}
	prefix := text[:limit]
	if newline := strings.LastIndexByte(prefix, '\n'); newline >= 0 {
		prefix = prefix[:newline]
	}
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	prefix = strings.TrimRight(prefix, "\n\r \t")
	_, err := io.WriteString(w, prefix+"\n"+marker)
	return err
}
