package harness_test

import (
	"encoding/json"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
)

// compileSchema compiles raw bytes via santhosh-tekuri/jsonschema to confirm
// the document is well-formed. Unknown formats (e.g. `jql`) are registered
// permissively so format checks degrade to no-op rather than tripping the
// compiler.
func compileSchema(t *testing.T, raw []byte) {
	t.Helper()

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc))

	c := jsonschema.NewCompiler()
	c.RegisterFormat(&jsonschema.Format{
		Name:     "jql",
		Validate: func(any) error { return nil },
	})
	require.NoError(t, c.AddResource("test://schema.json", doc))
	_, err := c.Compile("test://schema.json")
	require.NoError(t, err, "generated schema must compile")
}

func TestBuildTransitionSchema(t *testing.T) {
	type want struct {
		intentEnum     []string
		expectSlots    []string // slot names that must appear under properties.slots.properties
		absentSlots    []string // slot names that must NOT appear
		assertSlotProp func(t *testing.T, slotProps map[string]any)
	}

	tests := []struct {
		name           string
		appDef         *app.AppDef
		allowedIntents []string
		want           want
	}{
		{
			name: "empty intent list",
			appDef: &app.AppDef{
				App:     app.AppMeta{ID: "x"},
				Intents: map[string]app.Intent{},
			},
			allowedIntents: nil,
			want: want{
				intentEnum: []string{},
			},
		},
		{
			name: "one intent no slots",
			appDef: &app.AppDef{
				App: app.AppMeta{ID: "x"},
				Intents: map[string]app.Intent{
					"look": {Title: "Look"},
				},
			},
			allowedIntents: []string{"look"},
			want: want{
				intentEnum: []string{"look"},
			},
		},
		{
			name: "one intent required string slot with format jql",
			appDef: &app.AppDef{
				App: app.AppMeta{ID: "x"},
				Intents: map[string]app.Intent{
					"search": {
						Slots: map[string]app.Slot{
							"jql": {
								Type:        "string",
								Required:    true,
								Format:      "jql",
								Description: "JQL query",
							},
						},
					},
				},
			},
			allowedIntents: []string{"search"},
			want: want{
				intentEnum:  []string{"search"},
				expectSlots: []string{"jql"},
				assertSlotProp: func(t *testing.T, slotProps map[string]any) {
					jqlProp := slotProps["jql"].(map[string]any)
					assert.Equal(t, "jql", jqlProp["format"])
					assert.Equal(t, "string", jqlProp["type"])
					assert.Equal(t, "JQL query", jqlProp["description"])
				},
			},
		},
		{
			name: "multiple intents some with slots",
			appDef: &app.AppDef{
				App: app.AppMeta{ID: "x"},
				Intents: map[string]app.Intent{
					"look": {Title: "Look"},
					"go": {
						Slots: map[string]app.Slot{
							"direction": {
								Type:   "string",
								Values: []string{"north", "south"},
							},
						},
					},
					"hang_cloak": {Title: "Hang"},
				},
			},
			allowedIntents: []string{"look", "go", "hang_cloak"},
			want: want{
				intentEnum:  []string{"look", "go", "hang_cloak"},
				expectSlots: []string{"direction"},
				assertSlotProp: func(t *testing.T, slotProps map[string]any) {
					dir := slotProps["direction"].(map[string]any)
					enum := dir["enum"].([]any)
					assert.Len(t, enum, 2)
				},
			},
		},
		{
			name: "duplicate slot names — first allowed-intent wins",
			appDef: &app.AppDef{
				App: app.AppMeta{ID: "x"},
				Intents: map[string]app.Intent{
					"a": {Slots: map[string]app.Slot{"key": {Type: "string", Description: "from a"}}},
					"b": {Slots: map[string]app.Slot{"key": {Type: "integer", Description: "from b"}}},
				},
			},
			allowedIntents: []string{"a", "b"},
			want: want{
				intentEnum:  []string{"a", "b"},
				expectSlots: []string{"key"},
				assertSlotProp: func(t *testing.T, slotProps map[string]any) {
					k := slotProps["key"].(map[string]any)
					assert.Equal(t, "string", k["type"])
					assert.Equal(t, "from a", k["description"])
				},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			raw, err := harness.BuildTransitionSchema(tc.appDef, tc.allowedIntents)
			require.NoError(t, err)

			var root map[string]any
			require.NoError(t, json.Unmarshal(raw, &root))

			assert.Equal(t, "object", root["type"])
			assert.Equal(t, false, root["additionalProperties"])

			// Top-level discriminators that Anthropic's tool input_schema
			// rejects MUST NOT appear at the root.
			for _, k := range []string{"oneOf", "allOf", "anyOf"} {
				_, present := root[k]
				assert.False(t, present, "top-level %q must not be emitted (Anthropic API rejects it)", k)
			}

			props := root["properties"].(map[string]any)
			intentProp := props["intent"].(map[string]any)
			assert.Equal(t, "string", intentProp["type"])

			enum, _ := intentProp["enum"].([]any)
			assert.Len(t, enum, len(tc.want.intentEnum))
			for i, name := range tc.want.intentEnum {
				assert.Equal(t, name, enum[i])
			}

			slotsProp := props["slots"].(map[string]any)
			assert.Equal(t, "object", slotsProp["type"])
			slotProps, _ := slotsProp["properties"].(map[string]any)

			for _, slotName := range tc.want.expectSlots {
				_, present := slotProps[slotName]
				assert.True(t, present, "slots.properties.%s should be present", slotName)
			}
			for _, slotName := range tc.want.absentSlots {
				_, present := slotProps[slotName]
				assert.False(t, present, "slots.properties.%s should be absent", slotName)
			}

			if tc.want.assertSlotProp != nil && slotProps != nil {
				tc.want.assertSlotProp(t, slotProps)
			}

			compileSchema(t, raw)
		})
	}
}

// TestBuildTransitionSchema_NilAppDef confirms a nil appDef is a programmer
// error rather than a silent empty schema.
func TestBuildTransitionSchema_NilAppDef(t *testing.T) {
	_, err := harness.BuildTransitionSchema(nil, []string{"x"})
	require.Error(t, err)
}
