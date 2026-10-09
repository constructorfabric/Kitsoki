package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type Supervised struct{}

// activityBuffer retains command output while non-blockingly notifying the
// waiter that the child made progress. Claude's stream-json is written to
// stdout, so this turns real trace activity into a renewable liveness signal.
type activityBuffer struct {
	data     bytes.Buffer
	activity *activitySignal
	semantic bool
	pending []byte
}

type activitySignal struct {
	last atomic.Int64
}

func (s *activitySignal) touch() {
	if s == nil {
		return
	}
	s.last.Store(time.Now().UnixNano())
}

func (b *activityBuffer) Write(p []byte) (int, error) {
	n, err := b.data.Write(p)
	if n > 0 && b.activity != nil {
		if b.semantic { b.touchSemantic(p[:n]) } else { b.activity.touch() }
	}
	return n, err
}

func (b *activityBuffer) touchSemantic(p []byte) {
	b.pending = append(b.pending, p...)
	for {
		i := bytes.IndexByte(b.pending, '\n'); if i < 0 { return }
		line := bytes.TrimSpace(b.pending[:i]); b.pending = b.pending[i+1:]
		if len(line) == 0 { continue }
		var event struct { Type string `json:"type"`; Subtype string `json:"subtype"` }
		if json.Unmarshal(line, &event) != nil || event.Type != "system" || event.Subtype != "thinking_tokens" { b.activity.touch() }
	}
}

func (b *activityBuffer) String() string { return b.data.String() }

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
	cmd.Env = supervisedEnv(spec.Env, home, cache, spec.InheritHome)
	applyProcessGroup(cmd)
	applyResourceLimits(cmd, spec.Resources)

	activity := &activitySignal{}
	activity.touch()
	var stdout, stderr activityBuffer
	stdout.activity = activity
	stdout.semantic = spec.SemanticActivity
	stderr.activity = activity
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		os.RemoveAll(tempRoot)
		return nil, AppliedPolicy{}, fmt.Errorf("agentruntime supervised: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		os.RemoveAll(tempRoot)
		return nil, AppliedPolicy{}, fmt.Errorf("agentruntime supervised: stderr pipe: %w", err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(tempRoot)
		return nil, AppliedPolicy{}, fmt.Errorf("agentruntime supervised: start: %w", err)
	}
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() { _, _ = io.Copy(&stdout, stdoutPipe); close(stdoutDone) }()
	go func() { _, _ = io.Copy(&stderr, stderrPipe); close(stderrDone) }()

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
		go func() {
			err := cmd.Wait()
			<-stdoutDone
			<-stderrDone
			done <- err
		}()
		var waitErr error
		killed := false
		killReason := ""
		var activityC <-chan time.Time
		var activityTicker *time.Ticker
		if spec.Resources.ActivityTimeout > 0 {
			// Poll a fraction of the requested bound instead of resetting a
			// shared timer from an exec I/O goroutine. That avoids the Go timer
			// receive/reset race exactly when output and expiry coincide.
			interval := spec.Resources.ActivityTimeout / 4
			if interval < time.Millisecond {
				interval = time.Millisecond
			}
			activityTicker = time.NewTicker(interval)
			activityC = activityTicker.C
			defer activityTicker.Stop()
		}
		for {
			select {
			case waitErr = <-done:
				goto finished
			case <-activityC:
				last := activity.last.Load()
				if last != 0 && time.Since(time.Unix(0, last)) < spec.Resources.ActivityTimeout {
					continue
				}
				killed = true
				killReason = "activity_timeout"
				killProcessGroup(cmd)
				waitErr = <-done
				goto finished
			case <-waitCtx.Done():
				killed = true
				killReason = "cancelled"
				killProcessGroup(cmd)
				waitErr = <-done
				goto finished
			case <-runCtx.Done():
				killed = true
				if runCtx.Err() == context.DeadlineExceeded {
					killReason = "timeout"
				} else {
					killReason = "cancelled"
				}
				killProcessGroup(cmd)
				waitErr = <-done
				goto finished
			}
		}
	finished:
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
			Stdout:     stdout.String(),
			Stderr:     stderr.String(),
			ExitCode:   exitCode,
			Killed:     killed,
			KillReason: killReason,
			Duration:   time.Since(start),
			FinalDiff:  diff,
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

func supervisedEnv(base []string, home, cache string, inheritHome bool) []string {
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
		if ok && (keep[k] || (inheritHome && k == "HOME") || allowedProviderEnv(k)) {
			out = append(out, kv)
		}
	}
	if !inheritHome {
		out = append(out,
			"HOME="+home,
			"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			"XDG_CACHE_HOME="+cache,
			"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		)
	}
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
