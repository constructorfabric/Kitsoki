//go:build onboardsmoke

// Package onboardsmoke is a gated, reproducible end-to-end test of project
// onboarding against a real, pinned open-source repository.
//
// It is excluded from `make test` by the `onboardsmoke` build tag — it needs
// network (to clone), git, and the `kitsoki` binary installed on PATH. Run it
// explicitly:
//
//	make onboard-smoke              # or:
//	go test -tags onboardsmoke -run TestOnboardPinnedRepo -count=1 -v ./tools/onboard-smoke/
//
// What it proves: a binary-only user (no kitsoki checkout) can clone a project
// and onboard it to a fully working kitsoki environment — the dev-story app is
// resolved from the binary's EMBEDDED story library (`@kitsoki/dev-story`), and
// onboarding writes the config + instance AND installs the studio MCP + the
// skill/agent toolkit.
package onboardsmoke

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pinned target: yocto-queue v1.1.1 — a tiny MIT-licensed Node package. The SHA
// is the dereferenced tag commit (`refs/tags/v1.1.1^{}`), so the checkout is
// byte-reproducible regardless of later tag movement.
const (
	repoURL = "https://github.com/sindresorhus/yocto-queue.git"
	repoSHA = "0ac610dfa4e5cbd929b2e9b8fc34f5417f2f788b"
	// project id the discovery slugs from the repo dir basename.
	projectID = "yocto-queue"
)

func TestOnboardPinnedRepo(t *testing.T) {
	kitsoki, err := exec.LookPath("kitsoki")
	if err != nil {
		t.Skip("kitsoki not on PATH — run `make install` first")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	work := t.TempDir()
	repo := filepath.Join(work, projectID)
	db := filepath.Join(work, "onboard.db")

	// 1. Clone the pinned SHA reproducibly: init + fetch the exact commit (works
	//    even when the SHA is not a branch tip) + checkout.
	run(t, work, "git", "init", "-q", repo)
	run(t, repo, "git", "remote", "add", "origin", repoURL)
	run(t, repo, "git", "fetch", "-q", "--depth", "1", "origin", repoSHA)
	run(t, repo, "git", "checkout", "-q", repoSHA)

	// 2. Drive onboarding headless against the EMBEDDED dev-story (binary-only:
	//    no local kitsoki checkout, no --kitsoki-repo). The onboard request
	//    carries the absolute target path.
	const app = "@kitsoki/dev-story"
	session := func(args ...string) {
		base := []string{"session", "continue", "--app", app, "--db", db, "--key", "local:onboard"}
		run(t, repo, kitsoki, append(base, args...)...)
	}
	run(t, repo, kitsoki, "session", "create", "--app", app, "--db", db, "--key", "local:onboard")
	session("--intent", "work", "--slots", `{"request":"onboard `+repo+`"}`)
	session("--intent", "init_discovered")
	session("--intent", "confirm_init") // on_enter: apply + project-tools install
	session("--intent", "init_applied")

	// 3. Assert a fully working environment landed on disk.
	mustExist(t, repo, ".kitsoki.yaml")
	mustExist(t, repo, ".mcp.json")
	mustExist(t, repo, filepath.Join(".kitsoki", "stories", projectID+"-dev", "app.yaml"))
	mustExist(t, repo, filepath.Join(".claude", "skills", "kitsoki-story-authoring"))
	mustExist(t, repo, filepath.Join(".claude", "agents", "kitsoki-mcp-driver.md"))
	mustExist(t, repo, filepath.Join(".agents", "skills", "kitsoki-story-authoring", "SKILL.md"))

	// .mcp.json registers the kitsoki studio server.
	var mcp struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	readJSON(t, filepath.Join(repo, ".mcp.json"), &mcp)
	if mcp.MCPServers["kitsoki"].Command != "kitsoki" {
		t.Fatalf(".mcp.json missing kitsoki server: %+v", mcp)
	}

	// .claude/skills/<name> must be a relative symlink into .agents (the
	// install layout), not a copy.
	link := filepath.Join(repo, ".claude", "skills", "kitsoki-story-authoring")
	if dest, err := os.Readlink(link); err != nil {
		t.Fatalf("skill is not a symlink: %v", err)
	} else if want := filepath.Join("../..", ".agents", "skills", "kitsoki-story-authoring"); dest != want {
		t.Fatalf("skill symlink = %q, want %q", dest, want)
	}

	// 4. The generated instance loads (imports @kitsoki/dev-story from the
	//    embedded library) — a fresh session against it must create cleanly.
	run(t, repo, kitsoki, "session", "create",
		"--app", filepath.Join(".kitsoki", "stories", projectID+"-dev", "app.yaml"),
		"--db", filepath.Join(work, "instance.db"), "--key", "local:smoke")

	t.Logf("onboarded %s@%s → working kitsoki environment", projectID, repoSHA[:12])
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func mustExist(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		t.Fatalf("expected %s to exist: %v", rel, err)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
