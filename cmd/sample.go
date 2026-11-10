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
			fmt.Fprintf(cmd.OutOrStdout(), "\ntry: wkperf compare %s/baseline-*.json --vs %s/candidate-*.json\n", dir, dir)
			return nil
		},
	}
	c.Flags().String("out", "wkperf-sample", "output directory")
	return c
}
