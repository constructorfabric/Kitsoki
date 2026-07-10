package agentruntime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Supervised struct{}

func NewSupervised() *Supervised { return &Supervised{} }

func (s *Supervised) Name() string { return "supervised" }

func (s *Supervised) Probe(context.Context) Capabilities {
	return Capabilities{
		Backend:  s.Name(),
		Strength: StrengthSupervised,
		Features: []string{
			"process_group",
			"timeout_cancel",
			"env_allowlist",
			"temp_home",
			"final_diff",
		},
	}
}

func (s *Supervised) Launch(ctx context.Context, spec LaunchSpec) (*Running, AppliedPolicy, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, AppliedPolicy{}, fmt.Errorf("agentruntime supervised: command is required")
	}
	tempRoot, err := os.MkdirTemp("", "kitsoki-agent-runtime-*")
	if err != nil {
		return nil, AppliedPolicy{}, fmt.Errorf("agentruntime supervised: temp root: %w", err)
	}
	home := filepath.Join(tempRoot, "home")
	cache := filepath.Join(tempRoot, "cache")
	for _, p := range []string{home, cache} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			os.RemoveAll(tempRoot)
			return nil, AppliedPolicy{}, fmt.Errorf("agentruntime supervised: mkdir %s: %w", p, err)
		}
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if spec.Resources.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Resources.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}

	cmd := exec.CommandContext(runCtx, spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdin = spec.Stdin
	cmd.Env = supervisedEnv(spec.Env, home, cache)
	applyProcessGroup(cmd)
	applyResourceLimits(cmd, spec.Resources)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(tempRoot)
		return nil, AppliedPolicy{}, fmt.Errorf("agentruntime supervised: start: %w", err)
	}

	policy := AppliedPolicy{
		Backend:      s.Name(),
		Strength:     StrengthSupervised,
		MinStrength:  spec.EffectiveMin(),
		Repo:         spec.EffectiveRepo(),
		RW:           append([]string(nil), spec.RW...),
		Hidden:       append([]string(nil), spec.Hidden...),
		Network:      spec.EffectiveNetwork(),
		Degrade:      spec.EffectiveDegrade(),
		TempHome:     home,
		TempCacheDir: cache,
		Degraded:     supervisedDegradations(spec),
	}

	wait := func(waitCtx context.Context) (Result, error) {
		defer cancel()
		defer os.RemoveAll(tempRoot)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		var waitErr error
		killed := false
		select {
		case waitErr = <-done:
		case <-waitCtx.Done():
			killed = true
			killProcessGroup(cmd)
			waitErr = <-done
		case <-runCtx.Done():
			killed = true
			killProcessGroup(cmd)
			waitErr = <-done
		}
		exitCode := 0
		if waitErr != nil {
			if ee, ok := waitErr.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			} else {
				return Result{}, waitErr
			}
		}
		diff := ""
		if strings.TrimSpace(spec.RepoRoot) != "" {
			diff = captureGitDiff(waitCtx, spec.RepoRoot)
		}
		return Result{
			Stdout:    stdout.String(),
			Stderr:    stderr.String(),
			ExitCode:  exitCode,
			Killed:    killed,
			Duration:  time.Since(start),
			FinalDiff: diff,
		}, nil
	}
	return NewRunning(policy, wait), policy, nil
}

func supervisedDegradations(spec LaunchSpec) []string {
	var out []string
	if spec.EffectiveMin().Satisfies(StrengthFSConfined) {
		out = append(out, "supervised backend does not satisfy filesystem confinement")
	}
	if spec.EffectiveRepo() == RepoReadOnly || len(spec.RW) > 0 || len(spec.Hidden) > 0 {
		out = append(out, "filesystem policy recorded but not enforced by supervised backend")
	}
	if spec.EffectiveNetwork() != NetworkInherit {
		out = append(out, "network policy recorded but not enforced by supervised backend")
	}
	return out
}

func supervisedEnv(base []string, home, cache string) []string {
	keep := map[string]bool{
		"PATH":               true,
		"TMPDIR":             true,
		"LANG":               true,
		"LC_ALL":             true,
		"KITSOKI_SESSION_ID": true,
	}
	out := make([]string, 0, len(keep)+8)
	for _, kv := range base {
		k, _, ok := strings.Cut(kv, "=")
		if ok && (keep[k] || allowedProviderEnv(k)) {
			out = append(out, kv)
		}
	}
	out = append(out,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+cache,
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
	)
	return out
}

func allowedProviderEnv(k string) bool {
	prefixes := []string{
		"ANTHROPIC_",
		"OPENAI_",
		"CODEX_",
		"KITSOKI_",
		"GOOGLE_",
		"GEMINI_",
		"XAI_",
		"ZAI_",
		"GLM_",
		"HF_",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func captureGitDiff(ctx context.Context, repo string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "diff", "--")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}
