package metamode_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/metamode"
	"kitsoki/internal/store"
)

// realStoreOracle is a tiny stub OracleCaller: every Ask returns a
// canned reply and a stable claude_session_id so we can drive Send
// without spawning a real claude binary.
type realStoreOracle struct{ reply string }

func (o *realStoreOracle) Ask(_ context.Context, _ metamode.AskInput) (metamode.AskOutput, error) {
	return metamode.AskOutput{Reply: o.reply, NewClaudeSessionID: "stub-cs-id"}, nil
}

// TestRealStore_NewChatWithoutPriorSend exercises the path the user
// most-likely hit: /meta to enter, then /meta new immediately
// without ever sending a turn. The chat has no transcript and an
// empty claude_session_id — easy to forget in adapters that assume
// "send always runs first."
func TestRealStore_NewChatWithoutPriorSend(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	adapter := metamode.NewChatStoreAdapter(cs)

	appDef := &app.AppDef{
		App: app.AppMeta{ID: "dev-story"},
		MetaModes: map[string]*app.MetaModeDef{
			"story": {Trigger: "meta", Label: "improve the story", Agent: "story-author"},
		},
	}
	reg := &fakeRealAgentRegistry{agents: map[string]agents.Agent{
		"story-author": {Name: "story-author"},
	}}
	ctrl := &metamode.Controller{
		Chats:  adapter,
		Agents: reg,
		AppDef: appDef,
		Oracle: &realStoreOracle{reply: "stub"},
	}
	ctx := context.Background()

	sess, err := ctrl.Enter(ctx, metamode.Snapshot{State: "main"}, "story")
	require.NoError(t, err)
	oldID := sess.Chat.ID()

	// Drive /meta new with no prior Send — the chat is empty.
	fresh, err := ctrl.NewChat(ctx, sess)
	require.NoError(t, err, "NewChat on an empty chat must not error: %v", err)
	require.NotEqual(t, oldID, fresh.Chat.ID())
}

// TestRealStore_NewChatAfterSend reproduces the user-reported crash:
// open a real *chats.Store, enter meta, send a turn, then /meta new.
// Validates that no panic / nil-deref / "resolve returned archived
// chat" error leaks out — the operation is structurally identical to
// what the TUI's metaNewCmd triggers.
func TestRealStore_NewChatAfterSend(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	adapter := metamode.NewChatStoreAdapter(cs)

	appDef := &app.AppDef{
		App: app.AppMeta{ID: "dev-story"},
		MetaModes: map[string]*app.MetaModeDef{
			"story": {Trigger: "meta", Label: "improve the story", Agent: "story-author"},
		},
	}
	reg := &fakeRealAgentRegistry{agents: map[string]agents.Agent{
		"story-author": {Name: "story-author", SystemPrompt: "be helpful"},
	}}

	ctrl := &metamode.Controller{
		Chats:  adapter,
		Agents: reg,
		AppDef: appDef,
		Oracle: &realStoreOracle{reply: "hi back"},
	}

	ctx := context.Background()
	sess, err := ctrl.Enter(ctx, metamode.Snapshot{State: "main"}, "story")
	require.NoError(t, err)
	require.NotNil(t, sess)
	oldID := sess.Chat.ID()

	// Send one turn to populate the transcript — mirrors the user's
	// flow: /meta, type "hello", press Enter, see reply, then /meta new.
	sendRes, err := ctrl.Send(ctx, sess, "hello", metamode.TurnContext{StatePath: "main"})
	require.NoError(t, err, "Send before /meta new must succeed; got: %v / %+v", err, sendRes)
	require.Equal(t, "hi back", sendRes.Assistant)

	// Now /meta new. This is the path that allegedly crashed.
	fresh, err := ctrl.NewChat(ctx, sess)
	require.NoError(t, err, "NewChat after a sent turn must not crash; got: %v", err)
	require.NotNil(t, fresh)
	require.NotEqual(t, oldID, fresh.Chat.ID(),
		"NewChat must return a different chat row than the archived one")

	// Verify the new chat behaves: send a turn against it.
	sendRes2, err := ctrl.Send(ctx, fresh, "second", metamode.TurnContext{StatePath: "main"})
	require.NoError(t, err, "Send against the fresh chat must succeed; got: %v / %+v", err, sendRes2)
}

// TestRealStore_SessionWorkspaceAbsoluteWithRelativeAppFile is the
// integration test for bug 1: a relative appFile (the operator-typed
// path, e.g. `stories/bugfix/app.yaml`) must produce an absolute
// workspace_path when the value flows through SessionWorkspace into
// the chat_pty_sessions row written by chats.Store.AttachPTY.
//
// Without the resolveCwd absolutise fix, the persisted workspace_path
// is relative and tmux's `-c` flag silently drops the pane into $HOME
// for a fresh chat. The assertion below mirrors what /attach reads
// back via sessions.go:205 → tmux.NewSessionOptions.WorkingDir.
func TestRealStore_SessionWorkspaceAbsoluteWithRelativeAppFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	s, err := store.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	adapter := metamode.NewChatStoreAdapter(cs)

	appDef := &app.AppDef{
		App: app.AppMeta{ID: "dev-story"},
		MetaModes: map[string]*app.MetaModeDef{
			"story": {Trigger: "meta", Label: "improve the story", Agent: "story-author"},
		},
	}
	reg := &fakeRealAgentRegistry{agents: map[string]agents.Agent{
		"story-author": {Name: "story-author"},
	}}
	ctrl := &metamode.Controller{
		Chats:  adapter,
		Agents: reg,
		AppDef: appDef,
		Oracle: &realStoreOracle{reply: "stub"},
	}
	ctx := context.Background()

	// Pin the process cwd to a known temp dir so the relative path
	// the operator types resolves predictably. The /meta story bug
	// occurs because the user runs `hally run stories/bugfix/app.yaml`
	// from the repo root; mirror that here.
	repoRoot := t.TempDir()
	// Canonicalise: on macOS t.TempDir() is a /var/folders symlink to
	// /private/var/folders, and os.Chdir + os.Getwd below resolves it — so
	// storyDir (built from the raw repoRoot) must be resolved too or it won't
	// match the absolute workspace SessionWorkspace derives from the cwd.
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = resolved
	}
	storyDir := filepath.Join(repoRoot, "stories", "bugfix")
	require.NoError(t, os.MkdirAll(storyDir, 0o755))
	appAbs := filepath.Join(storyDir, "app.yaml")
	require.NoError(t, os.WriteFile(appAbs, []byte("# fixture"), 0o644))
	cwdBefore, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(cwdBefore) })
	require.NoError(t, os.Chdir(repoRoot))

	relAppFile := filepath.Join("stories", "bugfix", "app.yaml")

	sess, err := ctrl.Enter(ctx, metamode.Snapshot{State: "main"}, "story")
	require.NoError(t, err)

	// SessionWorkspace mirrors the call meta_attach.go::handleMetaAttach
	// makes when the user types /attach. The returned value MUST be
	// absolute or tmux drops the pane into $HOME.
	workspace := metamode.SessionWorkspace(sess, relAppFile)
	require.True(t, filepath.IsAbs(workspace),
		"SessionWorkspace returned non-absolute path %q (would land tmux pane in $HOME)", workspace)
	require.Equal(t, storyDir, workspace,
		"SessionWorkspace should resolve to the absolute story dir")

	// Drive the value through the same persistence path the production
	// /attach flow uses (chats.Store.AttachPTY), then read the row back
	// and assert workspace_path is absolute.
	_, err = cs.AttachPTY(ctx, chats.AttachPTYOptions{
		ChatID:         sess.Chat.ID(),
		TmuxSession:    "kitsoki-chat-" + sess.Chat.ID(),
		PermissionMode: "default",
		WorkspacePath:  workspace,
	})
	require.NoError(t, err)

	row, err := cs.GetPTY(ctx, sess.Chat.ID())
	require.NoError(t, err)
	require.Equal(t, storyDir, row.WorkspacePath,
		"persisted chat_pty_sessions.workspace_path must equal the absolute story dir")
	require.True(t, filepath.IsAbs(row.WorkspacePath),
		"persisted workspace_path must be absolute; got %q", row.WorkspacePath)

	// Sanity: query the row via raw SQL too (the spec asks the
	// caller demonstrate the SQL value directly). This belt-and-braces
	// check guards against any future row-scan refactor that silently
	// drops the column.
	var rawWP string
	err = s.DB().QueryRow(
		`SELECT workspace_path FROM chat_pty_sessions WHERE chat_id = ?`,
		sess.Chat.ID(),
	).Scan(&rawWP)
	require.NoError(t, err)
	require.Equal(t, storyDir, rawWP)
}

// fakeRealAgentRegistry is a minimal agents.Registry impl — the
// metamode package's controller_test.go already has fakeRegistry,
// but it lives in package metamode (white-box). This file is
// metamode_test (black-box) so we need our own.
type fakeRealAgentRegistry struct{ agents map[string]agents.Agent }

func (r *fakeRealAgentRegistry) Get(name string) (agents.Agent, bool) {
	a, ok := r.agents[name]
	return a, ok
}
func (r *fakeRealAgentRegistry) List() []string {
	out := make([]string, 0, len(r.agents))
	for n := range r.agents {
		out = append(out, n)
	}
	return out
}
func (r *fakeRealAgentRegistry) Register(a agents.Agent) { r.agents[a.Name] = a }
