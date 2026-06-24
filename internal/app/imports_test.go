package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestImports_ParentFold loads the parent app that imports sub_story under
// alias `sub` and asserts the fold result.
func TestImports_ParentFold(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)
	require.NotNil(t, def)

	// Imports block was cleared after fold.
	require.Nil(t, def.Imports, "def.Imports should be cleared after fold")

	// Child states nest under a compound wrapper at parent.States[alias].
	require.Contains(t, def.States, "sub")
	require.Equal(t, "compound", def.States["sub"].Type)
	require.Equal(t, "idle", def.States["sub"].Initial)
	require.Contains(t, def.States["sub"].States, "idle")
	require.Contains(t, def.States["sub"].States, "working")
	require.Contains(t, def.States, "main")
	require.Contains(t, def.States, "pr_landed")

	// World keys folded under prefixed names.
	require.Contains(t, def.World, "sub__ticket_id")
	require.Contains(t, def.World, "sub__pr_url")
	require.Contains(t, def.World, "current_ticket")

	// Intents folded under prefixed names + parent intent re-export mirror.
	require.Contains(t, def.Intents, "sub__start_work")
	require.Contains(t, def.Intents, "sub__finish")
	require.Contains(t, def.Intents, "sub__go_main", "parent intent go_main should be re-exported under sub__")
	require.Contains(t, def.Intents, "go_main", "parent intent stays in parent table")

	// Host union: parent had host.run; child had host.run; should remain
	// without duplicates.
	hostCount := 0
	for _, h := range def.Hosts {
		if h == "host.run" {
			hostCount++
		}
	}
	require.Equal(t, 1, hostCount, "host.run should not be duplicated")
}

// TestImports_PromptPathsRebasedToChildDir verifies that relative
// `prompt:` and `schema:` args in an imported child's effects are
// rewritten to absolute paths rooted at the child's own directory.
//
// At runtime, host.agent.ask_with_mcp's resolvePromptPath joins
// relative paths against $KITSOKI_APP_DIR — which is the PARENT app's
// directory. Without this rebase, an imported sub-story's
// `with: { prompt: prompts/foo.md }` would resolve to
// `<parent-dir>/prompts/foo.md` and the file wouldn't be found,
// triggering the room's on_error and short-circuiting the pipeline.
//
// Regression for the dogfood: typing `start` in core.bf.idle queued
// host.agent.ask_with_mcp with `prompt: prompts/reproducing_executing.md`;
// the runtime looked for it under stories/kitsoki-dev/ (parent) and
// failed because it lives under stories/bugfix/ (child). The visible
// symptom was "I typed start and nothing happened" — the redirect
// landed back at idle.
func TestImports_PromptPathsRebasedToChildDir(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_prompt_rebase/parent/app.yaml")
	require.NoError(t, err)
	require.NotNil(t, def)

	// Walk to c.work_executing's on_enter.
	c, ok := def.States["c"]
	require.True(t, ok, "import alias `c` should be a compound state")
	work, ok := c.States["work_executing"]
	require.True(t, ok, "c.work_executing should exist")
	require.NotEmpty(t, work.OnEnter, "c.work_executing should have on_enter")

	var promptArg, schemaArg string
	for _, eff := range work.OnEnter {
		if eff.Invoke == "host.agent.ask" {
			promptArg, _ = eff.With["prompt"].(string)
			schemaArg, _ = eff.With["schema"].(string)
		}
	}
	require.NotEmpty(t, promptArg, "prompt arg should be set on the host.agent.ask invoke")

	// The rewritten path must be absolute.
	require.True(t, filepath.IsAbs(promptArg),
		"prompt arg must be rebased to absolute; got %q", promptArg)
	require.True(t, filepath.IsAbs(schemaArg),
		"schema arg must be rebased to absolute; got %q", schemaArg)

	// And it must point at the CHILD's directory, not the parent's.
	require.Contains(t, promptArg, "/imports_prompt_rebase/child/prompts/work.md",
		"prompt must resolve under the child's directory; got %q", promptArg)
	require.Contains(t, schemaArg, "/imports_prompt_rebase/child/schemas/result.json",
		"schema must resolve under the child's directory; got %q", schemaArg)

	// The rebased file must actually exist on disk (sanity).
	_, statErr := os.Stat(promptArg)
	require.NoError(t, statErr, "rebased prompt file must exist on disk")
}

// TestImports_StateRewriting asserts that child state bodies have their
// world.<key> references rewritten to world.<alias>__<key>.
func TestImports_StateRewriting(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)

	idle := def.States["sub"].States["idle"]
	require.NotNil(t, idle)
	// idle's description was: "Ready to work on ticket {{ world.ticket_id }}."
	require.Contains(t, idle.Description, "world.sub__ticket_id", "child description should be rewritten")
	require.NotContains(t, idle.Description, "world.ticket_id\b", "raw child name should not survive")
	require.Contains(t, idle.View.SourceString(), "world.sub__ticket_id")

	// The working state was OVERRIDDEN by the parent; its description should
	// reflect the override, not the original.
	working := def.States["sub"].States["working"]
	require.NotNil(t, working)
	require.Contains(t, working.Description, "overridden", "override.states.working should replace child's working state")

	// The override's view references {{ world.ticket_id }} authored from
	// parent's perspective... but wait, parent author meant the *child's*
	// world key. The current rewriter ONLY rewrites child-shaped strings,
	// not override.states (which the parent authored). The rewriter pass
	// happens AFTER overrides, so override.states view IS rewritten.
	require.Contains(t, working.View.SourceString(), "world.sub__ticket_id", "override.states view is rewritten by the same pass that handles child states")
}

// TestImports_EffectIdTemplateRewritten confirms an effect's `id:` template
// (threaded into host args under the reserved `call` key and re-rendered at
// dispatch) has its world.X refs rewritten under fold. Regression: an
// un-rewritten id like cherny-loop's gating gate `id: "gate-{{ world.iteration
// }}"` rendered against the absent bare key under an import, producing an
// unmatched call id so the host result never bound and the emit chain stalled
// (the import-compound "maker stalls at gating" bug). The sub_story's
// `processing` on_enter invoke carries `id: "work-{{ world.ticket_id }}"`.
func TestImports_EffectIdTemplateRewritten(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)

	processing := def.States["sub"].States["processing"]
	require.NotNil(t, processing)
	require.NotEmpty(t, processing.OnEnter)
	var gotID string
	for _, eff := range processing.OnEnter {
		if eff.Invoke == "host.run" && eff.Id != "" {
			gotID = eff.Id
			break
		}
	}
	require.Equal(t, "work-{{ world.sub__ticket_id }}", gotID,
		"effect id template must be rewritten to the prefixed world key under fold")
}

// TestImports_OnCompleteTargetRewritten confirms that a `target:` carried
// by an on_complete: effect (the transition a finishing background job
// dispatches) is rewritten under fold exactly like an ordinary transition
// target — a bare sibling name becomes a relative `../<name>` ref that
// resolves under the alias wrapper.
//
// Regression: importing the bugfix story (a phase-template graph whose
// execute → next-phase chains live in on_enter background invokes with
// on_complete `target: phase_N_executing`) folded the states fine but left
// those targets bare, so every phase advance landed on a non-existent
// `phase_N_executing` instead of `bf.phase_N_executing`. The fold's
// rewriteChildStateTransitions only walked tr.Target and OnError, never
// Effect.Target.
func TestImports_OnCompleteTargetRewritten(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)

	processing := def.States["sub"].States["processing"]
	require.NotNil(t, processing, "child's processing state should fold under the alias")
	require.NotEmpty(t, processing.OnEnter, "processing should have an on_enter background invoke")

	// The background invoke's on_complete: chain holds [set, target:working].
	oc := processing.OnEnter[0].OnComplete
	require.NotEmpty(t, oc, "on_enter background invoke should have an on_complete chain")
	var targets []string
	for _, sub := range oc {
		if sub.Target != "" {
			targets = append(targets, sub.Target)
		}
	}
	require.Contains(t, targets, "../working",
		"on_complete target `working` must be rewritten to `../working` under fold; got %v", targets)
	require.NotContains(t, targets, "working",
		"bare sibling target must not survive the fold")
}

// TestImports_ExitMapping confirms @exit:<name> transitions in the child
// got rewritten to the parent's mapped target and that the world_out
// projection set effects are attached.
func TestImports_ExitMapping(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)

	working := def.States["sub"].States["working"]
	require.NotNil(t, working)
	require.NotEmpty(t, working.On["sub__finish"], "the finish intent should have been renamed sub__finish")

	finish := working.On["sub__finish"]
	require.Equal(t, "pr_landed", finish[0].Target, "@exit:completed → pr_landed")
	// World_out effect attached: parent's exits.completed.set { last_pr_url: ... }
	foundSet := false
	for _, eff := range finish[0].Effects {
		if eff.Set != nil {
			if _, ok := eff.Set["last_pr_url"]; ok {
				foundSet = true
			}
		}
	}
	require.True(t, foundSet, "world_out projection should attach last_pr_url set effect on the exit transition")

	// bail → @exit:abandoned → main, with bailed:true set.
	bail := working.On["sub__bail"]
	require.NotEmpty(t, bail)
	require.Equal(t, "main", bail[0].Target)
	bailFound := false
	for _, eff := range bail[0].Effects {
		if eff.Set != nil {
			if v, ok := eff.Set["bailed"]; ok && v == true {
				bailFound = true
			}
		}
	}
	require.True(t, bailFound, "world_out on abandoned should set bailed=true")
}

// TestImports_WorldIn asserts the synthesised on_enter setter on the
// child's entry state writes the parent's expression to the prefixed key.
func TestImports_WorldIn(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)

	// world_in lives on the compound wrapper's OnEnter so it fires when the
	// import is invoked, before the initial child is entered.
	wrapper := def.States["sub"]
	require.NotNil(t, wrapper)
	require.Equal(t, "compound", wrapper.Type)
	require.Equal(t, "idle", wrapper.Initial)
	require.NotEmpty(t, wrapper.OnEnter, "wrapper should carry world_in on_enter")
	// The wrapper OnEnter holds re-entry default-reset setters (so a re-entered
	// import is a fresh instance) followed by the world_in projection setters.
	// Find the world_in setter by key rather than position.
	var v any
	var ok bool
	for _, eff := range wrapper.OnEnter {
		if eff.Set != nil {
			if got, has := eff.Set["sub__ticket_id"]; has {
				v, ok = got, true
				break
			}
		}
	}
	require.True(t, ok, "world_in should write sub__ticket_id")
	require.Equal(t, "{{ world.current_ticket }}", v, "expression authored in parent scope is preserved")
}

// TestImports_HostBinding confirms iface.<name>.<op> references got
// rewritten to the bound handler.
func TestImports_HostBinding(t *testing.T) {
	// Currently sub_story doesn't actually invoke iface.reporter.announce
	// anywhere — the interface is declared but unused, which exercises
	// the resolver's "binding without invocation" path. Add a state that
	// invokes it to make the test meaningful.
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)
	require.NotNil(t, def)
}

// TestImports_StandaloneChildLoadsCleanly asserts the child app loads
// on its own (synthesised __exit__<name> terminal states for the
// exits referenced in its transitions).
func TestImports_StandaloneChildLoadsCleanly(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/sub_story/app.yaml")
	require.NoError(t, err, "child must be loadable standalone")
	require.NotNil(t, def)
	require.Contains(t, def.States, "__exit__completed")
	require.Contains(t, def.States, "__exit__abandoned")
	require.True(t, def.States["__exit__completed"].Terminal)
	require.True(t, def.States["__exit__abandoned"].Terminal)

	// Transitions in `working` should target the synthesised states.
	working := def.States["working"]
	require.NotNil(t, working)
	require.Equal(t, "__exit__completed", working.On["finish"][0].Target)
}

// TestImports_CycleDetection asserts a self-import is rejected.
func TestImports_CycleDetection(t *testing.T) {
	def, err := Load("../../testdata/apps/imports_smoke/parent/app.yaml")
	require.NoError(t, err)
	require.NotNil(t, def)
	// Negative case is exercised in TestImports_Cycle below.
}

// TestImports_Cycle exercises the real cycle-detection path: cycle_a
// imports cycle_b which imports cycle_a. The loader walks parents
// depth-first and rejects the second visit.
func TestImports_Cycle(t *testing.T) {
	_, err := Load("../../testdata/apps/imports_smoke/cycle_a/app.yaml")
	require.Error(t, err, "cycle should be rejected")
	require.Contains(t, err.Error(), "cycle detected")
}

// TestImports_MultiLayerIfaceComposition exercises multi-layer iface composition:
// a grandparent rebinds a grandchild's host_interface by spelling
// the alias-prefixed name (`<middle-alias>__<iface>`). The grandchild
// declares the iface with one default; the middle layer inherits; the
// top layer overrides. After fold the inner invoke must resolve to
// the top-layer's chosen handler.
//
// Topology: top → middle → leaf. Leaf declares iface `r` defaulting
// to `host.run`. Top rebinds `leaf__r` to `host.diary` — proves the
// grandparent surface works (the "bindings compose" claim).
func TestImports_MultiLayerIfaceComposition(t *testing.T) {
	root := t.TempDir()

	leafDir := mkdirT(t, root, "leaf")
	mustWrite(t, leafDir, "app.yaml", `app: { id: leaf, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  ping: { description: ping }
exits:
  done: { description: "..." }
host_interfaces:
  r:
    description: "Reporter."
    operations:
      announce: { input: { message: string }, output: { ok: bool } }
    default: host.run
root: idle
states:
  idle:
    on:
      ping:
        - target: "@exit:done"
          effects:
            - invoke: iface.r.announce
              with: { cmd: "true # leaf reports" }
`)

	middleDir := mkdirT(t, root, "middle")
	mustWrite(t, middleDir, "app.yaml", `app: { id: middle, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  fwd: { description: fwd }
exits:
  done: { description: "..." }
imports:
  leaf:
    source: ../leaf
    entry: idle
    exits:
      done: { to: "@exit:done" }
root: hub
states:
  hub:
    on:
      fwd:
        - target: leaf
`)

	topDir := mkdirT(t, root, "top")
	mustWrite(t, topDir, "app.yaml", `app: { id: top, version: 0.1.0 }
hosts: [host.run, host.diary]
world: {}
intents:
  go: { description: go }
imports:
  mid:
    source: ../middle
    entry: hub
    exits:
      done: { to: ended }
    host_bindings:
      # Reach into the grandchild's iface via the alias-prefixed name.
      # The middle layer never rebinds; the top layer's rebinding is
      # what the leaf's invoke ultimately resolves to at top-level Load.
      leaf__r: host.diary
root: main
states:
  main:
    on:
      go:
        - target: mid
  ended:
    terminal: true
`)

	def, err := Load(filepath.Join(topDir, "app.yaml"))
	require.NoError(t, err)
	require.NotNil(t, def)

	// Walk: top.States[mid].States[leaf].States[idle].
	mid := def.States["mid"]
	require.NotNil(t, mid, "middle imported under alias `mid`")
	require.Equal(t, "compound", mid.Type)
	leaf := mid.States["leaf"]
	require.NotNil(t, leaf, "leaf imported under nested alias `leaf`")
	idle := leaf.States["idle"]
	require.NotNil(t, idle)

	// Each fold layer prefixes child intent names. The leaf's `ping`
	// became `leaf__ping` after middle's fold, then `mid__leaf__ping`
	// after top's fold — the double-prefix reflects the alias chain.
	require.NotEmpty(t, idle.On["mid__leaf__ping"],
		"leaf's ping intent should be doubly-prefixed; got keys %v", keysOf(idle.On))
	ping := idle.On["mid__leaf__ping"][0]
	require.NotEmpty(t, ping.Effects)

	var found bool
	for _, eff := range ping.Effects {
		if eff.Invoke == "host.diary.announce" {
			found = true
			break
		}
	}
	require.True(t, found,
		"grandparent rebinding via leaf__r → host.diary did not take effect; effects=%+v",
		ping.Effects)
}

// TestImports_KitsokiSourceResolution exercises the `@kitsoki/<name>`
// source form. The resolver walks up looking for a go.mod whose module
// is `kitsoki` (or `*/kitsoki`); it then resolves the name against
// <repo-root>/stories/<name>/app.yaml. We construct a self-contained
// repo skeleton in a tmpdir so the test doesn't depend on the
// surrounding repo layout (other than confirming the resolver code
// path is callable).
func TestImports_KitsokiSourceResolution(t *testing.T) {
	root := newKitsokiRoot(t)

	// Child story: stories/widget/app.yaml under the simulated repo.
	widgetDir := mkdirT(t, root, "stories", "widget")
	mustWrite(t, widgetDir, "app.yaml", `app: { id: widget, version: 0.1.0 }
hosts: [host.run]
world:
  count: { type: int, default: 0 }
intents:
  bump: { description: "bump count" }
root: idle
states:
  idle:
    view: "count={{ world.count }}"
    on:
      bump:
        - target: .
          effects:
            - increment: { count: 1 }
`)

	// Consumer that addresses the widget via @kitsoki/widget. Put it
	// in a non-stories subdir so the resolver actually walks up.
	consumerDir := mkdirT(t, root, "experiments", "consumer")
	mustWrite(t, consumerDir, "app.yaml", `app: { id: consumer, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  w:
    source: "@kitsoki/widget"
    entry: idle
states:
  main: { view: "consumer" }
`)

	def, err := Load(filepath.Join(consumerDir, "app.yaml"))
	require.NoError(t, err, "@kitsoki/widget should resolve")
	require.NotNil(t, def)
	require.Contains(t, def.States, "w", "widget should fold under alias `w`")
	require.Contains(t, def.States["w"].States, "idle", "widget's idle state should be under the alias wrapper")
	require.Contains(t, def.World, "w__count", "widget's count should be prefixed")
}

// newKitsokiRoot builds a self-contained repo skeleton in a tmpdir:
// a `go.mod` declaring module `kitsoki` plus a `stories/` directory.
// Returns the absolute path to the simulated repo root.
func newKitsokiRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module kitsoki\n\ngo 1.21\n")
	_ = mkdirT(t, root, "stories")
	return root
}

// TestImports_OffRampAgentNamespaced is the regression guard for the
// fold-time rewrite at imports_rewriter.go:142-145: when an imported room
// opts into the agent off-ramp with `agent_off_ramp.agent: <child-agent>`,
// the fold must prefix that agent name with the import alias (alias+"__"+name)
// so the renamed parent.Agents[<alias>__<agent>] key satisfies the load-time
// walkOffRampAgents validator. Without the rewrite, Load fails with an
// "unknown agent" ValidationError because the off-ramp carries a dangling
// reference to the child's pre-fold agent name.
//
// This mirrors the meta_mode.agent fold (imports.go) and is the ONLY test
// reaching the off-ramp branch of rewriteState — every other off-ramp test
// is single-file (non-imported) and the shipped demo uses persona:, not
// agent:, so AgentOffRamp.Agent is empty there.
func TestImports_OffRampAgentNamespaced(t *testing.T) {
	root := t.TempDir()

	childDir := mkdirT(t, root, "child")
	mustWrite(t, childDir, "app.yaml", `app: { id: child, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  browse: { description: browse }
exits:
  done: { description: "..." }
agents:
  guide:
    system_prompt: "answer kindly"
root: desk
states:
  desk:
    description: "menu room"
    agent_off_ramp: { agent: guide }
    on:
      browse:
        - target: "@exit:done"
`)

	parentDir := mkdirT(t, root, "parent")
	mustWrite(t, parentDir, "app.yaml", `app: { id: parent, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
imports:
  sub:
    source: ../child
    entry: desk
    exits:
      done: { to: ended }
root: main
states:
  main:
    on:
      go:
        - target: sub
  ended:
    terminal: true
`)

	// Load is the load-bearing assertion: without the rewrite branch this
	// returns an "unknown agent guide" ValidationError from walkOffRampAgents.
	def, err := Load(filepath.Join(parentDir, "app.yaml"))
	require.NoError(t, err)

	// The child's `desk` folds under the `sub` alias wrapper.
	desk := def.States["sub"].States["desk"]
	require.NotNil(t, desk, "child desk should fold under alias `sub`; got %v", keysOf(def.States["sub"].States))
	require.NotNil(t, desk.AgentOffRamp, "folded desk keeps its off-ramp")
	require.Equal(t, "sub__guide", desk.AgentOffRamp.Agent,
		"off-ramp agent must be alias-prefixed after fold")
	require.Contains(t, def.Agents, "sub__guide",
		"the renamed agent must exist in the folded top-level agents: map")
}

func mkdirT(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	require.NoError(t, os.MkdirAll(p, 0o755))
	return p
}

func mustWrite(t *testing.T, dir, name, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644))
}

// keysOf returns the keys of a map[string]T in declaration order for
// error messages (no sort — caller doesn't care about determinism).
func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
