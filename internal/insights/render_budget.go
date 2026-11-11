package insights

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

type renderBlock struct {
	text       string
	markdown   string
	terminal   bool
	shown      int
	cutoffs    []renderCutoff
	heading    string
	hasContent bool
	detail     bool
	marker     string
}

type renderCutoff struct {
	start int
	end   int
}

const localTruncationMarker = "\n" + MarkdownTruncationMarker + "\n"

func tableDelimiter(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "|") && strings.Contains(line, "-") && strings.Trim(line, "|-: \t") == ""
}

func newRenderBlock(text string, terminal, color bool) renderBlock {
	block := renderBlock{text: text, markdown: text, terminal: terminal}
	if terminal {
		block.text = terminalText(text, color)
	}
	return block
}

// Classify Markdown before conversion, but keep cut points in rendered bytes.
func (block *renderBlock) prepare() {
	if block.markdown == "" {
		return
	}
	text := block.markdown
	block.markdown = ""
	contentEnd, renderedStart := 0, 0
	for start := 0; start < len(text); {
		end := len(text)
		if n := strings.IndexByte(text[start:], '\n'); n >= 0 {
			end = start + n + 1
		}
		renderedEnd := end
		if block.terminal {
			renderedEnd = len(block.text)
			if n := strings.IndexByte(block.text[renderedStart:], '\n'); n >= 0 {
				renderedEnd = renderedStart + n + 1
			}
		}
		line := strings.TrimSpace(text[start:end])
		heading := strings.HasPrefix(line, "#")
		if heading && block.heading == "" {
			block.heading = strings.TrimSuffix(block.text[renderedStart:renderedEnd], "\n")
		}
		next, _, _ := strings.Cut(text[end:], "\n")
		scaffolding := line == "" || heading || line == "---" || tableDelimiter(line) ||
			(strings.HasPrefix(line, "|") && tableDelimiter(next))
		if !scaffolding {
			if contentEnd < renderedStart {
				block.cutoffs = append(block.cutoffs, renderCutoff{contentEnd, renderedEnd})
			}
			contentEnd = renderedEnd
			block.hasContent = true
		}
		start, renderedStart = end, renderedEnd
	}
	if contentEnd < len(block.text) {
		block.cutoffs = append(block.cutoffs, renderCutoff{contentEnd, len(block.text)})
	}
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
			header: newRenderBlock(header, terminal, color), source: source,
			identityToken: convert(code(document.Report.Source)), terminal: terminal,
		}
		findings := v.findings()
		if len(findings) > 1 {
			findings[1] = findings[0] + findings[1]
			findings = findings[1:]
		}
		for _, text := range findings {
			doc.findings = append(doc.findings, newRenderBlock(text, terminal, color))
		}
		add := func(text string) {
			for text != "" {
				end := len(text)
				if n := strings.Index(text[1:], "\n## "); n >= 0 {
					end = n + 1
				}
				block := newRenderBlock(text[:end], terminal, color)
				block.detail = true
				doc.details = append(doc.details, block)
				text = text[end:]
			}
		}
		add(v.context())
		for _, text := range v.findingFacets() {
			add(text)
		}
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
	if block.shown == len(block.text) {
		return 0
	}
	block.prepare()
	if block.detail && !block.hasContent {
		return 0
	}
	reserved := len(block.marker)
	rest := block.text[block.shown:]
	if len(rest) <= available+reserved {
		block.shown = len(block.text)
		block.marker = ""
		return len(rest) - reserved
	}
	if block.detail && block.marker == "" {
		return 0
	}
	if reserved > 0 {
		available += reserved - len(localTruncationMarker)
	}
	if available <= 0 {
		return 0
	}
	end := strings.LastIndexByte(rest[:min(len(rest), available)], '\n')
	if end < 0 {
		return 0
	}
	end += block.shown + 1
	for _, cutoff := range block.cutoffs {
		if end <= cutoff.start {
			break
		}
		if end < cutoff.end {
			end = cutoff.start
			break
		}
	}
	if end <= block.shown {
		return 0
	}
	n := end - block.shown
	block.shown = end
	if reserved > 0 {
		block.marker = localTruncationMarker
	}
	return n + len(block.marker) - reserved
}

func reserveBlockMarkers(blocks []renderBlock, available int) int {
	used := 0
	for i := range blocks {
		block := &blocks[i]
		if available-used < len(localTruncationMarker) {
			break
		}
		block.prepare()
		if !block.hasContent || block.shown == len(block.text) || block.marker != "" {
			continue
		}
		size := len(localTruncationMarker)
		if block.heading != "" {
			size += len(block.heading) + 2
		}
		if size > available-used {
			continue
		}
		block.marker = localTruncationMarker
		if block.heading != "" {
			block.marker = "\n" + block.heading + "\n" + block.marker
		}
		used += size
	}
	return used
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

// compactPreamble preserves the leading mandatory diagnostic when a complete
// error line cannot fit. A shorter explicit marker leaves room for its code.
func compactPreamble(preamble string, cap int) string {
	const marker = "[truncated: use JSON]\n"
	if cap <= len(marker) {
		return marker[:max(0, cap)]
	}
	n := min(len(preamble), cap-len(marker)-1)
	for n > 0 && n < len(preamble) && !utf8.RuneStart(preamble[n]) {
		n--
	}
	return strings.TrimRight(preamble[:n], " \t\n") + "\n" + marker
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
	errors := newRenderBlock(preamble, false, false)
	remaining -= takeLines(&errors, remaining)
	if preamble != "" && errors.shown < len(preamble) {
		headingEnd := strings.Index(preamble, "\n\n")
		if errors.shown == 0 || (headingEnd >= 0 && errors.shown <= headingEnd+2) {
			return compactPreamble(preamble, cap)
		}
	}
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
	// Local notices must fit before any detail consumes its recording's share.
	active = len(documents)
	for i := range documents {
		if documents[i].identified() {
			remaining -= reserveBlockMarkers(documents[i].details, remaining/max(active, 1))
		}
		active--
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
			remaining -= reserveBlockMarkers(documents[i].details, remaining)
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
			b.WriteString(block.marker)
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
