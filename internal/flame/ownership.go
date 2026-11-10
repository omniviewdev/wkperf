package flame

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

// Classifier reuses compiled ownership rules across frames. It is read-only
// after construction and can be shared by concurrent analysis consumers.
type Classifier struct {
	appURLs     []*regexp.Regexp
	libraryURLs []*regexp.Regexp
}

// NewClassifier compiles whole-URL globs. Call OwnershipRules.Validate at input
// boundaries; invalid rules panic here rather than silently changing ownership.
func NewClassifier(rules OwnershipRules) *Classifier {
	classifier, err := compileOwnershipRules(rules)
	if err != nil {
		panic(err)
	}
	return classifier
}

// Validate rejects empty or malformed URL globs. A single star matches within
// one path segment, a double star spans segments, and **/ can match no segments.
// Question marks and bracket classes match one non-slash character. Paths and
// patterns use forward slashes; matching is case-sensitive.
func (rules OwnershipRules) Validate() error {
	_, err := compileOwnershipRules(rules)
	return err
}

func compileOwnershipRules(rules OwnershipRules) (*Classifier, error) {
	if len(rules.AppURLs) == 0 && len(rules.LibraryURLs) == 0 {
		return &defaultClassifier, nil
	}
	classifier := &Classifier{}
	for _, group := range []struct {
		name     string
		patterns []string
		out      *[]*regexp.Regexp
	}{
		{"libraryURLs", rules.LibraryURLs, &classifier.libraryURLs},
		{"appURLs", rules.AppURLs, &classifier.appURLs},
	} {
		for i, pattern := range group.patterns {
			compiled, err := compileOwnershipGlob(pattern)
			if err != nil {
				return nil, fmt.Errorf("%s[%d] %q: %w", group.name, i, pattern, err)
			}
			*group.out = append(*group.out, compiled)
		}
	}
	return classifier, nil
}

func compileOwnershipGlob(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("URL glob must not be empty")
	}
	chars := []rune(strings.ReplaceAll(pattern, "\\", "/"))
	var expression strings.Builder
	expression.WriteByte('^')
	for i := 0; i < len(chars); i++ {
		switch chars[i] {
		case '*':
			if i+1 < len(chars) && chars[i+1] == '*' {
				for i+1 < len(chars) && chars[i+1] == '*' {
					i++
				}
				if i+1 < len(chars) && chars[i+1] == '/' {
					expression.WriteString("(?:.*/)?")
					i++
				} else {
					expression.WriteString(".*")
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		case '[':
			end := i + 1
			if end < len(chars) && (chars[end] == '!' || chars[end] == '^') {
				end++
			}
			start := end
			for end < len(chars) && chars[end] != ']' {
				if chars[end] == '/' || chars[end] == '[' {
					return nil, fmt.Errorf("invalid bracket class")
				}
				end++
			}
			if end == len(chars) || end == start {
				return nil, fmt.Errorf("unterminated or empty bracket class")
			}
			class := "["
			if chars[i+1] == '!' || chars[i+1] == '^' {
				class += "^/"
			}
			class += string(chars[start:end]) + "]"
			compiledClass, err := ownershipClass(class)
			if err != nil {
				return nil, err
			}
			expression.WriteString(compiledClass)
			i = end
		case ']':
			return nil, fmt.Errorf("unmatched closing bracket")
		default:
			expression.WriteString(regexp.QuoteMeta(string(chars[i])))
		}
	}
	expression.WriteByte('$')
	return regexp.Compile(expression.String())
}

// Categorize requires source or explicit URL evidence before claiming app code.
// Known React names apply only to unresolved bundles, never mapped user sources
// or source files. Unknown bundle frames may belong to an application or library.
func (classifier *Classifier) Categorize(frame Frame) Category {
	name, path := frame.Name, frame.URL
	mapped := frame.Original != nil && frame.Original.URL != ""
	if frame.Original != nil && frame.Original.Name != "" {
		name = frame.Original.Name
	}
	if gcFunction(name) || (name != frame.Name && gcFunction(frame.Name)) {
		return CategoryGC
	}
	generated := strings.ReplaceAll(frame.URL, "\\", "/")
	if strings.HasPrefix(generated, "user-script:") {
		return CategoryInjected
	}
	if mapped {
		path = frame.Original.URL
	}
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" {
		return CategoryNative
	}
	if matchesOwnershipURL(classifier.libraryURLs, generated, path) {
		return CategoryFramework
	}
	if matchesOwnershipURL(classifier.appURLs, generated, path) {
		return CategoryApp
	}
	lower := strings.ToLower(path)
	if libraryPath(lower) {
		return CategoryFramework
	}
	if mapped || firstPartyPlugin(lower) || sourcePath(lower) {
		return CategoryApp
	}
	if reactFunction(name) || (name != frame.Name && reactFunction(frame.Name)) {
		return CategoryFramework
	}
	return CategoryUnknownBundled
}

func matchesOwnershipURL(patterns []*regexp.Regexp, generated, original string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(generated) || (original != generated && pattern.MatchString(original)) {
			return true
		}
	}
	return false
}

func gcFunction(name string) bool {
	lower := strings.ToLower(name)
	return lower == "gc" || lower == "(gc)" || strings.Contains(lower, "garbage collect") || strings.HasPrefix(lower, "jsc::heap::collect")
}

func libraryPath(path string) bool {
	return strings.Contains(path, "/node_modules/") || strings.HasPrefix(path, "node_modules/") ||
		strings.Contains(path, "/vendor/") || strings.HasPrefix(path, "vendor/") ||
		strings.Contains(path, "/vendor-") || strings.Contains(path, "/vendor.") || strings.HasPrefix(path, "vendor-")
}

func firstPartyPlugin(path string) bool {
	_, tail, found := strings.Cut(path, "/_/plugins/")
	name, _, hasPath := strings.Cut(tail, "/")
	return found && hasPath && name != ""
}

func sourcePath(path string) bool {
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")
	if strings.HasPrefix(path, "src/") || strings.Contains(path, "/src/") {
		return true
	}
	for _, extension := range []string{".ts", ".tsx", ".jsx", ".mts", ".cts", ".vue", ".svelte"} {
		if strings.HasSuffix(path, extension) {
			return true
		}
	}
	return false
}

func ownershipClass(class string) (string, error) {
	parsed, err := syntax.Parse(class, syntax.Perl)
	if err != nil {
		return "", err
	}
	if parsed.Op == syntax.OpCharClass {
		for i := 0; i < len(parsed.Rune); i += 2 {
			lo, hi := parsed.Rune[i], parsed.Rune[i+1]
			if lo <= '/' && hi >= '/' {
				// A valid range can span '/', but glob classes never match it.
				ranges := make([]rune, 0, len(parsed.Rune)+2)
				ranges = append(ranges, parsed.Rune[:i]...)
				if lo < '/' {
					ranges = append(ranges, lo, '/'-1)
				}
				if hi > '/' {
					ranges = append(ranges, '/'+1, hi)
				}
				ranges = append(ranges, parsed.Rune[i+2:]...)
				parsed.Rune = ranges
				break
			}
		}
	}
	return parsed.String(), nil
}
