package sysprompt_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/sysprompt"
)

// TestCompose_LayerOrderAndPresence locks the cache-critical invariant: layers
// appear most-stable → least-stable (kitsoki, project, task), and only the
// non-empty ones are present.
func TestCompose_LayerOrderAndPresence(t *testing.T) {
	tests := []struct {
		name       string
		spec       sysprompt.Spec
		wantLayers []string
	}{
		{
			name:       "all three",
			spec:       sysprompt.Spec{Verb: sysprompt.Decide, Project: "PROJECT", Task: "PERSONA"},
			wantLayers: []string{"kitsoki", "project", "task"},
		},
		{
			name:       "no project",
			spec:       sysprompt.Spec{Verb: sysprompt.Ask, Task: "PERSONA"},
			wantLayers: []string{"kitsoki", "task"},
		},
		{
			name:       "kitsoki only — empty project and task, route has no verb contract",
			spec:       sysprompt.Spec{Verb: sysprompt.Route},
			wantLayers: []string{"kitsoki"},
		},
		{
			name:       "decide with empty persona still gets a task layer from the verb contract",
			spec:       sysprompt.Spec{Verb: sysprompt.Decide},
			wantLayers: []string{"kitsoki", "task"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sysprompt.Compose(tc.spec)
			assert.Equal(t, tc.wantLayers, sysprompt.LayerNames(got.Layers))

			// Layer order must match byte order in the composed prompt.
			lastIdx := -1
			for _, name := range tc.wantLayers {
				var probe string
				switch name {
				case "kitsoki":
					probe = "operating inside **kitsoki**"
				case "project":
					probe = "PROJECT"
				case "task":
					probe = tc.spec.Task
					if probe == "" {
						probe = "decision gate" // verb contract text
					}
				}
				idx := strings.Index(got.SystemPrompt, probe)
				require.GreaterOrEqualf(t, idx, 0, "layer %q (%q) must appear in prompt", name, probe)
				assert.Greaterf(t, idx, lastIdx, "layer %q must appear after the previous layer", name)
				lastIdx = idx
			}
		})
	}
}

// TestCompose_ExcludeDynamicPolicy: every verb excludes Claude Code's dynamic
// sections except task.
func TestCompose_ExcludeDynamicPolicy(t *testing.T) {
	cases := map[sysprompt.Verb]bool{
		sysprompt.Route:         true,
		sysprompt.Ask:           true,
		sysprompt.Decide:        true,
		sysprompt.Converse:      true,
		sysprompt.Extract:       true,
		sysprompt.AskWithMCP:    true,
		sysprompt.AskStructured: true,
		sysprompt.Task:          false, // agentic work keeps repo context
	}
	for verb, wantExclude := range cases {
		t.Run(string(verb), func(t *testing.T) {
			got := sysprompt.Compose(sysprompt.Spec{Verb: verb, Task: "x"})
			assert.Equal(t, wantExclude, got.ExcludeDynamic)
		})
	}
}

// TestCompose_VerbContractLeadsTask: the verb contract precedes the persona
// inside the task layer.
func TestCompose_VerbContractLeadsTask(t *testing.T) {
	got := sysprompt.Compose(sysprompt.Spec{Verb: sysprompt.Decide, Task: "YOU-ARE-THE-JUDGE"})
	contractIdx := strings.Index(got.SystemPrompt, "resolves a single decision gate")
	personaIdx := strings.Index(got.SystemPrompt, "YOU-ARE-THE-JUDGE")
	require.GreaterOrEqual(t, contractIdx, 0)
	require.GreaterOrEqual(t, personaIdx, 0)
	assert.Less(t, contractIdx, personaIdx, "verb contract must lead the task layer")
}

// TestCompose_RouteHasNoVerbContract: routing supplies its own contract, so we
// don't inject a generic one.
func TestCompose_RouteHasNoVerbContract(t *testing.T) {
	got := sysprompt.Compose(sysprompt.Spec{Verb: sysprompt.Route, Task: "INTENT-LIBRARY"})
	assert.NotContains(t, got.SystemPrompt, "This call answers one question")
	assert.Contains(t, got.SystemPrompt, "INTENT-LIBRARY")
}

// TestCompose_KitsokiOverride: a non-empty Spec.Kitsoki shadows the embedded
// fragment (the overlay-experiment escape hatch); empty uses the builtin.
func TestCompose_KitsokiOverride(t *testing.T) {
	custom := sysprompt.Compose(sysprompt.Spec{Verb: sysprompt.Ask, Kitsoki: "CUSTOM-KITSOKI", Task: "p"})
	assert.Contains(t, custom.SystemPrompt, "CUSTOM-KITSOKI")
	assert.NotContains(t, custom.SystemPrompt, "operating inside **kitsoki**")

	builtin := sysprompt.Compose(sysprompt.Spec{Verb: sysprompt.Ask, Task: "p"})
	assert.Contains(t, builtin.SystemPrompt, "operating inside **kitsoki**")
}

// TestCompose_Deterministic: Compose is a pure function — identical Spec yields
// byte-identical output (the property the prompt cache relies on).
func TestCompose_Deterministic(t *testing.T) {
	spec := sysprompt.Spec{Verb: sysprompt.Decide, Project: "P", Task: "T"}
	a := sysprompt.Compose(spec)
	b := sysprompt.Compose(spec)
	assert.Equal(t, a.SystemPrompt, b.SystemPrompt)
	assert.Equal(t, sysprompt.LayerNames(a.Layers), sysprompt.LayerNames(b.Layers))
}

// TestKitsokiLayer_NonEmpty: the embedded Layer-1 fragment is present and
// carries the operator framing that grounds the moat.
func TestKitsokiLayer_NonEmpty(t *testing.T) {
	k := sysprompt.KitsokiLayer()
	require.NotEmpty(t, k)
	assert.Contains(t, k, "deterministic")
	assert.Contains(t, k, "one recorded decision point")
}
