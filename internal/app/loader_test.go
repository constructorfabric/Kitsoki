package app

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadCloak_Positive loads the Cloak of Darkness app and asserts
// structural invariants of the loaded app.
func TestLoadCloak_Positive(t *testing.T) {
	def, err := Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err, "Cloak app must load cleanly")
	require.NotNil(t, def)

	// App-level metadata.
	require.Equal(t, "cloak-of-darkness", def.App.ID)
	require.Equal(t, "0.1.0", def.App.Version)

	// Root state.
	root, ok := def.Root.(string)
	require.True(t, ok, "root must be a string")
	require.Equal(t, "foyer", root)

	// World schema — three app variables plus the built-in story-authoring room.
	require.Len(t, def.World, 6)
	require.Contains(t, def.World, "wearing_cloak")
	require.Contains(t, def.World, "disturbance")
	require.Contains(t, def.World, "message_rumpled")
	require.Contains(t, def.World, "story_authoring_request")
	require.Contains(t, def.World, "story_authoring_note")
	require.Contains(t, def.World, "story_authoring_return_state")

	// Intent library — six app intents plus the built-in story-authoring room.
	require.Len(t, def.Intents, 10, "expected 10 intents in library")
	goIntent, ok := def.Intents["go"]
	require.True(t, ok, "intent 'go' must exist")
	require.Equal(t, "Go", goIntent.Title)
	require.Equal(t, 100, goIntent.Priority)
	dir, ok := goIntent.Slots["direction"]
	require.True(t, ok, "go intent must have direction slot")
	require.Equal(t, "enum", dir.Type)
	require.True(t, dir.Required)
	require.Contains(t, dir.Values, "south")

	readMsg, ok := def.Intents["read_message"]
	require.True(t, ok, "intent 'read_message' must exist")
	require.Equal(t, "Read the message", readMsg.Title)
	require.Equal(t, 90, readMsg.Priority)

	dropCloak, ok := def.Intents["drop_cloak"]
	require.True(t, ok)
	require.True(t, dropCloak.Hidden, "drop_cloak must be hidden")

	// States — foyer, cloakroom, bar, ended at the top level.
	require.Contains(t, def.States, "foyer")
	require.Contains(t, def.States, "cloakroom")
	require.Contains(t, def.States, "bar")
	require.Contains(t, def.States, "ended")

	// bar is a compound state with two children.
	bar := def.States["bar"]
	require.Equal(t, "compound", bar.Type)
	require.Contains(t, bar.States, "dark")
	require.Contains(t, bar.States, "lit")

	// foyer.on has go and look.
	foyer := def.States["foyer"]
	require.Contains(t, foyer.On, "go")
	require.Contains(t, foyer.On, "look")

	// foyer.relevant_world contains wearing_cloak.
	require.Contains(t, foyer.RelevantWorld, "wearing_cloak")

	// ended is terminal.
	ended := def.States["ended"]
	require.True(t, ended.Terminal)

	// cloakroom.on contains hang_cloak with effects.
	cloakroom := def.States["cloakroom"]
	hangTransitions := cloakroom.On["hang_cloak"]
	require.GreaterOrEqual(t, len(hangTransitions), 1)

	// off_path block is present.
	require.NotNil(t, def.OffPath)
	require.Equal(t, "/freeform", def.OffPath.Trigger)
}

// TestLoadBytes_RoundTrip verifies LoadBytes returns the same structure as Load.
func TestLoadBytes_RoundTrip(t *testing.T) {
	b, err := os.ReadFile("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	def, err := LoadBytes(b)
	require.NoError(t, err)
	require.Equal(t, "cloak-of-darkness", def.App.ID)
}

// TestNegative is a table-driven test for each failure mode. Each bad fixture
// must produce a ValidationError containing the expected substring.
func TestNegative(t *testing.T) {
	cases := []struct {
		name        string
		fixture     string
		wantErrSnip string
	}{
		{
			name:        "unknown intent in on block",
			fixture:     "testdata/bad/unknown_intent.yaml",
			wantErrSnip: "nonexistent_intent",
		},
		{
			name:        "unknown transition target",
			fixture:     "testdata/bad/unknown_target.yaml",
			wantErrSnip: "nonexistent_room",
		},
		{
			name:        "root state missing",
			fixture:     "testdata/bad/missing_root.yaml",
			wantErrSnip: "does_not_exist",
		},
		{
			name:        "relevant_world key not in world schema",
			fixture:     "testdata/bad/bad_relevant_world.yaml",
			wantErrSnip: "stamina",
		},
		{
			name:        "compound initial child missing",
			fixture:     "testdata/bad/bad_compound_initial.yaml",
			wantErrSnip: "nonexistent_child",
		},
		{
			name:        "timeout target unknown",
			fixture:     "testdata/bad/bad_timeout_target.yaml",
			wantErrSnip: "nonexistent_state",
		},
		{
			name:        "timeout duration unparseable",
			fixture:     "testdata/bad/bad_timeout_duration.yaml",
			wantErrSnip: "forever",
		},
		{
			// Regression coverage for the "silent no-op" footgun: a story
			// invoking a host verb missing from its own hosts: allow-list
			// used to appear to succeed in traces while doing nothing. The
			// loader has always rejected this at load time (validateStates'
			// invoke: host.* check against allowedHosts, internal/app/loader.go)
			// — this test pins that behavior so it can never silently
			// regress. See docs/architecture/hosts.md "Allow-list enforcement".
			name:        "host invoke not in own allow-list",
			fixture:     "testdata/bad/host_not_in_allowlist.yaml",
			wantErrSnip: "not declared in app hosts",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.fixture)
			require.Error(t, err, "loading %s must fail", tc.fixture)
			require.True(t,
				containsSubstring(err, tc.wantErrSnip),
				"error message should mention %q; got: %v", tc.wantErrSnip, err,
			)
		})
	}
}

// TestLoad_Include verifies that the include: directive merges states from
// separate files.
func TestLoad_Include(t *testing.T) {
	def, err := Load("testdata/include_test/main.yaml")
	require.NoError(t, err, "include test app must load cleanly")
	require.NotNil(t, def)

	// Both states from the included files should be present.
	require.Contains(t, def.States, "room_a", "room_a must be merged from rooms/room_a.yaml")
	require.Contains(t, def.States, "room_b", "room_b must be merged from rooms/room_b.yaml")
	require.Equal(t, "include-test", def.App.ID)
}

// TestLoad_OffPathPersonaRoundTrip asserts that the optional persona: field
// under off_path: deserializes onto OffPathDef.Persona. Lets apps style the
// off-path agent voice (Zane Grey / Louis L'Amour cadence for Oregon Trail,
// sysadmin tone for a devops app, etc.) without modifying engine code.
func TestLoad_OffPathPersonaRoundTrip(t *testing.T) {
	yaml := []byte(`app:
  id: persona-test
  version: 0.1.0
root: start
states:
  start:
    view: "hello"
off_path:
  trigger: "/freeform"
  banner: "*** off-trail ***"
  return: "/onpath"
  persona: |
    You are a weathered frontier guide. Speak in short sentences.
`)
	def, err := LoadBytes(yaml)
	require.NoError(t, err)
	require.NotNil(t, def.OffPath)
	require.Equal(t, "/freeform", def.OffPath.Trigger)
	require.Contains(t, def.OffPath.Persona, "weathered frontier guide")
	require.Contains(t, def.OffPath.Persona, "short sentences")
}

// TestLoad_OffPathPersonaOptional asserts that an off_path block without a
// persona: field still loads (the field is optional and defaults to empty).
// Backward compat for apps like cloak that don't want a styled persona.
func TestLoad_OffPathPersonaOptional(t *testing.T) {
	yaml := []byte(`app:
  id: persona-optional
  version: 0.1.0
root: start
states:
  start:
    view: "hi"
off_path:
  trigger: "/freeform"
  return: "/onpath"
`)
	def, err := LoadBytes(yaml)
	require.NoError(t, err)
	require.NotNil(t, def.OffPath)
	require.Empty(t, def.OffPath.Persona, "persona must default to empty when omitted")
}

// TestLoad_AgentsRoundTrip asserts that the top-level `agents:` block
// deserializes into AppDef.Agents and that each entry's fields land where
// expected (system_prompt / model). This is the engine-side primitive that
// generalises OffPathDef.Persona into a per-call resource that any
// host.agent.* effect can name via `with: { agent: <name> }`.
func TestLoad_AgentsRoundTrip(t *testing.T) {
	yaml := []byte(`app:
  id: agents-test
  version: 0.1.0
root: start
states:
  start:
    view: "hello"
agents:
  wagon_master:
    system_prompt: |
      You are the wagon master. Speak with practical brevity.
  party_namer:
    system_prompt: "Output ONLY five names, comma-separated, no other text."
    model: claude-opus-4-7
`)
	def, err := LoadBytes(yaml)
	require.NoError(t, err)
	require.Len(t, def.Agents, 2)

	wm := def.Agents["wagon_master"]
	require.Contains(t, wm.SystemPrompt, "wagon master")
	require.Empty(t, wm.Model)

	pn := def.Agents["party_namer"]
	require.Contains(t, pn.SystemPrompt, "five names")
	require.Equal(t, "claude-opus-4-7", pn.Model)
}

// TestLoad_AgentRefResolves asserts that an effect's `with: { agent: <name> }`
// is statically resolved against the declared `agents:` block — a known agent
// loads cleanly; an unknown agent fails load with a descriptive error.
func TestLoad_AgentRefResolves(t *testing.T) {
	yaml := []byte(`app:
  id: agent-ref-ok
  version: 0.1.0
hosts: [host.agent.talk]
root: start
agents:
  wagon_master:
    system_prompt: "wm"
states:
  start:
    on_enter:
      - invoke: host.agent.talk
        with:
          question: "hi"
          agent: wagon_master
`)
	_, err := LoadBytes(yaml)
	require.NoError(t, err, "known agent ref must load")
}

// TestLoad_AgentRefUnknown asserts that an unknown agent: in a with: block
// fails load. Mirrors the host allow-list pattern: typos surface at load
// time, not as silent runtime no-ops.
func TestLoad_AgentRefUnknown(t *testing.T) {
	yaml := []byte(`app:
  id: agent-ref-bad
  version: 0.1.0
hosts: [host.agent.talk]
root: start
agents:
  known:
    system_prompt: "ok"
states:
  start:
    on_enter:
      - invoke: host.agent.talk
        with:
          question: "hi"
          agent: typo
`)
	_, err := LoadBytes(yaml)
	require.Error(t, err)
	require.Contains(t, err.Error(), `with.agent "typo" is not declared in agents`)
}

// (Test removed during meta-mode merge: the post-merge AgentDecl requires
// exactly one of system_prompt / system_prompt_path, so a description-only
// metadata entry no longer loads. internal/app/loader_agents_test.go covers
// the new constraint set.)

// TestLoad_OffPathAgentResolves asserts that off_path.agent: resolves against
// the agents block. Lets apps move the persona text out of off_path.persona
// into a shared agents entry referenced by name.
func TestLoad_OffPathAgentResolves(t *testing.T) {
	yaml := []byte(`app:
  id: offpath-agent
  version: 0.1.0
root: start
agents:
  frontier_guide:
    system_prompt: "speak like a frontier scout"
states:
  start:
    view: "hi"
off_path:
  trigger: "/freeform"
  return: "/onpath"
  agent: frontier_guide
`)
	def, err := LoadBytes(yaml)
	require.NoError(t, err)
	require.Equal(t, "frontier_guide", def.OffPath.Agent)
}

// TestLoad_OffPathAgentUnknown asserts that an off_path.agent name that
// doesn't resolve fails load. Symmetric with the effect-level agent: check.
func TestLoad_OffPathAgentUnknown(t *testing.T) {
	yaml := []byte(`app:
  id: offpath-agent-bad
  version: 0.1.0
root: start
states:
  start:
    view: "hi"
off_path:
  trigger: "/freeform"
  return: "/onpath"
  agent: missing
`)
	_, err := LoadBytes(yaml)
	require.Error(t, err)
	// Error message format is owned by validateAgentReferences (post-merge):
	// `agent reference "missing" at off_path.agent is undefined …`
	require.Contains(t, err.Error(), `"missing"`)
	require.Contains(t, err.Error(), `off_path.agent`)
}

// TestLoad_IncludeConflict verifies that duplicate keys across included files
// produce an error.
func TestLoad_IncludeConflict(t *testing.T) {
	// Write two temp files with the same state name and include both.
	dir := t.TempDir()
	mainYAML := `app:
  id: conflict-test
  version: 0.1.0
include: [rooms/*.yaml]
root: room_a
`
	roomA1 := `states:
  room_a:
    description: "First"
    view: "First room A"
`
	roomA2 := `states:
  room_a:
    description: "Second"
    view: "Second room A"
`
	require.NoError(t, os.WriteFile(dir+"/main.yaml", []byte(mainYAML), 0644))
	require.NoError(t, os.MkdirAll(dir+"/rooms", 0755))
	require.NoError(t, os.WriteFile(dir+"/rooms/a1.yaml", []byte(roomA1), 0644))
	require.NoError(t, os.WriteFile(dir+"/rooms/a2.yaml", []byte(roomA2), 0644))

	_, err := Load(dir + "/main.yaml")
	require.Error(t, err, "conflicting state names must produce an error")
	require.True(t, containsSubstring(err, "room_a"), "error must mention the duplicate key")
}

// containsSubstring checks whether the error string (including joined errors)
// contains the expected substring.
func containsSubstring(err error, sub string) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), sub) {
		return true
	}
	// Walk errors.Join chain.
	var joinErr interface{ Unwrap() []error }
	if errors.As(err, &joinErr) {
		for _, e := range joinErr.Unwrap() {
			if containsSubstring(e, sub) {
				return true
			}
		}
	}
	return false
}
