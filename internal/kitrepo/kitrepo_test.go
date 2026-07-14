package kitrepo

import (
	"os"
	"path/filepath"
	"testing"
)

// savedFileFor returns the ~/.kitsoki/repo path under the given HOME.
func savedFileFor(home string) string {
	return filepath.Join(home, ".kitsoki", "repo")
}

func TestResolve_EnvWinsAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	t.Setenv(EnvVar, repo)

	if got := Resolve(); got != repo {
		t.Fatalf("Resolve() = %q, want env value %q", got, repo)
	}
	// The env value must be persisted so a later run without the env finds it.
	b, err := os.ReadFile(savedFileFor(home))
	if err != nil {
		t.Fatalf("expected ~/.kitsoki/repo to be written: %v", err)
	}
	if got := string(b); got != repo+"\n" {
		t.Fatalf("persisted = %q, want %q", got, repo+"\n")
	}
}

func TestResolve_SavedFileUsedWhenEnvUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvVar, "")
	// chdir somewhere with no kitsoki go.mod ancestor so detection can't fire.
	t.Chdir(t.TempDir())

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(savedFileFor(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(savedFileFor(home), []byte(repo+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := Resolve(); got != repo {
		t.Fatalf("Resolve() = %q, want saved value %q", got, repo)
	}
}

func TestResolve_StaleSavedFileIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvVar, "")
	t.Chdir(t.TempDir())

	if err := os.MkdirAll(filepath.Dir(savedFileFor(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(t.TempDir(), "moved-away")
	if err := os.WriteFile(savedFileFor(home), []byte(stale+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := Resolve(); got != "" {
		t.Fatalf("Resolve() = %q, want \"\" (stale saved path must be ignored)", got)
	}
}

func TestResolve_AutoDetectsCheckoutAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvVar, "")

	// Build a fake checkout: <repo>/go.mod with `module kitsoki`, and a
	// nested cwd so detection has to walk up.
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module kitsoki\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "stories", "prd")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	got := Resolve()
	// Resolve returns the directory containing go.mod; compare resolved
	// symlinks since macOS/Linux temp dirs can be symlinked.
	wantEval, _ := filepath.EvalSymlinks(repo)
	gotEval, _ := filepath.EvalSymlinks(got)
	if gotEval != wantEval {
		t.Fatalf("Resolve() = %q, want checkout root %q", got, repo)
	}
	if _, err := os.Stat(savedFileFor(home)); err != nil {
		t.Fatalf("auto-detected repo must be persisted: %v", err)
	}
}

func TestResolve_CurrentCheckoutBeatsRememberedCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvVar, "")

	remembered := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(savedFileFor(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(savedFileFor(home), []byte(remembered+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	current := t.TempDir()
	if err := os.WriteFile(filepath.Join(current, "go.mod"), []byte("module kitsoki\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(current, ".capsules", "workspaces", "reliability")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	if got := Resolve(); got != current {
		t.Fatalf("Resolve() = %q, want current checkout %q instead of remembered %q", got, current, remembered)
	}
	b, err := os.ReadFile(savedFileFor(home))
	if err != nil {
		t.Fatalf("read refreshed location: %v", err)
	}
	if got := string(b); got != current+"\n" {
		t.Fatalf("persisted = %q, want refreshed current checkout %q", got, current+"\n")
	}
}

func TestResolve_UnresolvableReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvVar, "")
	t.Chdir(t.TempDir())

	if got := Resolve(); got != "" {
		t.Fatalf("Resolve() = %q, want \"\" when nothing resolves", got)
	}
}

func TestModuleIsKitsoki(t *testing.T) {
	cases := []struct {
		gomod string
		want  bool
	}{
		{"module kitsoki\n\ngo 1.25\n", true},
		{"module  kitsoki  \n", true},
		{"module kitsoki/internal/foo\n", false},
		{"module github.com/other/thing\n", false},
		{"// comment\nmodule kitsoki\n", true},
		{"", false},
	}
	for _, c := range cases {
		if got := moduleIsKitsoki([]byte(c.gomod)); got != c.want {
			t.Errorf("moduleIsKitsoki(%q) = %v, want %v", c.gomod, got, c.want)
		}
	}
}
