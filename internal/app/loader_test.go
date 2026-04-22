package app

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadCloak_Positive loads the Cloak of Darkness app and asserts
// structural invariants derived from the §3.1 port.
func TestLoadCloak_Positive(t *testing.T) {
	def, err := Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err, "Cloak app must load cleanly")
	require.NotNil(t, def)

	// App-level metadata.
	require.Equal(t, "cloak-of-darkness", def.App.ID)
	require.Equal(t, "0.1.0", def.App.Version)

	// Root state.
	root, ok := def.Root.(string)
	require.True(t, ok, "root must be a string")
	require.Equal(t, "foyer", root)

	// World schema — three declared variables.
	require.Len(t, def.World, 3)
	require.Contains(t, def.World, "wearing_cloak")
	require.Contains(t, def.World, "disturbance")
	require.Contains(t, def.World, "message_rumpled")

	// Intent library — six intents.
	require.Len(t, def.Intents, 6, "expected 6 intents in library")
	goIntent, ok := def.Intents["go"]
	require.True(t, ok, "intent 'go' must exist")
	require.Equal(t, "Go", goIntent.Title)
	require.Equal(t, 100, goIntent.Priority)
	dir, ok := goIntent.Slots["direction"]
	require.True(t, ok, "go intent must have direction slot")
	require.Equal(t, "enum", dir.Type)
	require.True(t, dir.Required)
	require.Contains(t, dir.Values, "south")

	readMsg, ok := def.Intents["read_message"]
	require.True(t, ok, "intent 'read_message' must exist")
	require.Equal(t, "Read the message", readMsg.Title)
	require.Equal(t, 90, readMsg.Priority)

	dropCloak, ok := def.Intents["drop_cloak"]
	require.True(t, ok)
	require.True(t, dropCloak.Hidden, "drop_cloak must be hidden")

	// States — foyer, cloakroom, bar, ended at the top level.
	require.Contains(t, def.States, "foyer")
	require.Contains(t, def.States, "cloakroom")
	require.Contains(t, def.States, "bar")
	require.Contains(t, def.States, "ended")

	// bar is a compound state with two children.
	bar := def.States["bar"]
	require.Equal(t, "compound", bar.Type)
	require.Contains(t, bar.States, "dark")
	require.Contains(t, bar.States, "lit")

	// foyer.on has go and look.
	foyer := def.States["foyer"]
	require.Contains(t, foyer.On, "go")
	require.Contains(t, foyer.On, "look")

	// foyer.relevant_world contains wearing_cloak.
	require.Contains(t, foyer.RelevantWorld, "wearing_cloak")

	// ended is terminal.
	ended := def.States["ended"]
	require.True(t, ended.Terminal)

	// cloakroom.on contains hang_cloak with effects.
	cloakroom := def.States["cloakroom"]
	hangTransitions := cloakroom.On["hang_cloak"]
	require.GreaterOrEqual(t, len(hangTransitions), 1)

	// off_path block is present.
	require.NotNil(t, def.OffPath)
	require.Equal(t, "/freeform", def.OffPath.Trigger)
}

// TestLoadBytes_RoundTrip verifies LoadBytes returns the same structure as Load.
func TestLoadBytes_RoundTrip(t *testing.T) {
	b, err := os.ReadFile("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	def, err := LoadBytes(b)
	require.NoError(t, err)
	require.Equal(t, "cloak-of-darkness", def.App.ID)
}

// TestNegative is a table-driven test for each failure mode. Each bad fixture
// must produce a ValidationError containing the expected substring.
func TestNegative(t *testing.T) {
	cases := []struct {
		name        string
		fixture     string
		wantErrSnip string
	}{
		{
			name:        "unknown intent in on block",
			fixture:     "testdata/bad/unknown_intent.yaml",
			wantErrSnip: "nonexistent_intent",
		},
		{
			name:        "unknown transition target",
			fixture:     "testdata/bad/unknown_target.yaml",
			wantErrSnip: "nonexistent_room",
		},
		{
			name:        "root state missing",
			fixture:     "testdata/bad/missing_root.yaml",
			wantErrSnip: "does_not_exist",
		},
		{
			name:        "relevant_world key not in world schema",
			fixture:     "testdata/bad/bad_relevant_world.yaml",
			wantErrSnip: "stamina",
		},
		{
			name:        "compound initial child missing",
			fixture:     "testdata/bad/bad_compound_initial.yaml",
			wantErrSnip: "nonexistent_child",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.fixture)
			require.Error(t, err, "loading %s must fail", tc.fixture)
			require.True(t,
				containsSubstring(err, tc.wantErrSnip),
				"error message should mention %q; got: %v", tc.wantErrSnip, err,
			)
		})
	}
}

// containsSubstring checks whether the error string (including joined errors)
// contains the expected substring.
func containsSubstring(err error, sub string) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), sub) {
		return true
	}
	// Walk errors.Join chain.
	var joinErr interface{ Unwrap() []error }
	if errors.As(err, &joinErr) {
		for _, e := range joinErr.Unwrap() {
			if containsSubstring(e, sub) {
				return true
			}
		}
	}
	return false
}
