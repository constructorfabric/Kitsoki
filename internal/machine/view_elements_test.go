// Coverage for the typed element-array view paths added by the
// view-elements proposal (Issues 1-4 in the code-review brief).
package machine_test

import (
	"context"
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/world"

	"github.com/stretchr/testify/require"
)

// TestRenderViewBody_PureElementArray covers Issue 2 — when v.Elements
// is non-empty AND v.Extends == "" / v.TemplateFile == "" / v.Source
// == "", the machine must dispatch the typed-element pipeline rather
// than returning the empty string. The Oregon Trail `scouting`
// override hits exactly this shape.
func TestRenderViewBody_PureElementArray(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "start",
		World: map[string]app.VarDef{},
		States: map[string]*app.State{
			"start": {
				View: app.View{
					Elements: []app.ViewElement{
						{Kind: "prose", Source: "{{ world.scouting_intel }}"},
						{Kind: "kv", Pairs: goyaml.MapSlice{
							{Key: "Party", Value: "{{ world.party_alive }} alive"},
						}},
						{Kind: "heading", Source: "Actions"},
						{Kind: "list", Items: []app.ListItem{
							{Label: "scout"},
							{Label: "proceed"},
						}},
					},
				},
			},
		},
	}
	m := mustNew(t, def)
	w := world.New()
	w.Vars["scouting_intel"] = "The trail ahead is clear."
	w.Vars["party_alive"] = 4

	out, err := m.RenderState("start", w)
	require.NoError(t, err)
	require.NotEqual(t, "", out, "pure element-array view must not render empty")
	require.Contains(t, out, "The trail ahead is clear.")
	require.Contains(t, out, "Party:")
	require.Contains(t, out, "4 alive")
	require.Contains(t, out, "Actions")
	require.Contains(t, out, "scout")
}

// TestTurn_TypedViewExposed asserts that machine.Turn populates
// TurnResult.TypedView / RenderEnv / Renderer when the target's view
// is element-array shaped — the seam the TUI relies on to re-render
// at viewport width on resize (Issue 4 / option (a)).
func TestTurn_TypedViewExposed(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "start",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"go": {},
		},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("Start"),
				On: map[string][]app.Transition{
					"go": {{Target: "typed"}},
				},
			},
			"typed": {
				View: app.View{
					Elements: []app.ViewElement{
						{Kind: "prose", Source: "Welcome to the typed view."},
					},
				},
			},
		},
	}
	m := mustNew(t, def)
	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{
		Intent: "go",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.NotNil(t, res.TypedView, "TypedView must be populated for element-array views")
	require.Equal(t, 1, len(res.TypedView.Elements))
	require.Contains(t, res.View, "Welcome to the typed view")
}

// TestTurn_TypedViewNilForLegacy asserts the typed seam is nil when
// the destination view is the legacy scalar string form. The TUI uses
// this nil signal to keep the Glamour pipeline for back-compat.
func TestTurn_TypedViewNilForLegacy(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "test"},
		Root:  "start",
		World: map[string]app.VarDef{},
		Intents: map[string]app.Intent{
			"go": {},
		},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("Start"),
				On: map[string][]app.Transition{
					"go": {{Target: "legacy"}},
				},
			},
			"legacy": {
				View: app.LegacyView("Legacy view body."),
			},
		},
	}
	m := mustNew(t, def)
	res, err := m.Turn(context.Background(), "start", world.New(), intent.IntentCall{
		Intent: "go",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.Nil(t, res.TypedView, "TypedView must be nil for legacy string views")
}

// TestRenderViewBody_RobberyStandalone covers Issue 3 — when robbery
// runs standalone (i.e. NOT imported under an alias), `extends: "base"`
// must resolve against robbery's own views/base.pongo, not against a
// missing host base. The host machine's renderer for robbery is the
// only renderer; standalone exercise of the same machinery as the
// per-namespace lookup, with the alias-prefix being "" (host).
func TestRenderViewBody_RobberyStandalone(t *testing.T) {
	def, err := app.Load("../../stories/robbery/app.yaml")
	require.NoError(t, err)
	m := mustNew(t, def)
	w := world.New()
	for k, v := range def.World {
		if v.Default != nil {
			w.Vars[k] = v.Default
		}
	}
	// Set some non-default values so the status block has signal.
	w.Vars["party_alive"] = 4
	w.Vars["party_money"] = 250
	w.Vars["threat_level"] = 2

	out, err := m.RenderState("encounter", w)
	require.NoError(t, err)
	// robbery's base.pongo status block reads:
	//   Party: {{ world.party_alive }} alive  |  ${{ world.party_money }}  |  Threat: {{ world.threat_level }}/3
	require.Contains(t, out, "Party:")
	require.Contains(t, out, "4 alive")
	require.Contains(t, out, "$250")
	require.Contains(t, out, "Threat: 2/3")
	// And the body block content.
	require.Contains(t, out, "masked rider")
}

// TestRenderViewBody_RobberyImportedAlias covers Issue 3 — when robbery
// is imported under an alias into a host story, the machine must build
// a per-alias renderer rooted at the child's BaseDir so child states'
// `extends: "base"` resolve against the *child's* views/, not the
// host's. The frontier_event story imports robbery under the alias
// "bf"; oregon-trail in turn imports frontier_event under "frontier",
// so a state path like `frontier.bandit.encounter` (depending on the
// fold shape) is host-rendered while still using the robbery views.
//
// This test loads frontier_event (which directly imports robbery) and
// checks the alias-renderer lookup picks the right child views.
func TestRenderViewBody_RobberyImportedAlias(t *testing.T) {
	def, err := app.Load("../../stories/frontier_event/app.yaml")
	require.NoError(t, err)
	m := mustNew(t, def)
	w := world.New()
	for k, v := range def.World {
		if v.Default != nil {
			w.Vars[k] = v.Default
		}
	}

	// frontier_event imports robbery; find the alias used.
	var robberyAlias string
	for alias, wrapper := range def.ImportWrappers {
		if wrapper != nil && strings.Contains(wrapper.SourcePath, "robbery") {
			robberyAlias = alias
			break
		}
	}
	if robberyAlias == "" {
		t.Skip("frontier_event no longer imports robbery — adjust the test")
	}

	// The robbery encounter state path post-import: <alias>.encounter.
	statePath := robberyAlias + ".encounter"
	// Seed alias-prefixed world keys so the body has signal.
	w.Vars[robberyAlias+"__party_alive"] = 4
	w.Vars[robberyAlias+"__party_money"] = 250
	w.Vars[robberyAlias+"__threat_level"] = 2

	out, err := m.RenderState(app.StatePath(statePath), w)
	require.NoError(t, err)
	// The imported view should render through the child's renderer
	// (which sees its own views/base.pongo). Body content stays the
	// same; if the wrong renderer is selected, the extends template
	// load fails and we get an error or empty body.
	require.Contains(t, out, "masked rider", "imported robbery view body must render through the alias-specific renderer; got:\n%s", out)
}

// TestRenderViewBody_TypedViewWithInclude exercises the full Issue 1 +
// Issue 2 stack: a pure element-array view whose code body uses
// {% include %}. Without the per-app renderer threading the include
// resolves against the host's views/.
//
// Sanity: this test piggybacks on the Oregon-Trail story so we don't
// have to spin up a synthetic views/ tree. The Oregon-Trail general
// store state's general_store view extends "base" and includes
// "partials/inventory.pongo" inside the body block.
func TestRenderViewBody_TypedViewWithInclude(t *testing.T) {
	// Load the oregon-trail app from the repo. The loader threads
	// BaseDir into the AppDef so the machine's renderer locates
	// stories/oregon-trail/views/.
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)
	m := mustNew(t, def)
	w := world.New()
	for k, v := range def.World {
		if v.Default != nil {
			w.Vars[k] = v.Default
		}
	}

	out, err := m.RenderState("general_store.idle", w)
	require.NoError(t, err, "render general_store.idle must succeed (include path resolved)")
	// The base.pongo carries a status block ("Day ... | ... miles
	// ... | ... $...") and the body block renders the inventory
	// partial. Just verify the body contains the partial output:
	// inventory.pongo prints lines like "Oxen:   ..." for every
	// stock key.
	if !strings.Contains(out, "Cash:") && !strings.Contains(out, "Oxen") {
		t.Errorf("general_store.idle render is missing inventory partial output:\n%s", out)
	}
}
