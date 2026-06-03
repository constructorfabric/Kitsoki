package host_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// ide_ambient_test.go — the "always injected when /ide is connected" behavior:
// when an editor selection rode the turn (host.WithIDEAmbient on the ctx), the
// operator-facing oracle verbs (ask, ask_with_mcp, converse) append the
// standardized selection block to the prompt without the story prompt having to
// reference args.ide. The decision verbs (decide/extract) and the task
// delegation verb must NOT, so routing/extraction and sub-agent context stay
// unbiased by an editor selection.

const ambientHeader = "## Active editor selection (via /ide)"

const ambientSelection = "func answer() int {\n\treturn 42\n}"

// ambientCtxOn returns a ctx carrying a non-empty editor selection.
func ambientCtxOn(parent context.Context) context.Context {
	return host.WithIDEAmbient(parent, host.IDEAmbient{
		File:      "/home/cloud-user/code/kitsoki/internal/host/oracle_ask.go",
		Selection: ambientSelection,
		Lines:     3,
		Range:     "10:0-12:1",
	})
}

// captureStdinRunner records the stdin (the rendered prompt) handed to the
// claude runner and returns a trivial success, so a test can assert what the
// handler dispatched without forking a subprocess.
func captureStdinRunner(into *string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, stdin, _ string) (host.ClaudeRun, error) {
		*into = stdin
		return host.ClaudeRun{Stdout: "ok"}, nil
	}
}

func TestIDEAmbientPreamble(t *testing.T) {
	t.Parallel()

	t.Run("absent without ambient", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, host.IDEAmbientPreamble(context.Background()),
			"no ambient on ctx must render no preamble")
	})

	t.Run("absent when selection empty", func(t *testing.T) {
		t.Parallel()
		// File set but no selected text — nothing to inject.
		ctx := host.WithIDEAmbient(context.Background(), host.IDEAmbient{File: "/x.go"})
		assert.Empty(t, host.IDEAmbientPreamble(ctx),
			"a file with no selection must render no preamble")
	})

	t.Run("renders header, file, range and selection", func(t *testing.T) {
		t.Parallel()
		got := host.IDEAmbientPreamble(ambientCtxOn(context.Background()))
		assert.Contains(t, got, ambientHeader)
		assert.Contains(t, got, "internal/host/oracle_ask.go")
		assert.Contains(t, got, "10:0-12:1")
		assert.Contains(t, got, ambientSelection)
	})
}

func TestOracleAsk_InjectsIDESelection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(p, []byte("INSTRUCTIONS"), 0o644))

	t.Run("with selection", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(ambientCtxOn(context.Background()), captureStdinRunner(&stdin))
		res, err := host.OracleAskHandler(ctx, map[string]any{"prompt_path": p})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		assert.Contains(t, stdin, "INSTRUCTIONS", "the original prompt must survive")
		assert.Contains(t, stdin, ambientHeader, "selection block must be appended")
		assert.Contains(t, stdin, ambientSelection)
	})

	t.Run("without selection is byte-identical", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(context.Background(), captureStdinRunner(&stdin))
		res, err := host.OracleAskHandler(ctx, map[string]any{"prompt_path": p})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		assert.Equal(t, "INSTRUCTIONS", stdin,
			"no editor selection must leave the prompt unchanged")
	})
}

func TestOracleConverse_InjectsIDESelection(t *testing.T) {
	t.Parallel()

	t.Run("with selection", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(ambientCtxOn(context.Background()), captureStdinRunner(&stdin))
		res, err := host.OracleConverseHandler(ctx, map[string]any{"question": "do this idea"})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		// converse passes the question via stdin in the stub path; the block
		// rides the conversational turn exactly like the typed text.
		assert.Contains(t, stdin, "do this idea")
		assert.Contains(t, stdin, ambientHeader)
		assert.Contains(t, stdin, ambientSelection)
	})

	t.Run("without selection is unchanged", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(context.Background(), captureStdinRunner(&stdin))
		res, err := host.OracleConverseHandler(ctx, map[string]any{"question": "do this idea"})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		assert.NotContains(t, stdin, ambientHeader)
	})
}

func TestOracleAskWithMCP_InjectsIDESelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotMCPBin(t))

	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(p, []byte("INSTRUCTIONS"), 0o644))

	// The fake bin echoes the prompt it received back as stdout, so the echoed
	// output is the rendered prompt the handler dispatched.
	res, err := host.OracleAskWithMCPHandler(ambientCtxOn(context.Background()),
		map[string]any{"prompt_path": p})
	require.NoError(t, err)
	require.Empty(t, res.Error, "unexpected Result.Error: %s", res.Error)
	out, _ := res.Data["stdout"].(string)
	assert.Contains(t, out, "INSTRUCTIONS")
	assert.Contains(t, out, ambientHeader)
	assert.Contains(t, out, ambientSelection)
}

// TestOracleDecide_DoesNotInjectIDESelection is the exclusion guarantee: a
// routing/gate decision must not be biased by whatever the operator happens to
// have selected in the editor. Even with a selection on the ctx, the decide
// prompt must be free of the ambient block.
func TestOracleDecide_DoesNotInjectIDESelection(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	promptPath := makePromptFile(t, "Should we proceed?")

	var stdin string
	ctx := host.WithClaudeRunner(ambientCtxOn(context.Background()), captureStdinRunner(&stdin))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"schema":      schemaPath,
	})
	require.NoError(t, err)
	require.Empty(t, res.Error)
	assert.NotContains(t, stdin, ambientHeader,
		"decide must not carry the editor selection")
}
