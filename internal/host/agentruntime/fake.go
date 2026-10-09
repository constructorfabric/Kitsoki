package agentruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FakeWriteAttempt struct {
	Path    string
	Content string
}

type Fake struct {
	Backend      string
	Strength     Strength
	LaunchResult Result
	LaunchError  error
	Seen         []LaunchSpec
	EnforceFS    bool
	Writes       []FakeWriteAttempt
}

func NewFake(strength Strength) *Fake {
	if strength == "" {
		strength = StrengthFSConfined
	}
	return &Fake{Backend: "fake", Strength: strength}
}

func (f *Fake) Name() string {
	if f.Backend != "" {
		return f.Backend
	}
	return "fake"
}

func (f *Fake) Probe(context.Context) Capabilities {
	st := f.Strength
	if st == "" {
		st = StrengthFSConfined
	}
	return Capabilities{Backend: f.Name(), Strength: st, Features: []string{"fake"}}
}

func (f *Fake) Launch(ctx context.Context, spec LaunchSpec) (*Running, AppliedPolicy, error) {
	f.Seen = append(f.Seen, spec)
	if f.LaunchError != nil {
		return nil, AppliedPolicy{}, f.LaunchError
	}
	policy := AppliedPolicy{
		Backend:     f.Name(),
		Strength:    f.Probe(ctx).Strength,
		MinStrength: spec.EffectiveMin(),
		Repo:        spec.EffectiveRepo(),
		RW:          append([]string(nil), spec.RW...),
		Hidden:      append([]string(nil), spec.Hidden...),
		Network:     spec.EffectiveNetwork(),
		Degrade:     spec.EffectiveDegrade(),
	}
	if !policy.Strength.Satisfies(policy.MinStrength) {
		policy.Degraded = append(policy.Degraded, fmt.Sprintf("fake strength %s below min %s", policy.Strength, policy.MinStrength))
	}
	return NewRunning(policy, func(context.Context) (Result, error) {
		if f.EnforceFS {
			return f.runWriteAttempts(spec, policy), nil
		}
		return f.LaunchResult, nil
	}), policy, nil
}

func (f *Fake) runWriteAttempts(spec LaunchSpec, policy AppliedPolicy) Result {
	res := f.LaunchResult
	for _, attempt := range f.Writes {
		target := resolveFakePath(spec.Dir, attempt.Path)
		if reason := fakeDenyReason(spec, target); reason != "" {
			res.ExitCode = 126
			res.Stderr += fmt.Sprintf("denied write %s: %s\n", target, reason)
			return res
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			res.ExitCode = 1
			res.Stderr += fmt.Sprintf("mkdir %s: %v\n", filepath.Dir(target), err)
			return res
		}
		if err := os.WriteFile(target, []byte(attempt.Content), 0o644); err != nil {
			res.ExitCode = 1
			res.Stderr += fmt.Sprintf("write %s: %v\n", target, err)
			return res
		}
	}
	if res.Stdout == "" {
		res.Stdout = fmt.Sprintf("fake runtime %s accepted %d write(s)\n", policy.Backend, len(f.Writes))
	}
	return res
}

func fakeDenyReason(spec LaunchSpec, target string) string {
	for _, hidden := range spec.Hidden {
		if pathContains(resolveFakePath(spec.Dir, hidden), target) {
			return "hidden path"
		}
	}
	for _, rw := range spec.RW {
		if pathContains(resolveFakePath(spec.Dir, rw), target) {
			return ""
		}
	}
	if spec.EffectiveRepo() == RepoReadOnly && spec.RepoRoot != "" && pathContains(resolveFakePath(spec.Dir, spec.RepoRoot), target) {
		return "repo is read_only and path is outside sandbox.rw"
	}
	return ""
}

func resolveFakePath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if base == "" {
		base = "."
	}
	return filepath.Clean(filepath.Join(base, p))
}

func pathContains(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}
