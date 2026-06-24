package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReservedWorldKeys_NotRewrittenOnFold is the key regression for the
// host-error visibility bug: when a story is imported under an alias, its
// references to the reserved global world keys (last_error / host_error) must
// stay BARE — they must NOT be rewritten to alias__last_error — because the
// engine writes those keys flat at every nesting depth.
func TestReservedWorldKeys_NotRewrittenOnFold(t *testing.T) {
	child := &AppDef{
		Root: "err",
		World: map[string]VarDef{
			// A reserved global AND an ordinary child key, so we can assert the
			// rewriter prefixes the ordinary one but leaves the reserved ones
			// alone.
			"last_error": {},
			"host_error": {},
			"ticket_id":  {},
		},
		States: map[string]*State{
			"err": {
				Description:   "failed: {{ world.last_error }} ns {{ world.host_error.namespace }} on {{ world.ticket_id }}",
				RelevantWorld: []string{"last_error", "host_error", "ticket_id"},
			},
		},
		Exits: map[string]*ExitDef{},
	}
	parent := &AppDef{
		World:  map[string]VarDef{},
		States: map[string]*State{},
	}
	imp := &ImportDef{Source: "./child", Entry: "err"}

	errs := foldChild(parent, "triage", imp, child, "parent.yaml")
	require.Empty(t, errs, "fold should succeed")

	// Reserved keys stay flat — no alias-prefixed schema entries synthesised.
	require.NotContains(t, parent.World, "triage__last_error",
		"reserved last_error must not be folded under the alias")
	require.NotContains(t, parent.World, "triage__host_error",
		"reserved host_error must not be folded under the alias")
	// The ordinary child key IS folded.
	require.Contains(t, parent.World, "triage__ticket_id")

	errState := parent.States["triage"].States["err"]
	require.NotNil(t, errState)
	// Reserved refs survive bare; the ordinary ref is prefixed.
	require.Contains(t, errState.Description, "world.last_error")
	require.Contains(t, errState.Description, "world.host_error.namespace")
	require.NotContains(t, errState.Description, "world.triage__last_error")
	require.NotContains(t, errState.Description, "world.triage__host_error")
	require.Contains(t, errState.Description, "world.triage__ticket_id")

	// relevant_world: reserved keys bare, ordinary key prefixed.
	rw := strings.Join(errState.RelevantWorld, ",")
	require.Contains(t, errState.RelevantWorld, "last_error")
	require.Contains(t, errState.RelevantWorld, "host_error")
	require.NotContains(t, rw, "triage__last_error")
	require.NotContains(t, rw, "triage__host_error")
	require.Contains(t, errState.RelevantWorld, "triage__ticket_id")
}

// TestReservedWorldKeys_ReferenceableWithoutDeclaration asserts a standalone
// story may reference world.last_error / world.host_error and list them in
// relevant_world without declaring them in its own world block — the loader
// seeds them as always-valid reserved globals.
func TestReservedWorldKeys_ReferenceableWithoutDeclaration(t *testing.T) {
	const yamlSrc = `
app:
  id: reserved-world-undeclared
  version: 0.1.0
world: {}
intents:
  enter: {}
root: start
states:
  start:
    relevant_world: [last_error, host_error]
    description: "last failure: {{ world.last_error }} from {{ world.host_error.namespace }}"
    on:
      enter:
        - target: start
`
	def, err := LoadBytes([]byte(yamlSrc))
	require.NoError(t, err, "reserved world keys must be valid without declaration")
	require.NotNil(t, def)
}
