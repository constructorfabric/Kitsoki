package viz_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/viz"
)

func TestParseDetailLevel(t *testing.T) {
	cases := []struct {
		input   string
		want    viz.DetailLevel
		wantErr bool
	}{
		{"rooms", viz.DetailRooms, false},
		{"states", viz.DetailStates, false},
		{"steps", viz.DetailSteps, false},
		{"full", viz.DetailFull, false},
		{"ROOMS", viz.DetailRooms, false}, // case-insensitive
		{"STATES", viz.DetailStates, false},
		{"", viz.DetailStates, true},
		{"invalid", viz.DetailStates, true},
		{"state", viz.DetailStates, true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := viz.ParseDetailLevel(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func TestDetailLevelString(t *testing.T) {
	require.Equal(t, "rooms", viz.DetailRooms.String())
	require.Equal(t, "states", viz.DetailStates.String())
	require.Equal(t, "steps", viz.DetailSteps.String())
	require.Equal(t, "full", viz.DetailFull.String())
}

// minimalApp is an inline AppDef with on_enter effects for testing.
func minimalApp() *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "test-flow", Title: "Test Flow"},
		Root: "idle",
		States: map[string]*app.State{
			"idle": {
				On: map[string][]app.Transition{
					"start": {{Target: "working"}},
				},
			},
			"working": {
				OnEnter: []app.Effect{
					{
						Invoke: "host.run",
						With:   map[string]any{"cmd": "python3 fetch.py"},
						Bind:   map[string]string{"result": "output"},
					},
					{
						Invoke: "host.agent.ask",
						With:   map[string]any{"prompt": "prompts/analyze.md"},
						Bind:   map[string]string{"analysis": "text"},
					},
				},
				On: map[string][]app.Transition{
					"done": {{Target: "finished"}},
				},
			},
			"finished": {
				Terminal: true,
			},
		},
	}
}

func TestFlowchartMinimal(t *testing.T) {
	def := minimalApp()

	for _, detail := range []viz.DetailLevel{
		viz.DetailRooms,
		viz.DetailStates,
		viz.DetailSteps,
		viz.DetailFull,
	} {
		t.Run(detail.String(), func(t *testing.T) {
			out, err := viz.FlowchartBytes(def, detail, viz.FlowchartFilter{})
			require.NoError(t, err)
			s := string(out)
			t.Logf("Flowchart [%s] output:\n%s", detail, s)

			// All outputs must start with flowchart LR.
			require.True(t, strings.Contains(s, "flowchart LR"),
				"output must contain 'flowchart LR'")

			// Must have the Start node.
			require.Contains(t, s, `Start(["<b>Start</b>"]):::input`)

			// Must have classDef blocks.
			require.Contains(t, s, "classDef input")
			require.Contains(t, s, "classDef llm")
			require.Contains(t, s, "classDef store")
			require.Contains(t, s, "classDef err")

			// Title comment must be present.
			require.Contains(t, s, "%% Test Flow")
		})
	}
}

func TestFlowchartMinimalStatesDetail(t *testing.T) {
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// Must have state nodes for each leaf state.
	require.Contains(t, s, "ST_idle")
	require.Contains(t, s, "ST_working")
	require.Contains(t, s, "ST_finished")

	// Must have transition edges.
	require.Contains(t, s, `"start"`)
	require.Contains(t, s, `"done"`)
}

func TestFlowchartMinimalStepsDetail(t *testing.T) {
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailSteps, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// Must include step nodes for on_enter effects.
	require.Contains(t, s, "STEP_working_0")
	require.Contains(t, s, "STEP_working_1")

	// Shell step (host.run) should have shell class.
	require.Contains(t, s, ":::shell")
	// Agent step (host.agent.ask) should have llm class.
	require.Contains(t, s, ":::llm")

	// Step label must include "step 0 —".
	require.Contains(t, s, "step 0 —")
	require.Contains(t, s, "step 1 —")
}

func TestFlowchartMinimalFullDetail(t *testing.T) {
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailFull, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// World-write nodes for bind effects.
	require.Contains(t, s, "WSB_working")
	require.Contains(t, s, ":::store")
	require.Contains(t, s, "world.result")
	require.Contains(t, s, "world.analysis")
}

func TestFlowchartMinimalRoomsDetail(t *testing.T) {
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailRooms, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// Room nodes use RI_ prefix.
	require.Contains(t, s, "RI_idle")
	require.Contains(t, s, "RI_working")
	require.Contains(t, s, "RI_finished")

	// Must connect Start to the initial room.
	require.Contains(t, s, "Start --> RI_idle")
}

func TestFlowchartCloak(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	for _, detail := range []viz.DetailLevel{
		viz.DetailRooms,
		viz.DetailStates,
		viz.DetailSteps,
		viz.DetailFull,
	} {
		t.Run(detail.String(), func(t *testing.T) {
			out, err := viz.FlowchartBytes(def, detail, viz.FlowchartFilter{})
			require.NoError(t, err)
			s := string(out)
			t.Logf("Cloak flowchart [%s] (first 600 chars):\n%.600s", detail, s)

			// Must start with flowchart LR.
			require.True(t, strings.Contains(s, "flowchart LR"),
				"output must contain 'flowchart LR'")

			// Must have Start node.
			require.Contains(t, s, "Start")

			// Must end with classDef block.
			require.Contains(t, s, "classDef input")
			require.Contains(t, s, "classDef room")

			// Must reference cloak app states.
			if detail >= viz.DetailStates {
				require.Contains(t, s, "subgraph SG_foyer")
				require.Contains(t, s, "ST_foyer")
				require.Contains(t, s, "ST_ended")
			}

			// Initial state (foyer) must be the start target.
			if detail >= viz.DetailStates {
				require.Contains(t, s, "Start --> ST_foyer")
			}

			// Rooms detail uses RI_ prefix.
			if detail == viz.DetailRooms {
				require.Contains(t, s, "RI_foyer")
				require.Contains(t, s, "Start --> RI_foyer")
			}
		})
	}
}

func TestFlowchartCloakTransitions(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// bar.lit → ended via read_message.
	require.Contains(t, s, "ST_bar_lit")
	require.Contains(t, s, "ST_ended")
	require.Contains(t, s, `"read_message"`)

	// bar.dark → foyer transition via go intent.
	require.Contains(t, s, "ST_bar_dark")
	require.Contains(t, s, "ST_foyer")
}

func TestFlowchartSubgraphs(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// Must have subgraph blocks.
	require.Contains(t, s, "subgraph SG_")
	require.Contains(t, s, "direction LR")
	require.Contains(t, s, "end")
}

func TestFlowchartEscaping(t *testing.T) {
	// Use a state description that contains HTML-unsafe characters so they
	// end up inside a subgraph label node string.
	def := &app.AppDef{
		App:  app.AppMeta{ID: "esc-test", Title: "Escape Test"},
		Root: "start",
		States: map[string]*app.State{
			"start": {
				// Description with HTML-unsafe chars — appears in subgraph label.
				Description: "handles <input> & \"quotes\"",
				OnEnter: []app.Effect{
					{
						Invoke: "host.agent.ask",
						With:   map[string]any{"prompt": "prompts/test.md"},
					},
				},
				On: map[string][]app.Transition{
					"done": {{Target: "end"}},
				},
			},
			"end": {Terminal: true},
		},
	}

	out, err := viz.FlowchartBytes(def, viz.DetailSteps, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// The description is used in the subgraph label and must be HTML-escaped.
	require.Contains(t, s, "&lt;input&gt;")
	require.Contains(t, s, "&amp;")
	require.Contains(t, s, "&quot;")
	// Raw angle brackets must not appear inside node label strings
	// (they would break the Mermaid parser).
	require.NotContains(t, s, "<input>")
}

func TestFlowchartOnErrorFullDetail(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "onerr-test", Title: "OnError Test"},
		Root: "working",
		States: map[string]*app.State{
			"working": {
				OnEnter: []app.Effect{
					{
						Invoke:  "host.run",
						With:    map[string]any{"cmd": "python3 run.py"},
						OnError: "error_state",
					},
				},
				On: map[string][]app.Transition{
					"done": {{Target: "done_state"}},
				},
			},
			"done_state":  {},
			"error_state": {},
		},
	}

	out, err := viz.FlowchartBytes(def, viz.DetailFull, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// Error node must be emitted with EERR_ prefix and err class.
	require.Contains(t, s, "EERR_working_0")
	require.Contains(t, s, ":::err")
	// Dotted connection from step to error node.
	require.Contains(t, s, "-.-")
}

func TestFlowchartBackgroundStep(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "bg-test"},
		Root: "working",
		States: map[string]*app.State{
			"working": {
				OnEnter: []app.Effect{
					{
						Invoke:     "host.agent.ask",
						Background: true,
						With:       map[string]any{"prompt": "prompts/bg.md"},
					},
				},
				On: map[string][]app.Transition{
					"done": {{Target: "done_state"}},
				},
			},
			"done_state": {},
		},
	}

	out, err := viz.FlowchartBytes(def, viz.DetailSteps, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)

	// Background prefix must appear in the label.
	require.Contains(t, s, "⚙")
}

func TestFlowchartFilterRoom(t *testing.T) {
	// minimalApp has rooms: idle, working, finished
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{Room: "working"})
	require.NoError(t, err)
	s := string(out)
	// working subgraph present
	require.Contains(t, s, "SG_working")
	require.Contains(t, s, "ST_working")
	// idle and finished NOT as full subgraphs
	require.NotContains(t, s, "SG_idle")
	require.NotContains(t, s, "SG_finished")
	// stub nodes for exits out of working
	require.Contains(t, s, "STUB_finished")
}

func TestFlowchartFilterFromTo(t *testing.T) {
	// minimalApp: idle → working → finished
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{From: "idle", To: "finished"})
	require.NoError(t, err)
	s := string(out)
	// All three rooms should be in the path idle→working→finished
	require.Contains(t, s, "SG_idle")
	require.Contains(t, s, "SG_working")
	require.Contains(t, s, "SG_finished")
}

func TestFlowchartFilterFromToMidSlice(t *testing.T) {
	// Use cloak app: bar → ended (via bar.lit → ended read_message)
	// bar also has a back-edge to foyer (go north), so foyer IS on a path from
	// bar to ended (foyer → bar → ended). cloakroom is also on that path via foyer.
	// Filtering From: "bar" To: "ended" should include bar, ended, foyer, cloakroom.
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)
	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{From: "bar", To: "ended"})
	require.NoError(t, err)
	s := string(out)
	// bar and ended are always included.
	require.Contains(t, s, "SG_bar")
	require.Contains(t, s, "SG_ended")
	// foyer is reachable from bar (bar→foyer) AND can reach ended (foyer→bar→ended).
	require.Contains(t, s, "SG_foyer")
	// cloakroom similarly (foyer→cloakroom; cloakroom→foyer→bar→ended).
	require.Contains(t, s, "SG_cloakroom")
}

// TestFlowchartUnknownTargetNoStubEdge guards against orphaned stub edges
// for transition targets that don't resolve to any known state/room. Such a
// target has no entry in rooms.RoomOf, so the filtered branch would otherwise
// emit an edge to a STUB_ node that is never declared (the stub node itself is
// suppressed because its room name is empty), leaving a dangling edge in the
// mermaid output.
func TestFlowchartUnknownTargetNoStubEdge(t *testing.T) {
	def := minimalApp()
	// Add a transition out of `working` that points at a state that does
	// not exist anywhere in the app.
	def.States["working"].On["escape_hatch"] = []app.Transition{{Target: "ghost_state"}}

	// Filter to the `working` room so the cross-room stub-edge path is the
	// one that handles this transition.
	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{Room: "working"})
	require.NoError(t, err)
	s := string(out)

	// No node or edge should reference the unknown target.
	require.NotContains(t, s, "ghost_state")
	// The dangling intent must not produce any edge at all.
	require.NotContains(t, s, "escape_hatch")
	// And there must be no edge pointing at a bare/empty STUB_ node.
	require.NotContains(t, s, "--> STUB_\n")
	require.NotContains(t, s, `--> STUB_ `)
}

func TestFlowchartFilterValidate(t *testing.T) {
	require.NoError(t, viz.FlowchartFilter{}.Validate())
	require.NoError(t, viz.FlowchartFilter{Room: "foo"}.Validate())
	require.NoError(t, viz.FlowchartFilter{From: "a", To: "b"}.Validate())
	require.Error(t, viz.FlowchartFilter{Room: "foo", From: "a"}.Validate())
	require.Error(t, viz.FlowchartFilter{From: "a"}.Validate())
	require.Error(t, viz.FlowchartFilter{To: "b"}.Validate())
}

func TestResolveFilterRoomsUnknown(t *testing.T) {
	def := minimalApp()
	_, err := viz.ResolveFilterRooms(def, viz.FlowchartFilter{Room: "nonexistent"})
	require.Error(t, err)
}

func TestPipelineOrder(t *testing.T) {
	def := minimalApp() // idle → working → finished
	order := viz.PipelineOrder(def)
	require.Equal(t, []string{"idle", "working", "finished"}, order)
}

func TestPipelineOrderExitsSkipped(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "exit-test"},
		Root: "start",
		States: map[string]*app.State{
			"start": {On: map[string][]app.Transition{
				"done": {{Target: "__exit__done"}},
				"next": {{Target: "end"}},
			}},
			"end":          {},
			"__exit__done": {},
		},
	}
	order := viz.PipelineOrder(def)
	require.Equal(t, []string{"start", "end"}, order)
	require.NotContains(t, order, "__exit__done")
}

func TestFlowchartSequenceNumbers(t *testing.T) {
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)
	// Subgraph labels should contain 0-indexed phase sequence numbers.
	require.Contains(t, s, "phase 0")
	require.Contains(t, s, "phase 1")
	require.Contains(t, s, "phase 2")
}

func TestFlowchartSequenceInRoomsDetail(t *testing.T) {
	def := minimalApp()
	out, err := viz.FlowchartBytes(def, viz.DetailRooms, viz.FlowchartFilter{})
	require.NoError(t, err)
	s := string(out)
	require.Contains(t, s, "phase 0")
}

// TestFlowchartBannersOptIn verifies the FlowchartWithMap Banners option:
// off → byte-identical to FlowchartBytes (no comments); on → one
// `%% banner <state> <text>` line per leaf state with a STATIC banner view
// element, and templated banner text is skipped (it's a runtime render, not
// declared graph metadata).
func TestFlowchartBannersOptIn(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "ban", Title: "Ban"},
		Root: "intake",
		States: map[string]*app.State{
			"intake": {
				View: app.View{Elements: []app.ViewElement{{Kind: "banner", Source: "INTAKE"}}},
				On:   map[string][]app.Transition{"go": {{Target: "done"}}},
			},
			"done": {
				Terminal: true,
				// Templated banner text must NOT be surfaced statically.
				View: app.View{Elements: []app.ViewElement{{Kind: "banner", Source: "{{ world.x }}"}}},
			},
		},
	}

	// Banners OFF: byte-identical to FlowchartBytes, no banner comments.
	res, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailStates})
	require.NoError(t, err)
	require.NotContains(t, res.Source, "%% banner")
	raw, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
	require.NoError(t, err)
	require.Equal(t, string(raw), res.Source, "Banners:false must be byte-identical to FlowchartBytes")

	// Banners ON: static banner emitted; templated banner skipped.
	res2, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailStates, Banners: true})
	require.NoError(t, err)
	require.Contains(t, res2.Source, "%% banner intake INTAKE")
	require.NotContains(t, res2.Source, "%% banner done")
}
