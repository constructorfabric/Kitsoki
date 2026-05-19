package metamode

// Regression tests for the deterministic post-turn git-commit hook
// (controller.go::sendLocked, calling host.CommitChangedFiles). This
// file exercises the MODERN direct-edit meta-mode path — the legacy
// authoring.propose/apply path has its own coverage in
// internal/host/authoring_commit_test.go.
//
// Setup shape per test:
//   - t.TempDir() + `git init` + an initial commit on app.yaml.
//   - newTestController(t) with a stub Oracle that mutates a file
//     inside the story dir during Ask (simulating claude's Edit tool).
//   - c.Send(...) with TurnContext{AppFile: appFile} so sendLocked's
//     pre/post tree snapshot detects the edit.
//   - Inspect SendResult.CommitSHA / CommitAmended / CommitError plus
//     HEAD via git.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// initStoryRepo creates a temp directory with `git init`, seeds a
// minimally-valid kitsoki app.yaml on `main` with one initial commit,
// and returns the dir + app.yaml path. The manifest is valid enough
// that app.Load succeeds against it — the post-edit validation gate
// in CommitChangedFiles re-runs that load, so the test's seed must
// not be rejected on a clean turn.
//
// Subsequent edits to files in this dir register as "modified"
// changes against HEAD.
func initStoryRepo(t *testing.T) (dir, appFile string) {
	t.Helper()
	dir = t.TempDir()
	appFile = filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appFile, []byte(minValidManifest), 0o644))

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, string(out))
	}
	run("init", "--quiet", "--initial-branch=main")
	run("config", "user.email", "meta-commit-test@kitsoki.invalid")
	run("config", "user.name", "Meta Commit Test")
	run("add", "app.yaml")
	run("commit", "--quiet", "-m", "init")
	return dir, appFile
}

// minValidManifest is the smallest manifest app.Load accepts: an app
// stanza, a root state, one declared intent, and a single state that
// references it. Edits to this file (e.g. setting `app.id` to a
// different value) keep it valid as long as the structure isn't
// disturbed.
const minValidManifest = `app:
  id: t
  version: 0.0.1
root: main
intents:
  go_main:
    description: "Go to main."
states:
  main:
    on:
      go_main:
        - target: main
`

// validManifestWithDescription is a manifest variant that differs
// from minValidManifest by description only — enough mtime/size churn
// to register as a change in storyTreeChanges. Used by tests that need
// to "edit" the manifest while still leaving a valid AppDef on disk.
func validManifestWithDescription(desc string) []byte {
	return []byte(strings.Replace(minValidManifest,
		`    description: "Go to main."`,
		`    description: "Go to main. `+desc+`"`,
		1))
}

func headBody(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%B", "HEAD").Output()
	require.NoError(t, err)
	return string(out)
}

func headFiles(t *testing.T, repo string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD").Output()
	require.NoError(t, err)
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func headSHA(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func headParent(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD~1").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// TestController_Send_DirectEdit_CommitsAutomatically is the core
// regression test for the modern direct-edit path: when the agent
// edits a file in the story tree (via Edit/Write tools — here
// simulated by the oracle stub touching the file), sendLocked must
// automatically run a git commit before returning. Pre-fix, the
// changed files just sat in the working tree as uncommitted changes.
func TestController_Send_DirectEdit_CommitsAutomatically(t *testing.T) {
	dir, appFile := initStoryRepo(t)
	initialHead := headSHA(t, dir)

	c, _, _ := newTestController(t)
	c.Oracle = oracleFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// Simulate the agent rewriting the manifest to a still-valid form.
		require.NoError(t, os.WriteFile(appFile, validManifestWithDescription("first"), 0o644))
		return AskOutput{Reply: "rewrote app.yaml"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	require.NoError(t, err)

	res, err := c.Send(context.Background(), s, "rename the app", TurnContext{AppFile: appFile})
	require.NoError(t, err)

	require.NotEmpty(t, res.CommitSHA, "direct edit must produce a commit")
	require.False(t, res.CommitAmended, "first turn must be a fresh commit, not an amend")
	require.Empty(t, res.CommitError, "first turn must not surface a commit error: %s", res.CommitError)

	require.Equal(t, res.CommitSHA, headSHA(t, dir))
	require.Equal(t, initialHead, headParent(t, dir),
		"first commit's parent must be the initial commit")
	require.Contains(t, headFiles(t, dir), "app.yaml")
	require.Contains(t, headBody(t, dir), "Kitsoki-Meta-Session:",
		"commit body must carry the session marker trailer")
	require.Contains(t, headBody(t, dir), "meta-mode: app.yaml",
		"commit subject must follow the deterministic template")
}

// TestController_Send_DirectEdit_SecondTurnAmends is the same-session
// regression: two consecutive Sends in the same chat must amend HEAD
// rather than appending a new commit. The user said "if someone wants
// updates in the same convo just amend the existing commit" — this
// test pins exactly that contract for the direct-edit path.
func TestController_Send_DirectEdit_SecondTurnAmends(t *testing.T) {
	dir, appFile := initStoryRepo(t)
	initialHead := headSHA(t, dir)

	c, _, _ := newTestController(t)
	turn := 0
	c.Oracle = oracleFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		turn++
		switch turn {
		case 1:
			require.NoError(t, os.WriteFile(appFile, validManifestWithDescription("first-edit"), 0o644))
			return AskOutput{Reply: "first edit"}, nil
		case 2:
			// Second turn writes a NEW file alongside the manifest.
			// The amended commit must include both app.yaml AND the
			// new file. The manifest stays valid so validation passes.
			require.NoError(t, os.WriteFile(appFile, validManifestWithDescription("second-edit"), 0o644))
			second := filepath.Join(dir, "second.txt")
			require.NoError(t, os.WriteFile(second, []byte("added in second turn\n"), 0o644))
			return AskOutput{Reply: "second edit"}, nil
		}
		return AskOutput{Reply: "no-op"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	require.NoError(t, err)

	res1, err := c.Send(context.Background(), s, "first ask", TurnContext{AppFile: appFile})
	require.NoError(t, err)
	require.False(t, res1.CommitAmended)
	firstSHA := res1.CommitSHA

	res2, err := c.Send(context.Background(), s, "second ask", TurnContext{AppFile: appFile})
	require.NoError(t, err)
	require.True(t, res2.CommitAmended,
		"second turn in same chat must amend HEAD, not append a new commit")
	require.NotEqual(t, firstSHA, res2.CommitSHA, "amend rewrites HEAD's SHA")
	require.Equal(t, initialHead, headParent(t, dir),
		"after amend, parent must still be the initial commit (no new history)")

	files := headFiles(t, dir)
	require.Contains(t, files, "app.yaml", "amended commit must keep the first turn's file")
	require.Contains(t, files, "second.txt", "amended commit must include the second turn's file")
}

// TestController_Send_DirectEdit_NoChangesNoCommit covers the
// negative case: a turn that doesn't touch any files must not produce
// a phantom commit (which would clutter history with empty / pointless
// "meta-mode:" commits whenever the user just chats with the agent).
func TestController_Send_DirectEdit_NoChangesNoCommit(t *testing.T) {
	dir, appFile := initStoryRepo(t)
	initialHead := headSHA(t, dir)

	c, _, _ := newTestController(t)
	c.Oracle = oracleFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// No file edits — pure conversation.
		return AskOutput{Reply: "let me know if you want me to change anything."}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	require.NoError(t, err)

	res, err := c.Send(context.Background(), s, "what does this room do?", TurnContext{AppFile: appFile})
	require.NoError(t, err)

	require.Empty(t, res.CommitSHA, "no-edit turn must not produce a commit")
	require.False(t, res.CommitAmended)
	require.Empty(t, res.CommitError)
	require.Empty(t, res.ChangedFiles)

	// HEAD must not have moved.
	require.Equal(t, initialHead, headSHA(t, dir),
		"HEAD must be unchanged after a no-edit turn")
}

// TestController_Send_DirectEdit_NoGitRepoSkipsCleanly mirrors the
// no-repo case from the legacy path: editing files in a directory
// that isn't a git repo must not error out; the changes still land,
// just without version control.
func TestController_Send_DirectEdit_NoGitRepoSkipsCleanly(t *testing.T) {
	dir := t.TempDir() // no git init
	appFile := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appFile, []byte(minValidManifest), 0o644))

	c, _, _ := newTestController(t)
	c.Oracle = oracleFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		require.NoError(t, os.WriteFile(appFile, validManifestWithDescription("edited-no-repo"), 0o644))
		return AskOutput{Reply: "edited"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	require.NoError(t, err)

	res, err := c.Send(context.Background(), s, "edit it", TurnContext{AppFile: appFile})
	require.NoError(t, err)

	require.NotEmpty(t, res.ChangedFiles, "ChangedFiles must still surface the edit")
	require.Empty(t, res.CommitSHA, "no-repo must skip commit silently")
	require.Empty(t, res.CommitError, "no-repo is a benign skip, not an error")

	// File still landed on disk.
	body, err := os.ReadFile(appFile)
	require.NoError(t, err)
	require.Contains(t, string(body), "edited-no-repo",
		"edit must have landed; got %q", string(body))
}

// TestController_Send_DirectEdit_BrokenYAMLBlocksCommit is the
// regression guard for the 2026-05-19 12:41 production incident: a
// meta-mode agent added a `continue:` arc to a room's `on:` block
// referencing an undeclared intent. The YAML was syntactically valid
// but the AppDef failed to load. Pre-fix, our auto-commit would have
// happily amended the broken state into HEAD; the user would have
// pushed a commit they couldn't reload. Post-fix, CommitChangedFiles
// runs `app.Load` against the manifest BEFORE staging — if it
// doesn't validate, no commit, and the failure is surfaced in
// SendResult.CommitError so the operator knows to fix the file before
// it gets pinned.
//
// The test simulates the exact production shape: oracle stub edits
// app.yaml to a YAML that parses but references an undeclared intent
// from an on: block. Result: ChangedFiles still surfaces the edit
// (reload can fire and the operator sees the broken state), but
// CommitSHA is empty and CommitError describes the load failure.
func TestController_Send_DirectEdit_BrokenYAMLBlocksCommit(t *testing.T) {
	dir, appFile := initStoryRepo(t)
	initialHead := headSHA(t, dir)

	// Seed the valid manifest in place of initStoryRepo's seed so the
	// initial-commit pre-state is well-formed; the oracle stub will
	// then break it during the turn. The seed already matches
	// minValidManifest, so this rewrite is a no-op content-wise but
	// keeps the test self-documenting about the precondition.
	require.NoError(t, os.WriteFile(appFile, []byte(minValidManifest), 0o644))
	// Refresh the initial commit so HEAD matches the seeded valid manifest.
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, string(out))
	}
	runGit("add", "app.yaml")
	runGit("commit", "--amend", "--no-edit")
	initialHead = headSHA(t, dir)

	c, _, _ := newTestController(t)
	c.Oracle = oracleFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// Break the manifest the same way the production trace did:
		// add an `on:` arc referencing an undeclared intent.
		brokenApp := `app: { id: t }
intents:
  go_main:
    description: "Go to main."
states:
  main:
    on:
      go_main:
        - target: main
      continue:
        - target: main
`
		require.NoError(t, os.WriteFile(appFile, []byte(brokenApp), 0o644))
		return AskOutput{Reply: "wired the continue intent"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	require.NoError(t, err)

	res, err := c.Send(context.Background(), s, "add a continue verb", TurnContext{AppFile: appFile})
	require.NoError(t, err, "Send turn must succeed even if validation rejects the commit")

	// File DID land (the agent's edit happened on disk).
	body, err := os.ReadFile(appFile)
	require.NoError(t, err)
	require.Contains(t, string(body), "continue:", "the agent's edit must be on disk")
	require.NotEmpty(t, res.ChangedFiles, "ChangedFiles must surface the edit so reload can fire")

	// But the COMMIT did NOT happen — broken YAML must never reach HEAD.
	require.Empty(t, res.CommitSHA, "broken AppDef must NOT produce a commit SHA")
	require.False(t, res.CommitAmended)
	require.NotEmpty(t, res.CommitError, "validation failure must surface in CommitError")
	require.Contains(t, res.CommitError, "does not validate",
		"CommitError must identify the failing step as validation; got %q", res.CommitError)
	require.Contains(t, res.CommitError, "continue",
		"CommitError must echo the AppDef loader's diagnostic so the operator can fix it; got %q", res.CommitError)

	// HEAD must NOT have moved — the broken state is uncommitted.
	require.Equal(t, initialHead, headSHA(t, dir),
		"HEAD must be unchanged when the post-edit AppDef doesn't validate")
}

// TestController_Send_DirectEdit_PreCommitHookFailureSurfaces
// verifies the best-effort contract: when git refuses (here, a
// pre-commit hook rejects), the file edits still land and the failure
// is surfaced via SendResult.CommitError without erroring the turn.
func TestController_Send_DirectEdit_PreCommitHookFailureSurfaces(t *testing.T) {
	dir, appFile := initStoryRepo(t)

	hookDir := filepath.Join(dir, ".git", "hooks")
	require.NoError(t, os.MkdirAll(hookDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hookDir, "pre-commit"),
		[]byte("#!/bin/sh\necho 'rejected'\nexit 1\n"), 0o755))

	c, _, _ := newTestController(t)
	c.Oracle = oracleFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		require.NoError(t, os.WriteFile(appFile, validManifestWithDescription("hook-blocked"), 0o644))
		return AskOutput{Reply: "edited"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	require.NoError(t, err)

	res, err := c.Send(context.Background(), s, "edit", TurnContext{AppFile: appFile})
	require.NoError(t, err, "Send turn must succeed even if commit fails")

	require.Empty(t, res.CommitSHA)
	require.NotEmpty(t, res.CommitError, "pre-commit hook rejection must surface in CommitError")
	require.Contains(t, res.CommitError, "git commit",
		"commit error must identify the failing git step; got %q", res.CommitError)
	require.NotEmpty(t, res.ChangedFiles, "ChangedFiles must still surface the edit so reload can fire")
}
