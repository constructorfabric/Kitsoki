//go:build qsbakeoff

// Package qsbakeoff is the gated, reproducible end-to-end check for the GENERIC
// external bug-fix benchmark ("should I use kitsoki for my project?"). It is
// repo-agnostic: it iterates every project under projects/<name>/manifest.yaml
// and, for each, (1) ONBOARDS a checkout via the binary's embedded dev-story
// (proving a binary-only user can stand up a working kitsoki environment on a
// real, mature repo) and (2) proves every fixture's hidden-oracle good/bad
// detector is armed (RED at baseline, GREEN at the real fix) by delegating to
// bench.py — the same deterministic grader the cost-bearing LLM cells use.
//
// Excluded from `make test` by the `qsbakeoff` build tag; needs network (clone +
// install), git, node/npm, python3+pyyaml, and an installed `kitsoki`. Run:
//
//	make qs-bakeoff   # or:
//	go test -tags qsbakeoff -run TestExternalBakeoff -count=1 -v ./tools/bugfix-bakeoff/external/
//
// Add a new repo by dropping a projects/<name>/manifest.yaml + oracle files; no
// code change here is needed — this test discovers it automatically.
package qsbakeoff

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type projectMeta struct {
	ID         string   `json:"id"`
	Repo       string   `json:"repo"`
	OnboardApp string   `json:"onboard_app"`
	LocalOnly  bool     `json:"local_only"`
	Baselines  []string `json:"baselines"`
	Bugs       []string `json:"bugs"`
}

func TestExternalBakeoff(t *testing.T) {
	kitsoki, err := exec.LookPath("kitsoki")
	if err != nil {
		t.Skip("kitsoki not on PATH — run `make install` first")
	}
	for _, tool := range []string{"git", "npm", "node", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	projects, err := filepath.Glob("projects/*/manifest.yaml")
	if err != nil || len(projects) == 0 {
		t.Fatalf("no projects/*/manifest.yaml found: %v", err)
	}

	for _, mf := range projects {
		name := filepath.Base(filepath.Dir(mf))
		t.Run(name, func(t *testing.T) {
			meta := metaFor(t, name)
			if meta.LocalOnly {
				t.Skipf("%s is local_only (heavy/private clone) — arm it via `make gears-bakeoff` (gearsbakeoff tag)", name)
			}

			// 1. Onboard a checkout via the embedded dev-story.
			t.Run("onboard", func(t *testing.T) {
				work := t.TempDir()
				repo := filepath.Join(work, meta.ID)
				run(t, work, "git", "init", "-q", repo)
				run(t, repo, "git", "remote", "add", "origin", meta.Repo)
				run(t, repo, "git", "fetch", "-q", "--depth", "1", "origin", meta.Baselines[0])
				run(t, repo, "git", "checkout", "-q", meta.Baselines[0])

				db := filepath.Join(work, "onboard.db")
				app := meta.OnboardApp
				sess := func(args ...string) {
					base := []string{"session", "continue", "--app", app, "--db", db, "--key", "local:bench"}
					run(t, repo, kitsoki, append(base, args...)...)
				}
				run(t, repo, kitsoki, "session", "create", "--app", app, "--db", db, "--key", "local:bench")
				sess("--intent", "work", "--slots", `{"request":"onboard `+repo+`"}`)
				sess("--intent", "init_discovered")
				sess("--intent", "confirm_init")
				sess("--intent", "init_applied")

				mustExist(t, repo, ".kitsoki.yaml")
				mustExist(t, repo, ".mcp.json")
				mustExist(t, repo, filepath.Join(".claude", "agents", "kitsoki-mcp-driver.md"))
				t.Logf("onboarded %s@%s -> working kitsoki env", meta.ID, meta.Baselines[0][:12])
			})

			// 2. Prove every fixture is armed (RED@baseline, GREEN@real-fix).
			t.Run("oracles", func(t *testing.T) {
				cmd := exec.Command("python3", "bench.py", "verify", "--project", name)
				cmd.Env = os.Environ()
				out, err := cmd.CombinedOutput()
				t.Logf("bench.py verify %s:\n%s", name, out)
				if err != nil {
					t.Fatalf("fixtures not armed for %s: %v", name, err)
				}
			})
		})
	}
}

func metaFor(t *testing.T, project string) projectMeta {
	t.Helper()
	out, err := exec.Command("python3", "bench.py", "meta", "--project", project).Output()
	if err != nil {
		t.Fatalf("bench.py meta %s: %v", project, err)
	}
	var m projectMeta
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("parse meta: %v", err)
	}
	if len(m.Baselines) == 0 {
		t.Fatalf("%s has no bugs", project)
	}
	return m
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
