package cmd

import (
	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve bounded recording queries over MCP stdio",
		Long:  "Serve newline-framed MCP JSON-RPC on stdin/stdout. Only protocol messages go to stdout. Each read-only tool accepts a local recording file, source maps, recording-relative window, and row limit; no network or shell is used.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return (&mcp.Server{Version: resolvedVersion()}).Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
