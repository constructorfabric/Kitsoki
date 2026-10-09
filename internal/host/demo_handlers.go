// Package host — host.demo.* (A2, ~/code/POG/.context/use-case-loop-plan.md
// §3.5): exec wrappers around kitsoki's own frontend-mockup-mcp pipeline
// scripts (tools/frontend-mockup-mcp/scripts/{create-mockup,record-tour,
// demo-doctor}.mjs — see .context/mockup-demo-tooling-contract.md for their
// frozen CLI/JSON contracts). starlark deliberately has no shell exec, so a
// story that wants "project → mockup → tours → capture → doctor" needs a
// Go-side verb to invoke node. DemoHandler is that verb.
//
// Three ops, registered bare at "host.demo" (longest-prefix convention, see
// host.graph):
//
//	create {scenario_path, out_path[, manifest, renderer]}      -> mockup HTML (+ starter manifest/tours/deck when manifest:true)
//	record {manifest_path}                                       -> capture all tours for a manifest, re-estimate, doctor
//	doctor {manifest_path[, json]}                                -> demo-doctor's pass/fail report
//
// Script-root resolution contract (generalizes checks.sh's POG_KITSOKI_SRC
// convention to a Go-side, per-call resolution order):
//  1. kit-relative: args["_kit_dir"]/scripts/<name> when a kit dispatched
//     this call (kitendpoint injects _kit_dir the same way host.graph.
//     presentation's starlark resolution does).
//  2. configured tool root: KITSOKI_DEMO_SCRIPTS_ROOT env var if set,
//     else <KITSOKI_SRC or self-locate>/tools/frontend-mockup-mcp/scripts.
//
// DemoRunner is an injectable seam so tests exercise the verb's argument
// wiring, script resolution and JSON-parsing WITHOUT spawning node or a
// browser — the Go-side half of "cassette these verbs" (§3.5): a fake
// runner stands in for the real exec the same way WithClaudeRunner stands
// in for a real claude exec elsewhere in this package.
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DemoRunner executes a node script and returns its stdout/stderr. The
// default implementation (defaultDemoRunner) shells out to `node`; tests
// substitute a fake that returns canned output.
type DemoRunner interface {
	Run(ctx context.Context, scriptPath string, args []string, dir string) (stdout, stderr []byte, err error)
}

type execDemoRunner struct{}

func (execDemoRunner) Run(ctx context.Context, scriptPath string, args []string, dir string) ([]byte, []byte, error) {
	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.CommandContext(ctx, "node", cmdArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// demoRunner is package-level so tests can swap it (WithDemoRunner) without
// threading a runner through every call site; restored via the returned
// cleanup func, mirroring the agent backend test seams in this package.
var demoRunner DemoRunner = execDemoRunner{}

// WithDemoRunner overrides the runner for the duration of a test; returns a
// restore func. Not safe for concurrent tests (package-level var), matching
// the existing WithClaudeRunner/WithCopilotRunner convention.
func WithDemoRunner(r DemoRunner) func() {
	prev := demoRunner
	demoRunner = r
	return func() { demoRunner = prev }
}

const demoScriptsRootEnv = "KITSOKI_DEMO_SCRIPTS_ROOT"

// resolveDemoScript implements the script-root resolution contract: kit-
// relative first (args["_kit_dir"]/scripts/<name>, only when that file
// exists — a kit that doesn't ship the pipeline scripts falls through
// rather than erroring here), then the configured tool root.
func resolveDemoScript(args map[string]any, name string) (string, error) {
	if kitDir, _ := args["_kit_dir"].(string); kitDir != "" {
		candidate := filepath.Join(kitDir, "scripts", name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	root := os.Getenv(demoScriptsRootEnv)
	if root == "" {
		src := os.Getenv("KITSOKI_SRC")
		if src == "" {
			// Self-locate: this file lives at <kitsoki-root>/internal/host/.
			if wd, err := os.Getwd(); err == nil {
				src = wd
			}
		}
		root = filepath.Join(src, "tools", "frontend-mockup-mcp", "scripts")
	}
	candidate := filepath.Join(root, name)
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("host.demo: script %q not found (checked kit-relative and %s=%q); set %s or run from a kitsoki checkout", name, demoScriptsRootEnv, root, demoScriptsRootEnv)
	}
	return candidate, nil
}

// DemoHandler implements the host.demo.* multi-op verb (A2).
func DemoHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	switch op {
	case "create":
		return demoCreateOp(ctx, args)
	case "record":
		return demoRecordOp(ctx, args)
	case "doctor":
		return demoDoctorOp(ctx, args)
	default:
		return Result{}, fmt.Errorf("host.demo: unknown op %q (want one of create, record, doctor)", op)
	}
}

func demoStringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

// demoCreateOp: {scenario_path, out_path[, manifest, renderer]} -> runs
// create-mockup.mjs. With manifest:true, also emits the starter *.demo.json
// + tours + deck (contract §4/§6) so record/doctor have something to act on.
func demoCreateOp(ctx context.Context, args map[string]any) (Result, error) {
	scenarioPath := demoStringArg(args, "scenario_path")
	outPath := demoStringArg(args, "out_path")
	if scenarioPath == "" || outPath == "" {
		return Result{}, fmt.Errorf("host.demo.create: requires both 'scenario_path' and 'out_path'")
	}
	script, err := resolveDemoScript(args, "create-mockup.mjs")
	if err != nil {
		return Result{}, err
	}
	cliArgs := []string{scenarioPath, outPath}
	manifest, _ := args["manifest"].(bool)
	if manifest {
		cliArgs = append(cliArgs, "--manifest")
	}
	if renderer := demoStringArg(args, "renderer"); renderer != "" {
		cliArgs = append(cliArgs, "--renderer", renderer)
	}
	stdout, stderr, err := demoRunner.Run(ctx, script, cliArgs, "")
	if err != nil {
		return Result{Error: fmt.Sprintf("host.demo.create: %v: %s", err, string(stderr))}, nil
	}
	return Result{Data: map[string]any{
		"out_path": outPath,
		"stdout":   string(stdout),
	}}, nil
}

// demoRecordOp: {manifest_path} -> runs record-tour.mjs (capture all tours,
// re-estimate, run doctor — the closed loop per contract §6).
func demoRecordOp(ctx context.Context, args map[string]any) (Result, error) {
	manifestPath := demoStringArg(args, "manifest_path")
	if manifestPath == "" {
		return Result{}, fmt.Errorf("host.demo.record: missing required arg 'manifest_path'")
	}
	script, err := resolveDemoScript(args, "record-tour.mjs")
	if err != nil {
		return Result{}, err
	}
	stdout, stderr, err := demoRunner.Run(ctx, script, []string{manifestPath}, "")
	if err != nil {
		return Result{Error: fmt.Sprintf("host.demo.record: %v: %s", err, string(stderr))}, nil
	}
	return Result{Data: map[string]any{
		"manifest_path": manifestPath,
		"stdout":        string(stdout),
	}}, nil
}

// demoDoctorOp: {manifest_path} -> runs demo-doctor.mjs --json and parses
// its machine report (contract §5); Result.Error is set (not a Go error)
// when the report itself says any check FAILed, so a story can branch on it
// without treating "doctor found a problem" as an infra failure.
func demoDoctorOp(ctx context.Context, args map[string]any) (Result, error) {
	manifestPath := demoStringArg(args, "manifest_path")
	if manifestPath == "" {
		return Result{}, fmt.Errorf("host.demo.doctor: missing required arg 'manifest_path'")
	}
	script, err := resolveDemoScript(args, "demo-doctor.mjs")
	if err != nil {
		return Result{}, err
	}
	stdout, stderr, runErr := demoRunner.Run(ctx, script, []string{manifestPath, "--json"}, "")

	var report map[string]any
	if jsonErr := json.Unmarshal(stdout, &report); jsonErr != nil {
		if runErr != nil {
			return Result{Error: fmt.Sprintf("host.demo.doctor: %v: %s", runErr, string(stderr))}, nil
		}
		return Result{}, fmt.Errorf("host.demo.doctor: could not parse --json report: %v (stdout: %s)", jsonErr, string(stdout))
	}

	data := map[string]any{"report": report}
	ok, _ := report["ok"].(bool)
	if !ok && runErr != nil {
		data["stderr"] = string(stderr)
		return Result{Data: data, Error: "host.demo.doctor: one or more checks failed"}, nil
	}
	return Result{Data: data}, nil
}
