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

	"kitsoki/internal/capsule"
	"kitsoki/internal/store"
)

var defaultProtectedAgentBranches = []string{
	"main",
	"master",
	"trunk",
	"integration/*",
	"staging/*",
}

// AgentLaunchPolicy is the deterministic preflight gate for external coding
// agent launches. It is not a filesystem sandbox; it rejects unsafe working
// directories before any backend CLI is forked, and records the decision.
type AgentLaunchPolicy struct {
	Enabled           bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	RequireCapsule    bool     `yaml:"require_capsule,omitempty" json:"require_capsule,omitempty"`
	ProtectedBranches []string `yaml:"protected_branches,omitempty" json:"protected_branches,omitempty"`
	ProtectedRoots    []string `yaml:"protected_roots,omitempty" json:"protected_roots,omitempty"`
	AllowedRoots      []string `yaml:"allowed_roots,omitempty" json:"allowed_roots,omitempty"`
}

// AgentLaunchDecision is the auditable result of checking a launch directory.
type AgentLaunchDecision struct {
	Enabled           bool     `json:"enabled"`
	Allowed           bool     `json:"allowed"`
	Reason            string   `json:"reason,omitempty"`
	Verb              string   `json:"verb,omitempty"`
	Agent             string   `json:"agent,omitempty"`
	WorkingDir        string   `json:"working_dir,omitempty"`
	GitRoot           string   `json:"git_root,omitempty"`
	GitBranch         string   `json:"git_branch,omitempty"`
	ProtectedRoot     string   `json:"protected_root,omitempty"`
	ProtectedBranch   string   `json:"protected_branch,omitempty"`
	CapsuleRoot       string   `json:"capsule_root,omitempty"`
	CapsuleName       string   `json:"capsule_name,omitempty"`
	CapsuleSpecPath   string   `json:"capsule_spec_path,omitempty"`
	RequireCapsule    bool     `json:"require_capsule,omitempty"`
	AllowedRoots      []string `json:"allowed_roots,omitempty"`
	ProtectedRoots    []string `json:"protected_roots,omitempty"`
	ProtectedBranches []string `json:"protected_branches,omitempty"`
}

type agentLaunchPolicyKey struct{}

func WithAgentLaunchPolicy(ctx context.Context, policy AgentLaunchPolicy) context.Context {
	if !policy.Enabled {
		return ctx
	}
	return context.WithValue(ctx, agentLaunchPolicyKey{}, policy.Normalized())
}

func AgentLaunchPolicyFromContext(ctx context.Context) AgentLaunchPolicy {
	p, _ := ctx.Value(agentLaunchPolicyKey{}).(AgentLaunchPolicy)
	return p.Normalized()
}

func (p AgentLaunchPolicy) Normalized() AgentLaunchPolicy {
	if !p.Enabled {
		return p
	}
	if len(p.ProtectedBranches) == 0 {
		p.ProtectedBranches = append([]string(nil), defaultProtectedAgentBranches...)
	}
	p.ProtectedBranches = cleanTrimStringList(p.ProtectedBranches)
	p.ProtectedRoots = cleanPathStringList(p.ProtectedRoots)
	p.AllowedRoots = cleanPathStringList(p.AllowedRoots)
	return p
}

func (p AgentLaunchPolicy) Check(ctx context.Context, verb, agentName, workingDir string) (AgentLaunchDecision, error) {
	p = p.Normalized()
	decision := AgentLaunchDecision{
		Enabled:           p.Enabled,
		Allowed:           true,
		Verb:              strings.TrimSpace(verb),
		Agent:             strings.TrimSpace(agentName),
		RequireCapsule:    p.RequireCapsule,
		AllowedRoots:      append([]string(nil), p.AllowedRoots...),
		ProtectedRoots:    append([]string(nil), p.ProtectedRoots...),
		ProtectedBranches: append([]string(nil), p.ProtectedBranches...),
	}
	if !p.Enabled {
		decision.Reason = "policy disabled"
		return decision, nil
	}
	wd := strings.TrimSpace(workingDir)
	if wd == "" {
		wd = "."
	}
	abs, err := filepath.Abs(wd)
	if err != nil {
		return denyLaunch(decision, fmt.Sprintf("resolve working_dir %q: %v", workingDir, err))
	}
	abs = resolveExistingPath(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return denyLaunch(decision, fmt.Sprintf("working_dir %s is not accessible: %v", abs, err))
	}
	if !info.IsDir() {
		return denyLaunch(decision, fmt.Sprintf("working_dir %s is not a directory", abs))
	}
	decision.WorkingDir = abs

	inAllowedRoot := false
	for _, root := range p.AllowedRoots {
		if root != "" && pathContains(root, abs) {
			inAllowedRoot = true
			break
		}
	}
	if len(p.AllowedRoots) > 0 && !inAllowedRoot {
		return denyLaunch(decision, fmt.Sprintf("working_dir %s is outside allowed agent roots", abs))
	}

	if !inAllowedRoot {
		for _, root := range p.ProtectedRoots {
			if root != "" && pathContains(root, abs) {
				decision.ProtectedRoot = resolveExistingPath(root)
				return denyLaunch(decision, fmt.Sprintf("working_dir %s is inside protected root %s", abs, decision.ProtectedRoot))
			}
		}
	}

	if capsuleRoot, manifest, ok := findOpenedCapsule(abs); ok {
		decision.CapsuleRoot = capsuleRoot
		decision.CapsuleName = manifest.CapsuleName
		decision.CapsuleSpecPath = manifest.SpecPath
	}

	if root, branch, ok := gitLaunchInfo(ctx, abs); ok {
		decision.GitRoot = root
		decision.GitBranch = branch
		gitInAllowedRoot := inAllowedRoot
		if !gitInAllowedRoot {
			for _, allowedRoot := range p.AllowedRoots {
				if allowedRoot != "" && pathContains(allowedRoot, root) {
					gitInAllowedRoot = true
					break
				}
			}
		}
		if !gitInAllowedRoot {
			for _, protectedRoot := range p.ProtectedRoots {
				if protectedRoot != "" && pathContains(protectedRoot, root) {
					decision.ProtectedRoot = resolveExistingPath(protectedRoot)
					return denyLaunch(decision, fmt.Sprintf("git root %s is inside protected root %s", root, decision.ProtectedRoot))
				}
			}
		}
		gitInsideCapsule := decision.CapsuleRoot != "" && pathContains(decision.CapsuleRoot, root)
		if !gitInsideCapsule {
			if protected := matchProtectedBranch(branch, p.ProtectedBranches); protected != "" {
				decision.ProtectedBranch = protected
				return denyLaunch(decision, fmt.Sprintf("git branch %q is protected by pattern %q", branch, protected))
			}
		}
	}

	if decision.CapsuleRoot == "" && p.RequireCapsule {
		return denyLaunch(decision, fmt.Sprintf("working_dir %s is not inside an opened Kitsoki capsule", abs))
	}

	decision.Reason = "allowed"
	return decision, nil
}

func CheckAgentLaunchPolicy(ctx context.Context, verb, agentName, workingDir string) (AgentLaunchDecision, error) {
	return AgentLaunchPolicyFromContext(ctx).Check(ctx, verb, agentName, workingDir)
}

func RequireAgentLaunchAllowed(ctx context.Context, verb, agentName, workingDir string) (AgentLaunchDecision, string) {
	decision, err := CheckAgentLaunchPolicy(ctx, verb, agentName, workingDir)
	if decision.Enabled {
		appendAgentLaunchPolicyEvent(ctx, "", decision)
	}
	if err != nil {
		return decision, err.Error()
	}
	return decision, ""
}

func AppendAgentLaunchPolicyEvent(ctx context.Context, callID string, decision AgentLaunchDecision) {
	appendAgentLaunchPolicyEvent(ctx, callID, decision)
}

func appendAgentLaunchPolicyEvent(ctx context.Context, callID string, decision AgentLaunchDecision) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	raw, err := json.Marshal(decision)
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        time.Now(),
		Kind:      store.EventKind("agent.launch.policy"),
		StatePath: oc.StatePath,
		Payload:   raw,
		CallID:    callID,
	})
}

func denyLaunch(decision AgentLaunchDecision, reason string) (AgentLaunchDecision, error) {
	decision.Allowed = false
	decision.Reason = reason
	return decision, fmt.Errorf("agent launch policy denied: %s", reason)
}

func cleanTrimStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cleanPathStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, filepath.Clean(s))
		}
	}
	return out
}

func resolveExistingPath(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func pathContains(root, path string) bool {
	root = resolveExistingPath(root)
	path = resolveExistingPath(path)
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func gitLaunchInfo(ctx context.Context, dir string) (root, branch string, ok bool) {
	gitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rootOut, err := exec.CommandContext(gitCtx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", false
	}
	root = strings.TrimSpace(string(rootOut))
	if root == "" {
		return "", "", false
	}
	root = resolveExistingPath(root)
	branchOut, err := exec.CommandContext(gitCtx, "git", "-C", dir, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err == nil {
		branch = strings.TrimSpace(string(branchOut))
	}
	if branch == "" {
		branchOut, _ = exec.CommandContext(gitCtx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
		branch = strings.TrimSpace(string(branchOut))
	}
	return root, branch, true
}

func matchProtectedBranch(branch string, patterns []string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		return ""
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if pattern == branch {
			return pattern
		}
		if ok, _ := filepath.Match(pattern, branch); ok {
			return pattern
		}
	}
	return ""
}

func findOpenedCapsule(dir string) (string, capsule.Manifest, bool) {
	dir = resolveExistingPath(dir)
	for {
		sentinel := filepath.Join(dir, capsule.SentinelFile)
		manifestPath := filepath.Join(dir, capsule.ManifestFile)
		if launchPolicyFileExists(sentinel) && launchPolicyFileExists(manifestPath) {
			manifest, err := capsule.ReadManifest(dir)
			if err == nil {
				return dir, manifest, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", capsule.Manifest{}, false
		}
		dir = parent
	}
}

func launchPolicyFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
