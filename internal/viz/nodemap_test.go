package viz_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/viz"
)

// -update refreshes the checked-in golden files from the current emitter output.
var update = flag.Bool("update", false, "overwrite golden files")

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// mermaidNodeIDs parses a Mermaid flowchart source and returns the set of all
// node IDs that are defined (i.e., appear on a line that starts with that ID).
// It matches:  <ID>[..., <ID>{..., <ID>(..., <ID>[(... — shape declarations.
// It also matches subgraph lines: subgraph SG_<id>[...
//
// The regex is deliberately broad: a node ID is a sequence of word-chars
// (\w+) that appears at the start of an indented line (possibly preceded by
// spaces) and is immediately followed by one of the Mermaid shape-open chars
// ([, {, (, or a space+double-dash for transitions).
var mermaidNodeRe = regexp.MustCompile(`(?m)^\s{2,}(\w+)[\[{("]`)

func nodeIDsInSource(src string) map[string]bool {
	set := map[string]bool{}
	for _, m := range mermaidNodeRe.FindAllStringSubmatch(src, -1) {
		if len(m) >= 2 {
			set[m[1]] = true
		}
	}
	// Also match subgraph declarations.
	sgRe := regexp.MustCompile(`(?m)^\s*subgraph\s+(\w+)`)
	for _, m := range sgRe.FindAllStringSubmatch(src, -1) {
		if len(m) >= 2 {
			set[m[1]] = true
		}
	}
	return set
}

// storyPath returns the path to the app.yaml for a story under the project root.
// Stories are looked up in:
//  1. testdata/apps/<name>/app.yaml (test-local apps, e.g. cloak)
//  2. ../../stories/<name>/app.yaml (repo stories)
func storyPath(t *testing.T, name string) string {
	t.Helper()
	// Paths relative to the test's package directory (internal/viz/).
	candidates := []string{
		filepath.Join("../../testdata/apps", name, "app.yaml"),
		filepath.Join("../../stories", name, "app.yaml"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatalf("story %q not found in testdata/apps/ or stories/", name)
	return ""
}

// goldenPath returns the path to the golden JSON file for a story.
func goldenPath(story string) string {
	return filepath.Join("testdata/nodemap", story+".json")
}

// flowchartGoldenPath returns the path to the Flowchart golden file for a story.
func flowchartGoldenPath(story string) string {
	return filepath.Join("testdata/flowchart", story+".mmd")
}

// loadGolden reads the golden JSON file for a story.
func loadGolden(t *testing.T, path string) viz.FlowchartResult {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "reading golden file %s", path)
	var result viz.FlowchartResult
	require.NoError(t, json.Unmarshal(b, &result), "unmarshalling golden %s", path)
	return result
}

// writeGolden writes the result to the golden JSON file.
func writeGolden(t *testing.T, path string, result viz.FlowchartResult) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	b, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o644))
	t.Logf("wrote golden %s", path)
}

// assertNodeMapCoversSource checks that every key in the NodeMap appears as a
// node ID in the Mermaid source.
func assertNodeMapCoversSource(t *testing.T, result viz.FlowchartResult) {
	t.Helper()
	nodeIDs := nodeIDsInSource(result.Source)
	for id := range result.NodeMap {
		require.True(t, nodeIDs[id],
			"NodeMap key %q does not appear as a node in Source; available IDs: %v",
			id, sortedStringSet(nodeIDs))
	}
}

// assertKindsPresent checks that at least one NodeRef of each expected Kind
// exists in the NodeMap.
func assertKindsPresent(t *testing.T, nm map[string]viz.NodeRef, expectedKinds ...string) {
	t.Helper()
	found := map[string]bool{}
	for _, ref := range nm {
		found[ref.Kind] = true
	}
	for _, k := range expectedKinds {
		require.True(t, found[k], "expected at least one NodeRef of Kind=%q but found none; kinds present: %v", k, sortedStringSetFromBool(found))
	}
}

func sortedStringSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortedStringSetFromBool(m map[string]bool) []string {
	return sortedStringSet(m)
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestFlowchartWithMap — golden tests for four stories
// ─────────────────────────────────────────────────────────────────────────────

// storyCase describes one story golden test.
type storyCase struct {
	name   string
	appKey string // lookup key for storyPath (may differ from test name)
	// expectedKinds lists Kinds that MUST appear in the NodeMap at DetailStates.
	// Transition kind is never expected because Mermaid flowchart edges have no
	// clickable node IDs — see FlowchartWithMap docstring.
	expectedKinds []string
	// fullKinds lists additional Kinds expected at DetailFull (beyond expectedKinds).
	fullKinds []string
}

var storyCases = []storyCase{
	{
		// cloak: atomic + compound states, no on_enter invoke effects.
		// At DetailStates: only state nodes.
		// At DetailFull:   state + world (has world schema but no invoke effects;
		//                  no WSB_ nodes appear → world Kind is absent at DetailFull too).
		name:          "cloak",
		appKey:        "cloak",
		expectedKinds: []string{"state"},
		fullKinds:     []string{}, // cloak has no on_enter invoke → no effect/world WSB nodes
	},
	{
		// oregon-trail: large app with many on_enter invocations and world writes.
		name:          "oregon-trail",
		appKey:        "oregon-trail",
		expectedKinds: []string{"state"},
		fullKinds:     []string{"effect", "world"},
	},
	{
		// bugfix: pipeline story with oracle invocations and world bind.
		name:          "bugfix",
		appKey:        "bugfix",
		expectedKinds: []string{"state"},
		fullKinds:     []string{"effect", "world"},
	},
	{
		// frontier_event: imports sub-story; has invoke effects but no bind/set.
		// At DetailFull: state + effect; no world Kind because no bind/set in on_enter.
		name:          "frontier_event",
		appKey:        "frontier_event",
		expectedKinds: []string{"state"},
		fullKinds:     []string{"effect"},
	},
}

func TestFlowchartWithMap(t *testing.T) {
	for _, tc := range storyCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := storyPath(t, tc.appKey)
			def, err := app.Load(path)
			require.NoError(t, err, "loading story %s from %s", tc.name, path)

			// ── DetailStates ─────────────────────────────────────────────────
			result, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailStates})
			require.NoError(t, err)

			// 1. Source must be non-empty and contain the flowchart header.
			require.NotEmpty(t, result.Source)
			require.Contains(t, result.Source, "flowchart LR")

			// 2. NodeMap must be non-empty.
			require.NotEmpty(t, result.NodeMap, "NodeMap must not be empty")

			// 3. Every NodeMap key must appear as a node in the Source.
			assertNodeMapCoversSource(t, result)

			// 4. Expected Kinds must be present.
			assertKindsPresent(t, result.NodeMap, tc.expectedKinds...)

			// 5. Source must equal FlowchartBytes (bit-identical).
			srcBytes, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
			require.NoError(t, err)
			require.Equal(t, string(srcBytes), result.Source,
				"FlowchartWithMap.Source must be identical to FlowchartBytes output")

			// 6. Golden check (DetailStates only — enough to lock the format).
			gp := goldenPath(tc.name)
			if *update {
				writeGolden(t, gp, result)
				return
			}
			if _, err := os.Stat(gp); os.IsNotExist(err) {
				t.Fatalf("golden file %s does not exist; run with -update to create it", gp)
			}
			golden := loadGolden(t, gp)
			require.Equal(t, golden.Source, result.Source, "Source differs from golden")
			require.Equal(t, golden.NodeMap, result.NodeMap, "NodeMap differs from golden")
		})
	}
}

// TestFlowchartWithMapDetailFull checks the additional Kinds available at
// DetailFull without requiring a separate golden (the structure is verified
// by the node-in-source assertion).
func TestFlowchartWithMapDetailFull(t *testing.T) {
	for _, tc := range storyCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := storyPath(t, tc.appKey)
			def, err := app.Load(path)
			require.NoError(t, err)

			result, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailFull})
			require.NoError(t, err)

			// All DetailStates Kinds must still be present at DetailFull.
			assertKindsPresent(t, result.NodeMap, tc.expectedKinds...)

			// Additional Kinds expected at DetailFull.
			assertKindsPresent(t, result.NodeMap, tc.fullKinds...)

			// Every key must appear as a node in Source.
			assertNodeMapCoversSource(t, result)

			// Source must be bit-identical to FlowchartBytes.
			srcBytes, err := viz.FlowchartBytes(def, viz.DetailFull, viz.FlowchartFilter{})
			require.NoError(t, err)
			require.Equal(t, string(srcBytes), result.Source)
		})
	}
}

// TestFlowchartUnchanged locks the FlowchartBytes output for the cloak story
// against regression from any future refactor of flowchart.go.
func TestFlowchartUnchanged(t *testing.T) {
	def, err := app.Load(storyPath(t, "cloak"))
	require.NoError(t, err)

	out, err := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
	require.NoError(t, err)

	gp := flowchartGoldenPath("cloak-states")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(gp), 0o755))
		require.NoError(t, os.WriteFile(gp, out, 0o644))
		t.Logf("wrote flowchart golden %s", gp)
		return
	}

	if _, err := os.Stat(gp); os.IsNotExist(err) {
		t.Fatalf("flowchart golden %s does not exist; run with -update to create it", gp)
	}
	golden, err := os.ReadFile(gp)
	require.NoError(t, err)
	require.Equal(t, string(golden), string(out),
		"FlowchartBytes output changed; if intentional run with -update")
}

// TestFlowchartWithMapMinimal is a quick sanity check using the inline
// minimalApp from flowchart_test.go rather than loading a real story.
// This runs in microseconds and is suitable as a first-pass regression guard.
func TestFlowchartWithMapMinimal(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "test-nm", Title: "NodeMap Test"},
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
						Invoke: "host.oracle.ask",
						With:   map[string]any{"prompt": "prompts/p.md"},
						Bind:   map[string]string{"analysis": "text"},
					},
				},
				On: map[string][]app.Transition{
					"done": {{Target: "done"}},
				},
			},
			"done": {Terminal: true},
		},
	}

	t.Run("DetailStates", func(t *testing.T) {
		result, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailStates})
		require.NoError(t, err)

		// Source identical to FlowchartBytes.
		src, _ := viz.FlowchartBytes(def, viz.DetailStates, viz.FlowchartFilter{})
		require.Equal(t, string(src), result.Source)

		// Three state nodes.
		require.Len(t, result.NodeMap, 3)
		require.Equal(t, "state", result.NodeMap["ST_idle"].Kind)
		require.Equal(t, "idle", result.NodeMap["ST_idle"].Ref)
		require.Equal(t, "state", result.NodeMap["ST_working"].Kind)
		require.Equal(t, "state", result.NodeMap["ST_done"].Kind)

		assertNodeMapCoversSource(t, result)
	})

	t.Run("DetailFull", func(t *testing.T) {
		result, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailFull})
		require.NoError(t, err)

		// STEP_ node for on_enter invoke.
		step, ok := result.NodeMap["STEP_working_0"]
		require.True(t, ok, "expected STEP_working_0 in NodeMap")
		require.Equal(t, "effect", step.Kind)
		require.Equal(t, "working:on_enter:0", step.Ref)

		// WSB_ node for the bind: {analysis: text}.
		wsb, ok := result.NodeMap["WSB_working_0"]
		require.True(t, ok, "expected WSB_working_0 in NodeMap")
		require.Equal(t, "world", wsb.Kind)
		require.True(t, strings.Contains(wsb.Ref, "analysis"), "world Ref should mention analysis: %s", wsb.Ref)

		assertNodeMapCoversSource(t, result)
	})

	t.Run("DetailRooms", func(t *testing.T) {
		result, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{Detail: viz.DetailRooms})
		require.NoError(t, err)

		// Room-level nodes use RI_ prefix.
		ri, ok := result.NodeMap["RI_idle"]
		require.True(t, ok, "expected RI_idle in NodeMap")
		require.Equal(t, "state", ri.Kind)
		require.Equal(t, "idle", ri.Ref)

		assertNodeMapCoversSource(t, result)
	})
}
