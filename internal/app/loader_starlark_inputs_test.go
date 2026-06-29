package app

// loader_starlark_inputs_test.go — the load-time guard against the slidey-edit
// "edits never show" footgun: a host.starlark.run `inputs:` value written as a
// BARE expression (`world.x`) instead of a `{{ }}` template reaches the script
// as the literal string, silently breaking resolution. validateStarlarkEffects
// must reject both shapes (bare-expr heuristic + non-template-literal-vs-type) at
// load, where the fix is one edit — not at runtime, deep in an on_error arc.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLooksLikeBareStarlarkInputExpr covers the heuristic in isolation: it must
// fire on the real footgun shapes and stay quiet on templates + plausible
// literals (the precision that keeps it usable inside `inputs:`).
func TestLooksLikeBareStarlarkInputExpr(t *testing.T) {
	bare := []string{
		"world.deck.spec_path",
		"world.scene_index",
		"slots.feedback",
		`world.annotation?.anchor?.semantic_element?.ref ?? ""`,
		`str(world.current_scene ?? "")`, // not world-rooted, but uses ??
		"world.refine_feedback != \"\" ? world.refine_feedback : world.x",
		"args.foo[0]",
	}
	for _, s := range bare {
		if !looksLikeBareStarlarkInputExpr(s) {
			t.Errorf("expected BARE expr to be flagged: %q", s)
		}
	}

	ok := []string{
		"{{ world.deck.spec_path }}",              // templated — correct
		`{{ string(world.current_scene ?? "") }}`, // templated with operator
		"{% if world.x %}a{% else %}b{% endif %}", // the other template form
		"stories/slidey-edit/baked/deck.json",     // a literal path
		"a normal label",                          // prose
		"Spatial Annotation",                      // prose
		"",                                        // empty
	}
	for _, s := range ok {
		if looksLikeBareStarlarkInputExpr(s) {
			t.Errorf("expected NOT flagged: %q", s)
		}
	}
}

// writeStarlarkProbeApp writes a minimal but loadable app whose `probing` state
// invokes host.starlark.run with the given inputs: block, plus the script +
// sidecar it needs on disk. Returns the app.yaml path.
func writeStarlarkProbeApp(t *testing.T, inputsYAML string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "scripts", "probe.star"), `def main(ctx):
    return {"result": "ok"}
`)
	mustWriteFile(t, filepath.Join(dir, "scripts", "probe.star.yaml"), `inputs:
  spec_path: { type: string, required: false }
  mode:      { type: string, required: false }
  count:     { type: int, required: false }
outputs:
  result: { type: string }
`)
	appYAML := `app:
  id: test-starlark-inputs
  version: 0.1.0
root: probing
states:
  probing:
    on_enter:
      - invoke: host.starlark.run
        id: probe
        with:
          script: scripts/probe.star
          inputs:
` + slInputsIndent(inputsYAML, 12) + `        bind:
          result: result
    on:
      go:
        - target: done
  done:
    terminal: true
`
	p := filepath.Join(dir, "app.yaml")
	mustWriteFile(t, p, appYAML)
	return p
}

func slInputsIndent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString(pad)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// slLoadErr loads the app and returns the joined error text ("" on success).
func slLoadErr(t *testing.T, appPath string) string {
	t.Helper()
	if _, err := Load(appPath); err != nil {
		return err.Error()
	}
	return ""
}

func TestValidateStarlarkInputs_RejectsBareExprAndTypeMismatch(t *testing.T) {
	// spec_path: bare world-ref (heuristic). mode: bare via the ?? operator
	// (heuristic, not world-rooted). count: a plain string literal to a declared
	// int (type check — can never satisfy).
	app := writeStarlarkProbeApp(t, `spec_path: "world.deck.spec_path"
mode: "str(world.m ?? \"\")"
count: "seven"`)
	got := slLoadErr(t, app)

	for _, want := range []string{
		`input "spec_path" is a bare expression`,
		`input "mode" is a bare expression`,
		`input "count" is declared int but is wired to a non-template`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected validation error containing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestValidateStarlarkInputs_AcceptsTemplatedInputs(t *testing.T) {
	// The fixed shape: every input templated; a sole {{ }} preserves the typed
	// value, so the int input is fine. No starlark-inputs error must appear.
	app := writeStarlarkProbeApp(t, `spec_path: "{{ world.deck.spec_path }}"
mode: "{{ string(world.m ?? \"\") }}"
count: "{{ world.count }}"`)
	got := slLoadErr(t, app)

	for _, bad := range []string{"is a bare expression", "can never satisfy"} {
		if strings.Contains(got, bad) {
			t.Errorf("templated inputs must NOT trip the starlark-inputs check, but got: %s", got)
		}
	}
}
