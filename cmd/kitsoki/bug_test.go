package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBugSlug covers the freeform-title → filesystem-slug mapping.
// The function is small but load-bearing — a sloppy slug produces
// unreadable filenames and confuses grep, so the corner cases get
// their own row.
func TestBugSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Hello World", "hello-world"},
		{"  Lots of   spaces  ", "lots-of-spaces"},
		{"Special!! chars$$$ matter#?", "special-chars-matter"},
		{"UPPERCASE", "uppercase"},
		{"123 numbers ok", "123-numbers-ok"},
		{"hyphens-survive", "hyphens-survive"},
		{"trailing-hyphens---", "trailing-hyphens"},
		{"", "bug"},
		{"!!!", "bug"}, // nothing slug-worthy → fallback
		{strings.Repeat("a", 100), strings.Repeat("a", 60)},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.want, bugSlug(tc.in))
		})
	}
}

// TestBugFilename composes the slug with a UTC timestamp.
func TestBugFilename(t *testing.T) {
	ts := time.Date(2026, 5, 13, 10, 32, 5, 0, time.UTC)
	got := bugFilename(ts, "TUI hangs on Esc")
	require.Equal(t, "2026-05-13T103205Z-tui-hangs-on-esc.md", got)
}

// TestRenderBugMarkdown_FullStoryPayload asserts the rendered file
// body contains every story-target field, with the YAML front-matter
// properly quoted, section comments present, and the body separated
// from front-matter by a blank line.
func TestRenderBugMarkdown_FullStoryPayload(t *testing.T) {
	ts := time.Date(2026, 5, 13, 10, 32, 5, 0, time.UTC)
	got := renderBugMarkdown(bugRecord{
		ID:         "2026-05-13T103205Z-tui-hangs-on-esc",
		Title:      "TUI hangs on Esc",
		Body:       "Expected the Esc menu to open.\nGot a frozen prompt instead.",
		ReproSteps: []string{"Run cloak", "Press Esc at the foyer"},
		Target:     "story",
		FiledAt:    ts,
		FiledBy:    "brad",
		AppID:      "cloak",
		StatePath:  "main.foyer",
		Severity:   "med",
		Status:     "open",
		TraceRef:   "traces/2026-05-13T103200Z-cloak.jsonl",
	})

	// Identity section.
	require.Contains(t, got, "# --- identity ---")
	require.Contains(t, got, `id: "2026-05-13T103205Z-tui-hangs-on-esc"`)
	require.Contains(t, got, `title: "TUI hangs on Esc"`)
	require.Contains(t, got, `target: "story"`)
	require.Contains(t, got, "filed_at: 2026-05-13T10:32:05Z")
	require.Contains(t, got, `filed_by: "brad"`)
	// Target context section.
	require.Contains(t, got, "# --- target context ---")
	require.Contains(t, got, `app_id: "cloak"`)
	require.Contains(t, got, `state_path: "main.foyer"`)
	require.NotContains(t, got, "component:")
	require.NotContains(t, got, "kitsoki_rev:")
	// Classification section.
	require.Contains(t, got, "# --- classification ---")
	require.Contains(t, got, `severity: "med"`)
	require.Contains(t, got, `status: "open"`)
	require.Contains(t, got, "labels: []")
	// Evidence section.
	require.Contains(t, got, "# --- evidence ---")
	require.Contains(t, got, `trace_ref: "traces/2026-05-13T103200Z-cloak.jsonl"`)
	require.Contains(t, got, "related: []")
	// Body.
	require.Contains(t, got, "# TUI hangs on Esc")
	require.Contains(t, got, "Expected the Esc menu to open.")
	require.Contains(t, got, "## Steps to reproduce")
	require.Contains(t, got, "1. Run cloak")
	require.Contains(t, got, "2. Press Esc at the foyer")
}

// TestRenderBugMarkdown_KitsokiPayload covers the kitsoki-target
// branch: component + kitsoki_rev replace app_id + state_path in the
// target-context block.
func TestRenderBugMarkdown_KitsokiPayload(t *testing.T) {
	ts := time.Date(2026, 5, 13, 10, 32, 5, 0, time.UTC)
	got := renderBugMarkdown(bugRecord{
		ID:         "2026-05-13T103205Z-tui-hangs-on-esc",
		Title:      "TUI hangs on Esc",
		Body:       "Esc froze the TUI.",
		Target:     "kitsoki",
		FiledAt:    ts,
		Component:  "tui",
		KitsokiRev: "7331630",
		Status:     "open",
	})

	require.Contains(t, got, `target: "kitsoki"`)
	require.Contains(t, got, `component: "tui"`)
	require.Contains(t, got, `kitsoki_rev: "7331630"`)
	require.NotContains(t, got, "app_id:")
	require.NotContains(t, got, "state_path:")
	require.NotContains(t, got, "filed_by:") // empty -> omitted
}

// TestRenderBugMarkdown_MinimalPayload asserts optional fields are
// omitted from front-matter, the target-context section is skipped
// entirely when empty, and "Steps to reproduce" is omitted when no
// repro steps were given. status / labels / related are always
// emitted as hand-edit hooks.
func TestRenderBugMarkdown_MinimalPayload(t *testing.T) {
	ts := time.Date(2026, 5, 13, 10, 32, 5, 0, time.UTC)
	got := renderBugMarkdown(bugRecord{
		ID:      "2026-05-13T103205Z-minimal",
		Title:   "minimal",
		Body:    "just the title and body",
		Target:  "story",
		FiledAt: ts,
		Status:  "open",
	})
	require.Contains(t, got, `title: "minimal"`)
	require.NotContains(t, got, "app_id:")
	require.NotContains(t, got, "state_path:")
	require.NotContains(t, got, "component:")
	require.NotContains(t, got, "kitsoki_rev:")
	require.NotContains(t, got, "filed_by:")
	require.NotContains(t, got, "severity:")
	require.NotContains(t, got, "trace_ref:")
	require.NotContains(t, got, "## Steps to reproduce")
	require.NotContains(t, got, "# --- target context ---")
	// Always emitted.
	require.Contains(t, got, "# --- identity ---")
	require.Contains(t, got, "# --- classification ---")
	require.Contains(t, got, "# --- evidence ---")
	require.Contains(t, got, `status: "open"`)
	require.Contains(t, got, "labels: []")
	require.Contains(t, got, "related: []")
}

// TestYAMLQuoteLine escapes embedded quotes and newlines so the
// front-matter line stays parseable.
func TestYAMLQuoteLine(t *testing.T) {
	require.Equal(t, `"hello"`, yamlQuoteLine("hello"))
	require.Equal(t, `"says \"hi\""`, yamlQuoteLine(`says "hi"`))
	require.Equal(t, `"line1\nline2"`, yamlQuoteLine("line1\nline2"))
	require.Equal(t, `"path\\to\\file"`, yamlQuoteLine(`path\to\file`))
}

// TestBugCreateCmd_WritesMarkdownFile drives the cobra command
// end-to-end against a temp dir and asserts the produced file's path
// + contents. Default target is `story`; output path is relative to
// the resolved target-root.
func TestBugCreateCmd_WritesMarkdownFile(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "create",
		"--target", "story",
		"--title", "Esc menu hang",
		"--body", "Pressing Esc froze the TUI.",
		"--repro", "Run cloak",
		"--repro", "Press Esc",
		"--state-path", "main.foyer",
		"--app-id", "cloak",
		"--severity", "med",
		"--target-dir", tmp,
		"--clock-now", "1747130000", // deterministic timestamp
	})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)

	require.NoError(t, root.Execute())

	relPath := strings.TrimSpace(out.String())
	// 1747130000 Unix seconds = 2025-05-13 09:53:20 UTC.
	require.Equal(t,
		filepath.Join("issues", "bugs", "2025-05-13T095320Z-esc-menu-hang.md"),
		relPath,
	)

	abs := filepath.Join(tmp, relPath)
	contents, err := os.ReadFile(abs)
	require.NoError(t, err)

	body := string(contents)
	require.Contains(t, body, `id: "2025-05-13T095320Z-esc-menu-hang"`)
	require.Contains(t, body, `title: "Esc menu hang"`)
	require.Contains(t, body, `target: "story"`)
	require.Contains(t, body, `app_id: "cloak"`)
	require.Contains(t, body, `state_path: "main.foyer"`)
	require.Contains(t, body, `severity: "med"`)
	require.Contains(t, body, `status: "open"`)
	require.Contains(t, body, "labels: []")
	require.Contains(t, body, "related: []")
	require.Contains(t, body, "Pressing Esc froze the TUI.")
	require.Contains(t, body, "1. Run cloak")
	require.Contains(t, body, "2. Press Esc")
}

// TestBugCreateCmd_TargetRequired asserts that --target must be set
// and must be one of story|kitsoki.
func TestBugCreateCmd_TargetRequired(t *testing.T) {
	tmp := t.TempDir()
	t.Run("missing target", func(t *testing.T) {
		root := newRootCmd()
		root.SetArgs([]string{
			"bug", "create",
			"--title", "x",
			"--body", "y",
			"--target-dir", tmp,
		})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "--target is required")
	})
	t.Run("bogus target", func(t *testing.T) {
		root := newRootCmd()
		root.SetArgs([]string{
			"bug", "create",
			"--target", "unicorn",
			"--title", "x",
			"--body", "y",
			"--target-dir", tmp,
		})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		err := root.Execute()
		require.Error(t, err)
		require.Contains(t, err.Error(), "story or kitsoki")
	})
}

// TestBugCreateCmd_KitsokiTarget asserts --target kitsoki with an
// explicit --target-dir writes under <tmp>/issues/bugs/ and emits a
// `target: "kitsoki"` field. The directory itself is not a real git
// repo, so kitsoki_rev is left empty and the command emits a warning
// to stderr — that warning is non-fatal and the bug file is still
// written.
func TestBugCreateCmd_KitsokiTarget(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "create",
		"--target", "kitsoki",
		"--title", "Esc menu hang",
		"--body", "TUI froze.",
		"--component", "tui",
		"--target-dir", tmp,
		"--clock-now", "1747130000",
	})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)

	require.NoError(t, root.Execute())

	relPath := strings.TrimSpace(out.String())
	require.Equal(t,
		filepath.Join("issues", "bugs", "2025-05-13T095320Z-esc-menu-hang.md"),
		relPath,
	)
	abs := filepath.Join(tmp, relPath)
	contents, err := os.ReadFile(abs)
	require.NoError(t, err)
	body := string(contents)
	require.Contains(t, body, `target: "kitsoki"`)
	require.Contains(t, body, `component: "tui"`)
}

// TestBugCreateCmd_KitsokiTargetMissingRoot covers the error path
// when neither --target-dir nor $KITSOKI_REPO is set.
func TestBugCreateCmd_KitsokiTargetMissingRoot(t *testing.T) {
	t.Setenv("KITSOKI_REPO", "")
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "create",
		"--target", "kitsoki",
		"--title", "x",
		"--body", "y",
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--target-dir or $KITSOKI_REPO")
}

// TestBugCreateCmd_StoryOnlyFlagsWarnOnKitsoki asserts that
// --state-path / --app-id with --target kitsoki produce a stderr
// warning but do not fail the command, and are not written to the
// frontmatter.
func TestBugCreateCmd_StoryOnlyFlagsWarnOnKitsoki(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "create",
		"--target", "kitsoki",
		"--title", "x",
		"--body", "y",
		"--state-path", "main.foyer",
		"--app-id", "cloak",
		"--target-dir", tmp,
		"--clock-now", "1747130000",
	})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	require.NoError(t, root.Execute())

	warnings := errBuf.String()
	require.Contains(t, warnings, "--state-path is story-only")
	require.Contains(t, warnings, "--app-id is story-only")

	relPath := strings.TrimSpace(out.String())
	contents, err := os.ReadFile(filepath.Join(tmp, relPath))
	require.NoError(t, err)
	require.NotContains(t, string(contents), "state_path:")
	require.NotContains(t, string(contents), "app_id:")
}

// TestBugCreateCmd_KitsokiOnlyFlagsWarnOnStory is the reverse: a
// --component on a story bug warns but does not fail.
func TestBugCreateCmd_KitsokiOnlyFlagsWarnOnStory(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "create",
		"--target", "story",
		"--title", "x",
		"--body", "y",
		"--component", "tui",
		"--target-dir", tmp,
		"--clock-now", "1747130000",
	})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	require.NoError(t, root.Execute())

	require.Contains(t, errBuf.String(), "--component is kitsoki-only")

	relPath := strings.TrimSpace(out.String())
	contents, err := os.ReadFile(filepath.Join(tmp, relPath))
	require.NoError(t, err)
	require.NotContains(t, string(contents), "component:")
}

// TestBugCreateCmd_RequiresTitle asserts the command fails (no file
// written) when --title is missing or whitespace-only.
func TestBugCreateCmd_RequiresTitle(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "create",
		"--target", "story",
		"--body", "no title",
		"--target-dir", tmp,
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "title")

	// No issues/ directory should exist if the command bailed before
	// the write.
	_, statErr := os.Stat(filepath.Join(tmp, "issues"))
	require.True(t, os.IsNotExist(statErr), "no issues/ should be created on validation failure")
}

// TestBugCreateCmd_RequiresBody asserts the command fails when --body
// is missing.
func TestBugCreateCmd_RequiresBody(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "create",
		"--target", "story",
		"--title", "only title",
		"--target-dir", tmp,
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "body")
}

// TestBugListCmd_HappyPath writes two bug files and asserts list
// prints them newest-first with id/severity/status/title columns.
func TestBugListCmd_HappyPath(t *testing.T) {
	tmp := t.TempDir()

	// Bug 1 — earlier timestamp, severity med.
	root1 := newRootCmd()
	root1.SetArgs([]string{
		"bug", "create",
		"--target", "story",
		"--title", "First bug",
		"--body", "alpha",
		"--severity", "med",
		"--target-dir", tmp,
		"--clock-now", "1747130000",
	})
	var out1 bytes.Buffer
	root1.SetOut(&out1)
	root1.SetErr(&out1)
	require.NoError(t, root1.Execute())

	// Bug 2 — later timestamp, no severity (should render as "?").
	root2 := newRootCmd()
	root2.SetArgs([]string{
		"bug", "create",
		"--target", "story",
		"--title", "Second bug",
		"--body", "beta",
		"--target-dir", tmp,
		"--clock-now", "1747140000",
	})
	var out2 bytes.Buffer
	root2.SetOut(&out2)
	root2.SetErr(&out2)
	require.NoError(t, root2.Execute())

	listRoot := newRootCmd()
	listRoot.SetArgs([]string{
		"bug", "list",
		"--target", "story",
		"--target-dir", tmp,
	})
	var listOut bytes.Buffer
	listRoot.SetOut(&listOut)
	listRoot.SetErr(&listOut)
	require.NoError(t, listRoot.Execute())

	lines := strings.Split(strings.TrimSpace(listOut.String()), "\n")
	require.Len(t, lines, 2, "expected two list rows, got %q", listOut.String())

	// Newest first.
	require.Contains(t, lines[0], "2025-05-13T124000Z-second-bug")
	require.Contains(t, lines[0], "Second bug")
	require.Contains(t, lines[0], "?")
	require.Contains(t, lines[0], "open")

	require.Contains(t, lines[1], "2025-05-13T095320Z-first-bug")
	require.Contains(t, lines[1], "First bug")
	require.Contains(t, lines[1], "med")
	require.Contains(t, lines[1], "open")
}

// TestBugListCmd_MissingDir asserts that listing against a target
// with no issues/bugs/ directory prints nothing and exits cleanly.
func TestBugListCmd_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "list",
		"--target", "story",
		"--target-dir", tmp,
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	require.NoError(t, root.Execute())
	require.Empty(t, strings.TrimSpace(out.String()))
}

// TestBugShowCmd_HappyPath writes a bug and reads it back verbatim
// via `kitsoki bug show <id>`.
func TestBugShowCmd_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	createRoot := newRootCmd()
	createRoot.SetArgs([]string{
		"bug", "create",
		"--target", "story",
		"--title", "Esc menu hang",
		"--body", "TUI froze.",
		"--target-dir", tmp,
		"--clock-now", "1747130000",
	})
	var createOut bytes.Buffer
	createRoot.SetOut(&createOut)
	createRoot.SetErr(&createOut)
	require.NoError(t, createRoot.Execute())

	relPath := strings.TrimSpace(createOut.String())
	id := strings.TrimSuffix(filepath.Base(relPath), ".md")
	expected, err := os.ReadFile(filepath.Join(tmp, relPath))
	require.NoError(t, err)

	showRoot := newRootCmd()
	showRoot.SetArgs([]string{
		"bug", "show", id,
		"--target", "story",
		"--target-dir", tmp,
	})
	var showOut bytes.Buffer
	showRoot.SetOut(&showOut)
	showRoot.SetErr(&showOut)
	require.NoError(t, showRoot.Execute())
	require.Equal(t, string(expected), showOut.String())
}

// TestBugShowCmd_MissingFile asserts a clean error when the id has
// no backing file.
func TestBugShowCmd_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{
		"bug", "show", "nope",
		"--target", "story",
		"--target-dir", tmp,
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `bug "nope" not found`)
}
