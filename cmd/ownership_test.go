package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/query"
)

func TestOwnershipFlagsAndConfig(t *testing.T) {
	for _, makeCommand := range []func() *cobra.Command{newAnalyzeCmd, newAuditCmd, newCompareCmd, newFlameCmd, newQueryCmd, newMCPCmd} {
		command := makeCommand()
		for _, flag := range []string{"app-url", "library-url"} {
			if command.Flags().Lookup(flag) == nil {
				t.Fatalf("%s lacks %s", command.Name(), flag)
			}
		}
	}
	file := queryFile(t)
	for _, config := range []bool{false, true} {
		t.Run(map[bool]string{false: "flags", true: "config"}[config], func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			command := newQueryCmd()
			args := []string{file, "top", "--app-url", "app.js", "--app-url", "unused/**", "--library-url", "vendor/**"}
			if config {
				viper.Set("app-url", []string{"app.js", "unused/**"})
				viper.Set("library-url", []string{"vendor/**"})
				args = []string{file, "top"}
			}
			command.SetArgs(args)
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&bytes.Buffer{})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			var result queryOutput[query.FunctionRow]
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			for _, row := range result.Rows {
				if result.Functions[row.FunctionID].Category != "app" {
					t.Fatalf("owner override lost: %+v", row)
				}
			}
			if strings.Count(strings.Join(result.Command, " "), "--app-url") != 2 {
				t.Fatalf("repeat rules lost: %v", result.Command)
			}
		})
	}
}

func TestOwnershipInvalidPatternFailsBeforeInput(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	for _, makeCommand := range []func() *cobra.Command{newAnalyzeCmd, newAuditCmd, newCompareCmd, newFlameCmd, newQueryCmd} {
		command := makeCommand()
		args := []string{"missing.json", "--app-url", "["}
		if command.Name() == "query" {
			args = append(args, "top")
		}
		if command.Name() == "compare" {
			args = append(args, "other.json")
		}
		command.SetArgs(args)
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "pattern") {
			t.Fatalf("%s must reject glob before input: %v", command.Name(), err)
		}
	}
}

func TestOwnershipRulesReachAnalysisAuditAndFocusedFlame(t *testing.T) {
	file := queryFile(t)
	for _, kind := range []string{"analyze", "audit", "flame"} {
		t.Run(kind, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			var command *cobra.Command
			args := []string{file, "--format", "json", "--app-url", "app.js", "--from", "0", "--to", "0.5"}
			switch kind {
			case "analyze":
				command = newAnalyzeCmd()
			case "audit":
				command = newAuditCmd()
			case "flame":
				command = newFlameCmd()
				args = append(args, "--focus", "root", "--invert")
			}
			command.SetArgs(args)
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&bytes.Buffer{})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			var analysis map[string]any
			switch kind {
			case "analyze":
				analysis = value["reports"].([]any)[0].(map[string]any)
			case "audit":
				analysis = value["report"].(map[string]any)["scenes"].([]any)[0].(map[string]any)["analysis"].(map[string]any)
			case "flame":
				analysis = value
			}
			frames := analysis["frames"].([]any)
			for _, raw := range frames {
				frame := raw.(map[string]any)
				if frame["url"] != "" && frame["category"] != "app" {
					t.Fatalf("%s loses owner across window/focus/invert: %v", kind, frame)
				}
			}
			if kind != "flame" {
				ownership := analysis["ownership"].(map[string]any)
				if len(ownership["appURLs"].([]any)) != 1 {
					t.Fatalf("%s omits effective rules", kind)
				}
				root := analysis["rootCause"].(map[string]any)
				if _, ok := root["contextAttribution"]; !ok {
					t.Fatal("legacy attribution wire")
				}
			}
		})
	}
}
