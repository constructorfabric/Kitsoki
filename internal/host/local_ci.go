// Package host — host.local — build/test runner provider.
//
// Implements the `ci` host_interface (see docs/architecture/hosts.md).  A
// single prefix-fallback handler dispatches the three ci ops via the
// `op` arg.
//
// The handler defaults to `go test ./...` for run_tests and `go build
// ./...` for build, but a per-world override can be supplied via the
// args.test_cmd / args.build_cmd keys (stories thread `world.test_cmd`
// into the invocation).  `remote_status` has no local meaning — it
// returns a clean "not implemented" Result.Error so YAML on_error:
// routing fires; remote CI plugs in later (Phase 5+).
package host

import (
	"context"
	"fmt"
	"strings"
)

// LocalCIHandler implements host.local (prefix-fallback).
//
// Required args:
//   - op (string): one of run_tests, build, remote_status.
//
// Optional args (per op):
//   - workdir   (string): cwd for the command.
//   - target    (string): forwarded as an argv suffix (e.g. a package path).
//   - test_cmd  (string): override the default `go test ./...`.
//   - build_cmd (string): override the default `go build ./...`.
func LocalCIHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	op = strings.TrimSpace(op)
	if op == "" {
		return Result{Error: "host.local: op argument is required"}, nil
	}
	workdir, _ := args["workdir"].(string)
	switch op {
	case "run_tests":
		return ciRunTests(ctx, workdir, args)
	case "build":
		return ciBuild(ctx, workdir, args)
	case "remote_status":
		// Local CI has no remote — the PR provider owns this op in
		// phases 5+.  Return a clean domain error.
		return Result{Error: "host.local: remote_status not supported by local CI"}, nil
	default:
		return Result{Error: fmt.Sprintf("host.local: unknown op %q", op)}, nil
	}
}

// ciRunTests runs the test command and reports pass/fail counts as best
// it can.  When the command exits cleanly we count tests via the
// classic `go test` `--- PASS:` / `--- FAIL:` lines if present;
// otherwise pass/fail just reflect the binary exit code.
func ciRunTests(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	cmd, cmdArgs := splitOverride(args, "test_cmd", []string{"go", "test", "./..."})
	if target, _ := args["target"].(string); target != "" {
		cmdArgs = append(cmdArgs, target)
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, cmd, cmdArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("ci.run_tests: exec: %v", err)}, nil
	}
	log := stdout
	if stderr != "" {
		log = log + stderr
	}
	passed, failed := countGoTestResults(log)
	return Result{Data: map[string]any{
		"ok":     code == 0,
		"passed": passed,
		"failed": failed,
		"log":    log,
		"junit":  "",
	}}, nil
}

// ciBuild runs `go build ./...` (or the override) and reports the log.
func ciBuild(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	cmd, cmdArgs := splitOverride(args, "build_cmd", []string{"go", "build", "./..."})
	if target, _ := args["target"].(string); target != "" {
		cmdArgs = append(cmdArgs, target)
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, cmd, cmdArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("ci.build: exec: %v", err)}, nil
	}
	log := stdout
	if stderr != "" {
		log = log + stderr
	}
	return Result{Data: map[string]any{
		"ok":  code == 0,
		"log": log,
	}}, nil
}

// splitOverride consults args[key] for a shell-style override.  When
// present it splits on whitespace and returns (cmd, argv) ready to
// hand to the runner.  When absent the supplied default is returned.
//
// The command is fork/exec'd directly (no shell), so a leading run of
// `NAME=VALUE` tokens — the POSIX env-prefix form a user reasonably
// writes, e.g. `CARGO_TARGET_DIR=/tmp/t cargo test` — would otherwise be
// fork/exec'd as a binary literally named `CARGO_TARGET_DIR=...` and fail
// `no such file or directory`.  To match the least-surprising shell
// behaviour without taking on full `sh -c` semantics, when the override
// begins with one or more env assignments we route the whole thing
// through `/usr/bin/env`, which applies the assignments then execs the
// rest — exactly `env NAME=VALUE cmd args`.
func splitOverride(args map[string]any, key string, def []string) (string, []string) {
	override, _ := args[key].(string)
	override = strings.TrimSpace(override)
	if override == "" {
		return def[0], def[1:]
	}
	parts := strings.Fields(override)
	if len(parts) == 0 {
		return def[0], def[1:]
	}
	if isEnvAssignment(parts[0]) {
		// `env CARGO_TARGET_DIR=x cargo test ...` — env consumes the
		// leading NAME=VALUE tokens and execs the remainder.
		return "env", parts
	}
	return parts[0], parts[1:]
}

// isEnvAssignment reports whether tok is a shell env-prefix assignment
// (`NAME=VALUE`): a `=` with a non-empty name to its left whose chars are
// all valid in a C identifier (letters, digits, underscore; not leading
// digit).  Guards against treating an arg like `--flag=val` as an
// assignment.
func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	name := tok[:eq]
	for i, r := range name {
		isLetter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit {
			return false
		}
		if i == 0 && isDigit {
			return false
		}
	}
	return true
}

// countGoTestResults grovels through `go test`'s human output for
// `--- PASS:` / `--- FAIL:` markers.  Approximate — production CI would
// emit JSON via `go test -json`, but this handler keeps the dependency
// to plain `go test` so a default invocation works without flags.
func countGoTestResults(log string) (int, int) {
	pass, fail := 0, 0
	for _, line := range strings.Split(log, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "--- PASS:"):
			pass++
		case strings.HasPrefix(strings.TrimSpace(line), "--- FAIL:"):
			fail++
		}
	}
	return pass, fail
}
