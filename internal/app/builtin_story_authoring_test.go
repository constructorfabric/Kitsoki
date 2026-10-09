package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/agents"
	"kitsoki/internal/storyauthoring"
)

func TestLoadBytes_InjectsBuiltinStoryAuthoringRoom(t *testing.T) {
	def, err := LoadBytes([]byte(`
app:
  id: story-authoring-injection
  version: 0.1.0
root: start
states:
  start:
    view:
      - prose: "Start"
`))
	require.NoError(t, err)

	require.Empty(t, def.Hosts, "injecting the room must not turn on host allow-list validation")
	require.Contains(t, def.World, storyauthoring.RequestWorld)
	require.Contains(t, def.World, storyauthoring.NoteWorld)
	require.Contains(t, def.World, storyauthoring.ReturnStateWorld)
	require.Contains(t, def.Intents, storyauthoring.EnterIntent)
	require.Contains(t, def.Intents, storyauthoring.CaptureIntent)
	require.True(t, def.Intents[storyauthoring.EnterIntent].Hidden)
	require.False(t, def.Intents[storyauthoring.CaptureIntent].Hidden)
	require.False(t, def.Intents[storyauthoring.ReturnIntent].Hidden)
	require.False(t, def.Intents[storyauthoring.ClearIntent].Hidden)

	room := def.States[storyauthoring.RoomState]
	require.NotNil(t, room)
	require.Equal(t, WriteModeReadOnly, room.WriteMode)
	require.Equal(t, storyauthoring.CaptureIntent, room.DefaultIntent)
	require.Equal(t, []string{
		storyauthoring.CaptureIntent,
		storyauthoring.ReturnIntent,
		storyauthoring.ClearIntent,
	}, room.Menu)
	require.NotNil(t, room.AgentOffRamp)
	require.Equal(t, agents.NameStoryExplainer, room.AgentOffRamp.Agent)
	requireStoryAuthoringViewExplainsIntake(t, room)

	require.Len(t, room.OnEnter, 1)
	task := room.OnEnter[0]
	require.Equal(t, "host.agent.task", task.Invoke)
	require.Equal(t, agents.NameStoryAuthor, task.With["agent"])
	acceptance, ok := task.With["acceptance"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, storyauthoring.SchemaRef, acceptance["schema"])
	require.Equal(t, map[string]string{storyauthoring.NoteWorld: "submitted"}, task.Bind)
	capture := room.On[storyauthoring.CaptureIntent]
	require.Len(t, capture, 1)
	require.Len(t, capture[0].Effects, 2)
	require.Contains(t, capture[0].Effects[1].Say, "Story-authoring proposal captured")

	start := def.States["start"]
	require.NotNil(t, start)
	enter := start.On[storyauthoring.EnterIntent]
	require.Len(t, enter, 1)
	require.Equal(t, storyauthoring.RoomState, enter[0].Target)
	require.NotNil(t, enter[0].PushHistory)
	require.False(t, *enter[0].PushHistory)
	require.Len(t, enter[0].Effects, 2)
	set := enter[0].Effects[0].Set
	require.Equal(t, "{{ slots.proposal ?? '' }}", set[storyauthoring.RequestWorld])
	require.Equal(t, "start", set[storyauthoring.ReturnStateWorld])
	require.Equal(t, "Story-authoring intake accepted; return state is start.", enter[0].Effects[1].Say)
}

func requireStoryAuthoringViewExplainsIntake(t *testing.T, room *State) {
	t.Helper()

	var hasProposalCode, hasStoryRoot, hasWriteMode bool
	for _, el := range room.View.Elements {
		if el.Kind == "code" && strings.Contains(el.Source, storyauthoring.RequestWorld) {
			hasProposalCode = true
		}
		if el.Kind != "kv" {
			continue
		}
		for _, p := range el.Pairs {
			key, _ := p.Key.(string)
			value, _ := p.Value.(string)
			switch key {
			case "Story root":
				hasStoryRoot = value != ""
			case "Write mode":
				hasWriteMode = strings.Contains(value, "read-only")
			}
		}
	}
	require.True(t, hasProposalCode, "story-authoring view should show the captured proposal")
	require.True(t, hasStoryRoot, "story-authoring view should show which story root is being edited")
	require.True(t, hasWriteMode, "story-authoring view should surface write-mode behavior")
}

func TestLoadBytes_StoryAuthoringExtendsExistingHostAllowlist(t *testing.T) {
	def, err := LoadBytes([]byte(`
app:
  id: story-authoring-hosts
  version: 0.1.0
hosts: [host.echo]
root: start
states:
  start:
    view: "Start"
`))
	require.NoError(t, err)
	require.Contains(t, def.Hosts, "host.echo")
	require.Contains(t, def.Hosts, "host.agent.task")
}

func TestLoadBytes_StoryAuthoringDeclarationWins(t *testing.T) {
	def, err := LoadBytes([]byte(`
app:
  id: story-authoring-custom
  version: 0.1.0
root: start
intents:
  author_story:
    title: "Custom authoring"
states:
  start:
    view: "Start"
    on:
      author_story:
        - target: custom
  custom:
    view: "Custom"
  story_authoring:
    description: "Custom room"
    view: "Custom room"
`))
	require.NoError(t, err)

	require.Equal(t, "Custom authoring", def.Intents[storyauthoring.EnterIntent].Title)
	require.Equal(t, "Custom room", def.States[storyauthoring.RoomState].Description)
	require.Equal(t, "custom", def.States["start"].On[storyauthoring.EnterIntent][0].Target)
}
