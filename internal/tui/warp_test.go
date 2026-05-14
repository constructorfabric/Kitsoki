package tui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
)

// TestShellLikeSplit verifies the quote-aware /warp argument splitter.
// The function is used to let authors write `world.k="value with spaces"`
// on the command line.
func TestShellLikeSplit(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "simple_tokens",
			input: "a b c",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "key_value_pairs",
			input: "state world.x=1 world.y=true",
			want:  []string{"state", "world.x=1", "world.y=true"},
		},
		{
			name:  "double_quoted_value_with_spaces",
			input: `state world.k="hello world" world.j=plain`,
			want:  []string{"state", "world.k=hello world", "world.j=plain"},
		},
		{
			name:  "empty_input",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace_only",
			input: "   ",
			want:  nil,
		},
		{
			name:    "unterminated_quote",
			input:   `state world.k="oops`,
			wantErr: true,
		},
		{
			name:  "multiple_spaces_collapse",
			input: "a   b",
			want:  []string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := shellLikeSplit(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestParseWarpValue verifies type coercion: int → bool → string.
// This is what makes `/warp foo world.money=400 world.bailed=true` produce
// an int 400 and a bool true at the orchestrator boundary.
func TestParseWarpValue(t *testing.T) {
	require.Equal(t, int64(400), parseWarpValue("400"))
	require.Equal(t, int64(-5), parseWarpValue("-5"))
	require.Equal(t, int64(0), parseWarpValue("0"))
	require.Equal(t, true, parseWarpValue("true"))
	require.Equal(t, false, parseWarpValue("false"))
	require.Equal(t, "hello", parseWarpValue("hello"))
	require.Equal(t, "Chimney Rock", parseWarpValue("Chimney Rock"))
	require.Equal(t, "", parseWarpValue(""))
	// Strings that don't parse as ints stay strings.
	require.Equal(t, "1.5", parseWarpValue("1.5"))
	require.Equal(t, "TRUE", parseWarpValue("TRUE")) // case-sensitive
}

// TestStateExistsInApp covers nested-state-path lookup used by /warp
// to validate the target before teleporting.
func TestStateExistsInApp(t *testing.T) {
	def := &app.AppDef{
		States: map[string]*app.State{
			"main": {},
			"compound": {
				Type:    "compound",
				Initial: "child",
				States: map[string]*app.State{
					"child": {},
					"sibling": {
						States: map[string]*app.State{
							"grandchild": {},
						},
					},
				},
			},
		},
	}

	require.True(t, stateExistsInApp(def, "main"))
	require.True(t, stateExistsInApp(def, "compound"))
	require.True(t, stateExistsInApp(def, "compound.child"))
	require.True(t, stateExistsInApp(def, "compound.sibling.grandchild"))

	require.False(t, stateExistsInApp(def, "missing"))
	require.False(t, stateExistsInApp(def, "compound.missing"))
	require.False(t, stateExistsInApp(def, "compound.child.bogus"))
	require.False(t, stateExistsInApp(def, ""))
	require.False(t, stateExistsInApp(nil, "main"))
}
