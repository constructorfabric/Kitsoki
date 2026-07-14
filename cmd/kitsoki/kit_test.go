package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/kitlock"
)

// runKit runs `kitsoki kit <args...>` against an isolated root command,
// mirroring runGraphLintCmd's pattern (graph_test.go).
func runKit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"kit"}, args...))
	err := root.Execute()
	if stderr.Len() > 0 {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), err
}

const kitFixtureManifest = `app: { id: widget, version: 1.2.0 }
hosts: [host.run]
world:
  count: { type: int, default: 0 }
intents:
  bump: { description: "bump count" }
root: idle
states:
  idle: { view: "x" }
`

func writeFixtureKit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(kitFixtureManifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKitAddListVerify_LocalPathSource(t *testing.T) {
	lib := t.TempDir()
	writeFixtureKit(t, filepath.Join(lib, "widget"))
	target := t.TempDir()

	out, err := runKit(t, "add", filepath.Join(lib, "widget"), "--name", "widget", "--version", "^1.0.0", "--target", target)
	if err != nil {
		t.Fatalf("kit add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "locked widget@1.2.0") {
		t.Errorf("kit add output = %q, want mention of locked widget@1.2.0", out)
	}

	lf, err := kitlock.Load(kitlock.Path(target))
	if err != nil {
		t.Fatalf("kitlock.Load: %v", err)
	}
	entry := lf.Kits["widget"]
	if entry == nil {
		t.Fatal("expected a widget lock entry")
	}
	if entry.Version != "1.2.0" {
		t.Errorf("locked version = %q, want 1.2.0", entry.Version)
	}
	if entry.TreeHash == "" {
		t.Error("locked tree_hash is empty")
	}

	listOut, err := runKit(t, "list", "--target", target)
	if err != nil {
		t.Fatalf("kit list: %v\n%s", err, listOut)
	}
	if !strings.Contains(listOut, "widget@1.2.0") {
		t.Errorf("kit list output = %q, want widget@1.2.0", listOut)
	}

	verifyOut, err := runKit(t, "verify", "--target", target)
	if err != nil {
		t.Fatalf("kit verify: %v\n%s", err, verifyOut)
	}
	if !strings.Contains(verifyOut, "widget: OK") {
		t.Errorf("kit verify output = %q, want widget: OK", verifyOut)
	}
}

func TestKitAdd_VersionConstraintRejectsMismatch(t *testing.T) {
	lib := t.TempDir()
	writeFixtureKit(t, filepath.Join(lib, "widget"))
	target := t.TempDir()

	_, err := runKit(t, "add", filepath.Join(lib, "widget"), "--name", "widget", "--version", "^2.0.0", "--target", target)
	if err == nil {
		t.Fatal("expected kit add to fail on a version-constraint mismatch")
	}
	if !strings.Contains(err.Error(), "does not satisfy constraint") {
		t.Errorf("err = %v, want mention of the constraint mismatch", err)
	}
}

func TestKitVerify_DetectsMismatch(t *testing.T) {
	lib := t.TempDir()
	writeFixtureKit(t, filepath.Join(lib, "widget"))
	target := t.TempDir()

	if _, err := runKit(t, "add", filepath.Join(lib, "widget"), "--name", "widget", "--target", target); err != nil {
		t.Fatalf("kit add: %v", err)
	}

	// Mutate the kit's content after locking.
	if err := os.WriteFile(filepath.Join(lib, "widget", "app.yaml"), []byte(kitFixtureManifest+"# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runKit(t, "verify", "--target", target)
	if err == nil {
		t.Fatalf("expected kit verify to report a mismatch, got clean pass:\n%s", out)
	}
	if !strings.Contains(out, "MISMATCH") {
		t.Errorf("verify output = %q, want MISMATCH", out)
	}
}

func TestKitVerify_NoLockfile(t *testing.T) {
	target := t.TempDir()
	_, err := runKit(t, "verify", "--target", target)
	if err == nil {
		t.Fatal("expected kit verify to error when no lockfile exists")
	}
}

func TestKitDev_SetAndClear(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	checkout := t.TempDir()
	writeFixtureKit(t, checkout)

	out, err := runKit(t, "dev", "widget", "--path", checkout)
	if err != nil {
		t.Fatalf("kit dev: %v\n%s", err, out)
	}
	if !strings.Contains(out, checkout) {
		t.Errorf("kit dev output = %q, want mention of %q", out, checkout)
	}

	listOut, err := runKit(t, "list", "--target", t.TempDir())
	if err != nil {
		t.Fatalf("kit list: %v", err)
	}
	_ = listOut // dev override is surfaced only for locked names; smoke-checked above

	clearOut, err := runKit(t, "dev", "widget", "--clear")
	if err != nil {
		t.Fatalf("kit dev --clear: %v\n%s", err, clearOut)
	}
	if !strings.Contains(clearOut, "cleared") {
		t.Errorf("kit dev --clear output = %q, want mention of cleared", clearOut)
	}
}

func TestKitDev_RequiresAppYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	empty := t.TempDir() // no app.yaml here

	_, err := runKit(t, "dev", "widget", "--path", empty)
	if err == nil {
		t.Fatal("expected an error when --path has no app.yaml")
	}
}

// TestKitAdd_GitSource proves `kit add`/`kit verify` work end-to-end against
// the git source tier using a real local git repo as the remote — no
// network required, mirroring internal/kitgit's own test fixture pattern.
func TestKitAdd_GitSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	remote := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = remote
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(remote, "app.yaml"), []byte(kitFixtureManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "app.yaml")
	run("commit", "-q", "-m", "initial")
	run("tag", "v1.2.0")

	target := t.TempDir()
	out, err := runKit(t, "add", "git+"+remote+"@v1.2.0", "--target", target, "--version", "^1.0.0")
	if err != nil {
		t.Fatalf("kit add (git source): %v\n%s", err, out)
	}
	if !strings.Contains(out, "commit:") {
		t.Errorf("kit add output = %q, want a commit: line for a git source", out)
	}

	verifyOut, err := runKit(t, "verify", "--target", target)
	if err != nil {
		t.Fatalf("kit verify (git source): %v\n%s", err, verifyOut)
	}
}

func TestKitDev_RequiresPathOrClear(t *testing.T) {
	_, err := runKit(t, "dev", "widget")
	if err == nil {
		t.Fatal("expected an error when neither --path nor --clear is given")
	}
}

