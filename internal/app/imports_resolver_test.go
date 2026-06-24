package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// widgetManifest is a minimal standalone child story used by the resolver
// tests. It folds cleanly under an alias.
const widgetManifest = `app: { id: widget, version: 0.1.0 }
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
`

// consumerManifest imports the widget via @kitsoki/widget.
const consumerManifest = `app: { id: consumer, version: 0.1.0 }
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
`

// TestResolveImportSource_EmbeddedFallback proves the injected resolver's
// embedded-library branch (override=false) resolves @kitsoki/<name> when there
// is NO on-disk kitsoki checkout to walk up to. This is the core slice-1
// behaviour: a foreign repo with only the binary loads a base story.
func TestResolveImportSource_EmbeddedFallback(t *testing.T) {
	// A "library" dir standing in for the embedded/materialized root.
	lib := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "widget"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lib, "widget", "app.yaml"), []byte(widgetManifest), 0o644))

	// Consumer lives in a tmpdir with NO go.mod / .kitsoki-root above it, so
	// findRepoRoot fails and the resolver fallback is the only path.
	consumerDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumerManifest), 0o644))

	var sawOverride, sawFallback bool
	resolver := func(name, _ string, override bool) (string, error) {
		if override {
			sawOverride = true
			return "", nil // no override configured
		}
		sawFallback = true
		return filepath.Join(lib, name, "app.yaml"), nil
	}

	def, err := LoadWithResolver(filepath.Join(consumerDir, "app.yaml"), nil, resolver)
	require.NoError(t, err, "@kitsoki/widget should resolve via the embedded fallback")
	require.NotNil(t, def)
	require.Contains(t, def.States, "w", "widget folds under alias w")
	require.Contains(t, def.World, "w__count")
	require.True(t, sawOverride, "resolver consulted for an override first")
	require.True(t, sawFallback, "resolver consulted for the embedded fallback after findRepoRoot failed")
}

// TestResolveImportSource_NilResolverErrors confirms backward compatibility:
// with no resolver and no on-disk root, @kitsoki/<name> errors as before.
func TestResolveImportSource_NilResolverErrors(t *testing.T) {
	consumerDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumerManifest), 0o644))

	_, err := Load(filepath.Join(consumerDir, "app.yaml"))
	require.Error(t, err, "no resolver + no on-disk root must error")
	require.Contains(t, err.Error(), "@kitsoki")
}

// TestResolveImportSource_OverrideWinsOverOnDiskRoot proves order: the
// --kitsoki-repo override (override=true returning a path) is used even when an
// on-disk kitsoki root EXISTS and could resolve the import.
func TestResolveImportSource_OverrideWinsOverOnDiskRoot(t *testing.T) {
	// On-disk kitsoki root containing widget with count default 0.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module kitsoki\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "stories", "widget"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stories", "widget", "app.yaml"), []byte(widgetManifest), 0o644))

	consumerDir := filepath.Join(root, "experiments", "consumer")
	require.NoError(t, os.MkdirAll(consumerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumerManifest), 0o644))

	// Override library: a DIFFERENT widget whose count default is 99, so we can
	// tell which copy was loaded.
	override := t.TempDir()
	overrideWidget := `app: { id: widget, version: 0.1.0 }
hosts: [host.run]
world:
  count: { type: int, default: 99 }
intents:
  bump: { description: "bump count" }
root: idle
states:
  idle: { view: "x" }
`
	require.NoError(t, os.MkdirAll(filepath.Join(override, "widget"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(override, "widget", "app.yaml"), []byte(overrideWidget), 0o644))

	resolver := func(name, _ string, isOverride bool) (string, error) {
		if isOverride {
			return filepath.Join(override, name, "app.yaml"), nil
		}
		t.Fatalf("embedded fallback must not be consulted when an override resolves")
		return "", nil
	}

	def, err := LoadWithResolver(filepath.Join(consumerDir, "app.yaml"), nil, resolver)
	require.NoError(t, err)
	require.EqualValues(t, 99, def.World["w__count"].Default, "override widget (default 99) must win over the on-disk root (default 0)")
}

// TestResolveImportSource_OverrideMissingStoryErrors proves an explicit
// override that returns an error is surfaced, never silently swallowed in
// favour of the embedded copy.
func TestResolveImportSource_OverrideMissingStoryErrors(t *testing.T) {
	consumerDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumerManifest), 0o644))

	resolver := func(name, _ string, override bool) (string, error) {
		if override {
			return "", fmt.Errorf("KITSOKI_REPO=/nope: story %q not found", name)
		}
		t.Fatalf("embedded fallback must not run when the override errored")
		return "", nil
	}

	_, err := LoadWithResolver(filepath.Join(consumerDir, "app.yaml"), nil, resolver)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
