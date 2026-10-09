package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// ── scanUnknownTemplateNamespaces (pure scanner) ─────────────────────────────

func TestScanUnknownTemplateNamespaces_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "known namespace does not warn",
			text: "Hello {{ world.foo }} and {{ args.bar }}",
			want: nil,
		},
		{
			name: "the historical bug: unknown context namespace warns",
			text: "Leg:\n```\n{{ context.args.leg_json }}\n```",
			want: []string{"context"},
		},
		{
			name: "nested dotted chain only flags the root, not intermediate segments",
			text: "{{ world.leg_results.items }}",
			want: nil,
		},
		{
			name: "unknown root inside a nested chain still flags only the root",
			text: "{{ ctx.slots.request }}",
			want: []string{"ctx"},
		},
		{
			name: "for-loop-bound single variable is not flagged",
			text: "{% for leg in world.leg_results.items %}{{ leg.transport }} verdict={{ leg.verdict }}{% endfor %}",
			want: nil,
		},
		{
			name: "for-loop-bound two-variable form is not flagged",
			text: "{% for k, v in world.pairs.items %}{{ k }}={{ v.value }}{% endfor %}",
			want: nil,
		},
		{
			name: "filter chains do not produce a spurious match",
			text: "{% for h in args.search_hits %}{{ h.score|floatformat:2 }}{% endfor %}",
			want: nil,
		},
		{
			name: "plain prose outside tags is never scanned",
			text: "See e.g. docs/foo.md for context.args details, but no tag here.",
			want: nil,
		},
		{
			name: "multiple distinct unknown namespaces are deduped and sorted",
			text: "{{ context.a }} {{ context.b }} {{ inputs.c }}",
			want: []string{"context", "inputs"},
		},
		{
			name: "forloop builtin is not flagged",
			text: "{% for x in world.items %}{{ forloop.Counter }}: {{ x.name }}{% endfor %}",
			want: nil,
		},
		{
			name: "dotted-looking prose inside a quoted default fallback is not flagged",
			text: `{{ world.rollup_missing_proof_handoff_summary|default:"After rollup, open rollup.md for last_result.driver_scenarios evidence." }}`,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanUnknownTemplateNamespaces(tc.text)
			require.Equal(t, tc.want, got)
		})
	}
}

// ── collectViewTemplateNamespaceWarnings ─────────────────────────────────────

func TestCollectViewTemplateNamespaceWarnings(t *testing.T) {
	cases := []struct {
		name       string
		state      *State
		wantIdents []string
	}{
		{
			name:       "unknown namespace in inline view warns",
			state:      &State{View: LegacyView("{{ context.foo }}")},
			wantIdents: []string{"context"},
		},
		{
			name:       "known namespaces do not warn",
			state:      &State{View: LegacyView("{{ world.status }} {{ slots.request }}")},
			wantIdents: nil,
		},
		{
			name:       "external template file is skipped (not inline-scannable)",
			state:      &State{View: View{TemplateFile: "diff.pongo"}},
			wantIdents: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			states := map[string]*State{"s": tc.state}
			got := collectViewTemplateNamespaceWarnings("", states)
			var idents []string
			for _, w := range got {
				require.Contains(t, w.Location, `"s"`)
				idents = append(idents, w.Ident)
			}
			require.ElementsMatch(t, tc.wantIdents, idents, "warnings mismatch: %#v", got)
		})
	}
}

// ── collectPromptTemplateNamespaceWarnings ───────────────────────────────────

// TestCollectPromptTemplateNamespaceWarnings_RealBugShape reproduces the exact
// shape of the two real bugs (0bb3c77e, 46a8a0e1): a host.agent.task effect's
// `with: {prompt: "prompts/foo.md"}` pointing at a file that references
// `{{ context.* }}`, a namespace that was never populated for prompt
// rendering. It also proves a clean prompt (using `args.*`) produces no
// warning, and that a relative prompt path resolves against baseDir the same
// way host.resolvePromptPath resolves it against KITSOKI_APP_DIR at runtime.
func TestCollectPromptTemplateNamespaceWarnings_RealBugShape(t *testing.T) {
	dir := t.TempDir()
	promptsDir := filepath.Join(dir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o755))

	broken := "You are driving a leg:\n```\n{{ context.args.leg_json }}\n```\n"
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "broken.md"), []byte(broken), 0o644))

	clean := "You are driving a leg:\n```\n{{ args.leg_json }}\n```\n"
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "clean.md"), []byte(clean), 0o644))

	states := map[string]*State{
		"broken_state": {
			OnEnter: []Effect{{
				Invoke: "host.agent.task",
				With:   map[string]any{"prompt": "prompts/broken.md"},
			}},
		},
		"clean_state": {
			On: map[string][]Transition{
				"go": {{
					Target: ".",
					Effects: []Effect{{
						Invoke: "host.agent.task",
						With:   map[string]any{"prompt": "prompts/clean.md"},
					}},
				}},
			},
		},
	}

	got := collectPromptTemplateNamespaceWarnings("", states, dir)
	require.Len(t, got, 1, "only the broken prompt should warn: %#v", got)
	require.Equal(t, "context", got[0].Ident)
	require.Contains(t, got[0].Location, `"broken_state"`)
	require.Contains(t, got[0].Location, "prompts/broken.md")
}

// TestCollectPromptTemplateNamespaceWarnings_UnreadablePromptSkipped proves a
// prompt path that can't be resolved (baseDir empty) or read (missing file)
// is silently skipped rather than treated as an error — this is a
// best-effort static lint, not a load-time file-existence check (that is a
// different, already-covered concern).
func TestCollectPromptTemplateNamespaceWarnings_UnreadablePromptSkipped(t *testing.T) {
	states := map[string]*State{
		"s": {
			OnEnter: []Effect{{
				Invoke: "host.agent.task",
				With:   map[string]any{"prompt": "prompts/does-not-exist.md"},
			}},
		},
	}
	got := collectPromptTemplateNamespaceWarnings("", states, t.TempDir())
	require.Empty(t, got)

	got = collectPromptTemplateNamespaceWarnings("", states, "")
	require.Empty(t, got)
}

// ── validateTemplateNamespaces load-time wiring (non-fatal) ─────────────────

// TestValidateTemplateNamespaces_LoadsNonFatal proves the advisory never
// aborts the load: an app with an unknown-namespace reference still loads
// cleanly (mirrors TestViewBindFallback_LoadsNonFatal for the sibling
// advisory pass).
func TestValidateTemplateNamespaces_LoadsNonFatal(t *testing.T) {
	const yamlSrc = `
app:
  id: template-namespace-lint
  version: 0.1.0
intents:
  go: {}
root: start
states:
  start:
    view: "{{ context.foo }}"
`
	def, err := LoadBytes([]byte(yamlSrc))
	require.NoError(t, err, "unknown-namespace view must load (warning is non-fatal)")
	require.NotNil(t, def)

	warnings := collectTemplateNamespaceWarnings(def)
	require.Len(t, warnings, 1)
	require.Equal(t, "context", warnings[0].Ident)
}
