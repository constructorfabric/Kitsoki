package authoring_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/authoring"
	"kitsoki/internal/host"
)

// fakeClaudeBin returns the absolute path to internal/authoring/
// testdata/fake-claude.sh. The fake reads the prompt from stdin and
// dispatches behaviour based on sentinel tokens inside the
// PROPOSAL section. cwd is the shadow story dir so any file edits
// land where authoring.diffDirs will see them.
func fakeClaudeBin(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-claude.sh requires bash")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	path := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-claude.sh")
	fi, err := os.Stat(path)
	require.NoError(t, err, "fake-claude.sh not found")
	require.NotZero(t, fi.Mode()&0o111, "fake-claude.sh is not executable")
	abs, err := filepath.Abs(path)
	require.NoError(t, err)
	return abs
}

// copyCloak makes a writable copy of the cloak app dir (app.yaml +
// siblings) in a temp dir so Propose+Apply round-trips don't touch
// the repo. Returns the path to the copied app.yaml.
func copyCloak(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "apps", "cloak")
	dst := t.TempDir()
	require.NoError(t, copyDir(src, dst))
	return filepath.Join(dst, "app.yaml")
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
}

// TestPropose_ModifiesYAMLFile drives the full pipeline: snapshot →
// fake claude appends a comment to app.yaml → diff → Proposal.
// Asserts the diff is captured and the real file is unchanged until
// Apply runs.
func TestPropose_ModifiesYAMLFile(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)
	originalBody, err := os.ReadFile(appPath)
	require.NoError(t, err)

	p, err := authoring.Propose(context.Background(), appPath, "ADD_LINE: append a marker", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = authoring.Discard(p) })

	require.Len(t, p.Changes, 1, "fake-claude should produce exactly one change")
	assert.Equal(t, "app.yaml", p.Changes[0].RelPath)
	assert.Equal(t, "modified", p.Changes[0].Kind)
	assert.Equal(t, "applied test edit", p.Summary)
	assert.Contains(t, p.UnifiedDiff, "fake-claude appended this comment")

	// Real file untouched until Apply.
	current, err := os.ReadFile(appPath)
	require.NoError(t, err)
	assert.Equal(t, string(originalBody), string(current),
		"Propose must not write to the real app dir")
}

// TestPropose_AddsNewFile verifies an "added" file change is detected.
func TestPropose_AddsNewFile(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	p, err := authoring.Propose(context.Background(), appPath, "ADD_FILE: drop a new prompt", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = authoring.Discard(p) })

	var added *authoring.FileChange
	for i := range p.Changes {
		if p.Changes[i].Kind == "added" {
			added = &p.Changes[i]
			break
		}
	}
	require.NotNil(t, added, "should detect the added file")
	assert.Equal(t, filepath.Join("prompts", "new_prompt.md"), added.RelPath)
	assert.Contains(t, string(added.After), "hello from fake-claude")
}

// TestApply_AppliesAllChanges checks that Apply copies files from the
// shadow dir back to the real app dir, then removes the shadow.
func TestApply_AppliesAllChanges(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)
	appDir := filepath.Dir(appPath)

	p, err := authoring.Propose(context.Background(), appPath,
		"ADD_LINE: marker; ADD_FILE: drop a prompt", nil)
	require.NoError(t, err)
	shadow := p.ShadowDir
	require.DirExists(t, shadow)

	require.NoError(t, authoring.Apply(p))

	// Modified file landed.
	body, err := os.ReadFile(appPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "fake-claude appended this comment")

	// Added file landed under prompts/.
	added, err := os.ReadFile(filepath.Join(appDir, "prompts", "new_prompt.md"))
	require.NoError(t, err)
	assert.Equal(t, "hello from fake-claude\n", string(added))

	// Shadow dir was cleaned up.
	_, err = os.Stat(shadow)
	assert.True(t, os.IsNotExist(err), "shadow dir should be removed after Apply")
}

// TestDiscard_RemovesShadow asserts Discard cleans up without
// touching the real app dir.
func TestDiscard_RemovesShadow(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)
	originalBody, err := os.ReadFile(appPath)
	require.NoError(t, err)

	p, err := authoring.Propose(context.Background(), appPath, "ADD_LINE: marker", nil)
	require.NoError(t, err)
	shadow := p.ShadowDir

	require.NoError(t, authoring.Discard(p))
	_, err = os.Stat(shadow)
	assert.True(t, os.IsNotExist(err))
	assert.Equal(t, "", p.ShadowDir)

	// Real file still untouched.
	current, err := os.ReadFile(appPath)
	require.NoError(t, err)
	assert.Equal(t, string(originalBody), string(current))
}

// TestPropose_RefusedReturnsTypedError verifies an ERROR: reply
// surfaces as ErrClaudeRefused, including the multi-line follow-up
// (the "right place to edit is likely…" hint).
func TestPropose_RefusedReturnsTypedError(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "REFUSE this proposal", nil)
	require.Error(t, err)
	var refused *authoring.ErrClaudeRefused
	require.True(t, errors.As(err, &refused), "want ErrClaudeRefused, got %T", err)
	assert.Contains(t, refused.Reason, "ambiguous")
	assert.Contains(t, refused.Reason, "right place to edit",
		"refusal should preserve the multi-line hint")
}

// TestPropose_NoChangesReturnsSentinel ensures we don't pretend a
// no-op run was successful — the user has to refine and retry.
func TestPropose_NoChangesReturnsSentinel(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "NO_CHANGES placeholder", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, authoring.ErrNoChanges))
}

// TestPropose_BrokenYAMLRejected guards the validation gate: if
// Claude leaves the manifest in a non-loadable state, Propose
// refuses to return a Proposal.
func TestPropose_BrokenYAMLRejected(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "BREAK_YAML on purpose", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not validate")
}

// TestPropose_ExecFailure surfaces an exec error verbatim.
func TestPropose_ExecFailure(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "FAIL_EXEC", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude exec failed")
}

// TestPropose_BinaryMissing returns ErrClaudeUnavailable (or an
// exec error) when no claude binary can be located.
func TestPropose_BinaryMissing(t *testing.T) {
	t.Setenv(host.OracleBinEnv, "/definitely/does/not/exist/claude")
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "anything", nil)
	require.Error(t, err)
	if !errors.Is(err, authoring.ErrClaudeUnavailable) {
		assert.True(t, strings.Contains(err.Error(), "exec failed"),
			"want ErrClaudeUnavailable or exec failure, got %v", err)
	}
}

// TestPropose_EmptyProposalRejected blocks accidentally invoking
// claude on a blank prompt.
func TestPropose_EmptyProposalRejected(t *testing.T) {
	appPath := copyCloak(t)
	_, err := authoring.Propose(context.Background(), appPath, "   \n\t", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty proposal")
}
