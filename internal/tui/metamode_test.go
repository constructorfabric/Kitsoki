// metamode_test.go — TUI tests for the /meta overlay.
//
// These run as `package tui_test` so they exercise the public RootModel
// surface plus the export_test.go helpers. Each test wires a real
// metamode.Controller against in-package fakes (a fake ChatStore +
// AgentCaller + Registry) so we cover the Enter/Send wiring without
// pulling in the chats SQLite or the host claude shellout.
package tui_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/metamode"
	"kitsoki/internal/trace"
	tuipkg "kitsoki/internal/tui"
)

// ─── Fakes ───────────────────────────────────────────────────────────────────

// fakeMetaChat is the minimal ChatHandle the TUI tests need: it records
// AppendMessage and SetClaudeSessionID calls plus returns whatever
// ClaudeSessionID is set.
type fakeMetaChat struct {
	mu              sync.Mutex
	id              string
	appID           string
	room            string
	scopeKey        string
	title           string
	updatedAt       time.Time
	claudeSessionID string
	appends         []struct{ Role, Text string }
	archived        bool
}

func (c *fakeMetaChat) ID() string              { return c.id }
func (c *fakeMetaChat) AppID() string           { return c.appID }
func (c *fakeMetaChat) Room() string            { return c.room }
func (c *fakeMetaChat) ScopeKey() string        { return c.scopeKey }
func (c *fakeMetaChat) Title() string           { return c.title }
func (c *fakeMetaChat) UpdatedAt() time.Time    { return c.updatedAt }
func (c *fakeMetaChat) ClaudeSessionID() string { return c.claudeSessionID }

func (c *fakeMetaChat) SetClaudeSessionID(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claudeSessionID = id
	return nil
}

func (c *fakeMetaChat) AppendMessage(role, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appends = append(c.appends, struct{ Role, Text string }{role, text})
	return nil
}

func (c *fakeMetaChat) FirstUserMessage() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.appends {
		if a.Role == "user" {
			return a.Text, nil
		}
	}
	return "", nil
}

// fakeMetaChatStore is the test-side ChatStore. It supports both the
// WS-A3 happy path (lazily-created `chat` for the first resolve) and
// the Phase A.5 list / get / archive surface (the seedChat method
// populates additional rows for /meta list / /meta resume).
type fakeMetaChatStore struct {
	mu          sync.Mutex
	chat        *fakeMetaChat
	rows        []*fakeMetaChat
	archivedIDs []string
	counter     int
}

func (s *fakeMetaChatStore) ResolveMeta(_ context.Context, appID, room, scopeKey, title string) (metamode.ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reuse a non-archived row matching the (appID, room, scopeKey) tuple.
	for _, r := range s.rows {
		if r.archived {
			continue
		}
		if r.appID == appID && r.room == room && r.scopeKey == scopeKey {
			s.chat = r
			return r, nil
		}
	}
	// Legacy WS-A3 behaviour: the canonical `s.chat` is auto-created
	// on the first resolve when no seedChat call has populated rows.
	if s.chat == nil && len(s.rows) == 0 {
		s.chat = &fakeMetaChat{id: "chat-1", appID: appID, room: room, scopeKey: scopeKey, title: title}
		s.rows = append(s.rows, s.chat)
		return s.chat, nil
	}
	// Otherwise mint a fresh row with a synthetic ID.
	s.counter++
	id := fmt.Sprintf("chat-fresh-%d", s.counter)
	fresh := &fakeMetaChat{id: id, appID: appID, room: room, scopeKey: scopeKey, title: title}
	s.rows = append(s.rows, fresh)
	s.chat = fresh
	return fresh, nil
}

func (s *fakeMetaChatStore) GetMeta(_ context.Context, chatID string) (metamode.ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.id == chatID {
			return r, nil
		}
	}
	return nil, fmt.Errorf("fakeMetaChatStore.GetMeta: chat %q not found", chatID)
}

func (s *fakeMetaChatStore) ListMeta(_ context.Context, appID string) ([]metamode.ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]metamode.ChatHandle, 0, len(s.rows))
	for _, r := range s.rows {
		if r.archived {
			continue
		}
		if r.appID != appID {
			continue
		}
		if !strings.HasPrefix(r.room, "meta:") {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *fakeMetaChatStore) ArchiveMeta(_ context.Context, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archivedIDs = append(s.archivedIDs, chatID)
	for _, r := range s.rows {
		if r.id == chatID {
			r.archived = true
			return nil
		}
	}
	return fmt.Errorf("fakeMetaChatStore.ArchiveMeta: chat %q not found", chatID)
}

// seedChat appends a row directly so Phase A.5 tests can populate the
// table without going through ResolveMeta.
func (s *fakeMetaChatStore) seedChat(c *fakeMetaChat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, c)
}

// WithLock — pass-through fake that runs fn under no synchronization,
// matching the controller-level fake in internal/metamode. TUI tests
// don't exercise lock contention; the controller's own tests do.
func (s *fakeMetaChatStore) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

// fakeMetaAgent scripts a reply (and optionally an error) for Ask.
type fakeMetaAgent struct {
	mu       sync.Mutex
	gotInput metamode.AskInput
	reply    string
	err      error
}

func (o *fakeMetaAgent) Ask(_ context.Context, in metamode.AskInput) (metamode.AskOutput, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gotInput = in
	if o.err != nil {
		return metamode.AskOutput{}, o.err
	}
	return metamode.AskOutput{Reply: o.reply, NewClaudeSessionID: ""}, nil
}

// fakeMetaRegistry is a stub agents.Registry.
type fakeMetaRegistry struct{ a map[string]agents.Agent }

func (r *fakeMetaRegistry) Get(name string) (agents.Agent, bool) { a, ok := r.a[name]; return a, ok }
func (r *fakeMetaRegistry) List() []string {
	out := make([]string, 0, len(r.a))
	for n := range r.a {
		out = append(out, n)
	}
	return out
}
func (r *fakeMetaRegistry) Register(a agents.Agent) { r.a[a.Name] = a }

// ─── Setup helpers ───────────────────────────────────────────────────────────

// buildMetaModeModel returns a RootModel pre-wired with a metamode.Controller
// backed by the supplied fakes. metaModes is the map injected into the
// orchestrator's AppDef so the loader-side ordering is respected.
func buildMetaModeModel(t *testing.T, metaModes map[string]*app.MetaModeDef, agentReply string) (tea.Model, *fakeMetaChatStore, *fakeMetaAgent) {
	t.Helper()

	orch, sid := setupCloak(t)
	// Mutate the AppDef in-place to declare meta modes. The orchestrator
	// stores a pointer so this is visible to anything calling AppDef()
	// after construction.
	orch.AppDef().MetaModes = metaModes

	store := &fakeMetaChatStore{}
	agent := &fakeMetaAgent{reply: agentReply}
	reg := &fakeMetaRegistry{a: map[string]agents.Agent{
		"story-author": {
			Name:         "story-author",
			SystemPrompt: "you are the story author.",
		},
		"story-improver": {
			Name:         "story-improver",
			SystemPrompt: "you review the completed run.",
			Tools:        []string{"Read", "Glob", "Grep"},
		},
		"story-bug-reporter": {
			Name:         "story-bug-reporter",
			SystemPrompt: "you file story bugs.",
		},
	}}

	ctrl := &metamode.Controller{
		Chats:  store,
		Agents: reg,
		AppDef: orch.AppDef(),
		Agent:  agent,
	}

	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithMetaController(ctrl),
	))
	return m, store, agent
}

// singleStoryMode is the canonical one-mode test fixture.
func singleStoryMode() map[string]*app.MetaModeDef {
	return map[string]*app.MetaModeDef{
		"story": {
			Trigger: "meta",
			Label:   "improve the story",
			Banner:  "*** meta:story — improving the story ***",
			Agent:   "story-author",
		},
	}
}

func storyImproveMode() map[string]*app.MetaModeDef {
	return map[string]*app.MetaModeDef{
		"story.improve": {
			Group:   "story",
			Trigger: "improve",
			Label:   "Improve run",
			Banner:  "Reviewing this run for improvements.",
			Agent:   "story-improver",
			Tools:   []string{"Read", "Glob", "Grep"},
		},
	}
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// TestMetaMode_EnterViaSlash drives /meta from ModeOnPath and asserts
// the overlay becomes active with the configured banner rendered into
// the transcript.
func TestMetaMode_EnterViaSlash(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "hello back")

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"after /meta, mode should be ModeMeta")

	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "meta:story",
		"banner from MetaModeDef.Banner should appear in the transcript")
	require.NotNil(t, store.chat, "ResolveMeta should have been called")
	require.Equal(t, "meta:story", store.chat.room)
}

// TestMetaMode_EnterViaSlash_NamedMode declares two grouped modes and
// dispatches `/meta story bug`. Asserts the controller resolved the
// story.bug mode (not the lexicographically-first story.edit mode).
func TestMetaMode_EnterViaSlash_NamedMode(t *testing.T) {
	modes := map[string]*app.MetaModeDef{
		"story.edit": {Group: "story", Trigger: "edit", Default: true, Banner: "*** story.edit ***", Agent: "story-author"},
		"story.bug":  {Group: "story", Trigger: "bug", Banner: "*** story.bug ***", Agent: "story-bug-reporter"},
	}
	m, store, _ := buildMetaModeModel(t, modes, "noted")

	m = runTurnBlocking(t, m, "/meta story bug")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))
	require.NotNil(t, store.chat)
	require.Equal(t, "meta:story.bug", store.chat.room,
		"named /meta story bug should resolve story.bug, not story.edit")
	require.Contains(t, extractTranscript(t, m), "*** story.bug ***")
}

func TestMetaMode_CompletionSuggestsImprove(t *testing.T) {
	m, _, _ := buildMetaModeModel(t, storyImproveMode(), "improvement report")

	for _, turn := range []string{
		"go west",
		"hang the cloak",
		"go east",
		"go south",
		"read the message",
	} {
		m = runTurnBlocking(t, m, turn)
	}

	content := extractTranscript(t, m)
	require.Contains(t, content, "Improve this run")
	require.Contains(t, content, "/meta improve")
	require.Contains(t, content, "Auto-run at completion")
}

func TestMetaMode_ImproveAutoRunsFirstTurn(t *testing.T) {
	m, store, agent := buildMetaModeModel(t, storyImproveMode(), "improvement report")

	m = runTurnBlocking(t, m, "/meta improve")

	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"/meta improve should enter meta mode")
	require.NotNil(t, store.chat)
	require.Equal(t, "meta:story.improve", store.chat.room)
	require.Len(t, store.chat.appends, 2,
		"empty improve chat should record the auto prompt and assistant reply")
	require.Equal(t, "user", store.chat.appends[0].Role)
	require.Contains(t, store.chat.appends[0].Text, "Review this completed session")
	require.Equal(t, "assistant", store.chat.appends[1].Role)
	require.Contains(t, extractTranscript(t, m), "improvement report")
	require.Contains(t, agent.gotInput.UserMessage, "Review this completed session")
}

func TestMetaMode_ImproveDoesNotAutoRunExistingChat(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, storyImproveMode(), "second report")

	store.seedChat(&fakeMetaChat{
		id:       "prior-improve",
		appID:    "cloak-of-darkness",
		room:     "meta:story.improve",
		scopeKey: "foyer",
		title:    "Improve run",
		appends: []struct{ Role, Text string }{
			{Role: "user", Text: "already reviewed"},
			{Role: "assistant", Text: "prior report"},
		},
	})

	m = runTurnBlocking(t, m, "/meta improve")

	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))
	require.Equal(t, "prior-improve", store.chat.ID(),
		"existing improve chat should be reused")
	require.Len(t, store.chat.appends, 2,
		"existing improve chat must not get a duplicate auto prompt")
}

// TestMetaMode_EnterViaSlash_BareVerb dispatches `/meta bug` — a bare
// VERB, not a group. "bug" is not a group name, so the only way this
// resolves is the bare-verb path in resolveMetaName, which maps the verb
// to the matching grouped builtin (preferring the story group). This is
// the dogfood fix: `/meta bug` must Just Work in every app without the
// operator typing `/meta story bug`. Without the bare-verb resolution
// this surfaces "unknown mode" and stays ModeOnPath.
func TestMetaMode_EnterViaSlash_BareVerb(t *testing.T) {
	modes := map[string]*app.MetaModeDef{
		"story.edit": {Group: "story", Trigger: "edit", Default: true, Banner: "*** story.edit ***", Agent: "story-author"},
		"story.bug":  {Group: "story", Trigger: "bug", Banner: "*** story.bug ***", Agent: "story-bug-reporter"},
	}
	m, store, _ := buildMetaModeModel(t, modes, "noted")

	m = runTurnBlocking(t, m, "/meta bug")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"bare `/meta bug` must enter meta mode")
	require.NotNil(t, store.chat)
	require.Equal(t, "meta:story.bug", store.chat.room,
		"bare `/meta bug` should resolve the story.bug builtin")
	require.Contains(t, extractTranscript(t, m), "*** story.bug ***")
}

// TestMetaMode_BareVerbPrefersStoryGroup asserts that when a verb exists
// in BOTH the story and kitsoki groups, a bare `/meta bug` targets the
// running story (story.bug), not the engine (kitsoki.bug) — the
// story-by-default rule. Engine-targeting stays explicit via
// `/meta kitsoki bug`.
func TestMetaMode_BareVerbPrefersStoryGroup(t *testing.T) {
	modes := map[string]*app.MetaModeDef{
		"story.bug":   {Group: "story", Trigger: "bug", Banner: "*** story.bug ***", Agent: "story-bug-reporter"},
		"kitsoki.bug": {Group: "kitsoki", Trigger: "bug", Banner: "*** kitsoki.bug ***", Agent: "story-bug-reporter"},
	}
	m, store, _ := buildMetaModeModel(t, modes, "noted")

	m = runTurnBlocking(t, m, "/meta bug")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))
	require.NotNil(t, store.chat)
	require.Equal(t, "meta:story.bug", store.chat.room,
		"bare `/meta bug` must prefer the story group over kitsoki")
}

// TestMetaMode_UnknownMode asserts /meta nonexistent stays in ModeOnPath
// and surfaces the error in the transcript.
func TestMetaMode_UnknownMode(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")

	m = runTurnBlocking(t, m, "/meta nonexistent")
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"unknown /meta target must not change mode")
	require.Nil(t, store.chat, "ResolveMeta should not be called for unknown mode")
	require.Contains(t, extractTranscript(t, m), "unknown mode")
}

// TestMetaMode_TurnAppendsTranscript types a user turn inside meta mode,
// presses Enter, and confirms both the user text and the assistant reply
// land in the transcript.
func TestMetaMode_TurnAppendsTranscript(t *testing.T) {
	m, _, agent := buildMetaModeModel(t, singleStoryMode(), "hello back")

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))

	// Type a chat turn + Enter.
	m, _ = typeString(m, "hello there")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = processCommands(m, cmd, 10)

	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "hello there",
		"user text should appear in the transcript")
	require.Contains(t, transcript, "hello back",
		"assistant reply should appear in the transcript")
	// The agent's UserMessage now carries the [context] preamble +
	// the [user]…[/user] block (see metamode.TurnContext). Assert the
	// typed text appears inside the user block rather than checking
	// strict equality.
	require.Contains(t, agent.gotInput.UserMessage, "[user]\nhello there\n[/user]",
		"agent should have been invoked with the typed text inside the [user] block")
	require.Contains(t, agent.gotInput.UserMessage, "[context]",
		"agent should have received a [context] preamble")
}

// TestMetaMode_ReloadOnApply injects a metaSendDoneMsg with ReloadRequested
// and verifies the TUI consumes it without crashing. We can't easily
// inspect that orchestrator.Reload was called from outside the package
// (no public seam), so this test asserts the post-state shape: mode
// stays ModeMeta and the transcript reports the reload — which only
// happens through the reload path when appPath is set. Here appPath is
// empty (tests build with appPath=""), so the reload-when-empty branch
// trips silently. Assert at least: the message is handled and the
// in-flight flag clears.
func TestMetaMode_ReloadOnApply(t *testing.T) {
	m, _, _ := buildMetaModeModel(t, singleStoryMode(), "applied")

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))

	// Drive a Send-done message with ReloadRequested directly.
	msg := tuipkg.NewMetaSendDoneMsgForTest("user text", "assistant text", true, nil)
	m, _ = m.Update(msg)

	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"ReloadRequested should not exit meta mode")
	require.Contains(t, extractTranscript(t, m), "assistant text",
		"assistant text should be appended even when reload is requested")
}

// TestMetaMode_ExitViaOnpath enters meta mode, types /onpath + Enter,
// and asserts the overlay tears down.
func TestMetaMode_ExitViaOnpath(t *testing.T) {
	m, _, _ := buildMetaModeModel(t, singleStoryMode(), "")

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))

	m, _ = typeString(m, "/onpath")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = processCommands(m, cmd, 5)

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"/onpath inside meta mode must return to ModeOnPath")
}

// TestMetaMode_MenuEntry opens the Esc menu, picks the meta-story row,
// and asserts the same Enter-cmd is dispatched.
func TestMetaMode_MenuEntry(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")

	// Open menu.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeMenu, extractMode(t, m))

	view := tuipkg.MenuSystemView(mustRoot(t, m))
	require.Contains(t, view, "improve the story",
		"meta-mode label should appear in the Esc menu")

	// Hotkey: meta-story sits at row 2 (Exit=1, meta-story=2, Meta sessions=3).
	// The legacy "Report bug" stub at row 2 was removed once `/meta bug`
	// became the real bug-filing flow.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = processCommands(m, cmd, 10)

	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"picking the meta-story row should enter ModeMeta via the same code path as /meta")
	require.NotNil(t, store.chat)
	require.Equal(t, "meta:story", store.chat.room)
}

// mustRoot is a tiny helper that fails the test if the model isn't a
// RootModel.
func mustRoot(t *testing.T, m tea.Model) tuipkg.RootModel {
	t.Helper()
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	return rm
}

// TestMetaMode_NoControllerWired_HintShown verifies the polite-hint
// branch: when the model has no controller wired, /meta surfaces a
// message instead of crashing.
func TestMetaMode_NoControllerWired_HintShown(t *testing.T) {
	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView))

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
	require.True(t, strings.Contains(extractTranscript(t, m), "meta mode"),
		"expected a 'meta mode' hint in the transcript")
}

// ─── Phase A.5: discovery subcommands ────────────────────────────────────────

// TestMetaMode_ListInline drives /meta list from ModeOnPath against a
// store pre-seeded with two meta chats. The transcript should reflect
// both rows (id + preview) without entering ModeMeta.
func TestMetaMode_ListInline(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")

	// Seed two chats so the listing has something to render. The
	// appID must match the orchestrator's AppDef (cloak fixture).
	rm, _ := tuipkg.ExtractRootModel(m)
	appID := rm.AppID()
	t0 := time.Now()
	store.seedChat(&fakeMetaChat{
		id: "abc12345one", appID: appID, room: "meta:story",
		scopeKey: "main", title: "improve the story",
		updatedAt: t0.Add(-time.Hour),
		appends:   []struct{ Role, Text string }{{"user", "older question"}},
	})
	store.seedChat(&fakeMetaChat{
		id: "def67890two", appID: appID, room: "meta:story",
		scopeKey: "alt", title: "improve the story",
		updatedAt: t0,
		appends:   []struct{ Role, Text string }{{"user", "newest question"}},
	})

	m = runTurnBlocking(t, m, "/meta list")
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"/meta list must not enter ModeMeta")

	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "abc12345", "older chat row should appear in the listing")
	require.Contains(t, transcript, "def67890", "newer chat row should appear in the listing")
	require.Contains(t, transcript, "newest question",
		"first-user-message preview should appear in the listing")
	require.Contains(t, transcript, "story", "mode name should appear in the listing")
}

// TestMetaMode_ListInline_Empty verifies the "no meta chats yet"
// message when ListChats returns an empty slice.
func TestMetaMode_ListInline_Empty(t *testing.T) {
	m, _, _ := buildMetaModeModel(t, singleStoryMode(), "")
	m = runTurnBlocking(t, m, "/meta list")
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "meta chats")
	require.Contains(t, transcript, "(no meta chats yet)")
}

// TestMetaMode_NewArchivesAndContinues enters meta, types /meta new,
// and asserts: the old chat was archived, the new session is active,
// the transcript was cleared (just banner + system note remain).
func TestMetaMode_NewArchivesAndContinues(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")

	// Enter meta first so there's an active session.
	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))
	require.NotNil(t, store.chat, "initial enter should have resolved a chat")
	oldID := store.chat.ID()

	// Now drive /meta new.
	m, _ = typeString(m, "/meta new")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = processCommands(m, cmd, 10)

	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"/meta new should stay in ModeMeta")
	require.Len(t, store.archivedIDs, 1, "old chat should have been archived once")
	require.Equal(t, oldID, store.archivedIDs[0],
		"the just-archived id should be the previously active chat")

	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "fresh chat",
		"the transcript should announce the new chat")
	require.NotContains(t, transcript, "older question",
		"the previous chat's content must not bleed into the new pane")
}

// TestMetaMode_DoneArchivesAndExits enters meta, types /meta done,
// and asserts the chat was archived AND the overlay closed
// (mode flips back to ModeOnPath). The transcript carries a
// confirmation with the 8-char id prefix so the user can recover
// via /meta resume if they regret it.
func TestMetaMode_DoneArchivesAndExits(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")

	// Enter meta first so there's an active session.
	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))
	require.NotNil(t, store.chat, "initial enter should have resolved a chat")
	oldID := store.chat.ID()

	// Drive /meta done.
	m, _ = typeString(m, "/meta done")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = processCommands(m, cmd, 10)

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"/meta done must drop us back to ModeOnPath, unlike /meta new which stays in ModeMeta")
	require.Len(t, store.archivedIDs, 1,
		"the active chat should have been archived exactly once")
	require.Equal(t, oldID, store.archivedIDs[0])

	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "meta done: archived chat",
		"confirmation must surface in the transcript")
	// The fake store's auto-minted IDs ("chat-1") are shorter than 8 chars;
	// the handler truncates only when len > 8, so we just check the full id
	// appears.
	require.Contains(t, transcript, oldID,
		"confirmation must include the chat id for recovery via /meta resume")
}

// TestMetaMode_DoneOutsideMetaIsHint asserts that typing /meta done
// from ModeOnPath (no active session) prints a hint rather than
// archiving anything random.
func TestMetaMode_DoneOutsideMetaIsHint(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")

	m, _ = typeString(m, "/meta done")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = processCommands(m, cmd, 5)

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
	require.Empty(t, store.archivedIDs, "no archive should fire when no session is active")
	require.Contains(t, extractTranscript(t, m), "only valid inside meta mode")
}

// TestMetaMode_ResumeByPrefix from ModeOnPath: seed a chat, type
// /meta resume <prefix>, assert ModeMeta with the resumed chat.
func TestMetaMode_ResumeByPrefix(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")

	rm, _ := tuipkg.ExtractRootModel(m)
	appID := rm.AppID()
	store.seedChat(&fakeMetaChat{
		id: "abc1resume", appID: appID, room: "meta:story",
		scopeKey: "main", title: "improve the story",
		updatedAt: time.Now(),
		appends: []struct{ Role, Text string }{
			{"user", "what did we discuss?"},
			{"assistant", "lots."},
		},
	})

	m = runTurnBlocking(t, m, "/meta resume abc1")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"prefix should resolve to the seeded chat and enter ModeMeta")
}

// TestMetaMode_ResumePrefix_Ambiguous asserts the disambiguation
// listing surface when two ids share the prefix.
func TestMetaMode_ResumePrefix_Ambiguous(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")
	rm, _ := tuipkg.ExtractRootModel(m)
	appID := rm.AppID()
	store.seedChat(&fakeMetaChat{id: "abc1one", appID: appID, room: "meta:story", scopeKey: "a", updatedAt: time.Now()})
	store.seedChat(&fakeMetaChat{id: "abc1two", appID: appID, room: "meta:story", scopeKey: "b", updatedAt: time.Now()})

	m = runTurnBlocking(t, m, "/meta resume abc1")
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"ambiguous prefix must not enter ModeMeta")
	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "abc1one")
	require.Contains(t, transcript, "abc1two")
}

// TestMetaMode_ResumePrefix_TooShort asserts the prefix-length guard.
func TestMetaMode_ResumePrefix_TooShort(t *testing.T) {
	m, _, _ := buildMetaModeModel(t, singleStoryMode(), "")
	m = runTurnBlocking(t, m, "/meta resume ab")
	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m))
	require.Contains(t, extractTranscript(t, m), "too short")
}

// ─── Phase A.6: per-turn TurnContext injection ───────────────────────────────

// TestMetaMode_TurnSendsContext drives a chat turn inside meta mode
// and asserts the AskInput.UserMessage captured by the fake agent
// contains a [context] preamble built from RootModel state — at
// minimum the StatePath (the cloak fixture's initial state) and the
// rendered view that the player is staring at.
func TestMetaMode_TurnSendsContext(t *testing.T) {
	m, _, agent := buildMetaModeModel(t, singleStoryMode(), "ok")

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))

	m, _ = typeString(m, "what state am I in?")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	processCommands(m, cmd, 10)

	got := agent.gotInput.UserMessage
	require.Contains(t, got, "[context]\n",
		"agent should receive a [context] block")
	require.Contains(t, got, "state: foyer\n",
		"the cloak fixture starts in `foyer` — TurnContext should carry that")
	require.Contains(t, got, "view: |\n",
		"the rendered view should be embedded as a YAML literal block")
	require.Contains(t, got, "world:\n",
		"world snapshot should appear in the preamble")
	require.Contains(t, got, "[user]\nwhat state am I in?\n[/user]\n",
		"original user text should appear inside the [user] block")
	// appPath in this test fixture is "" (see buildMetaModeModel),
	// so the app_file line must be omitted.
	require.NotContains(t, got, "app_file:",
		"empty appPath should omit the app_file line entirely")
}

// ─── Phase A.7: per-turn trace dump ──────────────────────────────────────────

// TestMetaMode_TurnIncludesTracePath wires a RootModel with a small
// pre-populated trace ring and a temp file path, drives a meta-mode
// turn, and asserts:
//
//   - the agent saw the trace_file: line in the [context] preamble,
//     pointing at the configured temp file path.
//   - the on-disk file was rewritten with the ring's snapshot (the
//     one pre-staged event is present in JSONL form).
//
// This is the end-to-end Phase A.7 contract: the TUI dumps the ring,
// the controller's preamble carries the path, and the file the agent
// would Read contains the events.
func TestMetaMode_TurnIncludesTracePath(t *testing.T) {
	orch, sid := setupCloak(t)
	orch.AppDef().MetaModes = singleStoryMode()

	store := &fakeMetaChatStore{}
	agent := &fakeMetaAgent{reply: "ok"}
	reg := &fakeMetaRegistry{a: map[string]agents.Agent{
		"story-author": {
			Name:         "story-author",
			SystemPrompt: "you are the story author.",
		},
	}}

	ctrl := &metamode.Controller{
		Chats:  store,
		Agents: reg,
		AppDef: orch.AppDef(),
		Agent:  agent,
	}

	// Pre-populate the ring with one event so the on-disk dump has
	// something distinctive to assert on.
	ring := trace.NewRingBuffer(16)
	logger := slog.New(ring)
	logger.Info("turn.start", "session_id", "sess-1", "turn", int64(1))

	tracePath := t.TempDir() + "/meta-trace.jsonl"

	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)

	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithMetaController(ctrl),
		tuipkg.WithTraceRingBuffer(ring),
		tuipkg.WithTraceFilePath(tracePath),
	))

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))

	m, _ = typeString(m, "what's happened so far?")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	processCommands(m, cmd, 10)

	// The agent should have received the trace_file: line in the
	// preamble, pointing at exactly the configured path.
	got := agent.gotInput.UserMessage
	require.Contains(t, got, "trace_file: "+tracePath+"\n",
		"agent should see the trace_file: line pinning the dump path")

	// And the on-disk file should contain the pre-staged event.
	body, err := os.ReadFile(tracePath)
	require.NoError(t, err, "trace dump file should exist after the turn")
	require.Contains(t, string(body), `"msg":"turn.start"`,
		"trace file should carry the pre-staged ring event")
	require.Contains(t, string(body), `"session_id":"sess-1"`,
		"trace file should carry the attrs from the pre-staged event")
}

// TestMetaMode_TurnUsesExternalTraceFile verifies that when the path
// is wired via WithExternalTraceFile (the --trace-on-disk case), the
// TUI does NOT rewrite the file — it just surfaces the path. The
// external writer (slog file sink in production; this test pre-writes
// a sentinel) owns the contents.
func TestMetaMode_TurnUsesExternalTraceFile(t *testing.T) {
	orch, sid := setupCloak(t)
	orch.AppDef().MetaModes = singleStoryMode()

	store := &fakeMetaChatStore{}
	agent := &fakeMetaAgent{reply: "ok"}
	reg := &fakeMetaRegistry{a: map[string]agents.Agent{
		"story-author": {Name: "story-author", SystemPrompt: "you are the story author."},
	}}
	ctrl := &metamode.Controller{Chats: store, Agents: reg, AppDef: orch.AppDef(), Agent: agent}

	// Pre-populate the ring with one event AND pre-write the external
	// file with a different sentinel. After the turn the file should
	// still hold the sentinel — the TUI must not have overwritten it.
	ring := trace.NewRingBuffer(16)
	slog.New(ring).Info("ring.event.must.not.appear")

	tracePath := t.TempDir() + "/external-trace.jsonl"
	sentinel := `{"src":"external-writer","msg":"keep-me"}` + "\n"
	require.NoError(t, os.WriteFile(tracePath, []byte(sentinel), 0o600))

	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)

	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithMetaController(ctrl),
		tuipkg.WithTraceRingBuffer(ring),
		tuipkg.WithExternalTraceFile(tracePath),
	))

	m = runTurnBlocking(t, m, "/meta")
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m))
	m, _ = typeString(m, "anything?")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	processCommands(m, cmd, 10)

	require.Contains(t, agent.gotInput.UserMessage, "trace_file: "+tracePath+"\n",
		"agent should see the external trace path in the preamble")

	body, err := os.ReadFile(tracePath)
	require.NoError(t, err)
	require.Equal(t, sentinel, string(body),
		"external trace file must not be rewritten by the TUI")
	require.NotContains(t, string(body), "ring.event.must.not.appear",
		"ring buffer contents must not leak into the external file")
}
