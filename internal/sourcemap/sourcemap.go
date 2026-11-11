// Package sourcemap resolves standard v3 source maps without fetching URLs.
// Generated WebKit locations remain intact; Original stores 1-based sources.
package sourcemap

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/model"
)

type segment struct{ generated, source, line, column, name int }
type section struct {
	line, column int
	mapping      *Map
}

// Map is a decoded source-map v3 document. Sources and names retain map order.
type Map struct {
	Version    int      `json:"version"`
	File       string   `json:"file,omitempty"`
	SourceRoot string   `json:"sourceRoot,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Names      []string `json:"names,omitempty"`
	Mappings   string   `json:"mappings"`
	lines      [][]segment
	sections   []section
}

// Parse validates base64 VLQ mappings, including unmapped segments and optional
// name deltas. Indexed v3 maps with embedded sections are supported as well.
func Parse(r io.Reader) (*Map, error) {
	var raw json.RawMessage
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode source map: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("source map must contain exactly one JSON object")
	}
	return parse(raw)
}

func parse(raw json.RawMessage) (*Map, error) {
	var m Map
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode source map: %w", err)
	}
	if m.Version != 3 {
		return nil, fmt.Errorf("unsupported source map version %d; expected 3", m.Version)
	}
	var indexed struct {
		Sections []struct {
			Offset struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"offset"`
			Map json.RawMessage `json:"map"`
			URL string          `json:"url"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(raw, &indexed); err != nil {
		return nil, err
	}
	for _, s := range indexed.Sections {
		if s.URL != "" {
			return nil, fmt.Errorf("external source-map section URLs are not fetched; supply an embedded section map")
		}
		if s.Offset.Line < 0 || s.Offset.Column < 0 {
			return nil, fmt.Errorf("negative source-map section offset")
		}
		if len(m.sections) > 0 {
			prev := m.sections[len(m.sections)-1]
			if s.Offset.Line < prev.line || (s.Offset.Line == prev.line && s.Offset.Column <= prev.column) {
				return nil, fmt.Errorf("source-map sections must have increasing offsets")
			}
		}
		sub, err := parse(s.Map)
		if err != nil {
			return nil, err
		}
		m.sections = append(m.sections, section{s.Offset.Line, s.Offset.Column, sub})
	}
	if len(m.sections) > 0 {
		if m.Mappings != "" {
			return nil, fmt.Errorf("indexed source map cannot also contain mappings")
		}
		return &m, nil
	}
	var source, originalLine, originalColumn, name int
	for lineIndex, encodedLine := range strings.Split(m.Mappings, ";") {
		var line []segment
		generated := 0
		if encodedLine != "" {
			for _, encoded := range strings.Split(encodedLine, ",") {
				values, err := decodeVLQ(encoded)
				if err != nil {
					return nil, fmt.Errorf("source map line %d: %w", lineIndex+1, err)
				}
				if len(values) != 1 && len(values) != 4 && len(values) != 5 {
					return nil, fmt.Errorf("source map line %d: segment has %d fields; expected 1, 4 or 5", lineIndex+1, len(values))
				}
				generated += values[0]
				if generated < 0 || values[0] < 0 {
					return nil, fmt.Errorf("source map line %d: decreasing generated column", lineIndex+1)
				}
				s := segment{generated: generated, source: -1, name: -1}
				if len(values) > 1 {
					source += values[1]
					originalLine += values[2]
					originalColumn += values[3]
					if source < 0 || source >= len(m.Sources) || originalLine < 0 || originalColumn < 0 {
						return nil, fmt.Errorf("source map line %d: source index or original location out of range", lineIndex+1)
					}
					s.source, s.line, s.column = source, originalLine, originalColumn
					if len(values) == 5 {
						name += values[4]
						if name < 0 || name >= len(m.Names) {
							return nil, fmt.Errorf("source map line %d: name index out of range", lineIndex+1)
						}
						s.name = name
					}
				}
				line = append(line, s)
			}
		}
		m.lines = append(m.lines, line)
	}
	return &m, nil
}

func decodeVLQ(encoded string) ([]int, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var values []int
	for i := 0; i < len(encoded); {
		var value uint64
		var shift uint
		for {
			if i == len(encoded) {
				return nil, fmt.Errorf("unterminated base64 VLQ")
			}
			digit := strings.IndexByte(alphabet, encoded[i])
			i++
			if digit < 0 {
				return nil, fmt.Errorf("invalid base64 VLQ character")
			}
			if shift >= 63 || uint64(digit&31) > (^uint64(0)>>shift) {
				return nil, fmt.Errorf("base64 VLQ integer overflow")
			}
			value |= uint64(digit&31) << shift
			if digit&32 == 0 {
				break
			}
			shift += 5
		}
		magnitude := value >> 1
		if magnitude > uint64(^uint(0)>>1) {
			return nil, fmt.Errorf("base64 VLQ integer overflow")
		}
		n := int(magnitude)
		if value&1 != 0 {
			n = -n
		}
		values = append(values, n)
	}
	return values, nil
}

// Lookup uses greatest-lower-bound lookup on one generated line. Arguments and
// returned source locations are 1-based, matching WebKit ScriptProfiler frames.
func (m *Map) Lookup(line, column int) (model.SourceLocation, bool) {
	return m.lookup(line, column, false)
}

func (m *Map) lookup(line, column int, before bool) (model.SourceLocation, bool) {
	if m == nil || line < 1 || column < 1 {
		return model.SourceLocation{}, false
	}
	line--
	column--
	if len(m.sections) > 0 {
		i := sort.Search(len(m.sections), func(i int) bool { s := m.sections[i]; return s.line > line || (s.line == line && s.column > column) }) - 1
		if i < 0 {
			return model.SourceLocation{}, false
		}
		s := m.sections[i]
		if line == s.line {
			column -= s.column
		}
		return s.mapping.lookup(line-s.line+1, column+1, before)
	}
	if line >= len(m.lines) {
		return model.SourceLocation{}, false
	}
	segments := m.lines[line]
	i := sort.Search(len(segments), func(i int) bool {
		return segments[i].generated > column || (before && segments[i].generated == column)
	}) - 1
	if i < 0 || segments[i].source < 0 {
		return model.SourceLocation{}, false
	}
	s := segments[i]
	location := model.SourceLocation{URL: sourceURL(m.SourceRoot, m.Sources[s.source]), Line: s.line + 1, Column: s.column + 1}
	if s.name >= 0 {
		location.Name = m.Names[s.name]
	}
	return location, true
}

func sourceURL(root, source string) string {
	if u, err := url.Parse(source); err == nil && u.IsAbs() {
		return source
	}
	if root == "" {
		return source
	}
	if u, err := url.Parse(root); err == nil && u.IsAbs() {
		if !strings.HasSuffix(u.Path, "/") {
			u.Path += "/"
		}
		if ref, err := url.Parse(source); err == nil {
			return u.ResolveReference(ref).String()
		}
	}
	return path.Join(root, source)
}

// Diagnostics reports measured resolution coverage and a heuristic minification
// hint. Minified is not a claim that every short name came from a minifier.
type Diagnostics struct {
	MappedFrames   int      `json:"mappedFrames"`
	UnmappedFrames int      `json:"unmappedFrames"`
	MissingMaps    []string `json:"missingMaps,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	Minified       bool     `json:"minified"`
}

type candidate struct {
	path     string
	explicit bool
}
type loadedMap struct {
	path    string
	mapping *Map
}

// DetectMinification uses distinct generated frames, not sample frequency.
// Short identifiers must dominate one file and have dense low-line locations.
func DetectMinification(rec *model.Recording) bool {
	if rec == nil {
		return false
	}
	type frameKey struct {
		name         string
		line, column int
	}
	type fileHint struct {
		frames map[frameKey]bool
		short  int
	}
	files := map[string]*fileHint{}
	for _, target := range rec.Samples {
		for _, stack := range target.StackTraces {
			for _, f := range stack.StackFrames {
				if f.URL == "" || f.Line < 1 || f.Column < 1 ||
					strings.EqualFold(f.SourceID, "native") || f.SourceID == "4294967295" ||
					strings.HasPrefix(f.URL, "user-script:") ||
					f.Name == "" || f.Name == "(anonymous)" || f.Name == "(program)" {
					continue
				}
				file := files[f.URL]
				if file == nil {
					file = &fileHint{frames: map[frameKey]bool{}}
					files[f.URL] = file
				}
				key := frameKey{f.Name, f.Line, f.Column}
				if _, seen := file.frames[key]; seen {
					continue
				}
				short := f.Line <= 20 && shortIdentifier(f.Name)
				file.frames[key] = short
				if short {
					file.short++
				}
			}
		}
	}
	for _, file := range files {
		if file.short < 2 || file.short*4 < len(file.frames)*3 {
			continue
		}
		type location struct{ line, column int }
		type lineHint struct {
			names     map[string]bool
			columns   map[int]bool
			maxColumn int
		}
		var early [2]lineHint
		names := map[string]bool{}
		locations := map[location]bool{}
		wide := map[location]bool{}
		for frame, short := range file.frames {
			if !short {
				continue
			}
			names[frame.name] = true
			loc := location{frame.line, frame.column}
			locations[loc] = true
			if frame.column >= 1000 {
				wide[loc] = true
			}
			if frame.line <= 2 {
				line := &early[frame.line-1]
				if line.names == nil {
					line.names, line.columns = map[string]bool{}, map[int]bool{}
				}
				line.names[frame.name] = true
				line.columns[frame.column] = true
				line.maxColumn = max(line.maxColumn, frame.column)
			}
		}
		for _, line := range early {
			if len(line.names) >= 2 && len(line.columns) >= 2 && line.maxColumn >= 80 {
				return true
			}
		}
		if len(names) >= 4 && len(locations) >= 4 && len(wide) >= 2 {
			return true
		}
	}
	return false
}

func shortIdentifier(name string) bool {
	if len(name) == 0 || len(name) > 3 {
		return false
	}
	for i := range len(name) {
		b := name[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' || b == '$' ||
			i > 0 && b >= '0' && b <= '9' {
			continue
		}
		return false
	}
	return true
}

// Resolve matches repeatable map files/directories and local bundle sidecars.
// A recording-adjacent bundle basename map also works for remote bundle URLs.
// Explicit files are strict; unreadable discovered maps produce warnings.
// It never fetches remote maps or overwrites generated frame identity.
func Resolve(rec *model.Recording, paths []string, recordingPath string) (Diagnostics, error) {
	d := Diagnostics{Minified: DetectMinification(rec)}
	if rec == nil {
		return d, nil
	}
	candidates := map[string]candidate{}
	add := func(name string, explicit bool) {
		absolute, err := filepath.Abs(name)
		if err == nil {
			name = absolute
		}
		if old, ok := candidates[name]; !ok || explicit {
			candidates[name] = candidate{name, explicit || old.explicit}
		}
	}
	discover := func(name string) {
		if absolute, err := filepath.Abs(name); err == nil {
			name = absolute
		}
		if _, known := candidates[name]; known {
			return
		}
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			add(name, false)
		}
	}
	for _, name := range paths {
		info, err := os.Stat(name)
		if err != nil {
			return d, diagnostic.Wrap(diagnostic.SourceMapError, name, err)
		}
		if !info.IsDir() {
			add(name, true)
			continue
		}
		if err := filepath.WalkDir(name, func(p string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".map") {
				add(p, false)
			}
			return nil
		}); err != nil {
			return d, diagnostic.Wrap(diagnostic.SourceMapError, name, err)
		}
	}
	seenBundles := map[string]bool{}
	for _, target := range rec.Samples {
		for _, stack := range target.StackTraces {
			for _, f := range stack.StackFrames {
				if seenBundles[f.URL] {
					continue
				}
				seenBundles[f.URL] = true
				if bundle := localBundle(f.URL, recordingPath); bundle != "" {
					discover(bundle + ".map")
				}
				if recordingPath != "" && f.URL != "" {
					basename := path.Base(bundlePath(f.URL))
					if basename != "." && basename != "/" {
						discover(filepath.Join(filepath.Dir(recordingPath), basename+".map"))
					}
				}
			}
		}
	}
	ordered := make([]string, 0, len(candidates))
	for name := range candidates {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	maps := make([]loadedMap, 0, len(ordered))
	for _, name := range ordered {
		mapping, err := loadMap(name)
		if err != nil {
			if candidates[name].explicit {
				return d, diagnostic.Wrap(diagnostic.SourceMapError, name, err)
			}
			d.Warnings = append(d.Warnings, "Source map could not be read: "+diagnostic.Wrap(diagnostic.SourceMapError, name, err).Error())
			continue
		}
		maps = append(maps, loadedMap{name, mapping})
	}
	missing := map[string]bool{}
	warnings := map[string]bool{}
	type match struct {
		index     int
		ambiguous bool
	}
	matches := map[string]match{}
	type locationKey struct {
		bundle       string
		line, column int
		named        bool
	}
	locations := map[locationKey]*model.SourceLocation{}
	type sourceKey struct {
		index int
		url   string
	}
	sources := map[sourceKey]string{}
	for ti := range rec.Samples {
		for si := range rec.Samples[ti].StackTraces {
			for fi := range rec.Samples[ti].StackTraces[si].StackFrames {
				f := &rec.Samples[ti].StackTraces[si].StackFrames[fi]
				if f.Original != nil {
					d.MappedFrames++
					continue
				}
				result, known := matches[f.URL]
				if !known {
					result.index = -1
					score := 0
					for i, m := range maps {
						s := matchScore(f.URL, m)
						if s > score {
							result = match{index: i}
							score = s
						} else if s > 0 && s == score {
							result.ambiguous = true
						}
					}
					matches[f.URL] = result
				}
				if result.index >= 0 && !result.ambiguous {
					named := f.Name != "" && f.Name != "(anonymous)"
					key := locationKey{f.URL, f.Line, f.Column, named}
					original, known := locations[key]
					if !known {
						mapping := maps[result.index].mapping
						if resolved, ok := mapping.Lookup(f.Line, f.Column); ok {
							resolved.Name = ""
							// Function-start columns point at parameters, not declaration names.
							// Keep the normal location unless the preceding token supplies a name.
							if named {
								if declaration, ok := mapping.lookup(f.Line, f.Column, true); ok && declaration.Name != "" {
									resolved = declaration
								}
							}
							if resolved.URL != "" {
								source := sourceKey{result.index, resolved.URL}
								resolvedURL, known := sources[source]
								if !known {
									var unresolved bool
									resolvedURL, unresolved = resolveSourcePath(maps[result.index].path, resolved.URL)
									sources[source] = resolvedURL
									if unresolved {
										warnings[fmt.Sprintf("Original source paths relative to %s could not be resolved on disk; pass --sourcemap from the build directory.", maps[result.index].path)] = true
									}
								}
								resolved.URL = resolvedURL
							}
							original = &resolved
						}
						locations[key] = original
					}
					if original != nil {
						f.Original = original
						d.MappedFrames++
						continue
					}
				}
				if result.ambiguous {
					warnings["Ambiguous source maps for "+f.URL+"; preserve generated location rather than guess."] = true
				}
				d.UnmappedFrames++
				if f.URL != "" {
					missing[f.URL] = true
				}
			}
		}
	}
	for bundle := range missing {
		d.MissingMaps = append(d.MissingMaps, bundle)
	}
	sort.Strings(d.MissingMaps)
	if d.Minified && len(d.MissingMaps) > 0 {
		warnings["Likely minified JavaScript lacks source-map coverage; use --sourcemap FILE or DIR to resolve original names and locations."] = true
	}
	for warning := range warnings {
		d.Warnings = append(d.Warnings, warning)
	}
	sort.Strings(d.Warnings)
	return d, nil
}

func loadMap(name string) (*Map, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	mapping, parseErr := Parse(file)
	closeErr := file.Close()
	if parseErr == nil {
		parseErr = closeErr
	}
	return mapping, parseErr
}

func resolveSourcePath(mapPath, raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || filepath.IsAbs(raw) {
		return raw, false
	}
	if u.IsAbs() {
		if u.Scheme != "webpack" {
			return raw, false
		}
		raw = strings.TrimPrefix(u.Path, "/")
	}
	joined := filepath.Clean(filepath.Join(filepath.Dir(mapPath), filepath.FromSlash(raw)))
	if _, err := os.Stat(joined); err == nil {
		return joined, false
	}
	cleaned := path.Clean(filepath.ToSlash(raw))
	for strings.HasPrefix(cleaned, "../") {
		cleaned = strings.TrimPrefix(cleaned, "../")
	}
	return cleaned, true
}

func localBundle(raw, recordingPath string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	var name string
	switch u.Scheme {
	case "":
		name = u.Path
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return ""
		}
		name = u.Path
	default:
		return ""
	}
	if !filepath.IsAbs(name) {
		if recordingPath == "" {
			return ""
		}
		name = filepath.Join(filepath.Dir(recordingPath), filepath.FromSlash(name))
	}
	if info, err := os.Stat(name); err != nil || info.IsDir() {
		return ""
	}
	return name
}

func bundlePath(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return strings.TrimPrefix(path.Clean(u.Path), "./")
	}
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(raw)), "./")
}

func matchScore(generated string, m loadedMap) int {
	if generated == "" {
		return 0
	}
	generatedPath := bundlePath(generated)
	filePath := bundlePath(m.mapping.File)
	adjacentPath := bundlePath(strings.TrimSuffix(m.path, ".map"))
	if generated == m.mapping.File || generatedPath == adjacentPath {
		return 100
	}
	if filePath != "" && filePath != "." && generatedPath == filePath {
		return 90
	}
	if strings.Contains(filePath, "/") && strings.HasSuffix(generatedPath, "/"+strings.TrimPrefix(filePath, "/")) {
		return 80
	}
	if suffix := matchingPathSuffix(generatedPath, adjacentPath); suffix > 1 {
		return 20 + min(suffix, 59)
	}
	if filePath != "" && filePath != "." && path.Base(generatedPath) == path.Base(filePath) {
		return 10
	}
	if path.Base(generatedPath) == path.Base(adjacentPath) {
		return 10
	}
	return 0
}

func matchingPathSuffix(a, b string) int {
	matches := 0
	for a != "" && b != "" {
		i, j := strings.LastIndexByte(a, '/'), strings.LastIndexByte(b, '/')
		if a[i+1:] != b[j+1:] {
			break
		}
		matches++
		if i < 0 || j < 0 {
			break
		}
		a, b = a[:i], b[:j]
	}
	return matches
}
