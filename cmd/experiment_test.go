package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omniviewdev/wkperf/internal/workload"
)

// The helper is the recording producer, not the implementation under test.
// Re-executing the test binary keeps the workload portable (no shell or cp).
func TestExperimentCaptureProcess(t *testing.T) {
	if os.Getenv("WKPERF_EXPERIMENT_TEST_CHILD") != "yes" {
		return
	}
	args := os.Args[len(os.Args)-2:]
	end := args[0]
	switch end {
	case "nested":
		// Mirror a recorder that owns its driver in a separate process group.
		ctx, stop := signal.NotifyContext(t.Context(), syscall.SIGTERM)
		defer stop()
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestExperimentCaptureProcess$", "--", "block", args[1])
		cleanup := workload.Own(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if err := cmd.Wait(); err == nil || ctx.Err() == nil {
			t.Fatalf("nested driver was not canceled: %v", err)
		}
		return
	case "paired-baseline", "paired-candidate":
		run, _, _ := strings.Cut(filepath.Base(filepath.Dir(args[1])), "-")
		pair, err := strconv.Atoi(run)
		if err != nil || pair < 1 || pair > 3 {
			t.Fatalf("unexpected fixture pair %q: %v", run, err)
		}
		ends := [3]string{"4", "13", "23"}
		if end == "paired-candidate" {
			ends = [3]string{"6", "33", "14"}
		}
		end = ends[pair-1]
	case "block":
		tmp := args[1] + ".pid"
		if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, args[1]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		t.Fatal("workload was not canceled")
	}
	data := `{"startTime":1,"endTime":` + end + `,"records":[]}`
	if err := os.WriteFile(args[1], []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentUsesWithinPairOrder(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	dst := filepath.Join(dir, "experiment")
	planFile := filepath.Join(dir, "plan.json")
	argv := func(end string) []string {
		return []string{filepath.Base(executable), "-test.run=^TestExperimentCaptureProcess$", "--", end, "{recording}"}
	}
	plan := experimentPlan{
		Baseline: argv("paired-baseline"), Candidate: argv("paired-candidate"), Pairs: 3, Metrics: []string{"overview.duration_s"},
		Environment: map[string]string{
			"PATH":                         filepath.Dir(executable),
			"WKPERF_EXPERIMENT_TEST_CHILD": "yes",
		},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(planFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runExperiment(context.Background(), planFile, dst, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(dst, "experiment.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Runs    []experimentRun
		Changes map[string]pairedChange
	}
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range report.Runs {
		order = append(order, r.Variant)
	}
	if !reflect.DeepEqual(order, []string{"baseline", "candidate", "candidate", "baseline", "baseline", "candidate"}) {
		t.Fatalf("order: %v", order)
	}
	d := report.Changes["overview.duration_s"]
	// Group medians differ by 1; the median paired difference is 2.
	if !reflect.DeepEqual(d.Differences, []float64{2, 20, -9}) || d.Median != 2 {
		t.Fatalf("paired result: %+v", d)
	}
}

func TestExperimentRejectsAmbiguousPlanBeforeCreatingOutput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		metrics     []string
		environment map[string]string
		want        string
	}{
		{name: "duplicate metric", metrics: []string{"overview.duration_s", "overview.duration_s"}, want: "duplicate plan metric key"},
		{name: "blank metric", metrics: []string{" "}, want: "metric keys must not be blank"},
		{name: "invalid environment key", metrics: []string{"overview.duration_s"}, environment: map[string]string{"PATH=extra": "value"}, want: "invalid environment override"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			planFile, dst := filepath.Join(dir, "plan.json"), filepath.Join(dir, "experiment")
			data, err := json.Marshal(experimentPlan{
				Baseline: []string{"unused"}, Candidate: []string{"unused"}, Pairs: 1,
				Metrics: tc.metrics, Environment: tc.environment,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(planFile, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := runExperiment(t.Context(), planFile, dst, io.Discard); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q: %v", tc.want, err)
			}
			if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid plan created the destination: %v", err)
			}
		})
	}
}

func TestExperimentCLIProcess(t *testing.T) {
	if os.Getenv("WKPERF_EXPERIMENT_TEST_CLI") != "yes" {
		return
	}
	cmd := newExperimentCmd()
	cmd.SetArgs(os.Args[len(os.Args)-3:])
	if err := cmd.Execute(); !errors.Is(err, context.Canceled) {
		t.Fatalf("experiment after SIGTERM: %v", err)
	}
}

func TestExperimentSIGTERMReapsNestedWorkload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX signals")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "experiment")
	planFile := filepath.Join(dir, "plan.json")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{executable, "-test.run=^TestExperimentCaptureProcess$", "--", "nested", "{recording}"}
	plan := experimentPlan{
		Baseline: argv, Candidate: argv, Pairs: 1, Metrics: []string{"overview.duration_s"},
		Environment: map[string]string{"WKPERF_EXPERIMENT_TEST_CHILD": "yes"},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestExperimentCLIProcess$", "--", planFile, "--out", dst)
	cmd.Env = append(os.Environ(), "WKPERF_EXPERIMENT_TEST_CLI=yes")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ready := filepath.Join(dst, "0001-baseline", "recording.json")
	var child *os.Process
	for child == nil {
		data, err := os.ReadFile(ready)
		if err == nil {
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			child, err = os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Release()
			defer child.Kill()
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("workload did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := child.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("workload was not reaped: signal 0 returned %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "experiment.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("report after cancellation: %v", err)
	}
}

func TestPublishExperimentReportCleansUpFailedRename(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "experiment.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishExperimentReport(t.Context(), dir, []byte("{}\n")); err == nil {
		t.Fatal("renamed report over a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "experiment.json" {
		t.Fatalf("unexpected output directory contents: %v", entries)
	}
}
