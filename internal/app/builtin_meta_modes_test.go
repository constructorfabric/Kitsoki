package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInjectBuiltinMetaModes_FillsStoryBuiltinsWhenAbsent asserts the
// injection step adds the three story.* builtins when the app didn't
// declare any of them.
func TestInjectBuiltinMetaModes_FillsStoryBuiltinsWhenAbsent(t *testing.T) {
	def := &AppDef{}
	injectBuiltinMetaModes(def)

	storyEdit, ok := def.MetaModes["story.edit"]
	require.True(t, ok, "story.edit builtin must be injected")
	require.Equal(t, "story-author", storyEdit.Agent)
	require.Equal(t, "edit", storyEdit.Trigger)
	require.Equal(t, "story", storyEdit.Group)
	require.True(t, storyEdit.Default, "story.edit is the default verb for the story group")
	require.Equal(t, "onpath", storyEdit.ExitIntentOrDefault())

	storyAsk, ok := def.MetaModes["story.ask"]
	require.True(t, ok, "story.ask builtin must be injected")
	require.Equal(t, "story-explainer", storyAsk.Agent)
	require.Equal(t, "ask", storyAsk.Trigger)
	require.Equal(t, "story", storyAsk.Group)
	require.False(t, storyAsk.Default)
	require.Equal(t, []string{"Read", "Glob", "Grep"}, storyAsk.Tools)

	storyBug, ok := def.MetaModes["story.bug"]
	require.True(t, ok, "story.bug builtin must be injected")
	require.Equal(t, "story-bug-reporter", storyBug.Agent)
	require.Equal(t, "bug", storyBug.Trigger)
	require.Equal(t, "story", storyBug.Group)
	require.False(t, storyBug.Default)
}

// TestInjectBuiltinMetaModes_AppOverrideWins asserts that an app-declared
// mode with the same `group.verb` key as a builtin is preserved
// verbatim — the builtin does not silently replace it.
func TestInjectBuiltinMetaModes_AppOverrideWins(t *testing.T) {
	custom := &MetaModeDef{
		Group:   "story",
		Trigger: "bug",
		Agent:   "my-custom-agent",
	}
	def := &AppDef{
		MetaModes: map[string]*MetaModeDef{
			"story.bug": custom,
		},
	}
	injectBuiltinMetaModes(def)

	got := def.MetaModes["story.bug"]
	require.Same(t, custom, got, "app-declared `story.bug` must survive injection unchanged")
	require.Equal(t, "my-custom-agent", got.Agent)
}

// TestInjectBuiltinMetaModes_KitsokiGroupRequiresEnvVar asserts the
// `kitsoki.*` builtins are only injected when KITSOKI_REPO is set.
// Test runs both branches by toggling the env var around the call.
func TestInjectBuiltinMetaModes_KitsokiGroupRequiresEnvVar(t *testing.T) {
	// Save and restore the env var so the test doesn't leak.
	original, hadOriginal := os.LookupEnv("KITSOKI_REPO")
	t.Cleanup(func() {
		if hadOriginal {
			_ = os.Setenv("KITSOKI_REPO", original)
		} else {
			_ = os.Unsetenv("KITSOKI_REPO")
		}
	})

	// Branch 1: KITSOKI_REPO unset — kitsoki.* are omitted.
	_ = os.Unsetenv("KITSOKI_REPO")
	def := &AppDef{}
	injectBuiltinMetaModes(def)
	_, hasEdit := def.MetaModes["kitsoki.edit"]
	require.False(t, hasEdit, "kitsoki.edit must NOT be injected when KITSOKI_REPO is unset")
	_, hasAsk := def.MetaModes["kitsoki.ask"]
	require.False(t, hasAsk, "kitsoki.ask must NOT be injected when KITSOKI_REPO is unset")
	_, hasBug := def.MetaModes["kitsoki.bug"]
	require.False(t, hasBug, "kitsoki.bug must NOT be injected when KITSOKI_REPO is unset")

	// Branch 2: KITSOKI_REPO set — kitsoki.* are present with the expected cwd.
	_ = os.Setenv("KITSOKI_REPO", "/tmp/fake-repo")
	def = &AppDef{}
	injectBuiltinMetaModes(def)

	kEdit, ok := def.MetaModes["kitsoki.edit"]
	require.True(t, ok, "kitsoki.edit MUST be injected when KITSOKI_REPO is set")
	require.Equal(t, "kitsoki-engineer", kEdit.Agent)
	require.Equal(t, "kitsoki", kEdit.Group)
	require.Equal(t, "edit", kEdit.Trigger)
	require.True(t, kEdit.Default, "kitsoki.edit is the default verb for the kitsoki group")
	require.Equal(t, "${KITSOKI_REPO}", kEdit.Cwd, "cwd stays in unexpanded form; loader's validateMetaModes does the expansion")

	kAsk, ok := def.MetaModes["kitsoki.ask"]
	require.True(t, ok, "kitsoki.ask MUST be injected when KITSOKI_REPO is set")
	require.Equal(t, "kitsoki-explainer", kAsk.Agent)
	require.Equal(t, []string{"Read", "Glob", "Grep"}, kAsk.Tools)
	require.False(t, kAsk.Default)

	kBug, ok := def.MetaModes["kitsoki.bug"]
	require.True(t, ok, "kitsoki.bug MUST be injected when KITSOKI_REPO is set")
	require.Equal(t, "kitsoki-bug-reporter", kBug.Agent)
	require.Equal(t, "${KITSOKI_REPO}", kBug.Cwd)
}

// TestInjectBuiltinMetaModes_LegacyKeysAbsent asserts the proposal §7
// clean-break: the single-token `bug` and `self` keys are gone. Apps
// that referenced them directly will see "unknown mode" instead.
func TestInjectBuiltinMetaModes_LegacyKeysAbsent(t *testing.T) {
	t.Setenv("KITSOKI_REPO", "/tmp/fake-repo")
	def := &AppDef{}
	injectBuiltinMetaModes(def)
	_, hasBug := def.MetaModes["bug"]
	require.False(t, hasBug, "legacy `bug` key must NOT be injected — replaced by story.bug / kitsoki.bug")
	_, hasSelf := def.MetaModes["self"]
	require.False(t, hasSelf, "legacy `self` key must NOT be injected — replaced by kitsoki.edit")
}

// TestInjectBuiltinMetaModes_NilDef is a defensive no-crash check.
func TestInjectBuiltinMetaModes_NilDef(t *testing.T) {
	injectBuiltinMetaModes(nil) // must not panic
}
