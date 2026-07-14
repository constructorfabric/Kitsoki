package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CapsuleCICommandRunner is injected so project-wrapper flow tests never fork
// real commands or spend on an LLM. Production deliberately uses the
// project-owned command string through a shell: project profiles are the same
// trust boundary as a Makefile or package script, and quoting must retain its
// normal meaning.
type CapsuleCICommandRunner interface {
	Run(context.Context, string, string) (string, int, error)
}

type CapsuleCICommandRunnerFunc func(context.Context, string, string) (string, int, error)

func (f CapsuleCICommandRunnerFunc) Run(ctx context.Context, workdir, command string) (string, int, error) {
	return f(ctx, workdir, command)
}

type shellCapsuleCICommandRunner struct{}

func (shellCapsuleCICommandRunner) Run(ctx context.Context, workdir, command string) (string, int, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "sh", "-c", command)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0, nil
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return string(out), exit.ExitCode(), nil
	}
	return string(out), -1, err
}

// NewCapsuleCIProjectChecksHandler returns the deterministic host used by the
// generated project CI wrapper. It reads the checked-in project profile, runs
// its test/build commands, persists bounded local evidence, and constructs the
// typed verdict. The story remains the CI pipeline: this host is one ordinary
// deterministic fact producer, not a second step-DAG runtime.
func NewCapsuleCIProjectChecksHandler(runner CapsuleCICommandRunner) Handler {
	if runner == nil {
		runner = shellCapsuleCICommandRunner{}
	}
	return func(ctx context.Context, args map[string]any) (Result, error) {
		workdir, err := filepath.Abs(capsuleCIStringArg(args, "workdir", "."))
		if err != nil {
			return Result{}, err
		}
		profilePath := filepath.Join(workdir, ".kitsoki", "project-profile.yaml")
		raw, err := os.ReadFile(profilePath)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.capsule_ci.project_checks: read project profile: %v", err), FailureKind: FailureFatal}, nil
		}
		var profile map[string]any
		if err := yaml.Unmarshal(raw, &profile); err != nil {
			return Result{Error: fmt.Sprintf("host.capsule_ci.project_checks: parse project profile: %v", err), FailureKind: FailureFatal}, nil
		}
		commands, _ := profile["commands"].(map[string]any)
		if commands == nil {
			commands = map[string]any{}
		}
		jobID := safeArtifactID(capsuleCIStringArg(args, "job_id", "run"))
		evidenceRel := filepath.ToSlash(filepath.Join(".artifacts", "capsule-ci", "checks", jobID+".json"))
		evidenceRef := "file:" + evidenceRel
		checks := make([]map[string]any, 0, 2)
		commandEvidence := make([]map[string]any, 0, 2)
		allPassed := true
		for _, check := range []struct {
			id  string
			key string
		}{{id: "tests", key: "test"}, {id: "build", key: "build"}} {
			command := strings.TrimSpace(fmt.Sprint(commands[check.key]))
			if command == "" || command == "<nil>" {
				continue
			}
			started := time.Now().UTC()
			log, exitCode, runErr := runner.Run(ctx, workdir, command)
			outcome := "passed"
			if runErr != nil || exitCode != 0 {
				outcome = "failed"
				allPassed = false
			}
			checks = append(checks, map[string]any{"id": check.id, "kind": "deterministic", "outcome": outcome, "evidence": []string{evidenceRef + "#" + check.id}})
			entry := map[string]any{"id": check.id, "command": command, "exit_code": exitCode, "outcome": outcome, "started_at": started, "finished_at": time.Now().UTC(), "log": boundedCheckLog(log)}
			if runErr != nil {
				entry["error"] = runErr.Error()
			}
			commandEvidence = append(commandEvidence, entry)
		}
		outcome := "passed"
		summary := "All declared project test and build commands passed."
		promotionEligible := allPassed
		if len(checks) == 0 {
			outcome = "needs_input"
			summary = "Project profile declares no test or build command."
			promotionEligible = false
		} else if !allPassed {
			outcome = "failed"
			summary = "One or more declared project commands failed."
		}
		artifact := map[string]any{"schema": "capsule-ci-project-checks/v1", "job_id": capsuleCIStringArg(args, "job_id", ""), "profile": filepath.ToSlash(filepath.Join(".kitsoki", "project-profile.yaml")), "checks": commandEvidence, "outcome": outcome}
		if err := writeCapsuleCIEvidence(filepath.Join(workdir, filepath.FromSlash(evidenceRel)), artifact); err != nil {
			return Result{}, fmt.Errorf("host.capsule_ci.project_checks: write evidence: %w", err)
		}
		verdict := map[string]any{
			"schema":             "capsule-ci-verdict/v1",
			"pipeline":           capsuleCIStringArg(args, "pipeline", ""),
			"outcome":            outcome,
			"summary":            summary,
			"checks":             checks,
			"promotion_eligible": promotionEligible,
			"source_digest":      capsuleCIStringArg(args, "source_digest", ""),
			"story_digest":       capsuleCIStringArg(args, "story_digest", ""),
			"environment_digest": capsuleCIStringArg(args, "environment_digest", ""),
			"envelope_digest":    capsuleCIStringArg(args, "envelope_digest", ""),
		}
		return Result{Data: map[string]any{"ok": allPassed && len(checks) > 0, "checks": checks, "evidence": evidenceRef, "verdict": verdict}}, nil
	}
}

var CapsuleCIProjectChecksHandler = NewCapsuleCIProjectChecksHandler(nil)

func capsuleCIStringArg(args map[string]any, key, fallback string) string {
	value := strings.TrimSpace(fmt.Sprint(args[key]))
	if value == "" || value == "<nil>" {
		return fallback
	}
	return value
}

func safeArtifactID(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "run"
	}
	return b.String()
}

func boundedCheckLog(value string) string {
	const max = 1 << 20
	if len(value) > max {
		return value[len(value)-max:]
	}
	return value
}

func writeCapsuleCIEvidence(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
