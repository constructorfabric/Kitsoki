package metamode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/store"
)

func TestStubAgent_ReadOnly_NoWrite(t *testing.T) {
	dir := t.TempDir()
	s := NewStubAgentCaller()
	out, err := s.Ask(context.Background(), AskInput{
		ToolAllowlist: []string{"Read", "Glob", "Grep"}, // read-only mode
		Cwd:           dir,
		UserMessage:   "[context]\nstate: foyer\n[/context]\n\n[user]\nwhere am I?\n[/user]\n",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.Reply)
	assert.Contains(t, out.Reply, "foyer", "read-only reply should echo the context state")

	_, statErr := os.Stat(filepath.Join(dir, "meta-edits.log"))
	assert.True(t, os.IsNotExist(statErr), "a read-only turn must not write to the story tree")
}

func TestStubAgent_ReadOnlyImprove_ReturnsReport(t *testing.T) {
	dir := t.TempDir()
	s := NewStubAgentCaller()
	out, err := s.Ask(context.Background(), AskInput{
		SystemPrompt:  "You are the `story-improver` agent for kitsoki.",
		ToolAllowlist: []string{"Read", "Glob", "Grep"},
		Cwd:           dir,
		UserMessage:   "[context]\nstate: done\n[/context]\n\n[user]\nReview this completed session for an improvement report.\n[/user]\n",
	})
	require.NoError(t, err)
	assert.Contains(t, out.Reply, "Introspection report")
	assert.Contains(t, out.Reply, "Tool and permission notes")
	assert.Contains(t, out.Reply, "Regression coverage")

	_, statErr := os.Stat(filepath.Join(dir, "meta-edits.log"))
	assert.True(t, os.IsNotExist(statErr), "an improve report is read-only")
}

func TestStubAgent_Edit_WritesFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStubAgentCaller()
	out, err := s.Ask(context.Background(), AskInput{
		ToolAllowlist: nil, // story.edit leaves Tools unset → edit-capable
		Cwd:           dir,
		UserMessage:   "[user]\nmake the foyer darker\n[/user]\n",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.Reply)

	b, rerr := os.ReadFile(filepath.Join(dir, "meta-edits.log"))
	require.NoError(t, rerr, "an edit turn must mutate the story tree so the reload handshake fires")
	assert.Contains(t, string(b), "meta-mode edit")
}

func TestEditCapable(t *testing.T) {
	assert.True(t, editCapable(nil), "empty allowlist (inherit full surface) is edit-capable")
	assert.True(t, editCapable([]string{"Read", "Edit"}), "explicit Edit is edit-capable")
	assert.False(t, editCapable([]string{"Read", "Glob", "Grep"}), "read-only allowlist is not edit-capable")
}

// TestController_Send_StubReloadHandshake exercises the FULL edit→reload path
// through a real Controller with the stub agent: an edit-capable mode mutates
// the temp story tree (so ReloadRequested fires), a read-only mode does not.
// No LLM, no cmd/kitsoki wiring.
func TestController_Send_StubReloadHandshake(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte("app:\n  id: x\n"), 0o644))

	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	cs, err := chats.NewStore(st.DB())
	require.NoError(t, err)

	def := &app.AppDef{}
	def.App.ID = "x"
	app.InjectBuiltinMetaModes(def) // story.edit + story.ask are always present

	ctrl := &Controller{
		Chats:  NewChatStoreAdapter(cs),
		Agents: agents.NewBuiltins(),
		AppDef: def,
		Agent:  NewStubAgentCaller(),
	}
	ctx := context.Background()
	turn := TurnContext{AppFile: appPath, StatePath: "foyer"}

	// story.edit → edit-capable → reload requested, meta-edits.log changed.
	es, err := ctrl.Enter(ctx, Snapshot{State: "foyer"}, "story.edit")
	require.NoError(t, err)
	eres, err := ctrl.Send(ctx, es, "make it darker", turn)
	require.NoError(t, err)
	assert.True(t, eres.ReloadRequested, "an edit turn must request a reload")
	assert.True(t, containsSuffix(eres.ChangedFiles, "meta-edits.log"),
		"changed files should include the stub's edit marker, got %v", eres.ChangedFiles)

	// story.ask → read-only → no reload.
	as, err := ctrl.Enter(ctx, Snapshot{State: "foyer"}, "story.ask")
	require.NoError(t, err)
	ares, err := ctrl.Send(ctx, as, "where am I?", turn)
	require.NoError(t, err)
	assert.False(t, ares.ReloadRequested, "a read-only turn must not request a reload")
	assert.NotEmpty(t, ares.Assistant)
}

func containsSuffix(xs []string, suffix string) bool {
	for _, x := range xs {
		if strings.HasSuffix(x, suffix) {
			return true
		}
	}
	return false
}
