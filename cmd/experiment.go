package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/workload"
	"github.com/spf13/cobra"
)

// Commands run directly, without shell expansion. Each writes one WebKit recording.
type experimentPlan struct {
	Baseline    []string          `json:"baseline"`
	Candidate   []string          `json:"candidate"`
	Pairs       int               `json:"pairs"`
	Metrics     []string          `json:"metrics"`
	Recording   string            `json:"recording,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Conditions  map[string]string `json:"conditions,omitempty"`
}

type experimentRun struct {
	Pair      int                       `json:"pair"`
	Variant   string                    `json:"variant"`
	Recording string                    `json:"recording"`
	Started   time.Time                 `json:"started"`
	ElapsedMS float64                   `json:"elapsedMs"`
	Metrics   map[string]metrics.Metric `json:"metrics"`
}

type pairedChange struct {
	Unit        metrics.Unit `json:"unit"`
	Differences []float64    `json:"candidateMinusBaseline"`
	Median      float64      `json:"median"`
}

func newExperimentCmd() *cobra.Command {
	var dest string
	c := &cobra.Command{Use: "experiment PLAN --out NEW_DIRECTORY", Short: "Run alternating baseline/candidate captures and report within-pair differences", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if dest == "" {
			return fmt.Errorf("--out must name a new experiment directory")
		}
		ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runExperiment(ctx, args[0], dest, c.ErrOrStderr())
	}}
	c.Flags().StringVar(&dest, "out", "", "new output directory (must not exist)")
	return c
}

func runExperiment(ctx context.Context, file, dest string, log io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var plan experimentPlan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("unexpected trailing plan data")
	}
	if plan.Pairs < 1 || len(plan.Metrics) == 0 {
		return fmt.Errorf("plan needs positive pairs and metric keys")
	}
	if plan.Recording == "" {
		plan.Recording = "recording.json"
	} else if !filepath.IsLocal(plan.Recording) || filepath.Clean(plan.Recording) != plan.Recording || plan.Recording == "." {
		return fmt.Errorf("recording must name a relative file inside each run directory")
	}
	for _, argv := range [][]string{plan.Baseline, plan.Candidate} {
		if len(argv) == 0 || argv[0] == "" {
			return fmt.Errorf("both capture commands required")
		}
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return err
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dest, "plan.json"), data, 0600); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	report := struct {
		Schema           string                   `json:"schema"`
		OS               string                   `json:"os"`
		Architecture     string                   `json:"architecture"`
		CPUs             int                      `json:"logicalCPUs"`
		HostDetails      map[string]string        `json:"hostDetails"`
		WorkingDirectory string                   `json:"workingDirectory"`
		Runs             []experimentRun          `json:"runs"`
		Changes          map[string]*pairedChange `json:"changes"`
	}{Schema: "wkperf-experiment/v1", OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU(), WorkingDirectory: cwd, Changes: map[string]*pairedChange{}}
	report.HostDetails = map[string]string{"goRuntime": runtime.Version()}
	// Capture the host, not user identifiers or the caller's environment.
	commands := map[string][]string{}
	if runtime.GOOS == "darwin" {
		commands["osBuild"] = []string{"sw_vers", "-buildVersion"}
		commands["hardware"] = []string{"sysctl", "-n", "hw.model"}
	} else if runtime.GOOS == "linux" {
		commands["kernel"] = []string{"uname", "-r"}
	}
	for key, argv := range commands {
		if out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output(); err == nil {
			report.HostDetails[key] = strings.TrimSpace(string(out))
		}
	}
	env := os.Environ()
	for k, v := range plan.Environment {
		env = append(env, k+"="+v)
	}
	for p := range plan.Pairs {
		var pair [2]map[string]metrics.Metric
		for step := range 2 {
			if err := ctx.Err(); err != nil {
				return err
			}
			i := (p%2 + step) % 2
			name := "baseline"
			argv := plan.Baseline
			if i == 1 {
				name = "candidate"
				argv = plan.Candidate
			}
			rel := fmt.Sprintf("%04d-%s", p+1, name)
			dir := filepath.Join(dest, rel)
			if err = os.Mkdir(dir, 0700); err != nil {
				return err
			}
			recording := filepath.Join(dir, plan.Recording)
			replace := strings.NewReplacer("{recording}", recording, "{dir}", dir)
			args := make([]string, len(argv))
			for j, s := range argv {
				args[j] = replace.Replace(s)
			}
			path, err := experimentExecutable(args[0], env)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			cmd := exec.CommandContext(ctx, path, args[1:]...)
			cmd.Args[0] = args[0]
			cmd.Stdout = log
			cmd.Stderr = log
			cmd.Env = env
			cmd.WaitDelay = 2 * time.Second
			start := time.Now()
			if err := runExperimentWorkload(ctx, cmd); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			elapsed := time.Since(start)
			r, err := model.Load(recording)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			s := analyze.Summarize(r, recording)
			pair[i] = make(map[string]metrics.Metric, len(plan.Metrics))
			for _, k := range plan.Metrics {
				m, ok := s.Lookup(k)
				if !ok {
					return fmt.Errorf("%s: metric %q unavailable", rel, k)
				}
				pair[i][k] = m
			}
			report.Runs = append(report.Runs, experimentRun{p + 1, name, filepath.Join(rel, plan.Recording), start.UTC(), float64(elapsed) / float64(time.Millisecond), pair[i]})
		}
		for k, b := range pair[0] {
			m := pair[1][k]
			d := report.Changes[k]
			if d == nil {
				d = &pairedChange{Unit: b.Unit}
				report.Changes[k] = d
			}
			d.Differences = append(d.Differences, m.Value-b.Value)
		}
	}
	for _, d := range report.Changes {
		v := slices.Clone(d.Differences)
		slices.Sort(v)
		n := len(v)
		d.Median = (v[(n-1)/2] + v[n/2]) / 2
	}
	result, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return publishExperimentReport(ctx, dest, append(result, '\n'))
}

func experimentExecutable(name string, env []string) (string, error) {
	if filepath.Base(name) != name || filepath.VolumeName(name) != "" {
		return name, nil
	}
	// Match os/exec's last-wins environment handling without changing the
	// runner's own PATH or looking up the name in it.
	var path string
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if key == "PATH" || runtime.GOOS == "windows" && strings.EqualFold(key, "PATH") {
			path = value
		}
	}
	for _, dir := range filepath.SplitList(path) {
		candidate := filepath.Join(dir, name)
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		resolved, err := exec.LookPath(absolute)
		if err == nil {
			if !filepath.IsAbs(candidate) {
				return "", &exec.Error{Name: name, Err: exec.ErrDot}
			}
			return resolved, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func runExperimentWorkload(ctx context.Context, cmd *exec.Cmd) error {
	cleanup := workload.OwnGracefully(cmd, 500*time.Millisecond)
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cleanup()
	// os/exec stops watching the context after the direct process exits,
	// even while a descendant still holds the output pipes open.
	stop := context.AfterFunc(ctx, cleanup)
	defer stop()
	err := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	return err
}

func publishExperimentReport(ctx context.Context, dest string, result []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(dest, ".wkperf-experiment-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(result)
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
	return os.Rename(f.Name(), filepath.Join(dest, "experiment.json"))
}
