package control

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevWorkspaceScriptProviderMapsProtectedLifecycle(t *testing.T) {
	project := t.TempDir()
	runControlGit(t, project, "init", "-b", "main")
	runControlGit(t, project, "config", "user.name", "test")
	runControlGit(t, project, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runControlGit(t, project, "add", "README.md")
	runControlGit(t, project, "commit", "-m", "base")
	runControlGit(t, project, "branch", "staging/local")

	var calls [][]string
	runner := ScriptRunnerFunc(func(ctx context.Context, _ string, _ string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		switch args[0] {
		case "create":
			path := args[argValue(t, args, "--id")+1]
			root := args[argValue(t, args, "--root")+1]
			path = filepath.Join(root, path)
			branch := args[argValue(t, args, "--branch")+1]
			base := args[argValue(t, args, "--base")+1]
			cmd := exec.CommandContext(ctx, "git", "clone", "--local", "--origin", "source", project, path)
			if out, err := cmd.CombinedOutput(); err != nil {
				return out, err
			}
			cmd = exec.CommandContext(ctx, "git", "switch", "-c", branch, "source/"+base)
			cmd.Dir = path
			if out, err := cmd.CombinedOutput(); err != nil {
				return out, err
			}
			return nil, os.WriteFile(filepath.Join(path, instanceSentinel), []byte("managed\n"), 0o644)
		case "teardown":
			return nil, os.RemoveAll(filepath.Join(args[argValue(t, args, "--root")+1], args[len(args)-1]))
		default:
			return nil, nil
		}
	})
	provider := DevWorkspaceScriptProvider{ProjectRoot: project, Runner: runner}
	path := filepath.Join(project, ".capsules", "workspaces", "one")
	definition := Definition{ID: "development", Source: Source{Kind: SourceDevWorkspaceScript, Development: DevelopmentSource{Base: "staging/local", Target: "staging/local", BranchPrefix: "agent/", Bootstrap: true}}}
	materialized, err := provider.Create(context.Background(), definition, Instance{ID: "one", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Path != path || materialized.Branch != "agent/one" || materialized.Head == "" {
		t.Fatalf("materialized %#v", materialized)
	}
	if !containsArgs(calls[0], "--bootstrap") || !containsArgs(calls[0], "--target", "staging/local") {
		t.Fatalf("create args %q", calls[0])
	}
	if err := provider.Integrate(context.Background(), definition, Instance{ID: "one", Path: path}, "go test ./internal/capsule"); err != nil {
		t.Fatal(err)
	}
	if !containsArgs(calls[1], "--gate", "go test ./internal/capsule") {
		t.Fatalf("integrate args %q", calls[1])
	}
	if err := provider.Close(context.Background(), Instance{ID: "one", Path: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after close: %v", err)
	}
}

func TestDevWorkspaceScriptProviderRefusesMissingConfiguredBase(t *testing.T) {
	project := t.TempDir()
	runControlGit(t, project, "init", "-b", "main")
	runControlGit(t, project, "config", "user.name", "test")
	runControlGit(t, project, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runControlGit(t, project, "add", "README.md")
	runControlGit(t, project, "commit", "-m", "base")
	provider := DevWorkspaceScriptProvider{ProjectRoot: project, Runner: ScriptRunnerFunc(func(context.Context, string, string, ...string) ([]byte, error) {
		t.Fatal("runner must not run for an unavailable base")
		return nil, nil
	})}
	_, err := provider.Create(context.Background(), Definition{ID: "development", Source: Source{Kind: SourceDevWorkspaceScript, Development: DevelopmentSource{Base: "staging/local", Target: "staging/local"}}}, Instance{ID: "one", Path: filepath.Join(project, ".capsules", "one")})
	if err == nil || !strings.Contains(err.Error(), "configured base") {
		t.Fatalf("missing-base error %v", err)
	}
}

func argValue(t *testing.T, args []string, want string) int {
	t.Helper()
	for i, value := range args {
		if value == want && i+1 < len(args) {
			return i
		}
	}
	t.Fatalf("%q not present in %q", want, args)
	return -1
}

func containsArgs(args []string, want ...string) bool {
	return strings.Contains("\x00"+strings.Join(args, "\x00")+"\x00", "\x00"+strings.Join(want, "\x00")+"\x00")
}

func runControlGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
