//go:build gearsbakeoff

// Gated arming check for the gears-rust corpus (projects/gears-rust). gears-rust
// is a large, PRIVATE Rust monorepo, so this is NOT cloned by the qsbakeoff loop
// (the manifest marks it local_only) and NOT part of `make test`. It proves the
// captured corpus is genuinely armed — each fixture's hidden oracle is RED at its
// baseline_sha and GREEN after the real fix's source — by delegating to the same
// deterministic grader (bench.py) the cost-bearing LLM cells use.
//
// It runs against a LOCAL checkout (no network): point BUGFIX_BAKEOFF_REPO at one.
// A throwaway `git clone --local --no-checkout` mirror is used so the grader's
// `git checkout <fix> -- <src>` never dirties your working tree's index. A shared
// CARGO_TARGET_DIR caches compiled deps across oracles.
//
//	BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-bakeoff
//	# or:
//	BUGFIX_BAKEOFF_REPO=/path/to/checkout \
//	  go test -tags gearsbakeoff -run TestGearsBakeoff -count=1 -v ./tools/bugfix-bakeoff/external/
//
// Skips (not fails) when BUGFIX_BAKEOFF_REPO is unset or git/cargo/python3 are absent,
// so it is safe to leave in the tree.
package qsbakeoff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGearsBakeoff(t *testing.T) {
	repo := os.Getenv("BUGFIX_BAKEOFF_REPO")
	if repo == "" {
		t.Skip("BUGFIX_BAKEOFF_REPO unset — point it at a local checkout to arm the corpus")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Skipf("BUGFIX_BAKEOFF_REPO=%q is not a git checkout: %v", repo, err)
	}
	for _, tool := range []string{"git", "cargo", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	// Throwaway --local mirror (hardlinked objects, no worktree) so the grader's
	// fix-source checkout never touches the real checkout's index.
	mirror := filepath.Join(t.TempDir(), "gears-mirror")
	if out, err := exec.Command("git", "clone", "--local", "--no-checkout", "-q", repo, mirror).CombinedOutput(); err != nil {
		t.Fatalf("git clone --local failed: %v\n%s", err, out)
	}

	// bench.py isolates the cargo target cache PER FIXTURE (a shared one would
	// cross-contaminate different baselines of the same workspace), so we do NOT
	// set a global CARGO_TARGET_DIR here.
	cmd := exec.Command("python3", "bench.py", "verify", "--project", "gears-rust", "--repo-dir", mirror)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	t.Logf("bench.py verify gears-rust:\n%s", out)
	if err != nil {
		t.Fatalf("gears-rust fixtures not all armed (RED@baseline -> GREEN@fix): %v", err)
	}
}
