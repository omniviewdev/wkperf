package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/mcp"
	"github.com/omniviewdev/wkperf/internal/query"
)

func newMCPCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "mcp",
		Short:   "Serve bounded recording queries over MCP stdio",
		Long:    "Serve newline-framed MCP JSON-RPC on stdin/stdout. Only protocol messages go to stdout. Each read-only tool accepts a local recording file, source maps, recording-relative window, and row limit; no network or shell is used.",
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindSourceMapFlags(cmd) },
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return (&mcp.Server{Version: resolvedVersion(), Sources: query.SourceOptions{Sourcemaps: viper.GetStringSlice("sourcemap"), Ownership: ownershipRules()}}).Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	addSourceMapFlags(c)
	return c
}
