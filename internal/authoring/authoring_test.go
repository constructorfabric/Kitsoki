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

	"hally/internal/authoring"
	"hally/internal/host"
)

// fakeClaudeBin returns the path to internal/authoring/testdata/fake-claude.sh.
// The fake reads the prompt from stdin and dispatches behaviour based on
// sentinel tokens inside the PROPOSAL section.
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
	return path
}

// copyCloak makes a writable copy of testdata/apps/cloak/app.yaml in a
// temp dir so Propose+Apply round-trips don't mutate the repo.
func copyCloak(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	body, err := os.ReadFile(src)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(dst, body, 0o644))
	return dst
}

// TestPropose_HappyPath_NoOpEdit verifies that the full prompt-claude-parse
// pipeline succeeds and produces an empty diff when the fake echoes the
// yaml back unchanged.
func TestPropose_HappyPath_NoOpEdit(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	p, err := authoring.Propose(context.Background(), appPath, "no-op proposal")
	require.NoError(t, err)
	assert.Equal(t, appPath, p.AppPath)
	assert.NotEmpty(t, p.OriginalYAML)
	assert.NotEmpty(t, p.NewYAML)
	assert.Equal(t, "applied test edit", p.Summary)
	// No-op echo: the diff should be empty (no +/- lines).
	assert.Empty(t, p.UnifiedDiff, "expected empty diff for no-op echo")
}

// TestPropose_HappyPath_WithEdit checks the diff is non-empty when the
// fake actually changes the yaml (ADD_LINE sentinel appends a comment).
func TestPropose_HappyPath_WithEdit(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	p, err := authoring.Propose(context.Background(), appPath, "ADD_LINE: append a marker")
	require.NoError(t, err)
	assert.NotEmpty(t, p.UnifiedDiff, "diff should be populated when content changes")
	assert.Contains(t, p.UnifiedDiff, "fake-claude appended this comment",
		"diff should reference the appended comment")
	// The original file is unchanged on disk until Apply is called.
	current, err := os.ReadFile(appPath)
	require.NoError(t, err)
	assert.Equal(t, p.OriginalYAML, current, "Propose must not write to disk")
}

// TestApply_WritesNewYAML verifies Apply writes p.NewYAML to disk and
// the new content load-validates.
func TestApply_WritesNewYAML(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	p, err := authoring.Propose(context.Background(), appPath, "ADD_LINE: marker")
	require.NoError(t, err)

	require.NoError(t, authoring.Apply(p))
	got, err := os.ReadFile(appPath)
	require.NoError(t, err)
	assert.Equal(t, string(p.NewYAML), string(got))
}

// TestPropose_RefusedReturnsTypedError checks that an `ERROR:`-only reply
// surfaces as ErrClaudeRefused with the reason text.
func TestPropose_RefusedReturnsTypedError(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "REFUSE this proposal")
	require.Error(t, err)
	var refused *authoring.ErrClaudeRefused
	require.True(t, errors.As(err, &refused), "want ErrClaudeRefused, got %T", err)
	assert.Contains(t, refused.Reason, "ambiguous")
}

// TestPropose_InvalidYAMLRejected ensures we don't accept a diff whose
// new content fails to load.
func TestPropose_InvalidYAMLRejected(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "RETURN_INVALID please")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not validate")
}

// TestPropose_MalformedReplyRejected ensures prose-only replies (no
// SUMMARY, no yaml fence) are rejected.
func TestPropose_MalformedReplyRejected(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "MALFORMED reply")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ```yaml block")
}

// TestPropose_ExecFailure surfaces the exec error verbatim.
func TestPropose_ExecFailure(t *testing.T) {
	t.Setenv(host.OracleBinEnv, fakeClaudeBin(t))
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "FAIL_EXEC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude exec failed")
}

// TestPropose_BinaryMissing verifies we return ErrClaudeUnavailable when
// no claude binary can be found.
func TestPropose_BinaryMissing(t *testing.T) {
	t.Setenv(host.OracleBinEnv, "/definitely/does/not/exist/claude")
	appPath := copyCloak(t)

	_, err := authoring.Propose(context.Background(), appPath, "anything")
	require.Error(t, err)
	// Either ErrClaudeUnavailable (LookPath failed too) or an exec error
	// referencing the bogus path.
	if !errors.Is(err, authoring.ErrClaudeUnavailable) {
		assert.True(t, strings.Contains(err.Error(), "exec failed"),
			"expected ErrClaudeUnavailable or exec failure, got %v", err)
	}
}

// TestPropose_EmptyProposalRejected guards against accidentally invoking
// claude on a blank prompt.
func TestPropose_EmptyProposalRejected(t *testing.T) {
	appPath := copyCloak(t)
	_, err := authoring.Propose(context.Background(), appPath, "   \n\t")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty proposal")
}
