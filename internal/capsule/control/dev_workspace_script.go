package control

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ScriptRunner is injected so the compatibility provider can be tested
// without creating a real repository or executing project hooks.
type ScriptRunner interface {
	Run(context.Context, string, string, ...string) ([]byte, error)
}

type ScriptRunnerFunc func(context.Context, string, string, ...string) ([]byte, error)

func (f ScriptRunnerFunc) Run(ctx context.Context, dir, program string, args ...string) ([]byte, error) {
	return f(ctx, dir, program, args...)
}

type execScriptRunner struct{}

func (execScriptRunner) Run(ctx context.Context, dir, program string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// DevWorkspaceScriptProvider is the compatibility adapter for the existing
// Kitsoki protected clone workflow. It is deliberately not a generic source:
// a project opts in by declaring source.kind dev-workspace-script.
type DevWorkspaceScriptProvider struct {
	ProjectRoot string
	Runner      ScriptRunner
}

func (DevWorkspaceScriptProvider) Name() string { return string(SourceDevWorkspaceScript) }

func (p DevWorkspaceScriptProvider) Create(ctx context.Context, def Definition, in Instance) (MaterializedWorkspace, error) {
	if def.Source.Kind != SourceDevWorkspaceScript {
		return MaterializedWorkspace{}, fmt.Errorf("dev workspace script: definition %q has source %q", def.ID, def.Source.Kind)
	}
	root, err := projectRoot(p.ProjectRoot)
	if err != nil {
		return MaterializedWorkspace{}, err
	}
	development := def.Source.Development
	base := development.Base
	if base == "" {
		base = "staging/local"
	}
	branchPrefix := development.BranchPrefix
	if branchPrefix == "" {
		branchPrefix = "agent/"
	}
	branch := branchPrefix + in.ID
	if _, err := runGit(ctx, root, "rev-parse", "--verify", base+"^{commit}"); err != nil {
		return MaterializedWorkspace{}, fmt.Errorf("dev workspace script: configured base %q is unavailable in this checkout; run from the protected project checkout or refresh its local branch: %w", base, err)
	}
	args := []string{"create", "--repo", root, "--root", filepath.Dir(in.Path), "--id", in.ID, "--branch", branch, "--base", base, "--target", development.Target}
	if development.Bootstrap {
		args = append(args, "--bootstrap")
	}
	if output, err := p.runner().Run(ctx, root, filepath.Join(root, "scripts", "dev-workspace.sh"), args...); err != nil {
		return MaterializedWorkspace{}, scriptError("create", err, output)
	}
	if _, err := os.Stat(filepath.Join(in.Path, instanceSentinel)); err != nil {
		return MaterializedWorkspace{}, fmt.Errorf("dev workspace script: missing capsule sentinel: %w", err)
	}
	head, err := runGit(ctx, in.Path, "rev-parse", "HEAD")
	if err != nil {
		return MaterializedWorkspace{}, err
	}
	currentBranch, err := runGit(ctx, in.Path, "branch", "--show-current")
	if err != nil {
		return MaterializedWorkspace{}, err
	}
	return MaterializedWorkspace{Path: in.Path, SourceRef: base, Head: strings.TrimSpace(head), Branch: strings.TrimSpace(currentBranch)}, nil
}

func (p DevWorkspaceScriptProvider) Integrate(ctx context.Context, _ Definition, in Instance, gate string) error {
	root, err := projectRoot(p.ProjectRoot)
	if err != nil {
		return err
	}
	args := []string{"merge", "--repo", root, "--root", filepath.Dir(in.Path), in.ID}
	if strings.TrimSpace(gate) != "" {
		args = append(args, "--gate", gate)
	}
	if output, err := p.runner().Run(ctx, root, filepath.Join(root, "scripts", "dev-workspace.sh"), args...); err != nil {
		return scriptError("integrate", err, output)
	}
	return nil
}

// WorkspaceMaterialized implements WorkspaceMaterializationVerifier: Create
// only returns success after `dev-workspace.sh create` completes AND the
// sentinel stat below passes, so its presence at in.Path is durable proof the
// managed clone/worktree is real, not merely a directory that happens to
// exist.
func (DevWorkspaceScriptProvider) WorkspaceMaterialized(in Instance) bool {
	_, err := os.Stat(filepath.Join(in.Path, instanceSentinel))
	return err == nil
}

func (p DevWorkspaceScriptProvider) Close(ctx context.Context, in Instance) error {
	root, err := projectRoot(p.ProjectRoot)
	if err != nil {
		return err
	}
	if output, err := p.runner().Run(ctx, root, filepath.Join(root, "scripts", "dev-workspace.sh"), "teardown", "--repo", root, "--root", filepath.Dir(in.Path), in.ID); err != nil {
		return scriptError("close", err, output)
	}
	return nil
}

func scriptError(operation string, err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("dev workspace script: %s: %w", operation, err)
	}
	return fmt.Errorf("dev workspace script: %s: %w: %s", operation, err, message)
}

func (p DevWorkspaceScriptProvider) runner() ScriptRunner {
	if p.Runner != nil {
		return p.Runner
	}
	return execScriptRunner{}
}

var _ WorkspaceProvider = DevWorkspaceScriptProvider{}
var _ WorkspaceIntegrator = DevWorkspaceScriptProvider{}
var _ WorkspaceMaterializationVerifier = DevWorkspaceScriptProvider{}
