package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func newSampleCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sample",
		Short: "Write synthetic baseline/candidate exports to try the tool",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, _ := cmd.Flags().GetString("out")
			cmd.SilenceUsage = true
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			for i := 1; i <= 3; i++ {
				for _, g := range []struct {
					name string
					seed int64
					p    testgen.Params
				}{
					{"baseline", int64(100 + i), testgen.Baseline()},
					{"candidate", int64(200 + i), testgen.Improved()},
				} {
					path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", g.name, i))
					data, err := json.Marshal(testgen.Generate(g.seed, g.p))
					if err != nil {
						return err
					}
					if err := os.WriteFile(path, data, 0o644); err != nil {
						return err
					}
					fmt.Fprintln(cmd.OutOrStdout(), "wrote", path)
				}
			}
			// A deliberately different-length pair to try --normalize / windowing.
			long := testgen.Improved()
			long.DurationSec *= 1.5
			for _, g := range []struct {
				name string
				seed int64
				p    testgen.Params
			}{
				{"different-length-baseline", 301, testgen.Baseline()},
				{"different-length-candidate", 302, long},
			} {
				path := filepath.Join(dir, g.name+".json")
				data, err := json.Marshal(testgen.Generate(g.seed, g.p))
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "wrote", path)
			}
			if err := writeSampleScenes(cmd, dir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\ntry: wkperf audit %s/scenes\n", dir)
			fmt.Fprintf(cmd.OutOrStdout(), "try: wkperf analyze %s/baseline-1.json\n", dir)
			fmt.Fprintf(cmd.OutOrStdout(), "try: wkperf compare %s/different-length-baseline.json %s/different-length-candidate.json   (10s vs 15s; normalized per second)\n", dir, dir)
			fmt.Fprintf(cmd.OutOrStdout(), "try: wkperf compare %s/baseline-*.json --vs %s/candidate-*.json\n", dir, dir)
			return nil
		},
	}
	c.Flags().String("out", "wkperf-sample", "output directory")
	return c
}

// writeSampleScenes writes a demo audit tree: dir/scenes/<scene>/run-<n>.json.
// The scenes differ on purpose so the audit has something to find.
func writeSampleScenes(cmd *cobra.Command, dir string) error {
	fns := func(fs ...testgen.FuncWeight) []testgen.FuncWeight { return fs }
	fw := func(n, f string, line int, w float64) testgen.FuncWeight {
		return testgen.FuncWeight{Name: n, URL: "https://app.test/" + f, Line: line, Weight: w}
	}
	home := testgen.Improved()
	home.MemoryGrowthMBPerSec = 0.01

	scroll := testgen.Baseline() // janky, layout-heavy, forced layouts
	scroll.FrameMeanMs = 38
	scroll.FrameJitterMs = 10
	scroll.LayoutScale = 5
	scroll.ForcedLayoutRate = 0.4
	scroll.MemoryGrowthMBPerSec = 0.01
	scroll.Functions = fns(fw("measureRows", "table.js", 88, 45), fw("renderRow", "table.js", 140, 30), fw("handleScroll", "main.js", 210, 25))

	logs := testgen.Baseline() // memory leak
	logs.FrameMeanMs = 17
	logs.FrameJitterMs = 3
	logs.MemoryGrowthMBPerSec = 9
	logs.Functions = fns(fw("appendLine", "log.js", 52, 50), fw("parseJSON", "data.js", 8, 30), fw("trimBuffer", "log.js", 91, 20))

	settings := testgen.Improved() // long tasks
	settings.MemoryGrowthMBPerSec = 0.01
	settings.LongTaskRate = 0.04
	settings.Functions = fns(fw("validateSchema", "settings.js", 17, 60), fw("renderForm", "settings.js", 120, 25), fw("idleTick", "util.js", 5, 15))

	scenes := []struct {
		name string
		p    testgen.Params
	}{{"home", home}, {"table-scroll", scroll}, {"log-stream", logs}, {"settings", settings}}
	for si, sc := range scenes {
		for run := 1; run <= 3; run++ {
			p := sc.p
			p.DurationSec = 8 + float64(run)
			path := filepath.Join(dir, "scenes", sc.name, fmt.Sprintf("run-%d.json", run))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			data, err := json.Marshal(testgen.Generate(int64(400+si*10+run), p))
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "wrote", path)
		}
	}
	return nil
}
