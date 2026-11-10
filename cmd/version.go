package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Set at link time via -ldflags "-X github.com/omniviewdev/wkperf/cmd.Version=...".
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// versionString resolves the version, falling back to build info so that
// `go install ...@vX` reports the module version.
func versionString() string {
	v := Version
	if v == "" {
		v = "dev"
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	s := "wkperf " + v
	if Commit != "" {
		s += " (" + Commit + ")"
	}
	if Date != "" {
		s += " built " + Date
	}
	return s
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the wkperf version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
		},
	}
}
