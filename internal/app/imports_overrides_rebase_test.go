package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRebaseWithMap_NestedTaskPaths pins that an imported child's
// host.agent.task paths rebase to the child story's directory:
//   - context.prompt / context.prompt_path (nested under with.context)
//   - acceptance.schema (nested under with.acceptance)
//
// Regression: acceptance.schema was NOT rebased, so the runtime joined the
// relative path against the PARENT app dir ($KITSOKI_APP_DIR) and failed with
// "schema ... not found" — silently swallowed by the room's on_error, leaving
// the brief unwritten. See rooms/proposal.yaml brief_distill.
func TestRebaseWithMap_NestedTaskPaths(t *testing.T) {
	childDir := "/repo/stories/dev-story"
	with := map[string]any{
		"agent":       "proposal_brief_writer",
		"working_dir": "{{ world.proposal_workspace }}", // template — left alone
		"acceptance": map[string]any{
			"schema": "schemas/brief-distill.json",
		},
		"context": map[string]any{
			"prompt": "prompts/proposal_brief_distill.md",
		},
	}

	rebaseWithMap(with, childDir)

	acc := with["acceptance"].(map[string]any)
	if got, want := acc["schema"].(string), filepath.Join(childDir, "schemas/brief-distill.json"); got != want {
		t.Errorf("acceptance.schema = %q, want %q", got, want)
	}
	ctx := with["context"].(map[string]any)
	if got, want := ctx["prompt"].(string), filepath.Join(childDir, "prompts/proposal_brief_distill.md"); got != want {
		t.Errorf("context.prompt = %q, want %q", got, want)
	}
	// Templated working_dir must be left untouched.
	if got := with["working_dir"].(string); got != "{{ world.proposal_workspace }}" {
		t.Errorf("working_dir rewritten unexpectedly: %q", got)
	}
}

// TestImports_RebasesNestedEffectAssetPaths proves imported host-call asset
// paths are rebased even when the host call sits inside Effect.Effects. Those
// nested effects are used by on_complete target blocks, so leaving their
// prompt/schema paths relative makes the runtime resolve them against the
// importing app directory instead of the defining child story directory.
func TestImports_RebasesNestedEffectAssetPaths(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	childDir := filepath.Join(root, "child")
	mustMkdirAll(t, filepath.Join(parentDir, "prompts"))
	mustMkdirAll(t, filepath.Join(parentDir, "schemas"))
	mustMkdirAll(t, filepath.Join(childDir, "prompts"))
	mustMkdirAll(t, filepath.Join(childDir, "schemas"))

	mustWriteFile(t, filepath.Join(parentDir, "prompts", "nested.md"), "parent prompt")
	mustWriteFile(t, filepath.Join(parentDir, "schemas", "nested.json"), `{"type":"object","properties":{"origin":{"const":"parent"}}}`)
	mustWriteFile(t, filepath.Join(childDir, "prompts", "nested.md"), "child prompt")
	mustWriteFile(t, filepath.Join(childDir, "schemas", "nested.json"), `{"type":"object","properties":{"origin":{"const":"child"}}}`)
	mustWriteFile(t, filepath.Join(parentDir, "app.yaml"), `
app: {id: parent, title: parent}
root: main
hosts: [host.agent.decide, host.run]
imports:
  sub:
    source: ../child
    entry: start
states:
  main:
    view: parent
`)
	mustWriteFile(t, filepath.Join(childDir, "app.yaml"), `
app: {id: child, title: child}
root: start
hosts: [host.agent.decide, host.run]
states:
  start:
    view: child
    on_enter:
      - invoke: host.run
        background: true
        on_complete:
          - target: done
            effects:
              - invoke: host.agent.decide
                with:
                  prompt_path: prompts/nested.md
                  schema: schemas/nested.json
  done:
    view: done
`)

	def, err := Load(filepath.Join(parentDir, "app.yaml"))
	if err != nil {
		t.Fatalf("Load imported app: %v", err)
	}
	start := def.States["sub"].States["start"]
	nested := start.OnEnter[0].OnComplete[0].Effects[0].With

	if got, want := nested["prompt_path"].(string), filepath.Join(childDir, "prompts", "nested.md"); got != want {
		t.Fatalf("nested prompt_path = %q, want child story path %q", got, want)
	}
	if got, want := nested["schema"].(string), filepath.Join(childDir, "schemas", "nested.json"); got != want {
		t.Fatalf("nested schema = %q, want child story path %q", got, want)
	}
}

// TestImports_RebasesWorkbenchAssetPaths proves imported workbench prompt and
// acceptance_schema paths are rooted at the child story before workbench:
// desugars them into a host.agent.task call. Without this, project-local
// dev-story instances validate prompts/landing.md against the thin parent app
// directory instead of the imported dev-story directory.
func TestImports_RebasesWorkbenchAssetPaths(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	childDir := filepath.Join(root, "child")
	mustMkdirAll(t, parentDir)
	mustMkdirAll(t, filepath.Join(childDir, "prompts"))
	mustMkdirAll(t, filepath.Join(childDir, "schemas"))
	mustWriteFile(t, filepath.Join(childDir, "prompts", "bench.md"), "child bench prompt")
	mustWriteFile(t, filepath.Join(childDir, "schemas", "out.json"), `{"type":"object"}`)

	mustWriteFile(t, filepath.Join(parentDir, "app.yaml"), `
app: {id: parent, title: parent}
root: sub
hosts: [host.agent.task]
imports:
  sub:
    source: ../child
    entry: bench
    hosts: declared
`)
	mustWriteFile(t, filepath.Join(childDir, "app.yaml"), `
app: {id: child, title: child}
root: bench
hosts: [host.agent.task]
world:
  prior_summary: {type: string, default: ""}
toolboxes:
  builder_toolbox:
    tools: [Read, Grep, Glob, Edit, Write, Bash]
    effect: write
states:
  bench:
    workbench:
      agent: builder
      prompt: prompts/bench.md
      acceptance_schema: schemas/out.json
      capture_slot: custom_request
      context_args:
        prior_summary: "{{ world.prior_summary }}"
  defaultbench:
    workbench:
      agent: builder
      prompt: prompts/bench.md
      acceptance_schema: schemas/out.json
agents:
  builder:
    system_prompt: build things
    toolbox: builder_toolbox
`)

	def, err := Load(filepath.Join(parentDir, "app.yaml"))
	if err != nil {
		t.Fatalf("Load imported workbench app: %v", err)
	}
	bench := def.States["sub"].States["bench"]
	if bench == nil {
		t.Fatalf("imported workbench state not found")
	}
	var task *Effect
	for i := range bench.OnEnter {
		if bench.OnEnter[i].Invoke == "host.agent.task" {
			task = &bench.OnEnter[i]
			break
		}
	}
	if task == nil {
		t.Fatalf("imported workbench did not synthesize host.agent.task")
	}
	if got, want := task.When, "world.sub__custom_request != ''"; got != want {
		t.Fatalf("workbench guard = %q, want %q", got, want)
	}
	if got, want := task.Bind["sub__bench_note"], "submitted"; got != want {
		t.Fatalf("workbench bind = %q, want %q", got, want)
	}
	if _, ok := def.World["sub__custom_request"]; !ok {
		t.Fatalf("workbench did not synthesize prefixed custom request world key")
	}
	if _, ok := def.World["sub__bench_note"]; !ok {
		t.Fatalf("workbench did not synthesize prefixed note world key")
	}
	if got, want := bench.DefaultIntent, "sub__bench_capture"; got != want {
		t.Fatalf("workbench default_intent = %q, want %q", got, want)
	}
	ctx := task.With["context"].(map[string]any)
	if got, want := ctx["prompt"].(string), filepath.Join(childDir, "prompts", "bench.md"); got != want {
		t.Fatalf("workbench prompt = %q, want child story path %q", got, want)
	}
	args := ctx["args"].(map[string]any)
	if got, want := args["request"].(string), "{{ world.sub__custom_request }}"; got != want {
		t.Fatalf("workbench request arg = %q, want %q", got, want)
	}
	if got, want := args["prior_summary"].(string), "{{ world.sub__prior_summary }}"; got != want {
		t.Fatalf("workbench context arg = %q, want %q", got, want)
	}
	acc := task.With["acceptance"].(map[string]any)
	if got, want := acc["schema"].(string), filepath.Join(childDir, "schemas", "out.json"); got != want {
		t.Fatalf("workbench acceptance schema = %q, want child story path %q", got, want)
	}

	defaultBench := def.States["sub"].States["defaultbench"]
	if defaultBench == nil {
		t.Fatalf("imported default-slot workbench state not found")
	}
	var defaultTask *Effect
	for i := range defaultBench.OnEnter {
		if defaultBench.OnEnter[i].Invoke == "host.agent.task" {
			defaultTask = &defaultBench.OnEnter[i]
			break
		}
	}
	if defaultTask == nil {
		t.Fatalf("imported default-slot workbench did not synthesize host.agent.task")
	}
	if got, want := defaultTask.When, "world.sub__defaultbench_request != ''"; got != want {
		t.Fatalf("default-slot workbench guard = %q, want %q", got, want)
	}
	if got, want := defaultTask.Bind["sub__defaultbench_note"], "submitted"; got != want {
		t.Fatalf("default-slot workbench bind = %q, want %q", got, want)
	}
}

// TestImports_ProjectKitsokiDevWorkbenchUsesPrefixedCaptureSlot guards the
// exact dogfood startup bug: imported dev-story's workbench capture_slot must be
// rewritten before workbench: desugaring, or the generated boot-time guard reads
// world.landing_request instead of world.core__landing_request and dispatches
// the landing agent on a cold boot.
func TestImports_ProjectKitsokiDevWorkbenchUsesPrefixedCaptureSlot(t *testing.T) {
	def, err := Load(filepath.Join("..", "..", ".kitsoki", "stories", "kitsoki-dev", "app.yaml"))
	if err != nil {
		t.Fatalf("Load kitsoki-dev app: %v", err)
	}
	landing := def.States["core"].States["landing"]
	if landing == nil {
		t.Fatalf("core.landing not found")
	}
	var task *Effect
	for i := range landing.OnEnter {
		if landing.OnEnter[i].Invoke == "host.agent.task" {
			task = &landing.OnEnter[i]
			break
		}
	}
	if task == nil {
		t.Fatalf("core.landing did not synthesize host.agent.task")
	}
	if got, want := task.When, "world.core__landing_request != ''"; got != want {
		t.Fatalf("core.landing workbench guard = %q, want %q", got, want)
	}
	if got, want := task.Bind["core__landing_note"], "submitted"; got != want {
		t.Fatalf("core.landing workbench bind = %q, want %q", got, want)
	}
	if got, want := landing.DefaultIntent, "core__landing_capture"; got != want {
		t.Fatalf("core.landing default_intent = %q, want %q", got, want)
	}
	if _, ok := landing.On["landing_capture"]; ok {
		t.Fatalf("core.landing unexpectedly kept unprefixed landing_capture arc")
	}
	ctx := task.With["context"].(map[string]any)
	args := ctx["args"].(map[string]any)
	if got, want := args["request"].(string), "{{ world.core__landing_request }}"; got != want {
		t.Fatalf("core.landing request arg = %q, want %q", got, want)
	}
	if got, want := args["prior_summary"].(string), "{{ world.core__landing_prior_summary }}"; got != want {
		t.Fatalf("core.landing prior_summary arg = %q, want %q", got, want)
	}
}

// TestImports_RebasesStarlarkScriptPath proves an imported child's
// host.starlark.run script is resolved from the child story directory. This is
// the Starlark counterpart to prompt/schema rebasing: generated project-owned
// dev-story instances import @kitsoki/dev-story, whose rooms reference
// scripts/*.star. Those scripts live with the imported story, not the thin
// project instance.
func TestImports_RebasesStarlarkScriptPath(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	childDir := filepath.Join(root, "child")
	mustMkdirAll(t, parentDir)
	mustMkdirAll(t, filepath.Join(childDir, "scripts"))

	mustWriteFile(t, filepath.Join(parentDir, "app.yaml"), `
app: {id: parent, title: parent}
root: sub
hosts: [host.starlark.run]
imports:
  sub:
    source: ../child
    entry: start
    hosts: declared
`)
	mustWriteFile(t, filepath.Join(childDir, "app.yaml"), `
app: {id: child, title: child}
root: start
hosts: [host.starlark.run]
states:
  start:
    view: child
    on_enter:
      - invoke: host.starlark.run
        with:
          script: scripts/derive.star
        bind:
          result: result
world:
  result: {type: string, default: ""}
`)
	mustWriteFile(t, filepath.Join(childDir, "scripts", "derive.star"), `
def main(ctx):
    return {"result": "ok"}
`)
	mustWriteFile(t, filepath.Join(childDir, "scripts", "derive.star.yaml"), `
inputs: {}
outputs:
  result: {type: string}
`)

	def, err := Load(filepath.Join(parentDir, "app.yaml"))
	if err != nil {
		t.Fatalf("Load imported Starlark app: %v", err)
	}
	start := def.States["sub"].States["start"]
	got := start.OnEnter[0].With["script"].(string)
	want := filepath.Join(childDir, "scripts", "derive.star")
	if got != want {
		t.Fatalf("starlark script = %q, want child story path %q", got, want)
	}
}

// TestImports_TransitiveRebaseNoDoublePrefix is the regression for the live
// dogfood bug (#32): a grandchild's prompt path was double-prefixed across two
// import levels — `gp/stories/parent/stories/child/prompts/...` instead of
// `child/prompts/...` — but ONLY when the top app was loaded via a RELATIVE
// path (so the first rebase produced a relative path the second pass re-rooted).
// The deterministic flows hid it because they stub the agent call (prompt never
// read from disk); it surfaced only on a live host.agent.decide. Loading via a
// relative path here reproduces the original trigger.
func TestImports_TransitiveRebaseNoDoublePrefix(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "gp"))
	mustMkdirAll(t, filepath.Join(root, "parent"))
	mustMkdirAll(t, filepath.Join(root, "child", "prompts"))
	mustWriteFile(t, filepath.Join(root, "child", "prompts", "deep.md"), "child prompt")

	mustWriteFile(t, filepath.Join(root, "gp", "app.yaml"), `
app: {id: gp, title: gp}
root: mid
hosts: [host.agent.decide]
imports:
  mid:
    source: ../parent
    entry: main
states:
  shell: {view: gp}
`)
	mustWriteFile(t, filepath.Join(root, "parent", "app.yaml"), `
app: {id: parent, title: parent}
root: leaf
hosts: [host.agent.decide]
imports:
  leaf:
    source: ../child
    entry: start
states:
  main: {view: parent}
`)
	mustWriteFile(t, filepath.Join(root, "child", "app.yaml"), `
app: {id: child, title: child}
root: start
hosts: [host.agent.decide]
states:
  start:
    view: child
    on_enter:
      - invoke: host.agent.decide
        with:
          prompt_path: prompts/deep.md
`)

	// Load via a RELATIVE path (the bug trigger): chdir into root, load "gp/app.yaml".
	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	def, err := Load(filepath.Join("gp", "app.yaml"))
	if err != nil {
		t.Fatalf("Load transitive app: %v", err)
	}

	// gp.mid -> parent.leaf -> child.start
	start := def.States["mid"].States["leaf"].States["start"]
	got := start.OnEnter[0].With["prompt_path"].(string)
	want := filepath.Join(root, "child", "prompts", "deep.md")
	// Normalize macOS /var -> /private/var symlink differences (filepath.Abs
	// resolves the symlink; t.TempDir may not).
	if g, err := filepath.EvalSymlinks(got); err == nil {
		got = g
	}
	if w, err := filepath.EvalSymlinks(want); err == nil {
		want = w
	}
	if got != want {
		t.Fatalf("transitive prompt_path = %q, want %q (double-prefix regression)", got, want)
	}
}

// TestImports_KitsokiRepoOverrideRelativeNoDoublePrefix is the precise
// regression for the live dogfood trigger behind issue #32: `app.Load`'s own
// top-level baseDir is ALWAYS absolutized (see LoadWithResolver), so a plain
// relative `source: ../x` import chain (TestImports_TransitiveRebaseNoDoublePrefix
// above) can never actually observe a non-absolute childDir — that test passes
// even with rebaseEffectPaths's absolutize step removed. The REAL trigger is
// the `@kitsoki/<name>` override-resolver branch: cmd/kitsoki/resolver.go's
// buildImportResolver joins `$KITSOKI_REPO` onto the story name WITHOUT
// absolutizing it first (`filepath.Join(repo, "stories", name, "app.yaml")`),
// so a relative KITSOKI_REPO/--kitsoki-repo value hands loadImportedChild a
// RELATIVE baseDir for the `@kitsoki/`-resolved child — and loadImportedChild
// (unlike the top-level LoadWithResolver) never re-absolutizes it. This is
// exactly the pets-dev → `@kitsoki/dev-story` → `../prd` shape from the bug
// report. The injected resolver here mirrors buildImportResolver's override
// branch verbatim (relative repo, no Abs call) to reproduce it deterministically.
func TestImports_KitsokiRepoOverrideRelativeNoDoublePrefix(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "gp"))
	mustMkdirAll(t, filepath.Join(root, "mid"))
	mustMkdirAll(t, filepath.Join(root, "child", "prompts"))
	mustWriteFile(t, filepath.Join(root, "child", "prompts", "deep.md"), "child prompt")

	// gp imports "mid" via `@kitsoki/mid` — resolved through the injected
	// override resolver, NOT plain relative `source:`. mid then imports
	// "leaf" via a plain relative `../child`, exactly mirroring dev-story's
	// own `../prd` / `../implementation` shape one level further in.
	mustWriteFile(t, filepath.Join(root, "gp", "app.yaml"), `
app: {id: gp, title: gp}
root: mid
hosts: [host.agent.decide]
imports:
  mid:
    source: "@kitsoki/mid"
    entry: main
states:
  shell: {view: gp}
`)
	mustWriteFile(t, filepath.Join(root, "mid", "app.yaml"), `
app: {id: mid, title: mid}
root: leaf
hosts: [host.agent.decide]
imports:
  leaf:
    source: ../child
    entry: start
states:
  main: {view: mid}
`)
	mustWriteFile(t, filepath.Join(root, "child", "app.yaml"), `
app: {id: child, title: child}
root: start
hosts: [host.agent.decide]
states:
  start:
    view: child
    on_enter:
      - invoke: host.agent.decide
        with:
          prompt_path: prompts/deep.md
`)

	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Mirrors buildImportResolver's override branch: `repo` is a RELATIVE
	// value (as $KITSOKI_REPO would be if set relative to the process CWD)
	// and the candidate is joined WITHOUT ever calling filepath.Abs.
	repo := "."
	resolver := func(name, _ string, override bool) (string, error) {
		if !override {
			return "", nil
		}
		candidate := filepath.Join(repo, name, "app.yaml")
		if _, statErr := os.Stat(candidate); statErr != nil {
			return "", statErr
		}
		return candidate, nil
	}

	def, err := LoadWithResolver(filepath.Join("gp", "app.yaml"), nil, resolver)
	if err != nil {
		t.Fatalf("Load transitive app: %v", err)
	}

	// gp.mid -> mid.leaf -> child.start
	start := def.States["mid"].States["leaf"].States["start"]
	got := start.OnEnter[0].With["prompt_path"].(string)
	want := filepath.Join(root, "child", "prompts", "deep.md")
	if g, evalErr := filepath.EvalSymlinks(got); evalErr == nil {
		got = g
	}
	if w, evalErr := filepath.EvalSymlinks(want); evalErr == nil {
		want = w
	}
	if got != want {
		t.Fatalf("transitive prompt_path = %q, want %q (double-prefix regression via relative $KITSOKI_REPO override)", got, want)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
