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

// resolvedVersion falls back to build info so that `go install ...@vX` reports
// the module version even when no link-time version was supplied.
func resolvedVersion() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func versionString() string {
	s := "wkperf " + resolvedVersion()
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
