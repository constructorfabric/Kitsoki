package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/render"
	"kitsoki/internal/sysprompt"
)

// hasFlag reports whether args contains flag immediately followed by a value,
// returning that value; ok is false when the flag is absent.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func hasBareFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// TestAppendComposedSystemPrompt_ReplacesAndLayers: the default path composes
// kitsoki → project → (verb contract + persona) and passes it via
// --system-prompt, with --exclude-dynamic for a non-task verb.
func TestAppendComposedSystemPrompt_ReplacesAndLayers(t *testing.T) {
	ctx := WithProjectContext(context.Background(), ProjectContext{Inline: "PROJECT-CONTEXT-XYZ"})

	args, composed := appendComposedSystemPrompt(ctx, []string{"-p"}, sysprompt.Decide, "PERSONA-ABC", false)

	sp, ok := flagValue(args, "--system-prompt")
	require.True(t, ok, "must pass --system-prompt")
	assert.NotContains(t, args, "--append-system-prompt", "default path must replace, not append")
	assert.True(t, hasBareFlag(args, "--exclude-dynamic-system-prompt-sections"),
		"non-task verb must exclude Claude Code's dynamic sections")

	// All three layers present, in order.
	kIdx := strings.Index(sp, "operating inside **kitsoki**")
	pIdx := strings.Index(sp, "PROJECT-CONTEXT-XYZ")
	tIdx := strings.Index(sp, "PERSONA-ABC")
	require.True(t, kIdx >= 0 && pIdx >= 0 && tIdx >= 0, "all layers must be present")
	assert.Less(t, kIdx, pIdx, "kitsoki precedes project")
	assert.Less(t, pIdx, tIdx, "project precedes task")
	assert.Equal(t, []string{"kitsoki", "project", "task"}, sysprompt.LayerNames(composed.Layers))
}

// TestAppendComposedSystemPrompt_TaskKeepsDynamic: the task verb keeps Claude
// Code's dynamic sections (agentic repo work needs them).
func TestAppendComposedSystemPrompt_TaskKeepsDynamic(t *testing.T) {
	args, _ := appendComposedSystemPrompt(context.Background(), nil, sysprompt.Task, "PERSONA", false)
	_, ok := flagValue(args, "--system-prompt")
	require.True(t, ok)
	assert.False(t, hasBareFlag(args, "--exclude-dynamic-system-prompt-sections"),
		"task must KEEP Claude Code's dynamic sections")
}

// TestAppendComposedSystemPrompt_InheritEscapeHatch: inherit_claude_default
// falls back to the legacy --append posture with no kitsoki grounding.
func TestAppendComposedSystemPrompt_InheritEscapeHatch(t *testing.T) {
	args, composed := appendComposedSystemPrompt(context.Background(), nil, sysprompt.Decide, "PERSONA-ESC", true)
	sp, ok := flagValue(args, "--append-system-prompt")
	require.True(t, ok, "escape hatch must use --append-system-prompt")
	assert.Equal(t, "PERSONA-ESC", sp, "appended prompt is the bare persona, no kitsoki layer")
	assert.NotContains(t, args, "--system-prompt")
	assert.Empty(t, composed.Layers, "no layers composed on the legacy path")
}

// TestAppendComposedSystemPrompt_InheritEmptyPersona: escape hatch with no
// persona adds no system-prompt flag at all (the true legacy clean-call).
func TestAppendComposedSystemPrompt_InheritEmptyPersona(t *testing.T) {
	args, _ := appendComposedSystemPrompt(context.Background(), []string{"-p"}, sysprompt.Ask, "", true)
	assert.NotContains(t, args, "--append-system-prompt")
	assert.NotContains(t, args, "--system-prompt")
}

// TestResolveProjectLayer_Sources: inline wins; absent → empty (no renderer, no
// convention file on disk in this test's cwd).
func TestResolveProjectLayer_Sources(t *testing.T) {
	inline := resolveProjectLayer(WithProjectContext(context.Background(), ProjectContext{Inline: "INLINE-CTX"}))
	assert.Equal(t, "INLINE-CTX", inline)

	none := resolveProjectLayer(context.Background())
	assert.Empty(t, none, "no inline/path/convention → empty project layer")
}

// TestComposeAgentSystemPrompt_GroundedWithoutPersona: even with no persona
// the composed prompt carries the kitsoki layer — grounding is unconditional.
func TestComposeAgentSystemPrompt_GroundedWithoutPersona(t *testing.T) {
	composed := composeAgentSystemPrompt(context.Background(), sysprompt.Ask, "")
	assert.Contains(t, composed.SystemPrompt, "operating inside **kitsoki**")
	assert.Contains(t, sysprompt.LayerNames(composed.Layers), "kitsoki")
}

func TestComposeAgentSystemPrompt_ProjectSystemPromptFile(t *testing.T) {
	project := t.TempDir()
	story := filepath.Join(project, "stories", "demo")
	require.NoError(t, os.MkdirAll(filepath.Join(project, ".kitsoki"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(story, "prompts", "_shared"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, ".kitsoki", "system-prompt.md"), []byte(`PROJECT-KITSOKI
{% include "@shared/system-frag.md" %}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(story, "prompts", "_shared", "system-frag.md"), []byte(`SHARED-FRAG`), 0o644))

	pr, err := render.NewPromptRenderer(render.PromptPath{
		Story:  story,
		Shared: []string{filepath.Join(story, "prompts", "_shared")},
	}, true)
	require.NoError(t, err)

	ctx := WithPromptRenderer(context.Background(), pr)
	composed := composeAgentSystemPrompt(ctx, sysprompt.Ask, "PERSONA")
	assert.Contains(t, composed.SystemPrompt, "PROJECT-KITSOKI")
	assert.Contains(t, composed.SystemPrompt, "SHARED-FRAG")
	assert.NotContains(t, composed.SystemPrompt, "operating inside **kitsoki**")
	assert.Equal(t, []string{"kitsoki", "task"}, sysprompt.LayerNames(composed.Layers))
}

// TestComposeAgentSystemPrompt_StablePrefixAcrossCalls locks the
// dispatch-context-floor invariant at the ctx-resolving wrapper (not just the
// pure sysprompt.Compose below it): repeated calls with the same verb,
// project context, and persona produce byte-identical composed prompts. This
// is what makes the prefix cache-eligible — a per-call volatile field (a
// timestamp, a turn number, an artifact id) accidentally threaded through
// resolveProjectLayer/resolveKitsokiOverride or the persona itself would
// break this test.
func TestComposeAgentSystemPrompt_StablePrefixAcrossCalls(t *testing.T) {
	ctx := WithProjectContext(context.Background(), ProjectContext{Inline: "PROJECT-STABLE"})
	first := composeAgentSystemPrompt(ctx, sysprompt.Decide, "PERSONA-STABLE")
	for i := 0; i < 5; i++ {
		again := composeAgentSystemPrompt(ctx, sysprompt.Decide, "PERSONA-STABLE")
		assert.Equal(t, first.SystemPrompt, again.SystemPrompt,
			"composed system prompt must be byte-identical across repeat calls for the same (verb, agent) — any drift breaks prompt-cache eligibility")
	}
}
