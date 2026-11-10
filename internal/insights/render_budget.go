package insights

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

type renderBlock struct {
	text  string
	shown int
}

type renderDocument struct {
	header        renderBlock
	findings      []renderBlock
	details       []renderBlock
	source        string
	identityToken string
	compact       string
	terminal      bool
}

func (d *renderDocument) identified() bool {
	return d.header.shown == len(d.header.text) || d.compact != "" ||
		(d.identityToken != "" && strings.Contains(d.header.text[:d.header.shown], d.identityToken))
}

func compactSource(source string, available int, terminal bool) string {
	format := func(value string) string {
		if !terminal {
			value = code(value)
		}
		return "Source: " + value + "\n"
	}
	if text := format(source); len(text) <= available {
		return text
	}
	start := max(0, len(source)-max(0, available-14))
	for start < len(source) && !utf8.RuneStart(source[start]) {
		start++
	}
	for start < len(source) {
		text := format("…" + source[start:])
		if len(text) <= available {
			return text
		}
		start += len(text) - available
		for start < len(source) && !utf8.RuneStart(source[start]) {
			start++
		}
	}
	return ""
}

func documentBlocks(documents []MarkdownDocument, brief, terminal, color bool) []renderDocument {
	out := make([]renderDocument, 0, len(documents))
	convert := func(text string) string {
		if terminal {
			return terminalText(text, color)
		}
		return text
	}
	for _, document := range documents {
		if document.Report == nil {
			continue
		}
		v := newMarkdownRenderer(document.Report, brief)
		header := v.header()
		if len(out) > 0 {
			header = "\n---\n\n" + header
		}
		source := document.Report.Source
		if source == "" {
			source = document.Report.Name
		}
		doc := renderDocument{
			header: renderBlock{text: convert(header)}, source: source,
			identityToken: convert(code(document.Report.Source)), terminal: terminal,
		}
		findings := v.findings()
		if len(findings) > 1 {
			findings[1] = findings[0] + findings[1]
			findings = findings[1:]
		}
		for _, text := range findings {
			doc.findings = append(doc.findings, renderBlock{text: convert(text)})
		}
		add := func(text string) {
			if text != "" {
				doc.details = append(doc.details, renderBlock{text: convert(text)})
			}
		}
		add(v.context())
		add(v.hotPath())
		add(v.functions())
		add(v.verification())
		for _, text := range v.findingDetails() {
			add(text)
		}
		if !brief {
			add(document.Suffix)
			add(v.rootDetails())
			add(v.otherDetails())
			add(v.appendix())
		}
		out = append(out, doc)
	}
	return out
}

func completeLength(documents []renderDocument, preamble string) int {
	n := len(preamble)
	for _, document := range documents {
		n += len(document.header.text)
		for _, block := range document.findings {
			n += len(block.text)
		}
		for _, block := range document.details {
			n += len(block.text)
		}
	}
	return n
}

// takeLines fills one block greedily, preserving complete Markdown lines.
// A short remainder can still be used by a later, smaller block.
func takeLines(block *renderBlock, available int) int {
	if available <= 0 || block.shown == len(block.text) {
		return 0
	}
	rest := block.text[block.shown:]
	n := len(rest)
	if n > available {
		end := strings.LastIndexByte(rest[:available], '\n')
		if end < 0 {
			return 0
		}
		n = end + 1
	}
	if block.shown == 0 && n < len(rest) {
		content := false
		for _, line := range strings.Split(rest[:n], "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && line != "---" {
				content = true
				break
			}
		}
		if !content {
			return 0
		}
	}
	block.shown += n
	return n
}

func takeBlocks(blocks []renderBlock, available int) int {
	used := 0
	for i := range blocks {
		used += takeLines(&blocks[i], available-used)
	}
	return used
}

func countOmitted(documents []renderDocument, preamble renderBlock) (blocks, recordings int) {
	if preamble.shown < len(preamble.text) {
		blocks++
	}
	for _, document := range documents {
		if document.header.shown < len(document.header.text) {
			blocks++
			if !document.identified() {
				recordings++
			}
		}
		for _, block := range document.findings {
			if block.shown < len(block.text) {
				blocks++
			}
		}
		for _, block := range document.details {
			if block.shown < len(block.text) {
				blocks++
			}
		}
	}
	return blocks, recordings
}

func truncationFooter(shown, total, blocks, recordings, cap int) string {
	marker := MarkdownTruncationMarker + "\n"
	if cap < 300 {
		return marker
	}
	return fmt.Sprintf("[truncated: %d of %d bytes shown; %d blocks omitted; %d recordings omitted]\n", shown, total, blocks, recordings) + marker
}

func packDocuments(documents []renderDocument, preamble string, cap int) string {
	total := completeLength(documents, preamble)
	bounded := cap > 0 && total > cap
	if !bounded {
		var b strings.Builder
		b.Grow(total)
		b.WriteString(preamble)
		for _, document := range documents {
			b.WriteString(document.header.text)
			for _, block := range document.findings {
				b.WriteString(block.text)
			}
			for _, block := range document.details {
				b.WriteString(block.text)
			}
		}
		return b.String()
	}
	// Reserve the final marker before selecting any report content. Its maximum
	// digit widths make the reservation stable while blocks are selected.
	count := 1
	for _, document := range documents {
		count += 1 + len(document.findings) + len(document.details)
	}
	reserve := len(truncationFooter(cap, total, count, len(documents), cap))
	if reserve > cap {
		return MarkdownTruncationMarker[:min(cap, len(MarkdownTruncationMarker))]
	}
	remaining := cap - reserve
	errors := renderBlock{text: preamble}
	remaining -= takeLines(&errors, remaining)
	// Recording identity outranks every detail and every other recording's
	// findings. This is a global reservation, not front truncation.
	for i := range documents {
		remaining -= takeLines(&documents[i].header, remaining)
	}
	missing := 0
	for i := range documents {
		if !documents[i].identified() {
			missing++
		}
	}
	for i := range documents {
		if documents[i].identified() {
			continue
		}
		allowance := remaining / max(missing, 1)
		compact := compactSource(documents[i].source, allowance, documents[i].terminal)
		if compact == "" {
			compact = compactSource(documents[i].source, remaining, documents[i].terminal)
		}
		documents[i].compact = compact
		remaining -= len(compact)
		missing--
	}
	active := 0
	for _, document := range documents {
		if document.identified() {
			active++
		}
	}
	for i := range documents {
		if !documents[i].identified() {
			continue
		}
		allowance := remaining / max(active, 1)
		remaining -= takeBlocks(documents[i].findings, allowance)
		active--
	}
	// All finding identities are reserved before details. Redistribute unused
	// reservation rather than wasting bytes on per-section fair shares.
	for i := range documents {
		if documents[i].identified() {
			remaining -= takeBlocks(documents[i].findings, remaining)
		}
	}
	active = len(documents)
	for i := range documents {
		if documents[i].identified() {
			remaining -= takeBlocks(documents[i].details, remaining/max(active, 1))
		}
		active--
	}
	for i := range documents {
		if documents[i].identified() {
			remaining -= takeBlocks(documents[i].details, remaining)
		}
	}
	var b strings.Builder
	b.Grow(cap)
	b.WriteString(errors.text[:errors.shown])
	for _, document := range documents {
		b.WriteString(document.header.text[:document.header.shown])
		b.WriteString(document.compact)
		for _, block := range document.findings {
			b.WriteString(block.text[:block.shown])
		}
		for _, block := range document.details {
			b.WriteString(block.text[:block.shown])
		}
	}
	blocks, recordings := countOmitted(documents, errors)
	b.WriteString(truncationFooter(b.Len(), total, blocks, recordings, cap))
	return b.String()
}

// RenderMarkdownDocuments reserves identities and all findings globally, then
// fills each recording's remaining share in priority order. Preamble errors are
// mandatory and scorecards remain inside the same final byte limit.
func RenderMarkdownDocuments(w io.Writer, documents []MarkdownDocument, preamble string, options MarkdownOptions) error {
	_, err := io.WriteString(w, packDocuments(documentBlocks(documents, options.Brief, false, false), preamble, options.MaxBytes))
	return err
}

func RenderTermDocuments(w io.Writer, documents []MarkdownDocument, preamble string, color bool, options MarkdownOptions) error {
	_, err := io.WriteString(w, packDocuments(documentBlocks(documents, options.Brief, true, color), terminalText(preamble, color), options.MaxBytes))
	return err
}

func terminalText(text string, color bool) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, "#") {
			line = strings.TrimLeft(line, "# ")
			if color {
				line = "\x1b[1m" + strings.TrimSuffix(line, "\n") + "\x1b[0m\n"
			}
		}
		b.WriteString(terminalInline(line))
	}
	return b.String()
}

func terminalInline(line string) string {
	table := strings.HasPrefix(line, "|")
	var b strings.Builder
	for len(line) > 0 {
		start := strings.IndexByte(line, '`')
		if start < 0 {
			b.WriteString(strings.ReplaceAll(line, "**", ""))
			break
		}
		b.WriteString(strings.ReplaceAll(line[:start], "**", ""))
		line = line[start:]
		n := 0
		for n < len(line) && line[n] == '`' {
			n++
		}
		fence := line[:n]
		end := n
		for {
			i := strings.Index(line[end:], fence)
			if i < 0 {
				end = -1
				break
			}
			end += i
			if end+n == len(line) || line[end+n] != '`' {
				break
			}
			for end < len(line) && line[end] == '`' {
				end++
			}
		}
		if end < 0 {
			b.WriteString(line)
			break
		}
		span := line[n:end]
		if strings.HasPrefix(span, " ") && strings.HasSuffix(span, " ") && strings.Trim(span, " ") != "" {
			span = span[1 : len(span)-1]
		}
		b.WriteString(span)
		line = line[end+n:]
	}
	if table {
		return strings.ReplaceAll(b.String(), "\\|", "|")
	}
	return b.String()
}
