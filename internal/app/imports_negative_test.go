package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestImports_NegativeCases is a table-driven test of every validation
// error the imports loader can surface (see docs/imports.md "Validation surface").
// Each case authors a minimal in-memory parent+child manifest pair,
// writes them to a temp directory, runs Load, and asserts the expected
// error fragment appears.
//
// Why one big test: every case shares the same scaffolding (write
// files, call Load), so factoring into subtests keeps the assertions
// dense and the fixtures inline. If new validation paths are added,
// drop a new case here next to its closest siblings.
func TestImports_NegativeCases(t *testing.T) {
	cases := []struct {
		name       string
		parent     string
		child      string
		extraFiles map[string]string
		wantError  string
	}{
		{
			name: "alias_collision_with_existing_state",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: foo
imports:
  foo:
    source: ./child
    entry: idle
states:
  foo: { view: "collides" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
root: idle
states:
  idle: { view: "child" }
`,
			wantError: "alias collides with existing parent state",
		},
		{
			name: "unmapped_exit",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    exits:
      # Missing "completed" mapping; child uses @exit:completed.
      abandoned: { to: main }
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  finish: { description: finish }
exits:
  completed: { description: "..." }
  abandoned: { description: "..." }
root: idle
states:
  idle:
    on:
      finish:
        - target: "@exit:completed"
`,
			wantError: "child uses @exit:completed but parent does not map it",
		},
		{
			name: "override_state_not_in_child",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    overrides:
      states:
        ghost: { view: "no such state" }
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
root: idle
states:
  idle: { view: "child" }
`,
			wantError: `child does not declare a top-level state named "ghost"`,
		},
		{
			name: "override_intent_not_in_child",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    overrides:
      intents:
        ghost_intent: { description: "no such" }
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
root: idle
states:
  idle: { view: "child" }
`,
			wantError: `child does not declare an intent named "ghost_intent"`,
		},
		{
			name: "intents_export_undefined_parent",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    intents:
      export: [does_not_exist]
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
root: idle
states:
  idle: { view: "child" }
`,
			wantError: `intents.export references undefined parent intent "does_not_exist"`,
		},
		{
			name: "intents_import_not_in_exports",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    intents:
      import: [unexposed]
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  unexposed: { description: "intentionally not in exports" }
# No exports.intents block at all.
root: idle
states:
  idle: { view: "child" }
`,
			wantError: "child does not declare it in exports.intents",
		},
		{
			name: "host_bindings_to_undeclared_iface",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    host_bindings:
      no_such_iface: host.run
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
root: idle
states:
  idle: { view: "child" }
`,
			wantError: "no matching host_interface",
		},
		{
			name: "hosts_declared_missing_child_host",
			parent: `app: { id: p, version: 0.1.0 }
# Intentionally missing host.run from parent's allow-list.
hosts: []
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    hosts: declared
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
root: idle
states:
  idle: { view: "child" }
`,
			wantError: "hosts: declared but parent does not list",
		},
		{
			name: "requires_not_set",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world:
  recovered_url: { type: string, default: "" }
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
    exits:
      done: { to: main }
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world:
  pr_url: { type: string, default: "" }
intents:
  finish: { description: finish }
exits:
  done: { requires: [pr_url] }
root: idle
states:
  idle:
    on:
      finish:
        # The transition does NOT set pr_url — should fail requires check.
        - target: "@exit:done"
`,
			wantError: "does not set required key(s)",
		},
		{
			name: "child_walks_above_namespace",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  sub:
    source: ./child
    entry: idle
states:
  main: { view: "p" }
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  escape: { description: "tries to reach the parent" }
root: idle
states:
  idle:
    on:
      escape:
        # Two dots from a top-level child state would land above the
        # wrapper — explicit attempt to reach into the parent.
        - target: "../../main"
`,
			wantError: "walks above the child's namespace",
		},
		{
			name: "parent_reaches_into_child_past_entry",
			parent: `app: { id: p, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  poke: { description: poke }
root: main
imports:
  sub:
    source: ./child
    entry: idle
states:
  main:
    on:
      # Reaching into the child past the entry — forbidden per §8/§16.7.
      poke:
        - target: sub.working
`,
			child: `app: { id: c, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  step: { description: step }
root: idle
states:
  idle:
    on:
      step:
        - target: working
  working: { view: "deep" }
`,
			wantError: "reaches into the imported child",
		},
		{
			name: "cycle_a_imports_b_imports_a",
			parent: `app: { id: cyc-a, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  b:
    source: ../b
    entry: main
states:
  main: { view: "a" }
`,
			child: `app: { id: cyc-b, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  a:
    source: ../a
    entry: main
states:
  main: { view: "b" }
`,
			extraFiles: map[string]string{},
			wantError:  "cycle detected",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			parentDir := filepath.Join(tmp, "parent")
			require.NoError(t, os.MkdirAll(parentDir, 0o755))

			// Cycle case: put the two manifests as sibling directories
			// `a` and `b` under tmp so the `source: ../X` references
			// resolve symmetrically.
			if tc.name == "cycle_a_imports_b_imports_a" {
				aDir := filepath.Join(tmp, "a")
				bDir := filepath.Join(tmp, "b")
				require.NoError(t, os.MkdirAll(aDir, 0o755))
				require.NoError(t, os.MkdirAll(bDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(aDir, "app.yaml"), []byte(tc.parent), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(bDir, "app.yaml"), []byte(tc.child), 0o644))

				_, err := Load(filepath.Join(aDir, "app.yaml"))
				require.Error(t, err, "cycle case must fail")
				require.True(t, strings.Contains(err.Error(), tc.wantError),
					"cycle case: want error containing %q; got %v", tc.wantError, err)
				return
			}

			require.NoError(t, os.WriteFile(filepath.Join(parentDir, "app.yaml"), []byte(tc.parent), 0o644))

			childDir := filepath.Join(tmp, "parent", "child")
			require.NoError(t, os.MkdirAll(childDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(childDir, "app.yaml"), []byte(tc.child), 0o644))

			for rel, body := range tc.extraFiles {
				p := filepath.Join(tmp, rel)
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
			}

			_, err := Load(filepath.Join(parentDir, "app.yaml"))
			require.Error(t, err, "case %q must fail to load", tc.name)
			require.True(t, strings.Contains(err.Error(), tc.wantError),
				"case %q: want error containing %q; got %v", tc.name, tc.wantError, err)
		})
	}
}
