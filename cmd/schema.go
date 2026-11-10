package cmd

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/schema"
)

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print complete JSON Schema contracts for machine-readable outputs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(schema.Document())
		},
	}
}
