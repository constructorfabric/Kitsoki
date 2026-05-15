package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMetaMode_Minimal loads an app with a single meta_modes entry and asserts
// that defaults resolve correctly via the helper methods. The builtin `bug`
// mode is also injected post-merge, so the assertion targets the story entry
// specifically rather than the map length.
func TestMetaMode_Minimal(t *testing.T) {
	def, err := Load("testdata/metamode_minimal.yaml")
	require.NoError(t, err)
	require.NotNil(t, def.MetaModes)

	story, ok := def.MetaModes["story"]
	require.True(t, ok, "story meta mode must be present")
	require.Equal(t, "meta", story.Trigger)
	require.Equal(t, "story-author", story.Agent)

	// Persist nil → PersistOrDefault() == true.
	require.Nil(t, story.Persist)
	require.True(t, story.PersistOrDefault())

	// Return nil → ExitIntentOrDefault() == "onpath".
	require.Nil(t, story.Return)
	require.Equal(t, "onpath", story.ExitIntentOrDefault())
}

// TestMetaMode_EmptyBlock verifies that an explicit `meta_modes: {}` parses
// into a non-nil map and that builtin injection still runs (the app gets
// the builtin `story.*` modes without declaring anything). Apps that want to
// suppress a builtin override it under the same `group.verb` key.
func TestMetaMode_EmptyBlock(t *testing.T) {
	def, err := Load("testdata/metamode_empty.yaml")
	require.NoError(t, err)
	require.NotNil(t, def.MetaModes, "explicit empty meta_modes: must yield a non-nil map")

	bug, ok := def.MetaModes["story.bug"]
	require.True(t, ok, "builtin `story.bug` mode must be injected for apps without their own declaration")
	require.Equal(t, "bug", bug.Trigger)
	require.Equal(t, "story", bug.Group)
	require.Equal(t, "story-bug-reporter", bug.Agent)
}

// TestMetaMode_DuplicateTrigger asserts that two meta modes claiming the same
// trigger fail validation with a message naming both modes.
func TestMetaMode_DuplicateTrigger(t *testing.T) {
	_, err := Load("testdata/metamode_duplicate_trigger.yaml")
	require.Error(t, err)
	require.True(t, containsSubstring(err, "rival"), "error must mention the second offender; got: %v", err)
	require.True(t, containsSubstring(err, "story"), "error must mention the first claimant; got: %v", err)
	require.True(t, containsSubstring(err, "meta"), "error must mention the duplicate trigger value; got: %v", err)
}

// TestMetaMode_MissingAgent asserts that omitting agent: fails validation.
func TestMetaMode_MissingAgent(t *testing.T) {
	_, err := Load("testdata/metamode_missing_agent.yaml")
	require.Error(t, err)
	require.True(t, containsSubstring(err, "agent is required"), "got: %v", err)
}

// TestMetaMode_MissingTrigger asserts that omitting trigger: fails validation.
func TestMetaMode_MissingTrigger(t *testing.T) {
	_, err := Load("testdata/metamode_missing_trigger.yaml")
	require.Error(t, err)
	require.True(t, containsSubstring(err, "trigger is required"), "got: %v", err)
}

// TestMetaMode_CwdEnvExpansion verifies that cwd: ${VAR} expands when the var
// is set, and errors when unset.
func TestMetaMode_CwdEnvExpansion(t *testing.T) {
	t.Run("env set", func(t *testing.T) {
		t.Setenv("KITSOKI_REPO", "/tmp/kitsoki-fake-repo")
		def, err := Load("testdata/metamode_cwd_envvar.yaml")
		require.NoError(t, err)
		m, ok := def.MetaModes["kitsoki.edit"]
		require.True(t, ok)
		require.Equal(t, "/tmp/kitsoki-fake-repo", m.Cwd, "cwd must be expanded in place")
	})

	t.Run("env unset", func(t *testing.T) {
		// Ensure the var is unset for this subtest.
		require.NoError(t, os.Unsetenv("KITSOKI_REPO"))
		_, err := Load("testdata/metamode_cwd_envvar.yaml")
		require.Error(t, err)
		require.True(t, containsSubstring(err, "KITSOKI_REPO"), "got: %v", err)
	})
}

// TestMetaMode_CwdEnvExpansion_KitsokiAppDir is the loader-side
// regression test for bug 2: a `cwd: "${KITSOKI_APP_DIR}"` field must
// validate successfully when the env var is set (production sets it
// in cmd/kitsoki via loadAppWithEnv BEFORE calling Load) and must
// produce a clean, named-var error when it is unset.
//
// The production fix is ordering — `os.Setenv(host.AppDirEnv, ...)`
// happens before `app.Load(appPath)` rather than after. This test
// pins both branches of expandMetaCwd's behaviour for KITSOKI_APP_DIR
// specifically, so a future refactor that inadvertently re-orders
// the setenv (or special-cases the var) trips it.
func TestMetaMode_CwdEnvExpansion_KitsokiAppDir(t *testing.T) {
	t.Run("env set", func(t *testing.T) {
		t.Setenv("KITSOKI_APP_DIR", "/tmp/kitsoki-fake-appdir")
		def, err := Load("testdata/metamode_cwd_appdir_envvar.yaml")
		require.NoError(t, err)
		self, ok := def.MetaModes["self"]
		require.True(t, ok)
		require.Equal(t, "/tmp/kitsoki-fake-appdir", self.Cwd,
			"KITSOKI_APP_DIR must be expanded in place at load time")
	})

	t.Run("env unset", func(t *testing.T) {
		require.NoError(t, os.Unsetenv("KITSOKI_APP_DIR"))
		_, err := Load("testdata/metamode_cwd_appdir_envvar.yaml")
		require.Error(t, err,
			"loader must reject ${KITSOKI_APP_DIR} when the env var is unset (production sets it before Load)")
		require.True(t, containsSubstring(err, "KITSOKI_APP_DIR"),
			"error must name the missing var; got: %v", err)
	})
}

// TestMetaMode_PersistFalse asserts that an explicit persist: false survives
// the load and overrides the default.
func TestMetaMode_PersistFalse(t *testing.T) {
	def, err := Load("testdata/metamode_persist_false.yaml")
	require.NoError(t, err)
	m, ok := def.MetaModes["ephemeral"]
	require.True(t, ok)
	require.NotNil(t, m.Persist)
	require.False(t, *m.Persist)
	require.False(t, m.PersistOrDefault())
}

// TestMetaMode_ReturnIntentOverride asserts that an explicit return.intent
// overrides the "onpath" default.
func TestMetaMode_ReturnIntentOverride(t *testing.T) {
	def, err := Load("testdata/metamode_return_intent.yaml")
	require.NoError(t, err)
	m, ok := def.MetaModes["story"]
	require.True(t, ok)
	require.NotNil(t, m.Return)
	require.Equal(t, "exit-meta", m.Return.Intent)
	require.Equal(t, "exit-meta", m.ExitIntentOrDefault())
	require.Equal(t, "back on the path.", m.Return.Message)
}

// TestMetaMode_PersistOrDefault_Helpers covers helper edge cases that aren't
// exercised through the loader fixtures.
func TestMetaMode_PersistOrDefault_Helpers(t *testing.T) {
	var nilMode *MetaModeDef
	require.True(t, nilMode.PersistOrDefault(), "nil receiver defaults to true")
	require.Equal(t, "onpath", nilMode.ExitIntentOrDefault())

	truthy := true
	m := &MetaModeDef{Persist: &truthy}
	require.True(t, m.PersistOrDefault())

	m.Return = &MetaReturnDef{}
	require.Equal(t, "onpath", m.ExitIntentOrDefault(), "empty intent falls back to onpath")
}

// TestMetaMode_IncludeMerge verifies that meta_modes from an included file are
// merged into the parent map, and a collision on key is a clear error.
func TestMetaMode_IncludeMerge(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		dir := t.TempDir()
		mainYAML := `app:
  id: meta-include
  version: 0.1.0
include: [extras/*.yaml]
root: foyer
states:
  foyer:
    view: "Foyer."
agents:
  bug-reporter:
    system_prompt: "fixture bug-reporter"
meta_modes:
  story:
    trigger: meta
    agent: story-author
`
		extra := `meta_modes:
  bug:
    trigger: report-bug
    agent: bug-reporter
`
		require.NoError(t, os.WriteFile(dir+"/main.yaml", []byte(mainYAML), 0644))
		require.NoError(t, os.MkdirAll(dir+"/extras", 0755))
		require.NoError(t, os.WriteFile(dir+"/extras/bug.yaml", []byte(extra), 0644))

		def, err := Load(dir + "/main.yaml")
		require.NoError(t, err)
		require.Contains(t, def.MetaModes, "story")
		require.Contains(t, def.MetaModes, "bug")
	})

	t.Run("collision", func(t *testing.T) {
		dir := t.TempDir()
		mainYAML := `app:
  id: meta-collide
  version: 0.1.0
include: [extras/*.yaml]
root: foyer
states:
  foyer:
    view: "Foyer."
meta_modes:
  story:
    trigger: meta
    agent: story-author
`
		extra := `meta_modes:
  story:
    trigger: meta2
    agent: other-agent
`
		require.NoError(t, os.WriteFile(dir+"/main.yaml", []byte(mainYAML), 0644))
		require.NoError(t, os.MkdirAll(dir+"/extras", 0755))
		require.NoError(t, os.WriteFile(dir+"/extras/dup.yaml", []byte(extra), 0644))

		_, err := Load(dir + "/main.yaml")
		require.Error(t, err)
		require.True(t, containsSubstring(err, "story"), "got: %v", err)
		require.True(t, containsSubstring(err, "meta_mode"), "error should reference meta_mode; got: %v", err)
	})
}
