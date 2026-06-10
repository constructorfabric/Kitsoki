package graph_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/app/graph"
)

// buildApp compiles an in-memory AppDef into an app.App for pure graph tests.
func buildApp(def *app.AppDef) app.App { return app.Compile(def) }

// linearDef builds idle → a → b → c (each via one intent), plus an
// unreachable room "orphan".
func linearDef() *app.AppDef {
	mk := func(target string) map[string][]app.Transition {
		if target == "" {
			return nil
		}
		return map[string][]app.Transition{"go": {{Target: target}}}
	}
	return &app.AppDef{
		World: map[string]app.VarDef{
			"idea":  {Type: "string"},
			"count": {Type: "int"},
		},
		Root: "idle",
		States: map[string]*app.State{
			"idle": {Description: "Start", On: mk("a")},
			"a":    {On: mk("b"), OnEnter: []app.Effect{{Invoke: "host.oracle.decide", With: map[string]any{"schema": "schemas/x.json"}, Bind: map[string]string{"idea": "submitted"}}}},
			"b":    {On: mk("c")},
			"c":    {On: mk("@exit:done")},
			"orphan": {On: mk("idle")},
		},
	}
}

func TestRoomList_BFSOrder(t *testing.T) {
	a := buildApp(linearDef())
	rooms := graph.RoomList(a)

	gotOrder := make([]string, len(rooms))
	for i, r := range rooms {
		gotOrder[i] = r.ID
	}
	// idle(0) a(1) b(2) c(3) then orphan (unreachable, sorted last by name).
	want := []string{"idle", "a", "b", "c", "orphan"}
	if len(gotOrder) != len(want) {
		t.Fatalf("room count = %d (%v), want %d (%v)", len(gotOrder), gotOrder, len(want), want)
	}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("BFS order = %v, want %v", gotOrder, want)
		}
	}

	byID := map[string]graph.RoomSummary{}
	for _, r := range rooms {
		byID[r.ID] = r
	}
	if byID["idle"].Distance != 0 {
		t.Errorf("idle distance = %v, want 0", byID["idle"].Distance)
	}
	if byID["c"].Distance != 3 {
		t.Errorf("c distance = %v, want 3", byID["c"].Distance)
	}
	if byID["orphan"].Distance < 1e8 {
		t.Errorf("orphan distance = %v, want unreachable sentinel", byID["orphan"].Distance)
	}
	if !byID["a"].HasOracle {
		t.Errorf("room a should report has_oracle (host.oracle.decide on_enter)")
	}
	if byID["b"].HasOracle {
		t.Errorf("room b should not report has_oracle")
	}
}

func TestDetail_WorldKeyDirection(t *testing.T) {
	// Room reads world.count in a view template and writes world.idea via bind.
	def := linearDef()
	def.States["a"].View = app.LegacyView("count is {{ world.count }}")
	a := buildApp(def)

	detail, ok := graph.Detail(a, "a", "stories/x/app.yaml")
	if !ok {
		t.Fatal("Detail(a) not ok")
	}
	dir := map[string]string{}
	for _, wk := range detail.WorldKeys {
		dir[wk.Name] = wk.Direction
	}
	if dir["count"] != "read" {
		t.Errorf("count direction = %q, want read", dir["count"])
	}
	if dir["idea"] != "write" {
		t.Errorf("idea direction = %q, want write", dir["idea"])
	}
	if detail.SourceRef == nil || detail.SourceRef.Path != "stories/x/app.yaml" || detail.SourceRef.Line != 1 {
		t.Errorf("source_ref = %+v, want {stories/x/app.yaml,1}", detail.SourceRef)
	}
}

func TestOracleContracts_CassetteKeyDerivation(t *testing.T) {
	a := buildApp(linearDef())
	contracts := graph.OracleContracts(a, "a")
	if len(contracts) != 1 {
		t.Fatalf("contracts = %d, want 1", len(contracts))
	}
	c := contracts[0]
	if c.Kind != "host.oracle.decide" {
		t.Errorf("kind = %q", c.Kind)
	}
	if c.CassetteKey.Handler != "host.oracle.decide" {
		t.Errorf("key.handler = %q", c.CassetteKey.Handler)
	}
	if c.CassetteKey.Phase != "a" {
		t.Errorf("key.phase = %q, want a", c.CassetteKey.Phase)
	}
	if c.CassetteKey.SchemaName != "x.json" {
		t.Errorf("key.schema_name = %q, want x.json", c.CassetteKey.SchemaName)
	}
}

func TestCassetteGlob(t *testing.T) {
	globs := graph.CassetteGlob("stories/prd")
	if len(globs) == 0 {
		t.Fatal("no cassette globs")
	}
	if globs[0] != filepath.Join("stories/prd", "cassettes", "*.yaml") {
		t.Errorf("glob[0] = %q", globs[0])
	}
	// Real stories keep cassettes one level deeper under flows/cassettes/;
	// that layout must be globbed or the workbench sees no recordings.
	want := filepath.Join("stories/prd", "flows", "cassettes", "*.cassette.yaml")
	found := false
	for _, g := range globs {
		if g == want {
			found = true
		}
	}
	if !found {
		t.Errorf("nested flows/cassettes glob %q not present in %v", want, globs)
	}
}

// TestDetail_ViewElements asserts the room detail carries the real typed view
// elements (PascalCase JSON Kind/Source/Items/Pairs that ViewElement.vue reads),
// not a lossy summary — so the reusable StoryViewer renders actual content.
func TestDetail_ViewElements(t *testing.T) {
	def := linearDef()
	def.States["a"].View = app.View{Elements: []app.ViewElement{
		{Kind: "heading", Source: "Hello"},
		{Kind: "list", Items: []app.ListItem{{Label: "one"}, {Label: "two"}}},
	}}
	a := buildApp(def)

	detail, ok := graph.Detail(a, "a", "")
	if !ok {
		t.Fatal("Detail(a) not ok")
	}
	if len(detail.View) != 2 {
		t.Fatalf("view elements = %d, want 2", len(detail.View))
	}
	if detail.View[0].Kind != "heading" || detail.View[0].Source != "Hello" {
		t.Errorf("view[0] = %+v", detail.View[0])
	}
	if detail.View[1].Kind != "list" || len(detail.View[1].Items) != 2 {
		t.Errorf("view[1] = %+v, want list with 2 items", detail.View[1])
	}
	// The JSON wire shape must be PascalCase so ViewElement.vue's el.Kind /
	// el.Source / el.Items switch matches at runtime.
	blob, err := json.Marshal(detail.View)
	if err != nil {
		t.Fatal(err)
	}
	js := string(blob)
	for _, key := range []string{`"Kind"`, `"Source"`, `"Items"`, `"Label"`} {
		if !strings.Contains(js, key) {
			t.Errorf("marshalled view missing %s: %s", key, js)
		}
	}
}

// TestRoomList_PRD loads the real prd story and asserts the BFS shape against
// the documented graph: idle is first (distance 0), and the five rooms are
// present. Pure: app.Load reads YAML only, no LLM.
func TestRoomList_PRD(t *testing.T) {
	def, err := app.Load("../../../stories/prd/app.yaml")
	if err != nil {
		t.Fatalf("load prd: %v", err)
	}
	a := app.Compile(def)
	rooms := graph.RoomList(a)
	if len(rooms) == 0 {
		t.Fatal("no rooms")
	}
	if rooms[0].ID != "idle" {
		t.Errorf("first room = %q, want idle", rooms[0].ID)
	}
	have := map[string]bool{}
	for _, r := range rooms {
		have[r.ID] = true
	}
	for _, want := range []string{"idle", "clarifying", "brief", "references", "drafting"} {
		if !have[want] {
			t.Errorf("missing room %q in %v", want, rooms)
		}
	}

	// clarifying runs host.oracle.decide on_enter — must report has_oracle and
	// produce an oracle contract.
	for _, r := range rooms {
		if r.ID == "clarifying" && !r.HasOracle {
			t.Errorf("clarifying should report has_oracle")
		}
	}
	contracts := graph.OracleContracts(a, "clarifying")
	if len(contracts) == 0 {
		t.Fatal("clarifying has no oracle contracts")
	}
	if contracts[0].Kind != "host.oracle.decide" {
		t.Errorf("clarifying oracle kind = %q, want host.oracle.decide", contracts[0].Kind)
	}
	if contracts[0].CassetteKey.SchemaName != "clarifications.json" {
		t.Errorf("clarifying schema_name = %q, want clarifications.json", contracts[0].CassetteKey.SchemaName)
	}
}

func TestRoomList_SecondStory(t *testing.T) {
	def, err := app.Load("../../../stories/oregon-trail/app.yaml")
	if err != nil {
		t.Skipf("oregon-trail not loadable: %v", err)
	}
	a := app.Compile(def)
	rooms := graph.RoomList(a)
	if len(rooms) == 0 {
		t.Fatal("no rooms")
	}
	if rooms[0].ID != "intro" {
		t.Errorf("first room = %q, want intro (root)", rooms[0].ID)
	}
	// Distances must be monotonic non-decreasing (BFS order).
	prev := -1.0
	for _, r := range rooms {
		if r.Distance < prev {
			t.Errorf("rooms not sorted by distance: %v", rooms)
			break
		}
		prev = r.Distance
	}
}
