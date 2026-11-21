package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/inspector"
)

func newRecordCmd() *cobra.Command {
	var endpoint, out, script string
	var duration time.Duration
	command := &cobra.Command{
		Use:   "record --endpoint <ws-url> --out <profile.json> [--duration <time> | --script <file> | -- <command> [args...]]",
		Short: "Record a WebKit Web Inspector target",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 && cmd.ArgsLenAtDash() != 0 {
				return fmt.Errorf("put the workload command after --")
			}
			if cmd.ArgsLenAtDash() == 0 && len(args) == 0 {
				return fmt.Errorf("expected a workload command after --")
			}
			if cmd.Flags().Changed("duration") {
				if duration <= 0 {
					return fmt.Errorf("--duration must be positive")
				}
			}
			if cmd.Flags().Changed("script") && script == "" {
				return fmt.Errorf("--script requires a file path")
			}
			if (duration != 0 && (len(args) != 0 || script != "")) || (len(args) != 0 && script != "") {
				return fmt.Errorf("--duration, --script, and a workload command are mutually exclusive")
			}
			if endpoint == "" || out == "" || out == "-" {
				return fmt.Errorf("--endpoint and --out <profile.json> are required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			f, err := os.CreateTemp(filepath.Dir(out), ".wkperf-record-*")
			if err != nil {
				return err
			}
			defer os.Remove(f.Name())
			err = inspector.Capture(ctx, inspector.Options{
				Endpoint: endpoint,
				Duration: duration,
				Workload: args,
				Script:   script,
				Output:   cmd.ErrOrStderr(),
			}, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return os.Rename(f.Name(), out)
		},
	}
	command.Flags().StringVar(&endpoint, "endpoint", "", "WebKit Inspector target WebSocket URL")
	command.Flags().StringVar(&out, "out", "", "output recording JSON path")
	command.Flags().DurationVar(&duration, "duration", 0, "capture duration (default 10s without a workload)")
	command.Flags().StringVar(&script, "script", "", "run a trusted JavaScript file in the target and wait for it to complete")
	return command
}
