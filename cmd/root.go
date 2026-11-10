// Package cmd implements the wkperf command line interface.
package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ExitError asks main to exit with a specific code.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "wkperf",
		Short:         "Compare WebKit Web Inspector Timelines exports",
		Long:          "wkperf compares two groups of WebKit Web Inspector Timelines exports (baseline vs candidate) and reports regressions and improvements.",
		SilenceUsage:  false,
		SilenceErrors: true,
	}
	root.AddCommand(newCompareCmd(), newSummaryCmd(), newSampleCmd(), newVersionCmd())
	return root
}

// Execute runs the CLI with the given arguments (without the program name).
func Execute(args []string) error {
	initConfig()
	root := newRootCmd()
	root.SetArgs(expandVS(args))
	return root.Execute()
}

// initConfig wires env vars (WKPERF_*) and the optional config file.
func initConfig() {
	viper.SetEnvPrefix("WKPERF")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	candidates := []string{".wkperf.yaml"}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "wkperf", "config.yaml"))
	} else if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "wkperf", "config.yaml"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			viper.SetConfigFile(c)
			_ = viper.ReadInConfig()
			return
		}
	}
}

// expandVS rewrites `--vs a b c` into `--vs=a --vs=b --vs=c` so candidate
// files can be listed after one flag. Consumption stops at the next flag.
func expandVS(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			out = append(out, args[i:]...)
			break
		}
		if args[i] != "--vs" {
			out = append(out, args[i])
			continue
		}
		j := i + 1
		for j < len(args) && !strings.HasPrefix(args[j], "-") {
			out = append(out, "--vs="+args[j])
			j++
		}
		if j == i+1 {
			out = append(out, "--vs") // let cobra report the missing value
		}
		i = j - 1
	}
	return out
}
