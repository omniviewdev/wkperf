package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func newSummaryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "summary FILES...",
		Short: "Print key metrics for one group of recordings (median across runs)",
		Args:  cobra.MinimumNArgs(1),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			return bindFlags(cmd, windowKeys...)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Flags().GetString("format")
			if format != "term" && format != "json" {
				return fmt.Errorf("unknown --format %q (want term|json)", format)
			}
			cmd.SilenceUsage = true
			loaded, files, err := loadGroup(args, windowOpts())
			sums := summaries(loaded)
			if err != nil {
				return err
			}
			rows := medianMetrics(sums)
			w := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(struct {
					Sources []string         `json:"sources"`
					Runs    int              `json:"runs"`
					Metrics []metrics.Metric `json:"metrics"`
				}{files, len(sums), rows})
			}
			fmt.Fprintf(w, "%d run(s): %s\n\n", len(files), strings.Join(files, ", "))
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			group := ""
			for _, m := range rows {
				if m.Group != group {
					group = m.Group
					fmt.Fprintf(tw, "\n[%s]\t\n", group)
				}
				fmt.Fprintf(tw, "  %s\t%s\n", m.Label, formatValue(m))
			}
			return tw.Flush()
		},
	}
	c.Flags().String("format", "term", "output format: term|json")
	addWindowFlags(c)
	return c
}

// medianMetrics returns one metric per key with Value set to the median
// across summaries. Order follows first appearance.
func medianMetrics(sums []*metrics.Summary) []metrics.Metric {
	var order []string
	first := map[string]metrics.Metric{}
	vals := map[string][]float64{}
	for _, s := range sums {
		for _, m := range s.Metrics {
			if _, ok := first[m.Key]; !ok {
				first[m.Key] = m
				order = append(order, m.Key)
			}
			vals[m.Key] = append(vals[m.Key], m.Value)
		}
	}
	out := make([]metrics.Metric, 0, len(order))
	for _, k := range order {
		m := first[k]
		m.Value = median(vals[k])
		out = append(out, m)
	}
	return out
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func formatValue(m metrics.Metric) string {
	switch m.Unit {
	case metrics.UnitMs:
		return fmt.Sprintf("%.2f ms", m.Value)
	case metrics.UnitPct:
		return fmt.Sprintf("%.1f %%", m.Value)
	case metrics.UnitRatio:
		return fmt.Sprintf("%.1f %%", m.Value*100)
	case metrics.UnitFPS:
		return fmt.Sprintf("%.1f fps", m.Value)
	case metrics.UnitMB:
		return fmt.Sprintf("%.2f MB", m.Value)
	case metrics.UnitBytes:
		return humanBytes(m.Value)
	default:
		return fmt.Sprintf("%.0f", m.Value)
	}
}

func humanBytes(b float64) string {
	units := []string{"B", "KiB", "MiB", "GiB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}
