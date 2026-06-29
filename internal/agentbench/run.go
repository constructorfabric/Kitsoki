package agentbench

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type RunOptions struct {
	ManifestPath string
	CaseID       string
	Trace        string
	Live         bool
}

type RunReport struct {
	Report
	Command        []string `json:"command,omitempty"`
	ExitCode       int      `json:"exit_code"`
	Stdout         string   `json:"stdout,omitempty"`
	Stderr         string   `json:"stderr,omitempty"`
	RunWallSeconds float64  `json:"run_wall_seconds,omitempty"`
}

func RunManifestCase(opts RunOptions) (RunReport, error) {
	m, err := LoadManifest(opts.ManifestPath)
	if err != nil {
		return RunReport{}, err
	}
	c, err := m.Case(opts.CaseID)
	if err != nil {
		return RunReport{}, err
	}
	if len(c.Run.Command) == 0 {
		return RunReport{}, fmt.Errorf("case %q has no run.command", c.ID)
	}
	if !opts.Live {
		return RunReport{}, fmt.Errorf("agent-bench run is live-gated; pass --live to execute %q", c.Run.Command[0])
	}

	timeout := 30 * time.Minute
	if c.Run.Timeout != "" {
		parsed, err := time.ParseDuration(c.Run.Timeout)
		if err != nil {
			return RunReport{}, fmt.Errorf("case %q run.timeout: %w", c.ID, err)
		}
		timeout = parsed
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	workdir := c.Run.Workdir
	if workdir == "" {
		workdir = filepath.Dir(opts.ManifestPath)
	}
	if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(filepath.Dir(opts.ManifestPath), workdir)
	}

	trace, traceErr := resolveCaseTrace(opts.ManifestPath, c, opts.Trace)
	if traceErr != nil {
		return RunReport{}, traceErr
	}
	if err := os.MkdirAll(filepath.Dir(trace), 0o755); err != nil {
		return RunReport{}, fmt.Errorf("prepare trace dir: %w", err)
	}
	if err := os.Remove(trace); err != nil && !os.IsNotExist(err) {
		return RunReport{}, fmt.Errorf("clean trace before run: %w", err)
	}

	start := time.Now()
	command := exec.CommandContext(ctx, c.Run.Command[0], c.Run.Command[1:]...)
	command.Dir = workdir
	command.Env = os.Environ()
	for key, val := range c.Run.Env {
		command.Env = append(command.Env, key+"="+val)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	elapsed := time.Since(start).Seconds()

	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("run timed out after %s", timeout)
	}

	report, scoreErr := ScoreTrace(trace, c)
	if scoreErr != nil {
		if err != nil {
			return RunReport{}, fmt.Errorf("%v; additionally failed to score trace: %w", err, scoreErr)
		}
		return RunReport{}, scoreErr
	}
	runReport := RunReport{
		Report:         report,
		Command:        append([]string(nil), c.Run.Command...),
		ExitCode:       exitCode,
		Stdout:         stdout.String(),
		Stderr:         stderr.String(),
		RunWallSeconds: elapsed,
	}
	if err != nil {
		runReport.Passed = false
		runReport.Failures = append([]string{err.Error()}, runReport.Failures...)
		return runReport, nil
	}
	return runReport, nil
}
