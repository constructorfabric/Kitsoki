package render

import (
	"strings"
	"sync"
	"testing"

	"kitsoki/internal/expr"
)

// makeEnv builds an expr.Env with helpers populated against a menu shape
// containing one primary intent ("start_journey") and one blocked intent
// ("buy_oxen", reason "not enough cash").
func makeEnv() expr.Env {
	env := expr.Env{
		World: map[string]any{
			"money":       100,
			"foo":         "hello",
			"empty":       "",
			"oxen":        2,
			"party_size":  4,
			"current_app": "oregon-trail",
			"items":       []any{"a", "b", "c"},
			"members": []any{
				map[string]any{"name": "Alice", "role": "leader"},
				map[string]any{"name": "Bob", "role": "scout"},
			},
		},
		Slots: map[string]any{
			"cmd": "go test",
		},
		Event: map[string]any{
			"kind": "user.input",
		},
		Run: expr.RunCtx{ID: "run-1", Turn: 7},
		Args: map[string]any{
			"target": "store",
		},
		Menu: map[string]any{
			"primary": []any{
				map[string]any{"intent": "start_journey", "display": "Start"},
			},
			"blocked": []any{
				map[string]any{"intent": "buy_oxen", "reason": "not enough cash"},
			},
		},
	}
	expr.PopulateMenuHelpers(&env)
	return env
}

func TestPongo_LiteralPassthrough(t *testing.T) {
	env := makeEnv()
	cases := []string{
		"",
		"plain text",
		"newlines\nare\nfine",
		"$ and & and < survive without delimiters",
	}
	for _, src := range cases {
		out, err := Pongo(src, env)
		if err != nil {
			t.Fatalf("Pongo(%q) error: %v", src, err)
		}
		if out != src {
			t.Fatalf("Pongo(%q) literal passthrough: got %q want %q", src, out, src)
		}
	}
}

func TestPongo_Interpolation(t *testing.T) {
	env := makeEnv()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"world scalar", "{{ world.foo }}", "hello"},
		{"world number", "${{ world.money }}", "$100"},
		{"slots scalar", "{{ slots.cmd }}", "go test"},
		{"run id", "{{ run.id }}", "run-1"},
		{"event kind", "{{ event.kind }}", "user.input"},
		{"args field", "{{ args.target }}", "store"},
		{"mixed literal + interpolation",
			"Day {{ run.turn }} of {{ world.party_size }}",
			"Day 7 of 4"},
		{"unknown top-level → empty", "[{{ nonexistent }}]", "[]"},
		{"unknown world key → empty", "[{{ world.nope }}]", "[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Pongo(tc.src, env)
			if err != nil {
				t.Fatalf("Pongo(%q) error: %v", tc.src, err)
			}
			if out != tc.want {
				t.Fatalf("Pongo(%q): got %q want %q", tc.src, out, tc.want)
			}
		})
	}
}

func TestPongo_DefaultFilter(t *testing.T) {
	env := makeEnv()
	cases := []struct {
		name string
		src  string
		want string
	}{
		// pongo2/v6 filter-arg syntax uses ':' (Django form), NOT parens.
		// |default falsy → fallback (semantics: !IsTrue)
		{"default falsy → fallback", `{{ world.empty|default:"(unset)" }}`, "(unset)"},
		{"default truthy → keep", `{{ world.foo|default:"(unset)" }}`, "hello"},
		{"default missing → fallback", `{{ world.nope|default:"(unset)" }}`, "(unset)"},
		// upper/lower work without args.
		{"upper", `{{ world.foo|upper }}`, "HELLO"},
		{"lower", `{{ world.current_app|lower }}`, "oregon-trail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Pongo(tc.src, env)
			if err != nil {
				t.Fatalf("Pongo(%q) error: %v", tc.src, err)
			}
			if out != tc.want {
				t.Fatalf("Pongo(%q): got %q want %q", tc.src, out, tc.want)
			}
		})
	}
}

func TestPongo_IfBlocks(t *testing.T) {
	env := makeEnv()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"if true", "{% if world.foo %}A{% endif %}", "A"},
		{"if false", "{% if world.empty %}A{% endif %}", ""},
		{"if/else true", "{% if world.foo %}A{% else %}B{% endif %}", "A"},
		{"if/else false", "{% if world.empty %}A{% else %}B{% endif %}", "B"},
		{"if comparison", "{% if world.money > 50 %}rich{% else %}poor{% endif %}", "rich"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Pongo(tc.src, env)
			if err != nil {
				t.Fatalf("Pongo(%q) error: %v", tc.src, err)
			}
			if out != tc.want {
				t.Fatalf("Pongo(%q): got %q want %q", tc.src, out, tc.want)
			}
		})
	}
}

func TestPongo_ForLoop(t *testing.T) {
	env := makeEnv()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"for over strings",
			"{% for x in world.items %}{{ x }},{% endfor %}",
			"a,b,c,"},
		{"for over maps with field access",
			"{% for m in world.members %}{{ m.name }}={{ m.role }};{% endfor %}",
			"Alice=leader;Bob=scout;"},
		{"for with forloop.Counter",
			"{% for x in world.items %}{{ forloop.Counter }}.{{ x }} {% endfor %}",
			"1.a 2.b 3.c "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Pongo(tc.src, env)
			if err != nil {
				t.Fatalf("Pongo(%q) error: %v", tc.src, err)
			}
			if out != tc.want {
				t.Fatalf("Pongo(%q): got %q want %q", tc.src, out, tc.want)
			}
		})
	}
}

func TestPongo_Helpers(t *testing.T) {
	env := makeEnv()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"available true", "{{ available('start_journey') }}", "True"},
		{"available false", "{{ available('nope') }}", "False"},
		{"blocked true", "{{ blocked('buy_oxen') }}", "True"},
		{"blocked false", "{{ blocked('nope') }}", "False"},
		{"blocked_reason", "{{ blocked_reason('buy_oxen') }}", "not enough cash"},
		{"blocked_reason unknown → empty", "{{ blocked_reason('nope') }}", ""},
		{"intent_status available", "{{ intent_status('start_journey') }}", "available"},
		{"intent_status blocked", "{{ intent_status('buy_oxen') }}", "blocked"},
		{"intent_status unknown", "{{ intent_status('nope') }}", "unknown"},
		{"helper in if",
			"{% if available('start_journey') %}go{% else %}wait{% endif %}",
			"go"},
		{"helper in if (blocked path)",
			"{% if available('buy_oxen') %}go{% else %}wait{% endif %}",
			"wait"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Pongo(tc.src, env)
			if err != nil {
				t.Fatalf("Pongo(%q) error: %v", tc.src, err)
			}
			if out != tc.want {
				t.Fatalf("Pongo(%q): got %q want %q", tc.src, out, tc.want)
			}
		})
	}
}

func TestPongo_HelperStubsWhenNil(t *testing.T) {
	// An env without PopulateMenuHelpers should still render — the package
	// installs no-op stubs so templates referencing helpers from non-view
	// contexts don't blow up.
	env := expr.Env{World: map[string]any{}}
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"available stub", "{{ available('x') }}", "False"},
		{"blocked stub", "{{ blocked('x') }}", "False"},
		{"blocked_reason stub", "[{{ blocked_reason('x') }}]", "[]"},
		{"intent_status stub", "{{ intent_status('x') }}", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Pongo(tc.src, env)
			if err != nil {
				t.Fatalf("Pongo(%q) error: %v", tc.src, err)
			}
			if out != tc.want {
				t.Fatalf("Pongo(%q): got %q want %q", tc.src, out, tc.want)
			}
		})
	}
}

func TestPongo_AutoescapeDisabled(t *testing.T) {
	// HTML special chars must pass through unchanged — kitsoki renders into
	// a terminal, not a browser.
	env := expr.Env{World: map[string]any{"name": "A & B <c>"}}
	out, err := Pongo("{{ world.name }}", env)
	if err != nil {
		t.Fatalf("Pongo error: %v", err)
	}
	if out != "A & B <c>" {
		t.Fatalf("autoescape disabled: got %q want %q", out, "A & B <c>")
	}
}

// TestAutoescapeRemainsDisabledAcrossConcurrentRenders is a sentinel
// for the package-global `pongo2.SetAutoescape` invariant. pongo2/v6
// has no per-TemplateSet autoescape configuration; the only way to
// disable HTML escaping is the global, and any other caller of
// `pongo2.SetAutoescape(true)` would silently corrupt our terminal
// output. The test renders 100 concurrent goroutines and asserts that
// HTML entities never appear — if a future init() (or test) flips the
// global, the entity escape leaks into the rendered text and this
// test fires with a clear failure pointing back to the package doc's
// "DO NOT call SetAutoescape elsewhere" rule.
func TestAutoescapeRemainsDisabledAcrossConcurrentRenders(t *testing.T) {
	t.Parallel()
	const src = `{{ world.name }}`
	const want = `A & B <c> "d" 'e'`
	env := expr.Env{World: map[string]any{"name": want}}

	var wg sync.WaitGroup
	const N = 100
	wg.Add(N)
	errs := make(chan string, N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			out, err := Pongo(src, env)
			if err != nil {
				errs <- "render error: " + err.Error()
				return
			}
			if out != want {
				errs <- "autoescape leaked: got " + out
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

func TestPongo_ErrorWrapping(t *testing.T) {
	env := makeEnv()
	// Bogus filter name — pongo2 should fail at parse time.
	_, err := Pongo("{{ world.foo|definitely_not_a_filter }}", env)
	if err == nil {
		t.Fatal("expected error for unknown filter, got nil")
	}
	if !strings.Contains(err.Error(), "render: pongo2 template") {
		t.Fatalf("error missing wrap prefix: %v", err)
	}
	// Snippet of the source should appear in the message for author UX.
	if !strings.Contains(err.Error(), "definitely_not_a_filter") {
		t.Fatalf("error missing template source snippet: %v", err)
	}
}

func TestPongo_UnclosedBlockErrors(t *testing.T) {
	env := makeEnv()
	_, err := Pongo("{% if world.foo %}oops", env)
	if err == nil {
		t.Fatal("expected error for unclosed {% if %}, got nil")
	}
	if !strings.Contains(err.Error(), "render: pongo2 template") {
		t.Fatalf("error missing wrap prefix: %v", err)
	}
}

func TestPongo_FastPathSkipsParse(t *testing.T) {
	// A literal-only string with no delimiters should pass through without
	// pongo2 seeing it (otherwise embedded curly-braces in code examples
	// would fail to parse). This input would NOT parse as a pongo2
	// template — proving the fast path runs.
	env := makeEnv()
	src := "func main() { fmt.Println(\"{ literal\") }"
	out, err := Pongo(src, env)
	if err != nil {
		t.Fatalf("fast path should not parse: %v", err)
	}
	if out != src {
		t.Fatalf("fast path: got %q want %q", out, src)
	}
}

func TestToContext_KeysExposed(t *testing.T) {
	env := makeEnv()
	ctx := ToContext(env)
	for _, key := range []string{
		"world", "slots", "event", "run", "args", "menu", "item",
		"available", "blocked", "blocked_reason", "intent_status",
	} {
		if _, ok := ctx[key]; !ok {
			t.Errorf("ToContext missing key %q", key)
		}
	}
}
