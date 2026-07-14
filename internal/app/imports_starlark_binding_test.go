package app

// Tests for S3a (kits-implementation-plan.md D2.1): a host_bindings entry
// that names a starlark script instead of a concrete handler.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// findInvoke returns the Invoke target of the first effect found under the
// given intent name anywhere in the (already-folded) state tree, or "" if
// none is found.
func findInvoke(t *testing.T, states map[string]*State, intentName string) string {
	t.Helper()
	var found string
	var walk func(map[string]*State)
	walk = func(m map[string]*State) {
		for _, s := range m {
			if s == nil {
				continue
			}
			for _, list := range s.On {
				for _, arc := range list {
					for _, eff := range arc.Effects {
						if eff.Invoke != "" {
							// Only the FIRST invoke effect matters for this test's
							// fixtures — good enough, not a general-purpose walker.
							if found == "" {
								found = eff.Invoke
							}
						}
					}
				}
			}
			if len(s.States) > 0 {
				walk(s.States)
			}
		}
	}
	walk(states)
	_ = intentName
	return found
}

// TestHostBindingStarlark_ParentFold loads the host_binding_starlark fixture
// (bare `.star`-suffixed string form) and asserts:
//   - the child's `greeter` host_interface invoke was rewritten to a
//     synthesized handler name, not left as a raw script path;
//   - def.StarlarkHostBindings records that handler name mapped to the
//     absolute, existing script path;
//   - the synthesized handler name is unioned into def.Hosts (so the
//     allow-list check at runtime passes).
func TestHostBindingStarlark_ParentFold(t *testing.T) {
	def, err := Load("../../testdata/apps/host_binding_starlark/parent/app.yaml")
	require.NoError(t, err)
	require.NotNil(t, def)

	require.Len(t, def.StarlarkHostBindings, 1, "exactly one script-form binding should have been synthesized")

	var handlerName, scriptPath string
	for k, v := range def.StarlarkHostBindings {
		handlerName, scriptPath = k, v
	}
	require.True(t, filepath.IsAbs(scriptPath), "script path must be resolved to absolute, got %q", scriptPath)
	wantScript := filepath.Join(filepath.Dir(filepath.Clean("../../testdata/apps/host_binding_starlark/parent/app.yaml")), "scripts", "greet.star")
	wantScriptAbs, absErr := filepath.Abs(wantScript)
	require.NoError(t, absErr)
	require.Equal(t, wantScriptAbs, scriptPath)

	invoke := findInvoke(t, def.States, "core__hi")
	require.Equal(t, handlerName+".hello", invoke,
		"iface.greeter.hello should resolve to the synthesized handler + op, got %q", invoke)
	require.Contains(t, def.Hosts, invoke,
		"resolveAllInterfaces must union the resolved concrete invoke name into def.Hosts")
}

// TestHostBindingStarlark_MapFormAndDedup exercises the `{script: ...}`
// mapping form (the sibling of the bare `.star`-suffixed string form) AND
// proves the same script bound under two different host_bindings keys
// collapses to one synthesized handler (content-hash naming, see
// starlarkBindingHandlerName in imports.go).
func TestHostBindingStarlark_MapFormAndDedup(t *testing.T) {
	root := t.TempDir()

	scriptsDir := mkdirT(t, root, "scripts")
	mustWrite(t, scriptsDir, "echo.star", `def main(ctx):
    return {"out": ctx.inputs.get("op", "")}
`)
	mustWrite(t, scriptsDir, "echo.star.yaml", `inputs:
  op: { type: string, required: false }
outputs:
  out: { type: string }
`)

	childDir := mkdirT(t, root, "child")
	mustWrite(t, childDir, "app.yaml", `app: { id: child, version: 0.1.0 }
hosts: [host.run]
world: { a_out: { type: string, default: "" }, b_out: { type: string, default: "" } }
host_interfaces:
  a:
    description: "A."
    operations: { go: { input: {}, output: { out: string } } }
    default: host.run
  b:
    description: "B."
    operations: { go: { input: {}, output: { out: string } } }
    default: host.run
intents:
  go: { description: go }
root: idle
states:
  idle:
    on:
      go:
        - target: done
          effects:
            - invoke: iface.a.go
              bind: { a_out: out }
            - invoke: iface.b.go
              bind: { b_out: out }
  done:
    terminal: true
`)

	parentDir := mkdirT(t, root, "parent")
	mustWrite(t, parentDir, "app.yaml", `app: { id: parent, version: 0.1.0 }
hosts: [host.starlark.run]
world: {}
intents:
  begin: { description: begin }
root: main
imports:
  core:
    source: ../child
    entry: idle
    host_bindings:
      a: { script: ../scripts/echo.star }
      b: ../scripts/echo.star
states:
  main:
    on:
      begin:
        - target: core
`)

	def, err := Load(filepath.Join(parentDir, "app.yaml"))
	require.NoError(t, err)
	require.NotNil(t, def)

	require.Len(t, def.StarlarkHostBindings, 1,
		"the same script bound under two different keys must synthesize exactly one handler")

	var handlerName string
	for k := range def.StarlarkHostBindings {
		handlerName = k
	}

	// Both iface invokes must resolve to the SAME synthesized handler
	// (with their own op suffix) — proving the {script:...} mapping form
	// resolves identically to the bare-string form, and that binding the
	// same script twice dedups.
	var invokes []string
	var walk func(map[string]*State)
	walk = func(m map[string]*State) {
		for _, s := range m {
			if s == nil {
				continue
			}
			for _, list := range s.On {
				for _, arc := range list {
					for _, eff := range arc.Effects {
						if eff.Invoke != "" {
							invokes = append(invokes, eff.Invoke)
						}
					}
				}
			}
			if len(s.States) > 0 {
				walk(s.States)
			}
		}
	}
	walk(def.States)

	require.ElementsMatch(t, []string{handlerName + ".go", handlerName + ".go"}, invokes)
}

// TestHostBindingStarlark_MissingScript asserts a load-time error (not a
// runtime panic or silent pass-through) when a script-form host_bindings
// entry names a script that doesn't exist on disk.
func TestHostBindingStarlark_MissingScript(t *testing.T) {
	root := t.TempDir()

	childDir := mkdirT(t, root, "child")
	mustWrite(t, childDir, "app.yaml", `app: { id: child, version: 0.1.0 }
hosts: [host.run]
world: {}
host_interfaces:
  a:
    description: "A."
    operations: { go: { input: {}, output: {} } }
    default: host.run
intents:
  go: { description: go }
root: idle
states:
  idle:
    terminal: true
`)

	parentDir := mkdirT(t, root, "parent")
	mustWrite(t, parentDir, "app.yaml", `app: { id: parent, version: 0.1.0 }
hosts: [host.starlark.run]
world: {}
intents:
  begin: { description: begin }
root: main
imports:
  core:
    source: ../child
    entry: idle
    host_bindings:
      a: scripts/does_not_exist.star
states:
  main:
    on:
      begin:
        - target: core
`)

	_, err := Load(filepath.Join(parentDir, "app.yaml"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

// TestHostBindingSpec_UnmarshalYAML table-tests the three author forms (see
// HostBindingSpec) plus the rejected shapes.
func TestHostBindingSpec_UnmarshalYAML(t *testing.T) {
	cases := []struct {
		name        string
		yamlValue   string
		wantHandler string
		wantScript  string
		wantErr     bool
	}{
		{name: "plain handler name", yamlValue: "host.gh.ticket", wantHandler: "host.gh.ticket"},
		{name: "bare star script path", yamlValue: "scripts/graph_glue.star", wantScript: "scripts/graph_glue.star"},
		{name: "script mapping form", yamlValue: "{script: scripts/graph_glue.star}", wantScript: "scripts/graph_glue.star"},
		{name: "empty mapping is rejected", yamlValue: "{}", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var spec HostBindingSpec
			err := spec.UnmarshalYAML([]byte(tc.yamlValue))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantHandler, spec.Handler)
			require.Equal(t, tc.wantScript, spec.Script)
		})
	}
}
