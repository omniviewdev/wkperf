package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestOutputTerminalBriefMatchesMarkdown(t *testing.T) {
	file := writeRec(t, t.TempDir(), "profile.json", 9, testgen.Baseline())
	full, _, err := runPipeline(t, "analyze", file, "--no-color", "--no-scorecard")
	if err != nil {
		t.Fatal(err)
	}
	brief, _, err := runPipeline(t, "analyze", file, "--brief", "--no-color", "--no-scorecard")
	if err != nil {
		t.Fatal(err)
	}
	markdown, _, err := runPipeline(t, "analyze", file, "--format", "md", "--brief", "--no-scorecard")
	if err != nil {
		t.Fatal(err)
	}
	if len(brief) >= len(full) {
		t.Errorf("default terminal ignores --brief: brief=%d full=%d", len(brief), len(full))
	}
	var lines []string
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "#") {
			line = strings.TrimLeft(line, "# ")
		}
		lines = append(lines, strings.NewReplacer("`", "", "**", "", "\\|", "|").Replace(line))
	}
	if strings.TrimSpace(brief) != strings.TrimSpace(strings.Join(lines, "\n")) {
		t.Error("default terminal brief differs from Markdown brief content")
	}
}

func TestOutputTerminalByteCaps(t *testing.T) {
	dir := t.TempDir()
	one := writeRec(t, dir, "one.json", 1, testgen.Baseline())
	two := writeRec(t, dir, "two.json", 2, testgen.Baseline())
	for _, format := range []string{"", "term", "all"} {
		for _, limit := range []int{64, 200, 1000, 5000} {
			t.Run(fmt.Sprintf("%s/%d", format, limit), func(t *testing.T) {
				args := []string{"analyze", one, two, "--max-bytes", strconv.Itoa(limit), "--no-color"}
				if format != "" {
					args = append(args, "--format", format)
				}
				var outputDir string
				if format == "all" {
					outputDir = t.TempDir()
					args = append(args, "--out", outputDir)
				}
				out, _, err := runPipeline(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(out) > limit {
					t.Errorf("stdout exceeds %d-byte cap: %d", limit, len(out))
				}
				last := strings.TrimSpace(out)
				if newline := strings.LastIndexByte(last, '\n'); newline >= 0 {
					last = last[newline+1:]
				}
				if !strings.Contains(last, "[truncated:") {
					t.Errorf("truncation marker must be last: %q", last)
				}
				if limit >= 1000 && (!strings.Contains(out, one) || !strings.Contains(out, two)) {
					t.Error("bounded terminal output dropped recording identity")
				}
				if format == "all" {
					data, err := os.ReadFile(filepath.Join(outputDir, defaultAnalyzeBase+".md"))
					if err != nil {
						t.Fatal(err)
					}
					if len(data) > limit {
						t.Errorf("Markdown file exceeds %d-byte cap: %d", limit, len(data))
					}
					lines := strings.Split(strings.TrimSpace(string(data)), "\n")
					if !strings.Contains(lines[len(lines)-1], "[truncated:") {
						t.Error("Markdown file must end with truncation marker")
					}
					if limit >= 1000 && (!strings.Contains(string(data), one) || !strings.Contains(string(data), two)) {
						t.Error("bounded Markdown file dropped recording identity")
					}
				}
			})
		}
	}
	for _, limit := range []string{"-1", "1", "63"} {
		if _, _, err := runPipeline(t, "analyze", one, "--max-bytes", limit); err == nil {
			t.Errorf("default terminal accepted --max-bytes %s", limit)
		}
	}
}

func TestOutputAnalyzeDashWritesStdout(t *testing.T) {
	dir := t.TempDir()
	file := writeRec(t, dir, "profile.json", 9, testgen.Baseline())
	t.Chdir(dir)
	for _, format := range []string{"md", "json", "html", "folded", "term"} {
		t.Run(format, func(t *testing.T) {
			out, stderr, err := runPipeline(t, "analyze", file, "--format", format, "--out", "-", "--no-color")
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out) == "" {
				t.Error("--out - must write report to stdout")
			}
			if strings.Contains(stderr, "wrote -") {
				t.Error("stdout output must not announce a file write")
			}
			if _, err := os.Stat("-"); !os.IsNotExist(err) {
				t.Errorf("--out - created a literal file: %v", err)
			}
			if format == "json" {
				decodeDocument(t, out)
			}
			if format == "html" && !strings.Contains(strings.ToLower(out), "<!doctype html") {
				t.Error("HTML stdout lacks report document")
			}
		})
	}
	for _, args := range [][]string{
		{"analyze", file, "--format", "all", "--out", "-"},
		{"analyze", file, "--format", "html", "--out", "-", "--open"},
	} {
		if _, _, err := runPipeline(t, args...); err == nil {
			t.Errorf("incompatible stdout flags must fail: %v", args)
		}
	}
}

func outputReproductionFixture() *model.Recording {
	rec := testgen.Periodic()
	for i, frame := range []model.StackFrame{
		{Name: "heartbeat", URL: "https://app.test/app.js", Line: 10, Column: 25},
		{Name: "heartbeat", URL: "https://app.test/app.js", Line: 10, Column: 28},
		{URL: "https://app.test/app.js", Line: 20, Column: 3},
	} {
		at := 6 + float64(i)*.2
		rec.Records = append(rec.Records, model.Record{Type: model.TypeScript, EventType: "event-dispatched", StartTime: at, EndTime: at + .1})
		rec.Samples[0].StackTraces = append(rec.Samples[0].StackTraces, model.StackTrace{Timestamp: at + .01, StackFrames: []model.StackFrame{
			{Name: "getAnimations", SourceID: "native", Line: -1, Column: -1}, frame,
		}})
		rec.Samples[0].Durations = append(rec.Samples[0].Durations, .008)
	}
	rec.Records = append(rec.Records,
		model.Record{Type: model.TypeRenderingFrame, StartTime: 8, EndTime: 8.12},
		model.Record{Type: model.TypeScript, EventType: "event-dispatched", StartTime: 8, EndTime: 8.12},
		model.Record{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 8.02, EndTime: 8.06},
	)
	return rec
}

func TestOutputReproduceCommands(t *testing.T) {
	for _, samples := range []bool{true, false} {
		t.Run(fmt.Sprintf("samples=%t", samples), func(t *testing.T) {
			rec := outputReproductionFixture()
			if !samples {
				rec.Samples = nil
			}
			data, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "record '$x|``.json")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			out, _, err := runPipeline(t, "analyze", file, "--format", "json")
			if err != nil {
				t.Fatal(err)
			}
			var doc any
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			commands := map[string]*float64{}
			var walk func(any)
			walk = func(value any) {
				switch value := value.(type) {
				case map[string]any:
					if command, ok := value["command"].(string); ok && strings.HasPrefix(command, "wkperf ") {
						if number, ok := value["value"].(float64); ok {
							commands[command] = &number
						} else if _, exists := commands[command]; !exists {
							commands[command] = nil
						}
					}
					for key, child := range value {
						if command, ok := child.(string); strings.HasSuffix(key, "Command") && ok && strings.HasPrefix(command, "wkperf ") {
							if _, exists := commands[command]; !exists {
								commands[command] = nil
							}
						}
						if entries, ok := child.(map[string]any); key == "commands" && ok {
							for _, entry := range entries {
								if command, ok := entry.(string); ok && strings.HasPrefix(command, "wkperf ") {
									if _, exists := commands[command]; !exists {
										commands[command] = nil
									}
								}
							}
						}
						walk(child)
					}
				case []any:
					for _, child := range value {
						walk(child)
					}
				}
			}
			walk(doc)
			reports := doc.(map[string]any)["reports"].([]any)
			root := reports[0].(map[string]any)["rootCause"].(map[string]any)
			attributions, _ := root["appAttribution"].([]any)
			for _, value := range attributions {
				command := value.(map[string]any)["command"].(string)
				args := outputCommandArgs(t, command)
				if len(args) < 5 || args[3] != "paths" || !strings.HasPrefix(args[4], "f_") {
					t.Errorf("attribution command lacks stable function selector: %s", command)
				}
			}
			noSampleWindows, gcOverlaps := 0, 0
			for _, section := range []string{"windows", "longTasks", "slowFrames", "eventBursts", "gcOverlaps"} {
				values, _ := root[section].([]any)
				for _, value := range values {
					evidence := value.(map[string]any)
					status, _ := evidence["sampleStatus"].(string)
					if section != "gcOverlaps" && !strings.Contains(strings.ToLower(status), "no overlapping") {
						continue
					}
					if section == "gcOverlaps" {
						gcOverlaps++
					} else {
						noSampleWindows++
					}
					command := evidence["command"].(string)
					args := outputCommandArgs(t, command)
					if len(args) < 5 || args[3] != "rootcause" {
						t.Errorf("timeline-only command must resolve a scalar: %s", command)
					}
					duration := evidence["durationMs"].(float64)
					commands[command] = &duration
				}
			}
			if noSampleWindows == 0 || gcOverlaps == 0 {
				t.Errorf("fixture lacks timeline-only evidence: windows=%d gc=%d", noSampleWindows, gcOverlaps)
			}
			for _, brief := range []bool{false, true} {
				args := []string{"analyze", file, "--format", "md"}
				if brief {
					args = append(args, "--brief")
				}
				markdown, _, err := runPipeline(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				for _, content := range outputCodeSpans(markdown) {
					if strings.HasPrefix(content, "wkperf ") {
						if _, exists := commands[content]; !exists {
							commands[content] = nil
						}
					}
				}
				refs := regexp.MustCompile("\\[(metrics|rootcause): `([^`]+)`\\]")
				figure := regexp.MustCompile(`: (-?[0-9.e+]+) (?:ms|count|frames|s|%|ratio)\b`)
				impact := regexp.MustCompile(`; impact (-?[0-9.e+]+)ms`)
				duration := regexp.MustCompile(`Active duration: (-?[0-9.e+]+)s`)
				for _, line := range strings.Split(markdown, "\n") {
					if strings.HasPrefix(line, "|") {
						for _, content := range outputCodeSpans(line) {
							for _, prefix := range []string{"functions.", "modules.", "breakdown.", "samples.", "slowFrames.", "longTasks.", "commonStacks.", "triggers.", "wakeups.", "findings."} {
								if strings.HasPrefix(content, prefix) {
									command := outputScalarCommand(file, "metrics", content)
									if _, exists := commands[command]; !exists {
										commands[command] = nil
									}
									break
								}
							}
						}
					}
					for _, ref := range refs.FindAllStringSubmatch(line, -1) {
						command := outputScalarCommand(file, ref[1], ref[2])
						var expected *float64
						for _, pattern := range []*regexp.Regexp{figure, impact, duration} {
							if match := pattern.FindStringSubmatch(line); len(match) > 1 {
								number, err := strconv.ParseFloat(match[1], 64)
								if err != nil {
									t.Fatal(err)
								}
								expected = &number
								break
							}
						}
						if expected != nil {
							commands[command] = expected
						} else if _, exists := commands[command]; !exists {
							commands[command] = nil
						}
					}
				}
			}
			if len(commands) < 20 {
				t.Fatalf("insufficient reproduced evidence: %d commands", len(commands))
			}
			ordered := make([]string, 0, len(commands))
			for command := range commands {
				ordered = append(ordered, command)
			}
			sort.Strings(ordered)
			for _, command := range ordered {
				expected := commands[command]
				args := outputCommandArgs(t, command)
				result, stderr, err := runPipeline(t, args[1:]...)
				if err != nil {
					t.Errorf("printed command failed: %s\n%v\n%s", command, err, stderr)
					continue
				}
				if args[1] == "analyze" {
					document := decodeDocument(t, result)
					var reports []json.RawMessage
					if err := json.Unmarshal(document["reports"], &reports); err != nil || len(reports) == 0 {
						t.Errorf("printed analyze command returned no reports: %s: %v", command, err)
					}
					continue
				}
				var queryResult query.Result
				if err := json.Unmarshal([]byte(result), &queryResult); err != nil {
					t.Errorf("printed query returned invalid JSON: %s: %v", command, err)
					continue
				}
				if queryResult.Status != "ok" || len(queryResult.Rows) == 0 {
					t.Errorf("printed query returned no evidence: %s: status=%s rows=%d", command, queryResult.Status, len(queryResult.Rows))
				}
				if expected != nil && len(queryResult.Rows) > 0 {
					actual := queryResult.Rows[0].Value
					if math.Abs(actual-*expected) > math.Max(math.Abs(actual), math.Abs(*expected))*2e-5+1e-9 {
						t.Errorf("printed value differs: %s: want %.12g got %.12g", command, *expected, actual)
					}
				}
			}
			if samples {
				top, _, err := runPipeline(t, "query", file, "top", `/heartbeat|app\.js/`, "--format", "json")
				if err != nil {
					t.Fatal(err)
				}
				var functions query.Result
				if err := json.Unmarshal([]byte(top), &functions); err != nil {
					t.Fatal(err)
				}
				if len(functions.Rows) != 3 {
					t.Fatalf("expected duplicate-name and anonymous functions: %s", top)
				}
				for _, function := range functions.Rows {
					result, _, err := runPipeline(t, "query", file, "paths", function.ID)
					var paths query.Result
					if err != nil || json.Unmarshal([]byte(result), &paths) != nil || paths.Status != "ok" || len(paths.Rows) != 1 {
						t.Errorf("stable selector does not resolve paths: %s: %v %s", function.ID, err, result)
					}
				}
			}
		})
	}
}

func outputCodeSpans(text string) []string {
	var spans []string
	for _, line := range strings.Split(text, "\n") {
		for {
			start := strings.IndexByte(line, '`')
			if start < 0 {
				break
			}
			width := 1
			for start+width < len(line) && line[start+width] == '`' {
				width++
			}
			line = line[start+width:]
			end := strings.Index(line, strings.Repeat("`", width))
			if end < 0 {
				break
			}
			spans = append(spans, strings.TrimSpace(line[:end]))
			line = line[end+width:]
		}
	}
	return spans
}

func outputCommandArgs(t *testing.T, command string) []string {
	t.Helper()
	var args []string
	var value strings.Builder
	var quote rune
	started, escaped := false, false
	for _, ch := range command {
		if escaped {
			value.WriteRune(ch)
			escaped = false
			started = true
			continue
		}
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			} else {
				value.WriteRune(ch)
			}
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if quote == '"' {
			if ch == '"' {
				quote = 0
			} else {
				value.WriteRune(ch)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote, started = ch, true
		} else if unicode.IsSpace(ch) {
			if started {
				args = append(args, value.String())
				value.Reset()
				started = false
			}
		} else {
			value.WriteRune(ch)
			started = true
		}
	}
	if quote != 0 || escaped {
		t.Fatalf("printed command has incomplete shell quoting: %s", command)
	}
	if started {
		args = append(args, value.String())
	}
	if len(args) < 2 || args[0] != "wkperf" {
		t.Fatalf("invalid printed command: %s", command)
	}
	return args
}

func outputScalarCommand(file, operation, key string) string {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	return "wkperf query " + quote(file) + " " + operation + " " + quote(key) + " --format json"
}

func TestOutputMoreEvidenceCommand(t *testing.T) {
	data, err := json.Marshal(outputReproductionFixture())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "more-evidence.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runPipeline(t, "analyze", file, "--top", "1", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	type evidenceReport struct {
		MoreEvidenceCommand string `json:"moreEvidenceCommand"`
		RootCause           struct {
			AppAttribution      []json.RawMessage `json:"appAttribution"`
			AppAttributionTotal int               `json:"appAttributionTotal"`
		} `json:"rootCause"`
	}
	var selected struct {
		Reports []evidenceReport `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &selected); err != nil || len(selected.Reports) != 1 {
		t.Fatalf("selected report: %v", err)
	}
	report := selected.Reports[0]
	if len(report.RootCause.AppAttribution) != 1 || report.RootCause.AppAttributionTotal <= 1 {
		t.Fatal("fixture must omit application attribution evidence at --top 1")
	}
	if report.MoreEvidenceCommand == "" {
		t.Fatal("omitted evidence requires a runnable more-evidence command")
	}
	args := outputCommandArgs(t, report.MoreEvidenceCommand)
	if args[1] != "analyze" {
		t.Fatalf("more-evidence command must use existing analyze operation: %s", report.MoreEvidenceCommand)
	}
	out, stderr, err := runPipeline(t, args[1:]...)
	if err != nil {
		t.Fatalf("more-evidence command failed: %v %s", err, stderr)
	}
	var complete struct {
		Reports []evidenceReport `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &complete); err != nil || len(complete.Reports) != 1 {
		t.Fatalf("complete report: %v", err)
	}
	root := complete.Reports[0].RootCause
	if len(root.AppAttribution) != root.AppAttributionTotal || root.AppAttributionTotal != report.RootCause.AppAttributionTotal {
		t.Fatalf("more-evidence command did not recover omitted rows: got %d/%d want %d", len(root.AppAttribution), root.AppAttributionTotal, report.RootCause.AppAttributionTotal)
	}
}
