package query

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func keyRows(rec *model.Recording, tree *flame.Tree, source Source, req Request) ([]Row, error) {
	match, err := keyMatcher(req.Selector, req.Operation == "keys")
	if err != nil {
		return nil, diagnostic.Wrap(diagnostic.SelectorError, req.File, err)
	}
	var registry []Row
	if isScalarSelector(req.Operation, req.Selector) {
		registry = scalarRows(rec, tree, source, req.Operation)
	} else {
		if req.Operation == "metrics" || req.Operation == "keys" {
			registry = append(registry, metricRegistry(rec, tree, source, req)...)
		}
		if req.Operation == "rootcause" || req.Operation == "keys" {
			registry = append(registry, rootRegistry(rec, tree, source, req)...)
		}
	}
	sort.Slice(registry, func(i, j int) bool {
		if registry[i].Key != registry[j].Key {
			return registry[i].Key < registry[j].Key
		}
		return registry[i].Operation < registry[j].Operation
	})
	rows := make([]Row, 0)
	for _, row := range registry {
		if match(row.Key) {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 && req.Selector != "" {
		return nil, &diagnostic.Error{File: req.File, Code: diagnostic.UnknownKey, Err: fmt.Errorf("unknown %s key selector %q", req.Operation, req.Selector), Candidates: nearestKeys(req.Selector, registry)}
	}
	return rows, nil
}

func metricRegistry(rec *model.Recording, tree *flame.Tree, source Source, req Request) []Row {
	values := map[string]Row{}
	options := analyze.Options{FromSec: req.From, ToSec: req.To, TrimStartSec: req.TrimStart, TrimEndSec: req.TrimEnd}
	for _, metric := range analyze.SummarizeWith(rec, req.File, options).Metrics {
		values[metric.Key] = Row{Key: metric.Key, Value: clean(metric.Value), Unit: string(metric.Unit), Description: metric.Label, Basis: "measured", Operation: "metrics"}
	}
	clipped := profile.Clip(rec, rec.StartTime+source.From, rec.StartTime+source.To)
	report := insights.Analyze(clipped, req.File, insights.Options{Top: MaxLimit, JankMs: req.JankMS, LongTaskMs: req.LongTaskMS, WindowStartSec: source.From, Sourcemaps: req.Sourcemaps, Ownership: req.ownership()})
	for key, value := range insights.NumericValues(report) {
		if _, exists := values[key]; !exists {
			values[key] = Row{Key: key, Value: clean(value.Value), Unit: string(value.Unit), Description: value.Description, Basis: metricBasis(key), Operation: "metrics"}
		}
	}
	for _, row := range scalarRows(rec, tree, source, "metrics") {
		values[row.Key] = row
	}
	rows := make([]Row, 0, len(values))
	for _, row := range values {
		rows = append(rows, row)
	}
	return rows
}

func rootRegistry(rec *model.Recording, tree *flame.Tree, source Source, req Request) []Row {
	report := rootcause.Build(rec, tree, rootcause.Options{Source: req.File, FromSec: source.From, ToSec: source.To, Top: req.RootCauseTop, LongTaskMs: req.LongTaskMS, JankMs: req.JankMS, Sourcemaps: req.Sourcemaps, Ownership: req.ownership()})
	values := map[string]Row{}
	for _, stat := range rootcause.Stats(report) {
		values[stat.Key] = Row{Key: stat.Key, Value: clean(stat.Value), Unit: stat.Unit, Description: stat.Label, Basis: stat.Basis, Operation: "rootcause"}
	}
	for _, row := range scalarRows(rec, tree, source, "rootcause") {
		values[row.Key] = row
	}
	rows := make([]Row, 0, len(values))
	for _, row := range values {
		rows = append(rows, row)
	}
	return rows
}

func metricBasis(key string) string {
	switch {
	case strings.HasPrefix(key, "functions."), strings.HasPrefix(key, "modules."), strings.HasPrefix(key, "commonStacks."), strings.HasPrefix(key, "samples."):
		return "sampled"
	case strings.HasPrefix(key, "findings."):
		return "estimated"
	case strings.Contains(key, ".functions."):
		return "sampled"
	default:
		return "measured"
	}
}

func keyMatcher(selector string, prefix bool) (func(string) bool, error) {
	if selector == "" {
		return func(string) bool { return true }, nil
	}
	if strings.HasPrefix(selector, "/") {
		if len(selector) < 2 || !strings.HasSuffix(selector, "/") {
			return nil, fmt.Errorf("malformed key regex %q: require /regex/", selector)
		}
		expression, err := regexp.Compile(selector[1 : len(selector)-1])
		if err != nil {
			return nil, fmt.Errorf("malformed key regex: %w", err)
		}
		return expression.MatchString, nil
	}
	if strings.HasSuffix(selector, ".*") {
		selector = strings.TrimSuffix(selector, "*")
		prefix = true
	}
	if prefix {
		return func(key string) bool { return strings.HasPrefix(key, selector) }, nil
	}
	return func(key string) bool { return key == selector }, nil
}

type keyCandidate struct {
	key      string
	prefix   int
	distance int
}

// nearestKeys ranks the longest shared prefix first, then edit distance and key.
// Registry order and map iteration never affect the bounded suggestion list.
func nearestKeys(selector string, registry []Row) []string {
	selector = strings.TrimSuffix(selector, ".*")
	seen := make(map[string]bool, len(registry))
	candidates := make([]keyCandidate, 0, len(registry))
	previous, current := make([]int, len(selector)+1), make([]int, len(selector)+1)
	for _, row := range registry {
		if seen[row.Key] {
			continue
		}
		seen[row.Key] = true
		prefix := 0
		for prefix < len(selector) && prefix < len(row.Key) && selector[prefix] == row.Key[prefix] {
			prefix++
		}
		for i := range previous {
			previous[i] = i
		}
		for i := range len(row.Key) {
			current[0] = i + 1
			for j := range len(selector) {
				cost := 0
				if row.Key[i] != selector[j] {
					cost = 1
				}
				current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
			}
			previous, current = current, previous
		}
		candidates = append(candidates, keyCandidate{key: row.Key, prefix: prefix, distance: previous[len(selector)]})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].prefix != candidates[j].prefix {
			return candidates[i].prefix > candidates[j].prefix
		}
		if candidates[i].distance != candidates[j].distance {
			return candidates[i].distance < candidates[j].distance
		}
		return candidates[i].key < candidates[j].key
	})
	out := make([]string, min(10, len(candidates)))
	for i := range out {
		out[i] = candidates[i].key
	}
	return out
}
