package app

import (
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"
)

// stateYAML wraps a view: snippet in a minimal state-like document so we
// exercise the same unmarshal path as the loader (View as a field of a
// containing struct, not as the document root).
type stateYAML struct {
	View View `yaml:"view"`
}

// unmarshalView decodes the supplied YAML body into a View via the
// containing-struct path.
func unmarshalView(t *testing.T, body string) View {
	t.Helper()
	var s stateYAML
	if err := goyaml.UnmarshalWithOptions([]byte(body), &s, goyaml.Strict()); err != nil {
		t.Fatalf("unmarshal: %v\nbody:\n%s", err, body)
	}
	return s.View
}

// unmarshalViewErr returns the error from decoding so tests can assert on
// the failure mode.
func unmarshalViewErr(body string) error {
	var s stateYAML
	return goyaml.UnmarshalWithOptions([]byte(body), &s, goyaml.Strict())
}

// ---- String form ------------------------------------------------------------

func TestView_StringForm_PlainScalar(t *testing.T) {
	v := unmarshalView(t, `view: "hello world"`)
	if got, want := v.Source, "hello world"; got != want {
		t.Errorf("Source = %q, want %q", got, want)
	}
	if got, want := len(v.Elements), 1; got != want {
		t.Fatalf("Elements len = %d, want %d", got, want)
	}
	if got := v.Elements[0]; got.Kind != "template" || got.Source != "hello world" {
		t.Errorf("Elements[0] = %+v, want {Kind:template Source:hello world}", got)
	}
	if v.IsEmpty() {
		t.Errorf("IsEmpty() = true, want false")
	}
	if v.SourceString() != "hello world" {
		t.Errorf("SourceString = %q", v.SourceString())
	}
}

func TestView_StringForm_BlockLiteral(t *testing.T) {
	body := "view: |\n  line one\n  line two\n"
	v := unmarshalView(t, body)
	want := "line one\nline two\n"
	if v.Source != want {
		t.Errorf("Source = %q, want %q", v.Source, want)
	}
	if len(v.Elements) != 1 || v.Elements[0].Kind != "template" {
		t.Errorf("expected single template element, got %+v", v.Elements)
	}
}

func TestView_EmptyScalar(t *testing.T) {
	// `view:` with no body is the empty scalar — represents "no view".
	v := unmarshalView(t, `view:`)
	if !v.IsEmpty() {
		t.Errorf("IsEmpty() = false, want true for missing view")
	}
}

// ---- Array form -------------------------------------------------------------

func TestView_ArrayForm_AllKinds(t *testing.T) {
	body := `view:
  - prose: "narration paragraph"
  - heading: "Section"
  - list:
      items:
        - "bare string item"
        - { label: "labeled", hint: "hint text" }
        - label: "with-when"
          when: "world.show"
      marker: "*"
  - kv:
      pairs:
        First: "1"
        Second: "{{ world.x }}"
        Third: "z"
  - code: |
      step 1
      step 2
  - template: "{{ world.legacy }}"
`
	v := unmarshalView(t, body)
	if v.Source != "" {
		t.Errorf("Source = %q, want empty for array form", v.Source)
	}
	if got, want := len(v.Elements), 6; got != want {
		t.Fatalf("Elements len = %d, want %d", got, want)
	}

	// prose
	if e := v.Elements[0]; e.Kind != "prose" || e.Source != "narration paragraph" {
		t.Errorf("prose element wrong: %+v", e)
	}
	// heading
	if e := v.Elements[1]; e.Kind != "heading" || e.Source != "Section" {
		t.Errorf("heading element wrong: %+v", e)
	}
	// list
	list := v.Elements[2]
	if list.Kind != "list" || list.Marker != "*" || len(list.Items) != 3 {
		t.Fatalf("list element wrong: %+v", list)
	}
	if list.Items[0] != (ListItem{Label: "bare string item"}) {
		t.Errorf("list item 0 = %+v", list.Items[0])
	}
	if list.Items[1] != (ListItem{Label: "labeled", Hint: "hint text"}) {
		t.Errorf("list item 1 = %+v", list.Items[1])
	}
	if list.Items[2] != (ListItem{Label: "with-when", When: "world.show"}) {
		t.Errorf("list item 2 = %+v", list.Items[2])
	}
	// kv — pair order must be First, Second, Third.
	kv := v.Elements[3]
	if kv.Kind != "kv" || len(kv.Pairs) != 3 {
		t.Fatalf("kv element wrong: %+v", kv)
	}
	wantPairs := []struct{ k, v string }{
		{"First", "1"},
		{"Second", "{{ world.x }}"},
		{"Third", "z"},
	}
	for i, want := range wantPairs {
		gotK, _ := kv.Pairs[i].Key.(string)
		gotV, _ := kv.Pairs[i].Value.(string)
		if gotK != want.k || gotV != want.v {
			t.Errorf("kv pair %d = (%v, %v), want (%s, %s)", i, kv.Pairs[i].Key, kv.Pairs[i].Value, want.k, want.v)
		}
	}
	// code
	if e := v.Elements[4]; e.Kind != "code" || !strings.HasPrefix(e.Source, "step 1") {
		t.Errorf("code element wrong: %+v", e)
	}
	// template
	if e := v.Elements[5]; e.Kind != "template" || e.Source != "{{ world.legacy }}" {
		t.Errorf("template element wrong: %+v", e)
	}
}

func TestView_ArrayForm_ElementLevelWhen(t *testing.T) {
	body := `view:
  - prose: "shown when guard passes"
    when: "world.flag"
`
	v := unmarshalView(t, body)
	if got, want := v.Elements[0].When, "world.flag"; got != want {
		t.Errorf("When = %q, want %q", got, want)
	}
}

// ---- Block-inheritance form -------------------------------------------------

func TestView_ExtendsBlocksForm(t *testing.T) {
	body := `view:
  extends: "base"
  blocks:
    body:
      - prose: "body paragraph"
    choices:
      - list:
          items:
            - "first"
            - "second"
`
	v := unmarshalView(t, body)
	if v.Source != "" {
		t.Errorf("Source = %q, want empty", v.Source)
	}
	if len(v.Elements) != 0 {
		t.Errorf("Elements should be empty for extends form, got %d", len(v.Elements))
	}
	if v.Extends != "base" {
		t.Errorf("Extends = %q, want base", v.Extends)
	}
	if got, want := len(v.Blocks), 2; got != want {
		t.Fatalf("Blocks len = %d, want %d", got, want)
	}
	if v.Blocks["body"][0].Kind != "prose" {
		t.Errorf("body block first element kind = %q", v.Blocks["body"][0].Kind)
	}
	choices := v.Blocks["choices"]
	if len(choices) != 1 || choices[0].Kind != "list" || len(choices[0].Items) != 2 {
		t.Errorf("choices block wrong: %+v", choices)
	}
	if v.IsEmpty() {
		t.Errorf("IsEmpty() = true for extends form; expected false")
	}
}

// ---- Helpers ----------------------------------------------------------------

func TestLegacyView_ConstructorRoundTrip(t *testing.T) {
	v := LegacyView("body text")
	if v.Source != "body text" {
		t.Errorf("Source = %q", v.Source)
	}
	if len(v.Elements) != 1 || v.Elements[0].Kind != "template" || v.Elements[0].Source != "body text" {
		t.Errorf("Elements normalization wrong: %+v", v.Elements)
	}
	if LegacyView("").IsEmpty() != true {
		t.Errorf("LegacyView(\"\").IsEmpty() should be true")
	}
}

func TestView_IsEmpty(t *testing.T) {
	if !(View{}).IsEmpty() {
		t.Errorf("zero View should be empty")
	}
	if (View{Source: "x", Elements: []ViewElement{{Kind: "template", Source: "x"}}}).IsEmpty() {
		t.Errorf("string-form View should not be empty")
	}
	if (View{Elements: []ViewElement{{Kind: "prose", Source: "x"}}}).IsEmpty() {
		t.Errorf("array-form View should not be empty")
	}
	if (View{Extends: "base"}).IsEmpty() {
		t.Errorf("extends-form View should not be empty")
	}
}

// ---- Validation -------------------------------------------------------------

func TestView_Validate_UnknownKind(t *testing.T) {
	// Manually construct a malformed element — the unmarshaller normally
	// catches kind mismatches but Validate is the load-time backstop.
	v := View{Elements: []ViewElement{{Kind: "bogus"}}}
	err := v.Validate()
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "unknown element kind") {
		t.Errorf("error message = %q; want it to mention 'unknown element kind'", err.Error())
	}
}

func TestView_Validate_ListRequiresItems(t *testing.T) {
	v := View{Elements: []ViewElement{{Kind: "list"}}}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "list requires items") {
		t.Errorf("err = %v; want list-requires-items error", err)
	}
}

func TestView_Validate_KvRequiresPairs(t *testing.T) {
	v := View{Elements: []ViewElement{{Kind: "kv"}}}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "kv requires pairs") {
		t.Errorf("err = %v; want kv-requires-pairs error", err)
	}
}

func TestView_Validate_KvValuesMustBeString(t *testing.T) {
	v := View{Elements: []ViewElement{{
		Kind:  "kv",
		Pairs: goyaml.MapSlice{{Key: "x", Value: 42}},
	}}}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("err = %v; want kv-string-value error", err)
	}
}

func TestView_Unmarshal_MultipleKinds(t *testing.T) {
	body := `view:
  - prose: "p"
    heading: "h"
`
	err := unmarshalViewErr(body)
	if err == nil || !strings.Contains(err.Error(), "multiple kinds") {
		t.Errorf("err = %v; want multiple-kinds error", err)
	}
}

func TestView_Unmarshal_NoKind(t *testing.T) {
	body := `view:
  - when: "world.flag"
`
	err := unmarshalViewErr(body)
	if err == nil || !strings.Contains(err.Error(), "no kind") {
		t.Errorf("err = %v; want no-kind error", err)
	}
}

func TestView_Unmarshal_ListItem_EmptyString(t *testing.T) {
	body := `view:
  - list:
      items:
        - ""
`
	err := unmarshalViewErr(body)
	if err == nil || !strings.Contains(err.Error(), "empty string") {
		t.Errorf("err = %v; want empty-string error", err)
	}
}

// ---- Loader roundtrip -------------------------------------------------------

// loaderRoundTripFixture is a minimal AppDef YAML carrying both view forms
// to confirm Load() / LoadBytes() flow the View through unchanged.
const loaderRoundTripFixture = `
app:
  id: roundtrip
  version: "0.0.1"
intents:
  go:
    title: "Go"
root: start
states:
  start:
    type: atomic
    view: "legacy string view"
    on:
      go:
        - target: typed
  typed:
    type: atomic
    view:
      - prose: "narration"
      - heading: "Choices"
      - list:
          items:
            - "first"
            - { label: "second", hint: "with hint" }
      - kv:
          pairs:
            Alpha: "1"
            Beta:  "2"
      - code: |
          example
      - template: "{{ world.legacy }}"
    on:
      go:
        - target: terminal
          view: "transition view"
  terminal:
    type: atomic
    terminal: true
`

func TestView_LoaderRoundTrip_StringAndArrayForms(t *testing.T) {
	def, err := LoadBytes([]byte(loaderRoundTripFixture))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	// String form.
	start := def.States["start"]
	if start == nil {
		t.Fatalf("missing start state")
	}
	if start.View.Source != "legacy string view" {
		t.Errorf("start.View.Source = %q", start.View.Source)
	}
	if len(start.View.Elements) != 1 || start.View.Elements[0].Kind != "template" {
		t.Errorf("start.View elements wrong: %+v", start.View.Elements)
	}
	// Transition view (string form).
	tr := start.On["go"][0]
	if tr.View.SourceString() != "" {
		// transition view lives on the typed state's transition; start's transition has none.
		t.Errorf("start->typed transition should have no view, got %q", tr.View.SourceString())
	}
	// Array form.
	typed := def.States["typed"]
	if typed == nil {
		t.Fatalf("missing typed state")
	}
	if typed.View.Source != "" {
		t.Errorf("typed.View.Source = %q, want empty", typed.View.Source)
	}
	if len(typed.View.Elements) != 6 {
		t.Fatalf("typed.View.Elements len = %d, want 6", len(typed.View.Elements))
	}
	kinds := make([]string, len(typed.View.Elements))
	for i, e := range typed.View.Elements {
		kinds[i] = e.Kind
	}
	wantKinds := []string{"prose", "heading", "list", "kv", "code", "template"}
	for i, want := range wantKinds {
		if kinds[i] != want {
			t.Errorf("Elements[%d].Kind = %q, want %q", i, kinds[i], want)
		}
	}
	// Transition view (string form) on typed.go.
	typedTr := typed.On["go"][0]
	if typedTr.View.SourceString() != "transition view" {
		t.Errorf("typed.go transition view source = %q", typedTr.View.SourceString())
	}
	if len(typedTr.View.Elements) != 1 || typedTr.View.Elements[0].Kind != "template" {
		t.Errorf("typed.go transition view elements wrong: %+v", typedTr.View.Elements)
	}
}

func TestView_LoaderRejectsUnknownKind(t *testing.T) {
	const fixture = `
app: {id: x, version: "0.0.1"}
root: start
states:
  start:
    view:
      - quux: "bogus"
`
	_, err := LoadBytes([]byte(fixture))
	if err == nil || !strings.Contains(err.Error(), "no kind") && !strings.Contains(err.Error(), "unknown") {
		t.Errorf("err = %v; want kind error", err)
	}
}

func TestView_Unmarshal_ListItem_MissingLabel(t *testing.T) {
	body := `view:
  - list:
      items:
        - { hint: "hint without label" }
`
	err := unmarshalViewErr(body)
	if err == nil || !strings.Contains(err.Error(), "label is required") {
		t.Errorf("err = %v; want missing-label error", err)
	}
}
