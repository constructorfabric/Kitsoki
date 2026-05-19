package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAgents_InlinePrompt covers the happy path for an inline system prompt:
// the agent loads, tools normalise to host.x.y form, and the model passes
// through unchanged.
func TestAgents_InlinePrompt(t *testing.T) {
	def, err := Load("testdata/agents_inline.yaml")
	require.NoError(t, err)
	require.NotNil(t, def.Agents)
	require.Len(t, def.Agents, 1)

	a, ok := def.Agents["story-author"]
	require.True(t, ok)
	require.NotEmpty(t, a.SystemPrompt)
	require.Contains(t, a.SystemPrompt, "YAML editor for the inline app")
	require.Empty(t, a.SystemPromptPath, "system_prompt_path must be empty when inline form is used")
	require.Equal(t, "claude-opus-4-7", a.Model)
	require.Equal(t, []string{"host.Edit", "host.Write", "host.Read"}, a.Tools)
}

// TestAgents_FilePrompt covers the file-ref happy path: the loader reads the
// file relative to the app YAML directory, lands its contents in
// SystemPrompt, and clears SystemPromptPath.
func TestAgents_FilePrompt(t *testing.T) {
	def, err := Load("testdata/agents_file/app.yaml")
	require.NoError(t, err)
	a, ok := def.Agents["weather-bot"]
	require.True(t, ok)
	require.Contains(t, a.SystemPrompt, "You are a weather bot.")
	require.Empty(t, a.SystemPromptPath)
	require.Equal(t, []string{"host.weather.forecast"}, a.Tools)
}

// TestAgents_BothPromptsRejected asserts that setting both system_prompt and
// system_prompt_path fails the one-of rule.
func TestAgents_BothPromptsRejected(t *testing.T) {
	_, err := Load("testdata/agents_both_prompts.yaml")
	require.Error(t, err)
	require.True(t, containsSubstring(err, "mutually exclusive"), "got: %v", err)
	require.True(t, containsSubstring(err, "story-author"), "got: %v", err)
}

// TestAgents_NeitherPromptRejected asserts that omitting both inline prompt
// and file-path errors the same way.
func TestAgents_NeitherPromptRejected(t *testing.T) {
	_, err := Load("testdata/agents_neither_prompt.yaml")
	require.Error(t, err)
	require.True(t, containsSubstring(err, "is required"), "got: %v", err)
	require.True(t, containsSubstring(err, "silent"), "got: %v", err)
}

// TestAgents_MissingFile asserts that a referenced system_prompt_path file
// that does not exist fails load, with the full resolved path appearing in
// the error message so the user knows exactly where the loader looked.
func TestAgents_MissingFile(t *testing.T) {
	_, err := Load("testdata/agents_missing_file.yaml")
	require.Error(t, err)
	wantPath, absErr := filepath.Abs("testdata/agents_missing_file.yaml")
	require.NoError(t, absErr)
	wantDir := filepath.Dir(wantPath)
	wantResolved := filepath.Join(wantDir, "prompts/does_not_exist.md")
	require.True(t, containsSubstring(err, wantResolved),
		"error must mention the full resolved path %q; got: %v", wantResolved, err)
}

// TestAgents_CwdUnsetEnv asserts that a cwd referencing an undefined env var
// fails load and names the variable.
func TestAgents_CwdUnsetEnv(t *testing.T) {
	require.NoError(t, os.Unsetenv("KITSOKI_AGENTS_TEST_UNSET"))
	_, err := Load("testdata/agents_cwd_unset.yaml")
	require.Error(t, err)
	require.True(t, containsSubstring(err, "KITSOKI_AGENTS_TEST_UNSET"), "got: %v", err)
}

// TestAgents_IncludeMergeCollision asserts that two app YAML files declaring
// agents with the same name across an include boundary fail with a clear
// "already declared" error.
func TestAgents_IncludeMergeCollision(t *testing.T) {
	dir := t.TempDir()
	mainYAML := `app:
  id: agents-collide
  version: 0.1.0
include: [extras/*.yaml]
root: foyer
states:
  foyer:
    view: "Foyer."
agents:
  story-author:
    system_prompt: "main"
`
	extra := `agents:
  story-author:
    system_prompt: "extra"
`
	require.NoError(t, os.WriteFile(dir+"/main.yaml", []byte(mainYAML), 0644))
	require.NoError(t, os.MkdirAll(dir+"/extras", 0755))
	require.NoError(t, os.WriteFile(dir+"/extras/dup.yaml", []byte(extra), 0644))

	_, err := Load(dir + "/main.yaml")
	require.Error(t, err)
	require.True(t, containsSubstring(err, "story-author"), "got: %v", err)
	require.True(t, containsSubstring(err, "agent"), "error should reference agent; got: %v", err)
}

// TestAgents_AgentSpecs covers the AppDef.AgentSpecs() converter that hands
// resolved declarations to agents.BuildRegistry. Specs should reflect the
// post-load resolved state: SystemPrompt populated, Tools normalised.
func TestAgents_AgentSpecs(t *testing.T) {
	def, err := Load("testdata/agents_inline.yaml")
	require.NoError(t, err)
	specs := def.AgentSpecs()
	require.Len(t, specs, 1)
	require.Equal(t, "story-author", specs[0].Name)
	require.Contains(t, specs[0].SystemPrompt, "YAML editor for the inline app")
	require.Equal(t, "claude-opus-4-7", specs[0].Model)
	require.Equal(t, []string{"host.Edit", "host.Write", "host.Read"}, specs[0].Tools)
	require.Equal(t, "", specs[0].DefaultCwd)
}

// TestAgents_AgentSpecs_NilOnEmpty asserts that an app with no agents:
// block produces a nil spec slice, suitable for agents.BuildRegistry(nil).
func TestAgents_AgentSpecs_NilOnEmpty(t *testing.T) {
	def, err := Load("testdata/metamode_minimal.yaml")
	require.NoError(t, err)
	require.Nil(t, def.AgentSpecs())
}

// TestAgents_IncludeMerge covers the happy-path merge: agents declared in an
// included file appear in the merged AppDef and resolve their
// system_prompt_path relative to the included file's directory.
func TestAgents_IncludeMerge(t *testing.T) {
	dir := t.TempDir()
	mainYAML := `app:
  id: agents-include
  version: 0.1.0
include: [extras/*.yaml]
root: foyer
states:
  foyer:
    view: "Foyer."
agents:
  main-agent:
    system_prompt: "from main"
`
	require.NoError(t, os.WriteFile(dir+"/main.yaml", []byte(mainYAML), 0644))
	require.NoError(t, os.MkdirAll(dir+"/extras/prompts", 0755))
	extra := `agents:
  extra-agent:
    system_prompt_path: "./prompts/extra.md"
`
	require.NoError(t, os.WriteFile(dir+"/extras/agents.yaml", []byte(extra), 0644))
	require.NoError(t, os.WriteFile(dir+"/extras/prompts/extra.md", []byte("from include\n"), 0644))

	def, err := Load(dir + "/main.yaml")
	require.NoError(t, err)
	require.Contains(t, def.Agents, "main-agent")
	require.Contains(t, def.Agents, "extra-agent")
	require.Equal(t, "from main", strings.TrimSpace(def.Agents["main-agent"].SystemPrompt))
	require.Equal(t, "from include", strings.TrimSpace(def.Agents["extra-agent"].SystemPrompt))
}
