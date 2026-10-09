package metamode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

// ─── Fakes ───────────────────────────────────────────────────────────────────

// fakeChatStore captures the args of the most recent ResolveMeta and
// returns a fakeChat whose state the test can inspect. It also backs
// the Phase A.5 list/get/archive surface — rows is the in-memory
// table indexed by ID; ResolveMeta auto-creates a default chat on
// first call so the WS-A3 tests don't need to pre-populate.
type fakeChatStore struct {
	mu sync.Mutex
	// Captures
	gotAppID      string
	gotRoom       string
	gotScopeKey   string
	gotTitle      string
	archivedIDs   []string
	getMetaCalls  []string
	listMetaCalls []string
	// Behaviour
	chat *fakeChat // the single "default" chat ResolveMeta lazily creates
	rows []*fakeChat
	err  error
	// nextNewID supplies a synthetic id for NewChat-driven creates.
	// When empty, ResolveMeta increments a counter ("chat-2", "chat-3"…).
	nextNewIDCounter int
	// withLockErr is the error WithLock returns instead of running fn.
	// Tests that simulate a busy chat lock set this to a wrapped
	// ErrChatBusy and assert the controller surfaces it cleanly.
	withLockErr error
}

func (s *fakeChatStore) ResolveMeta(_ context.Context, appID, room, scopeKey, title string) (ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gotAppID = appID
	s.gotRoom = room
	s.gotScopeKey = scopeKey
	s.gotTitle = title
	if s.err != nil {
		return nil, s.err
	}
	// Legacy behaviour: when s.chat is pre-seeded by a test and not
	// archived, return it. Phase A.5 tests pre-populate s.rows
	// instead and leave s.chat zero so the new branch fires.
	if s.chat != nil && !s.chat.archived {
		// Keep the captured args in sync but don't mutate identity
		// — tests assert that the same handle persists across re-entry.
		return s.chat, nil
	}
	// Look for an existing non-archived row for this (appID, room, scopeKey).
	for _, r := range s.rows {
		if r.archived {
			continue
		}
		if r.appID == appID && r.room == room && r.scopeKey == scopeKey {
			return r, nil
		}
	}
	// None active — mint a new id and track it. The first auto-mint
	// uses "chat-1" so the WS-A3 / WS-A4 tests that pre-stash the
	// host-side ledger under "chat-1" stay valid; subsequent mints
	// (which only happen in Phase A.5 "NewChat" tests) use a
	// counter-suffixed id so the test can tell them apart.
	id := "chat-1"
	if len(s.rows) > 0 {
		s.nextNewIDCounter++
		id = fmt.Sprintf("chat-fresh-%d", s.nextNewIDCounter)
	}
	fresh := &fakeChat{id: id, appID: appID, room: room, scopeKey: scopeKey, title: title}
	s.rows = append(s.rows, fresh)
	// Also reflect the auto-created chat through s.chat when none was
	// pre-seeded, so the WS-A3 legacy tests that read s.chat see it.
	if s.chat == nil {
		s.chat = fresh
	}
	return fresh, nil
}

func (s *fakeChatStore) GetMeta(_ context.Context, chatID string) (ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getMetaCalls = append(s.getMetaCalls, chatID)
	for _, r := range s.rows {
		if r.id == chatID {
			return r, nil
		}
	}
	if s.chat != nil && s.chat.id == chatID {
		return s.chat, nil
	}
	return nil, fmt.Errorf("fakeChatStore.GetMeta: chat %q not found", chatID)
}

func (s *fakeChatStore) ListMeta(_ context.Context, appID string) ([]ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listMetaCalls = append(s.listMetaCalls, appID)
	out := make([]ChatHandle, 0, len(s.rows))
	for _, r := range s.rows {
		if r.appID != appID {
			continue
		}
		if r.archived {
			continue
		}
		if !strings.HasPrefix(r.room, "meta:") {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *fakeChatStore) ArchiveMeta(_ context.Context, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archivedIDs = append(s.archivedIDs, chatID)
	for _, r := range s.rows {
		if r.id == chatID {
			r.archived = true
			return nil
		}
	}
	if s.chat != nil && s.chat.id == chatID {
		s.chat.archived = true
		return nil
	}
	return fmt.Errorf("fakeChatStore.ArchiveMeta: chat %q not found", chatID)
}

// seedChat adds a row directly (Phase A.5 list/resume tests use this
// to pre-populate the table without going through ResolveMeta).
func (s *fakeChatStore) seedChat(c *fakeChat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, c)
}

// WithLock — fake lock that calls fn immediately. When
// withLockErr is set the fn never runs and the error is returned,
// simulating a busy chat / locked-by-other-driver scenario for
// Controller.Send tests.
func (s *fakeChatStore) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	s.mu.Lock()
	err := s.withLockErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return fn(ctx)
}

// fakeChat records every append + session-id update.
type fakeChat struct {
	mu              sync.Mutex
	id              string
	appID           string
	room            string
	scopeKey        string
	title           string
	updatedAt       time.Time
	claudeSessionID string
	appends         []appendCall
	sessionIDSets   []string
	setSessionErr   error
	appendErr       error
	archived        bool
}

type appendCall struct {
	Role string
	Text string
}

func (c *fakeChat) ID() string              { return c.id }
func (c *fakeChat) AppID() string           { return c.appID }
func (c *fakeChat) Room() string            { return c.room }
func (c *fakeChat) ScopeKey() string        { return c.scopeKey }
func (c *fakeChat) Title() string           { return c.title }
func (c *fakeChat) UpdatedAt() time.Time    { return c.updatedAt }
func (c *fakeChat) ClaudeSessionID() string { return c.claudeSessionID }

// FirstUserMessage walks appends and returns the first "user"-role text.
// Empty string + nil when none.
func (c *fakeChat) FirstUserMessage() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.appends {
		if a.Role == "user" {
			return a.Text, nil
		}
	}
	return "", nil
}

func (c *fakeChat) SetClaudeSessionID(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.setSessionErr != nil {
		return c.setSessionErr
	}
	c.sessionIDSets = append(c.sessionIDSets, id)
	c.claudeSessionID = id
	return nil
}

func (c *fakeChat) AppendMessage(role, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.appendErr != nil {
		return c.appendErr
	}
	c.appends = append(c.appends, appendCall{Role: role, Text: text})
	return nil
}

// fakeAgent records the AskInput it received and returns a scripted reply.
type fakeAgent struct {
	mu       sync.Mutex
	gotInput AskInput
	inputs   []AskInput
	out      AskOutput
	outs     []AskOutput
	err      error
	errs     []error
	calls    int
}

func (o *fakeAgent) Ask(_ context.Context, in AskInput) (AskOutput, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	o.gotInput = in
	o.inputs = append(o.inputs, in)
	if len(o.errs) > 0 {
		err := o.errs[0]
		o.errs = o.errs[1:]
		if err != nil {
			return AskOutput{}, err
		}
	} else if o.err != nil {
		return AskOutput{}, o.err
	}
	if len(o.outs) > 0 {
		out := o.outs[0]
		o.outs = o.outs[1:]
		return out, nil
	}
	return o.out, nil
}

// fakeRegistry is a tiny agent registry for the tests.
type fakeRegistry struct {
	agents map[string]agents.Agent
}

func newFakeRegistry(as ...agents.Agent) *fakeRegistry {
	r := &fakeRegistry{agents: make(map[string]agents.Agent, len(as))}
	for _, a := range as {
		r.agents[a.Name] = a
	}
	return r
}

func (r *fakeRegistry) Get(name string) (agents.Agent, bool) {
	a, ok := r.agents[name]
	return a, ok
}
func (r *fakeRegistry) List() []string {
	out := make([]string, 0, len(r.agents))
	for n := range r.agents {
		out = append(out, n)
	}
	return out
}
func (r *fakeRegistry) Register(a agents.Agent) { r.agents[a.Name] = a }

// newTestController wires a Controller against the supplied fakes and a
// minimal AppDef that declares one meta mode + matching agent unless
// overridden by the test.
func newTestController(t *testing.T, opts ...func(*Controller)) (*Controller, *fakeChatStore, *fakeAgent) {
	t.Helper()
	store := &fakeChatStore{}
	agent := &fakeAgent{
		out: AskOutput{Reply: "ok", NewClaudeSessionID: ""},
	}
	reg := newFakeRegistry(agents.Agent{
		Name:         "story-author",
		SystemPrompt: "you are the story author.",
		Tools:        []string{"host.agent.converse", "host.agent.ask"},
		DefaultCwd:   "/tmp/agent-default-cwd",
	})
	def := &app.AppDef{
		App: app.AppMeta{ID: "test-app"},
		MetaModes: map[string]*app.MetaModeDef{
			"story": {
				Trigger: "meta",
				Label:   "improve the story",
				Agent:   "story-author",
				Tools:   []string{"host.agent.converse"},
			},
		},
	}
	c := &Controller{
		Chats:  store,
		Agents: reg,
		AppDef: def,
		Agent:  agent,
		Clock:  func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, store, agent
}

func makeSnapshot(state string) Snapshot {
	return Snapshot{
		SessionID: app.SessionID("sess-1"),
		State:     app.StatePath(state),
		World:     world.New(),
	}
}

// ─── Controller.Enter tests ──────────────────────────────────────────────────

func TestController_Enter_NewChat(t *testing.T) {
	c, store, _ := newTestController(t)
	snap := makeSnapshot("forest/clearing")

	s, err := c.Enter(context.Background(), snap, "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}

	if got, want := store.gotAppID, "test-app"; got != want {
		t.Errorf("ResolveMeta appID = %q, want %q", got, want)
	}
	if got, want := store.gotRoom, "meta:story"; got != want {
		t.Errorf("ResolveMeta room = %q, want %q", got, want)
	}
	if got, want := store.gotScopeKey, "forest/clearing"; got != want {
		t.Errorf("ResolveMeta scopeKey = %q, want %q", got, want)
	}
	if got, want := store.gotTitle, "improve the story"; got != want {
		t.Errorf("ResolveMeta title = %q, want %q", got, want)
	}

	if s.Mode != c.AppDef.MetaModes["story"] {
		t.Errorf("Session.Mode is not the AppDef pointer")
	}
	if s.Agent.Name != "story-author" {
		t.Errorf("Session.Agent.Name = %q, want %q", s.Agent.Name, "story-author")
	}
	if s.Snapshot.EnteredAt.IsZero() {
		t.Error("Session.Snapshot.EnteredAt was not stamped")
	}
}

func TestController_Enter_UnknownMode(t *testing.T) {
	c, _, _ := newTestController(t)
	_, err := c.Enter(context.Background(), makeSnapshot("main"), "no-such-mode")
	if err == nil {
		t.Fatal("Enter: want error, got nil")
	}
	if !contains(err.Error(), `unknown mode "no-such-mode"`) {
		t.Errorf("Enter error = %q, want mention of unknown mode", err.Error())
	}
}

func TestController_Enter_UnknownAgent(t *testing.T) {
	c, _, _ := newTestController(t)
	c.AppDef.MetaModes["story"].Agent = "ghost-agent"
	_, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err == nil {
		t.Fatal("Enter: want error, got nil")
	}
	if !contains(err.Error(), `unknown agent "ghost-agent"`) {
		t.Errorf("Enter error = %q, want mention of unknown agent", err.Error())
	}
}

// ─── Controller.Send tests ───────────────────────────────────────────────────

func TestController_Send_PersistsTurns(t *testing.T) {
	c, _, agent := newTestController(t)
	agent.out = AskOutput{Reply: "hi back"}

	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "hello?", TurnContext{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("Send returned res.Err: %v", res.Err)
	}
	if res.Assistant != "hi back" {
		t.Errorf("res.Assistant = %q, want %q", res.Assistant, "hi back")
	}
	if res.ReloadRequested {
		t.Error("res.ReloadRequested true; WS-A3 must always emit false")
	}
	fc := s.Chat.(*fakeChat)
	if got := len(fc.appends); got != 2 {
		t.Fatalf("appends = %d, want 2", got)
	}
	if fc.appends[0].Role != "user" || fc.appends[0].Text != "hello?" {
		t.Errorf("appends[0] = %+v, want user/hello?", fc.appends[0])
	}
	if fc.appends[1].Role != "assistant" || fc.appends[1].Text != "hi back" {
		t.Errorf("appends[1] = %+v, want assistant/hi back", fc.appends[1])
	}
}

func TestController_Send_ResumesClaudeSession(t *testing.T) {
	c, store, agent := newTestController(t)
	// Pre-seed: existing claude session id on the chat row before Send.
	store.chat = &fakeChat{
		id:              "chat-1",
		appID:           "test-app",
		room:            "meta:story",
		scopeKey:        "main",
		claudeSessionID: "prev-session-xyz",
	}
	agent.out = AskOutput{Reply: "resumed.", NewClaudeSessionID: "new-session-abc"}

	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if _, err := c.Send(context.Background(), s, "go on.", TurnContext{}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Agent should have received the prior session id.
	if got := agent.gotInput.ClaudeSessionID; got != "prev-session-xyz" {
		t.Errorf("Ask saw ClaudeSessionID = %q, want %q", got, "prev-session-xyz")
	}
	// The new id should be persisted.
	fc := s.Chat.(*fakeChat)
	if len(fc.sessionIDSets) != 1 || fc.sessionIDSets[0] != "new-session-abc" {
		t.Errorf("sessionIDSets = %v, want [new-session-abc]", fc.sessionIDSets)
	}
	if fc.claudeSessionID != "new-session-abc" {
		t.Errorf("chat.claudeSessionID = %q, want %q", fc.claudeSessionID, "new-session-abc")
	}
}

func TestController_Send_StaleClaudeSessionRetriesFresh(t *testing.T) {
	c, store, agent := newTestController(t)
	store.chat = &fakeChat{
		id:              "chat-1",
		appID:           "test-app",
		room:            "meta:story",
		scopeKey:        "main",
		claudeSessionID: "6768ed59-74b5-4c22-a8e1-03314844897b",
	}
	agent.errs = []error{
		fmt.Errorf("metamode.AgentAdapter: handler: claude exited with code 1: Error: threadresume: threadresume failed: no rollout found for thread id 6768ed59-74b5-4c22-a8e1-03314844897b (code -32600)"),
		nil,
	}
	agent.outs = []AskOutput{
		{Reply: "filed https://github.com/example/repo/issues/122", NewClaudeSessionID: "fresh-session-abc"},
	}

	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "i want to file the bug on github", TurnContext{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Assistant != "filed https://github.com/example/repo/issues/122" {
		t.Fatalf("Assistant = %q", res.Assistant)
	}
	if agent.calls != 2 {
		t.Fatalf("agent calls = %d, want 2", agent.calls)
	}
	if got := agent.inputs[0].ClaudeSessionID; got != "6768ed59-74b5-4c22-a8e1-03314844897b" {
		t.Errorf("first Ask ClaudeSessionID = %q", got)
	}
	if got := agent.inputs[1].ClaudeSessionID; got != "" {
		t.Errorf("retry Ask ClaudeSessionID = %q, want fresh empty session", got)
	}
	fc := s.Chat.(*fakeChat)
	if got, want := fc.sessionIDSets, []string{"", "fresh-session-abc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessionIDSets = %v, want %v", got, want)
	}
	if fc.claudeSessionID != "fresh-session-abc" {
		t.Errorf("chat.claudeSessionID = %q, want fresh-session-abc", fc.claudeSessionID)
	}
	if got := len(fc.appends); got != 2 {
		t.Fatalf("appends = %d, want one user and one assistant", got)
	}
	if fc.appends[0].Role != "user" || fc.appends[0].Text != "i want to file the bug on github" {
		t.Errorf("appends[0] = %+v", fc.appends[0])
	}
	if fc.appends[1].Role != "assistant" {
		t.Errorf("appends[1] = %+v", fc.appends[1])
	}
}

// Send must NOT call SetClaudeSessionID when the agent echoes back the
// same id (typical of the WS-A3 adapter, which has no session-resume
// hook on the public handler yet). This guards against churn writes.
func TestController_Send_NoSessionWriteWhenUnchanged(t *testing.T) {
	c, store, agent := newTestController(t)
	store.chat = &fakeChat{
		id:              "chat-1",
		appID:           "test-app",
		room:            "meta:story",
		scopeKey:        "main",
		claudeSessionID: "same-id",
	}
	agent.out = AskOutput{Reply: "ok", NewClaudeSessionID: "same-id"}

	s, _ := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if _, err := c.Send(context.Background(), s, "ping", TurnContext{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	fc := s.Chat.(*fakeChat)
	if got := len(fc.sessionIDSets); got != 0 {
		t.Errorf("sessionIDSets had %d entries, want 0 (no churn write)", got)
	}
}

func TestController_Send_ToolAllowlistPassed(t *testing.T) {
	c, _, agent := newTestController(t)
	// Override the mode's tool list to a known value.
	c.AppDef.MetaModes["story"].Tools = []string{"host.agent.converse"}

	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if _, err := c.Send(context.Background(), s, "anything", TurnContext{}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := agent.gotInput.ToolAllowlist
	want := []string{"host.agent.converse"}
	if !equalStrings(got, want) {
		t.Errorf("ToolAllowlist = %v, want %v", got, want)
	}
}

// studioArgs digs the studio server's args out of a captured MCPServers map,
// or nil if no studio server was attached.
func studioArgs(servers map[string]any) []any {
	entry, ok := servers[studioMCPName].(map[string]any)
	if !ok {
		return nil
	}
	args, _ := entry["args"].([]any)
	return args
}

func argsContain(args []any, want string) bool {
	for _, a := range args {
		if s, ok := a.(string); ok && s == want {
			return true
		}
	}
	return false
}

func TestController_Send_AttachesStudioMCP(t *testing.T) {
	appFile := filepath.Join(t.TempDir(), "app.yaml")

	t.Run("edit mode is read-write", func(t *testing.T) {
		c, _, agent := newTestController(t)
		c.AppDef.MetaModes["story"].Tools = nil // edit-capable

		s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
		if err != nil {
			t.Fatalf("Enter: %v", err)
		}
		if _, err := c.Send(context.Background(), s, "x", TurnContext{AppFile: appFile}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		args := studioArgs(agent.gotInput.MCPServers)
		if args == nil {
			t.Fatal("no studio MCP server attached for edit mode")
		}
		if argsContain(args, "--read-only") {
			t.Errorf("edit mode must not pass --read-only; args = %v", args)
		}
	})

	t.Run("ask mode is read-only", func(t *testing.T) {
		c, _, agent := newTestController(t)
		c.AppDef.MetaModes["story"].Tools = []string{"Read", "Glob", "Grep"}

		s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
		if err != nil {
			t.Fatalf("Enter: %v", err)
		}
		if _, err := c.Send(context.Background(), s, "x", TurnContext{AppFile: appFile}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		args := studioArgs(agent.gotInput.MCPServers)
		if args == nil {
			t.Fatal("no studio MCP server attached for ask mode")
		}
		if !argsContain(args, "--read-only") {
			t.Errorf("ask mode must pass --read-only; args = %v", args)
		}
	})

	t.Run("no app file → no studio MCP", func(t *testing.T) {
		c, _, agent := newTestController(t)
		s, _ := c.Enter(context.Background(), makeSnapshot("main"), "story")
		if _, err := c.Send(context.Background(), s, "x", TurnContext{}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if agent.gotInput.MCPServers != nil {
			t.Errorf("MCPServers should be nil with no AppFile; got %v", agent.gotInput.MCPServers)
		}
	})
}

func TestController_Send_AgentError(t *testing.T) {
	c, _, agent := newTestController(t)
	agent.err = errors.New("boom")

	s, _ := c.Enter(context.Background(), makeSnapshot("main"), "story")
	res, err := c.Send(context.Background(), s, "x", TurnContext{})
	if err == nil {
		t.Fatal("Send: want error, got nil")
	}
	if res.Err == nil {
		t.Error("res.Err is nil; want agent error")
	}
	fc := s.Chat.(*fakeChat)
	// user was appended before the agent ran; assistant must not have been.
	if len(fc.appends) != 1 {
		t.Errorf("appends = %d, want 1 (only user)", len(fc.appends))
	}
}

// TestController_Send_ChatBusySurfaces verifies that when the chat
// lock is held by another driver, Send returns metamode.ErrChatBusy
// without calling the agent and without writing to the transcript.
// This is the contract the TUI's metaSendCmd hook relies on to
// render the busy-chat warning.
func TestController_Send_ChatBusySurfaces(t *testing.T) {
	c, store, agent := newTestController(t)
	store.withLockErr = fmt.Errorf("%w: simulated", ErrChatBusy)

	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}

	res, err := c.Send(context.Background(), s, "hello", TurnContext{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrChatBusy) {
		t.Errorf("expected ErrChatBusy, got %v", err)
	}
	if !errors.Is(res.Err, ErrChatBusy) {
		t.Errorf("res.Err should also wrap ErrChatBusy: %v", res.Err)
	}
	// Agent was never called.
	if agent.calls != 0 {
		t.Errorf("agent was invoked %d times despite busy lock", agent.calls)
	}
	// Transcript was not mutated.
	fc := s.Chat.(*fakeChat)
	if len(fc.appends) != 0 {
		t.Errorf("transcript should be untouched on busy lock; got %d appends", len(fc.appends))
	}
}

// TestController_Send_PopulatesChatID covers the SendResult.ChatID
// surface used by the TUI to render the kitsoki-chat-attach hint.
func TestController_Send_PopulatesChatID(t *testing.T) {
	c, _, _ := newTestController(t)
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "hi", TurnContext{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ChatID == "" {
		t.Error("SendResult.ChatID should be populated on success")
	}
	if res.ChatID != s.Chat.ID() {
		t.Errorf("ChatID = %q, want %q", res.ChatID, s.Chat.ID())
	}
}

// ─── Controller.Exit tests ───────────────────────────────────────────────────

// TestController_Exit_ArchivesEphemeralChat covers the persist:false
// case: Exit must archive the backing chat when the mode opts out of
// persistence, and must NOT archive when the mode is persistent
// (default). Two modes are declared so the same store instance can
// observe both behaviours.
//
// The fake store's ResolveMeta sticky-caches a single s.chat across
// Enter calls (legacy WS-A3 behaviour), so this test seeds two
// distinct rows on the rows-table path and constructs the Sessions
// directly to keep the chat identities cleanly separated.
func TestController_Exit_ArchivesEphemeralChat(t *testing.T) {
	c, store, _ := newTestController(t)
	// Declare a second meta mode that opts out of persistence. Uses a
	// synthetic `extra` key/agent — neutral name, no overlap with the
	// builtin `story.*` / `kitsoki.*` namespaces.
	ephemeral := false
	c.Agents.Register(agents.Agent{Name: "extra-agent", SystemPrompt: "ephemeral test agent."})
	c.AppDef.MetaModes["extra"] = &app.MetaModeDef{
		Trigger: "meta-extra",
		Label:   "ephemeral test mode",
		Agent:   "extra-agent",
		Persist: &ephemeral,
	}

	storyChat := &fakeChat{
		id:       "story-chat-1",
		appID:    c.AppDef.App.ID,
		room:     "meta:story",
		scopeKey: "forest/clearing",
		title:    "improve the story",
	}
	extraChat := &fakeChat{
		id:       "extra-chat-1",
		appID:    c.AppDef.App.ID,
		room:     "meta:extra",
		scopeKey: "forest/path",
		title:    "ephemeral test mode",
	}
	store.seedChat(storyChat)
	store.seedChat(extraChat)

	storyAgent, _ := c.Agents.Get("story-author")
	extraAgent, _ := c.Agents.Get("extra-agent")

	storyS := &Session{
		Mode:  c.AppDef.MetaModes["story"],
		Agent: storyAgent,
		Chat:  storyChat,
	}
	extraS := &Session{
		Mode:  c.AppDef.MetaModes["extra"],
		Agent: extraAgent,
		Chat:  extraChat,
	}

	ctx := context.Background()
	if err := c.Exit(ctx, storyS); err != nil {
		t.Fatalf("Exit story: %v", err)
	}
	if err := c.Exit(ctx, extraS); err != nil {
		t.Fatalf("Exit extra: %v", err)
	}

	// Assert: the persistent story chat was NOT archived; the
	// ephemeral extra chat WAS archived.
	for _, id := range store.archivedIDs {
		if id == storyChat.id {
			t.Errorf("persistent story chat %q was archived on Exit; want preserved (archivedIDs=%v)", storyChat.id, store.archivedIDs)
		}
	}
	extraArchived := false
	for _, id := range store.archivedIDs {
		if id == extraChat.id {
			extraArchived = true
		}
	}
	if !extraArchived {
		t.Errorf("ephemeral extra chat %q was not archived on Exit; archivedIDs=%v", extraChat.id, store.archivedIDs)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Compile-time interface assertion so the fake list keeps drifting in sync.
var _ ChatStore = (*fakeChatStore)(nil)
var _ ChatHandle = (*fakeChat)(nil)
var _ AgentCaller = (*fakeAgent)(nil)
var _ agents.Registry = (*fakeRegistry)(nil)

// guard against accidentally importing a sleep / external clock for
// the deterministic tests above.
var _ = fmt.Sprintf

// ─── tool-name normalisation + direct-edit reload tests ─────────────────────

// agentFunc is a func-shaped AgentCaller so individual tests can
// inject custom Ask behaviour without writing a new struct each time.
type agentFunc func(ctx context.Context, in AskInput) (AskOutput, error)

func (f agentFunc) Ask(ctx context.Context, in AskInput) (AskOutput, error) {
	return f(ctx, in)
}

// TestController_Send_DirectEdit_TriggersReload covers the modern
// flow: the agent edits app.yaml directly via Read/Write/Edit, with
// no propose/apply tokens. Send must detect the mtime+size change and
// set ReloadRequested. Uses a fake agent that "edits" the file by
// rewriting it during the Ask call.
func TestController_Send_DirectEdit_TriggersReload(t *testing.T) {
	dir := t.TempDir()
	appFile := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(appFile, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed app file: %v", err)
	}
	// Pre-set an mtime slightly in the past so the rewrite below
	// produces a guaranteed-larger ModTime even on filesystems with
	// coarse mtime granularity.
	old := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(appFile, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	c, _, _ := newTestController(t)
	c.Agent = agentFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// Simulate claude editing the file in-place during the call.
		if err := os.WriteFile(appFile, []byte("after, materially different content\n"), 0o644); err != nil {
			return AskOutput{}, err
		}
		return AskOutput{Reply: "edited the file"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "rename the foyer", TurnContext{AppFile: appFile})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !res.ReloadRequested {
		t.Errorf("ReloadRequested = false after agent edited app file; want true")
	}
}

// TestController_Send_DirectEdit_IncludeFileTriggersReload covers
// changes to files OTHER than app.yaml — included YAML fragments,
// prompt templates, scripts. The story-tree walk should pick them up
// and trigger a reload + populate SendResult.ChangedFiles.
func TestController_Send_DirectEdit_IncludeFileTriggersReload(t *testing.T) {
	dir := t.TempDir()
	appFile := filepath.Join(dir, "app.yaml")
	includeFile := filepath.Join(dir, "rooms", "main.yaml")
	promptFile := filepath.Join(dir, "prompts", "advice.md")

	if err := os.MkdirAll(filepath.Join(dir, "rooms"), 0o755); err != nil {
		t.Fatalf("mkdir rooms: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("manifest\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	if err := os.WriteFile(includeFile, []byte("main: original\n"), 0o644); err != nil {
		t.Fatalf("write include: %v", err)
	}
	if err := os.WriteFile(promptFile, []byte("be helpful\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	old := time.Now().Add(-2 * time.Second)
	for _, f := range []string{appFile, includeFile, promptFile} {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", f, err)
		}
	}

	c, _, _ := newTestController(t)
	c.Agent = agentFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// Simulate claude editing the INCLUDED yaml, not the manifest.
		return AskOutput{Reply: "edited the include"},
			os.WriteFile(includeFile, []byte("main: revised\n"), 0o644)
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "rename the main room", TurnContext{AppFile: appFile})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !res.ReloadRequested {
		t.Fatal("ReloadRequested = false after agent edited rooms/main.yaml; want true")
	}
	if len(res.ChangedFiles) == 0 {
		t.Fatal("ChangedFiles empty; want at least rooms/main.yaml")
	}
	found := false
	for _, f := range res.ChangedFiles {
		if f == filepath.Join("rooms", "main.yaml") {
			found = true
		}
	}
	if !found {
		t.Errorf("ChangedFiles = %v; missing rooms/main.yaml", res.ChangedFiles)
	}
}

// TestController_Send_NoEdit_NoReload is the negative regression:
// when the agent replies without touching the app file, mtime is
// unchanged and ReloadRequested stays false even though TurnContext
// carries a real AppFile path.
func TestController_Send_NoEdit_NoReload(t *testing.T) {
	dir := t.TempDir()
	appFile := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(appFile, []byte("unchanged\n"), 0o644); err != nil {
		t.Fatalf("seed app file: %v", err)
	}
	c, _, _ := newTestController(t)
	c.Agent = agentFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		return AskOutput{Reply: "just talking, no edits"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "tell me about the foyer", TurnContext{AppFile: appFile})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ReloadRequested {
		t.Errorf("ReloadRequested = true on read-only turn; want false")
	}
}

// TestController_Send_DirectEdit_DeleteTriggersReload covers the case
// where the agent removes a file from the story tree (e.g. deleting a
// room include the author no longer needs). storyTreeChanges must
// surface the deleted relative path and Send must flag a reload.
func TestController_Send_DirectEdit_DeleteTriggersReload(t *testing.T) {
	dir := t.TempDir()
	appFile := filepath.Join(dir, "app.yaml")
	roomsDir := filepath.Join(dir, "rooms")
	includeFile := filepath.Join(roomsDir, "foo.yaml")

	if err := os.MkdirAll(roomsDir, 0o755); err != nil {
		t.Fatalf("mkdir rooms: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("manifest\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	if err := os.WriteFile(includeFile, []byte("foo: original\n"), 0o644); err != nil {
		t.Fatalf("write include: %v", err)
	}
	// Pin mtimes in the past so the pre-snapshot is taken cleanly
	// (deletion doesn't depend on mtime, but staying consistent with
	// the existing reload tests keeps the recipe predictable).
	old := time.Now().Add(-2 * time.Second)
	for _, f := range []string{appFile, includeFile} {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", f, err)
		}
	}

	c, _, _ := newTestController(t)
	c.Agent = agentFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// Simulate claude deleting the include file mid-turn.
		if err := os.Remove(includeFile); err != nil {
			return AskOutput{}, err
		}
		return AskOutput{Reply: "removed the foo room"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "delete the foo room", TurnContext{AppFile: appFile})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !res.ReloadRequested {
		t.Fatal("ReloadRequested = false after agent deleted an include; want true")
	}
	wantRel := filepath.Join("rooms", "foo.yaml")
	found := false
	for _, f := range res.ChangedFiles {
		if f == wantRel {
			found = true
		}
	}
	if !found {
		t.Errorf("ChangedFiles = %v; missing %q (deleted file)", res.ChangedFiles, wantRel)
	}
}

// TestController_Send_DirectEdit_CreateTriggersReload covers the case
// where the agent adds a brand-new file to the story tree (e.g. a new
// prompt). storyTreeChanges must surface the created relative path and
// Send must flag a reload.
func TestController_Send_DirectEdit_CreateTriggersReload(t *testing.T) {
	dir := t.TempDir()
	appFile := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(appFile, []byte("manifest\n"), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}
	// Match the recipe of the other reload tests so the pre-snapshot
	// is taken on a stable mtime.
	old := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(appFile, old, old); err != nil {
		t.Fatalf("chtimes app: %v", err)
	}

	promptDir := filepath.Join(dir, "prompts")
	newPrompt := filepath.Join(promptDir, "new.md")

	c, _, _ := newTestController(t)
	c.Agent = agentFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// Simulate claude creating a brand-new prompt directory + file.
		if err := os.MkdirAll(promptDir, 0o755); err != nil {
			return AskOutput{}, err
		}
		if err := os.WriteFile(newPrompt, []byte("# new prompt\n"), 0o644); err != nil {
			return AskOutput{}, err
		}
		return AskOutput{Reply: "added prompts/new.md"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "draft a new prompt", TurnContext{AppFile: appFile})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !res.ReloadRequested {
		t.Fatal("ReloadRequested = false after agent created a new file; want true")
	}
	wantRel := filepath.Join("prompts", "new.md")
	found := false
	for _, f := range res.ChangedFiles {
		if f == wantRel {
			found = true
		}
	}
	if !found {
		t.Errorf("ChangedFiles = %v; missing %q (created file)", res.ChangedFiles, wantRel)
	}
}

// TestController_Send_ImportedManifestEditTriggersReload covers the
// story-imports auto-watch surface: an edit to a file
// in an IMPORTED sibling story's directory must trigger a reload,
// even though that directory sits outside `filepath.Dir(turn.AppFile)`.
// The TurnContext's ImportedManifestPaths threads the loader's list
// through; the controller folds each parent dir into the snapshot tree
// before/after the Agent call.
func TestController_Send_ImportedManifestEditTriggersReload(t *testing.T) {
	dir := t.TempDir()

	// Lay out: <dir>/main/app.yaml (root) + <dir>/imported/app.yaml
	// (sibling story). Pretend the root imports the sibling — we don't
	// actually invoke the loader here; we just plumb the manifest paths
	// straight into TurnContext.
	mainDir := filepath.Join(dir, "main")
	importedDir := filepath.Join(dir, "imported")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}
	if err := os.MkdirAll(importedDir, 0o755); err != nil {
		t.Fatalf("mkdir imported: %v", err)
	}
	mainApp := filepath.Join(mainDir, "app.yaml")
	importedApp := filepath.Join(importedDir, "app.yaml")
	importedPrompt := filepath.Join(importedDir, "prompts", "intro.md")
	if err := os.MkdirAll(filepath.Dir(importedPrompt), 0o755); err != nil {
		t.Fatalf("mkdir imported/prompts: %v", err)
	}
	if err := os.WriteFile(mainApp, []byte("# main manifest\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(importedApp, []byte("# imported manifest\n"), 0o644); err != nil {
		t.Fatalf("write imported app: %v", err)
	}
	if err := os.WriteFile(importedPrompt, []byte("# original\n"), 0o644); err != nil {
		t.Fatalf("write imported prompt: %v", err)
	}
	// Pin all mtimes to the past so post-edit mtime moves forward
	// deterministically.
	old := time.Now().Add(-2 * time.Second)
	for _, f := range []string{mainApp, importedApp, importedPrompt} {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", f, err)
		}
	}

	c, _, _ := newTestController(t)
	c.Agent = agentFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		// Simulate the agent editing the imported sibling's prompt.
		if err := os.WriteFile(importedPrompt, []byte("# edited by agent\n"), 0o644); err != nil {
			return AskOutput{}, err
		}
		return AskOutput{Reply: "patched the imported prompt"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	res, err := c.Send(context.Background(), s, "edit the imported prompt", TurnContext{
		AppFile:               mainApp,
		ImportedManifestPaths: []string{importedApp},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !res.ReloadRequested {
		t.Fatal("ReloadRequested = false after agent edited a file in an imported sibling story; want true")
	}
	if len(res.ChangedFiles) == 0 {
		t.Fatal("ChangedFiles = empty after agent edited imported prompt; want at least one entry")
	}
}

// TestController_Send_NormalisesToolNames verifies that short-form
// tool names from YAML ("authoring.propose") are normalised to the
// fully-qualified form before being passed to the AgentCaller.
func TestController_Send_NormalisesToolNames(t *testing.T) {
	c, _, _ := newTestController(t)
	// Mix of short and qualified names — both should pass through
	// to the agent as host.*.
	c.AppDef.MetaModes["story"].Tools = []string{
		"agent.converse",
		"host.agent.ask",
		"git.diff",
	}
	var seenAllowlist []string
	c.Agent = agentFunc(func(ctx context.Context, in AskInput) (AskOutput, error) {
		seenAllowlist = append([]string(nil), in.ToolAllowlist...)
		return AskOutput{Reply: "ok"}, nil
	})
	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if _, err := c.Send(context.Background(), s, "hi", TurnContext{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := []string{"host.agent.converse", "host.agent.ask", "host.git.diff"}
	if !equalStrings(seenAllowlist, want) {
		t.Errorf("ToolAllowlist = %v, want %v", seenAllowlist, want)
	}
}

// TestResolveCwd_AppFileFallback exercises the mode > agent > appFile
// precedence chain. Absolute selections pass through; relative ones
// are absolutised (this is the bug-1 fix — tmux's `-c` flag must see
// an absolute path or the pane lands in $HOME).
func TestResolveCwd_AppFileFallback(t *testing.T) {
	mode := &app.MetaModeDef{Cwd: "/from-mode"}
	agent := agents.Agent{DefaultCwd: "/from-agent"}
	empty := agents.Agent{}
	emptyMode := &app.MetaModeDef{}

	cases := []struct {
		name    string
		mode    *app.MetaModeDef
		agent   agents.Agent
		appFile string
		want    string
	}{
		{"mode wins", mode, agent, "/abs/app.yaml", "/from-mode"},
		{"agent next", emptyMode, agent, "/abs/app.yaml", "/from-agent"},
		{"app-file fallback", emptyMode, empty, "/abs/dir/app.yaml", "/abs/dir"},
		{"app-file fallback with nil mode", nil, empty, "/abs/dir/app.yaml", "/abs/dir"},
		{"all empty", emptyMode, empty, "", ""},
	}
	for _, tc := range cases {
		got := resolveCwd(tc.mode, tc.agent, tc.appFile)
		if got != tc.want {
			t.Errorf("%s: resolveCwd = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestResolveCwd_AbsolutisesRelative is the regression test for the
// /meta story → $HOME bug. A relative appFile (the operator-typed
// path) must produce an absolute fallback cwd because tmux's `-c`
// flag resolves relative paths against the tmux server's inherited
// cwd, which is not the kitsoki process. Same goes for relative
// mode.Cwd / agent.DefaultCwd — those resolve against the appFile
// dir for predictability.
func TestResolveCwd_AbsolutisesRelative(t *testing.T) {
	// Use a real on-disk tempdir so filepath.Abs against the appFile
	// produces a stable, prefix-matchable expected value.
	tmp := t.TempDir()
	// Canonicalise: on macOS t.TempDir() returns a /var/folders/... path that is
	// a symlink to /private/var/folders/..., and os.Chdir + os.Getwd below
	// resolves it. Without this, wantDir (built from the raw tmp) would never
	// match resolveCwd's output (built from the resolved getwd).
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}
	appAbs := filepath.Join(tmp, "stories", "bugfix", "app.yaml")
	if err := os.MkdirAll(filepath.Dir(appAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(appAbs, []byte("# fixture"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Pretend the operator passed a path relative to tmp. We chdir
	// into tmp so that filepath.Abs("stories/bugfix/app.yaml")
	// produces appAbs deterministically.
	cwdBefore, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwdBefore) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	relAppFile := filepath.Join("stories", "bugfix", "app.yaml")
	wantDir := filepath.Join(tmp, "stories", "bugfix")

	// Fallback: relative appFile, no mode/agent cwd → absolutised
	// directory of appFile.
	got := resolveCwd(&app.MetaModeDef{}, agents.Agent{}, relAppFile)
	if got != wantDir {
		t.Errorf("appFile fallback: resolveCwd(rel) = %q, want %q", got, wantDir)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("appFile fallback: resolveCwd returned non-absolute path %q", got)
	}

	// Relative mode.Cwd resolves against the appFile dir (most useful
	// for author-written `cwd: ./includes`-style values).
	gotMode := resolveCwd(&app.MetaModeDef{Cwd: "includes"}, agents.Agent{}, relAppFile)
	wantMode := filepath.Join(wantDir, "includes")
	if gotMode != wantMode {
		t.Errorf("relative mode.Cwd: resolveCwd = %q, want %q", gotMode, wantMode)
	}

	// Relative agent.DefaultCwd is treated the same way.
	gotAgent := resolveCwd(&app.MetaModeDef{}, agents.Agent{DefaultCwd: "scratch"}, relAppFile)
	wantAgent := filepath.Join(wantDir, "scratch")
	if gotAgent != wantAgent {
		t.Errorf("relative agent.DefaultCwd: resolveCwd = %q, want %q", gotAgent, wantAgent)
	}
}

// TestResolveCwd_EnvVarCwd_NotDoubled is the regression test for the
// web-UI "kitsoki.* meta chat does nothing" bug. The builtin kitsoki.*
// meta modes (kitsoki.ask / kitsoki.edit / kitsoki.bug) carry
// Cwd: "${KITSOKI_REPO}" in *raw, unexpanded* form. The effective
// working dir the adapter hands the claude subprocess is the
// composition expandCwd(resolveCwd(mode, agent, appFile)).
//
// The bug: resolveCwd ran filepath.Abs on the RAW "${KITSOKI_REPO}"
// literal (prepending the process cwd to the un-expanded token), and
// expandCwd then expanded the env var to its OWN absolute path —
// yielding "<process-cwd>/<abs-KITSOKI_REPO>", a doubled path that does
// not exist. claude then failed to chdir into it before it could even
// run, so the meta turn produced no reply (story.* modes were fine
// because they carry no Cwd). Env expansion must happen BEFORE
// absolutising.
func TestResolveCwd_EnvVarCwd_NotDoubled(t *testing.T) {
	// A real absolute dir so the result is a stable, comparable path.
	repo := t.TempDir()
	t.Setenv("KITSOKI_REPO", repo)

	// The home-screen ("self") driver passes appFile="" — exactly the
	// scope where the bug bit hardest (no app dir to fall back to).
	mode := &app.MetaModeDef{Cwd: "${KITSOKI_REPO}"}

	// Reproduce the production seam: controller.Send computes
	// resolveCwd(...) into AskInput.Cwd, then adapter applies expandCwd
	// on the way to the handler's working_dir.
	effective := expandCwd(resolveCwd(mode, agents.Agent{}, ""))

	if effective != repo {
		t.Errorf("effective working dir = %q, want %q (the env var's value, not a doubled path)", effective, repo)
	}
	if strings.Count(effective, repo) > 1 {
		t.Errorf("effective working dir %q contains the repo path twice — env expansion ran AFTER absolutising", effective)
	}
}

func TestNormaliseToolName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"agent.converse", "host.agent.converse"},
		{"host.agent.ask", "host.agent.ask"},
		{"git.diff", "host.git.diff"},
		{"", ""},
		// Tokens with multiple dots still get a single host. prefix.
		{"deeply.nested.name", "host.deeply.nested.name"},
	}
	for _, tc := range cases {
		got := NormaliseToolName(tc.in)
		if got != tc.want {
			t.Errorf("NormaliseToolName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─── Phase A.6: per-turn context injection ───────────────────────────────────

// TestController_Send_PrependsTurnContext drives Send with a populated
// TurnContext and asserts the AskInput.UserMessage seen by the agent
// carries the [context] preamble (state, app_file, view, world) AND
// the original text inside a [user] block.
func TestController_Send_PrependsTurnContext(t *testing.T) {
	c, _, agent := newTestController(t)

	turn := TurnContext{
		StatePath:    "main.foyer",
		AppFile:      "/tmp/kitsoki/app.yaml",
		TracePath:    "/tmp/kitsoki-meta-trace-1234.jsonl",
		RenderedView: "> You stand in the foyer of a great hall…\n>\n> Exits: north",
		World: map[string]any{
			"current_workspace": "kitsoki",
			"inbox_unread":      3,
			"wearing_cloak":     true,
		},
	}

	s, err := c.Enter(context.Background(), makeSnapshot("main.foyer"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if _, err := c.Send(context.Background(), s, "what state am I in?", turn); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := agent.gotInput.UserMessage

	mustContain := []string{
		"[context]\n",
		"state: main.foyer\n",
		"app_file: /tmp/kitsoki/app.yaml\n",
		"trace_file: /tmp/kitsoki-meta-trace-1234.jsonl\n",
		"view: |\n",
		// View literal-block lines must be two-space-indented:
		"  > You stand in the foyer of a great hall…\n",
		"  > Exits: north\n",
		"world:\n",
		"  current_workspace: kitsoki\n",
		"  inbox_unread: 3\n",
		"  wearing_cloak: true\n",
		"[/context]\n\n",
		"[user]\nwhat state am I in?\n[/user]\n",
	}
	for _, sub := range mustContain {
		if !strings.Contains(got, sub) {
			t.Errorf("UserMessage missing %q\n--- full body ---\n%s", sub, got)
		}
	}
}

// TestController_Send_OmitsEmptyContextFields populates only StatePath
// and asserts the preamble has no app_file / view / world lines.
func TestController_Send_OmitsEmptyContextFields(t *testing.T) {
	c, _, agent := newTestController(t)

	turn := TurnContext{StatePath: "foyer"}

	s, err := c.Enter(context.Background(), makeSnapshot("foyer"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if _, err := c.Send(context.Background(), s, "hi", turn); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := agent.gotInput.UserMessage
	if !strings.Contains(got, "state: foyer\n") {
		t.Errorf("UserMessage missing state line:\n%s", got)
	}
	forbidden := []string{"app_file:", "trace_file:", "view:", "world:"}
	for _, sub := range forbidden {
		if strings.Contains(got, sub) {
			t.Errorf("UserMessage unexpectedly contains %q (should be omitted when empty)\n%s", sub, got)
		}
	}
}

// TestController_Send_ZeroTurnContextNoPreamble asserts that a zero-value
// TurnContext produces no preamble — every pre-existing test passes
// TurnContext{} and relies on that property.
func TestController_Send_ZeroTurnContextNoPreamble(t *testing.T) {
	c, _, agent := newTestController(t)

	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if _, err := c.Send(context.Background(), s, "ping", TurnContext{}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := agent.gotInput.UserMessage
	if strings.Contains(got, "[context]") {
		t.Errorf("zero-value TurnContext leaked a [context] block:\n%s", got)
	}
	if want := "[user]\nping\n[/user]\n"; !strings.Contains(got, want) {
		t.Errorf("UserMessage missing %q:\n%s", want, got)
	}
}

// TestRenderTurnContextPreamble_TruncatesLongWorldValues asserts the
// 200-rune-per-value cap on world var previews.
func TestRenderTurnContextPreamble_TruncatesLongWorldValues(t *testing.T) {
	long := strings.Repeat("a", 250)
	turn := TurnContext{
		World: map[string]any{"long": long},
	}
	got := renderTurnContextPreamble(turn)
	if !strings.Contains(got, "  long: "+strings.Repeat("a", 200)+"…\n") {
		t.Errorf("preamble did not truncate long value at 200 runes:\n%s", got)
	}
}

// ─── Phase A.5: discovery surface ────────────────────────────────────────────

// TestController_ListChats seeds three rows (two meta + one non-meta)
// and asserts the two meta rows come back sorted by UpdatedAt desc,
// non-meta room is filtered, and the FirstUserMessage preview lands.
func TestController_ListChats(t *testing.T) {
	c, store, _ := newTestController(t)

	t0 := time.Unix(1_700_000_000, 0).UTC()
	older := &fakeChat{
		id:        "abc1older",
		appID:     c.AppDef.App.ID,
		room:      "meta:story",
		scopeKey:  "forest/clearing",
		title:     "improve the story",
		updatedAt: t0,
		appends:   []appendCall{{Role: "user", Text: "first older question"}},
	}
	newer := &fakeChat{
		id:        "def2newer",
		appID:     c.AppDef.App.ID,
		room:      "meta:story",
		scopeKey:  "forest/path",
		title:     "improve the story",
		updatedAt: t0.Add(time.Hour),
		appends:   []appendCall{{Role: "user", Text: "newer question"}},
	}
	nonMeta := &fakeChat{
		id:        "xyz3room",
		appID:     c.AppDef.App.ID,
		room:      "freeform",
		scopeKey:  "",
		title:     "freeform chat",
		updatedAt: t0.Add(30 * time.Minute),
	}
	store.seedChat(older)
	store.seedChat(newer)
	store.seedChat(nonMeta)

	got, err := c.ListChats(context.Background(), c.AppDef.App.ID)
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListChats: got %d rows, want 2", len(got))
	}
	if got[0].ID != "def2newer" {
		t.Errorf("ListChats[0].ID = %q, want %q (newest first)", got[0].ID, "def2newer")
	}
	if got[1].ID != "abc1older" {
		t.Errorf("ListChats[1].ID = %q, want %q", got[1].ID, "abc1older")
	}
	if got[0].ModeName != "story" {
		t.Errorf("ModeName = %q, want %q", got[0].ModeName, "story")
	}
	if got[0].FirstUserMessage != "newer question" {
		t.Errorf("FirstUserMessage = %q, want %q", got[0].FirstUserMessage, "newer question")
	}
	if got[0].ScopeKey != "forest/path" {
		t.Errorf("ScopeKey = %q, want %q", got[0].ScopeKey, "forest/path")
	}
}

// TestController_ListChats_TruncatesPreview asserts the
// FirstUserMessage preview is bounded at firstUserMessageMaxLen runes.
func TestController_ListChats_TruncatesPreview(t *testing.T) {
	c, store, _ := newTestController(t)

	// 150-rune body should be cut to 100 (firstUserMessageMaxLen).
	body := strings.Repeat("a", 150)
	store.seedChat(&fakeChat{
		id:        "abc1",
		appID:     c.AppDef.App.ID,
		room:      "meta:story",
		scopeKey:  "main",
		updatedAt: time.Unix(1_700_000_000, 0),
		appends:   []appendCall{{Role: "user", Text: body}},
	})
	got, err := c.ListChats(context.Background(), c.AppDef.App.ID)
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if rl := len([]rune(got[0].FirstUserMessage)); rl != firstUserMessageMaxLen {
		t.Errorf("FirstUserMessage rune-len = %d, want %d", rl, firstUserMessageMaxLen)
	}
}

// TestController_EnterByChatID_HappyPath seeds a meta chat, calls
// EnterByChatID, and asserts the returned Session points at the seeded
// chat with a fresh ledger and the right Mode/Agent.
func TestController_EnterByChatID_HappyPath(t *testing.T) {
	c, store, _ := newTestController(t)
	seed := &fakeChat{
		id:       "abc1existing",
		appID:    c.AppDef.App.ID,
		room:     "meta:story",
		scopeKey: "forest/clearing",
		title:    "improve the story",
	}
	store.seedChat(seed)

	s, err := c.EnterByChatID(context.Background(), makeSnapshot("forest/clearing"), "story", "abc1existing")
	if err != nil {
		t.Fatalf("EnterByChatID: %v", err)
	}
	if s.Chat.ID() != "abc1existing" {
		t.Errorf("Chat.ID = %q, want %q", s.Chat.ID(), "abc1existing")
	}
	if s.Agent.Name != "story-author" {
		t.Errorf("Agent.Name = %q", s.Agent.Name)
	}
	if s.Snapshot.EnteredAt.IsZero() {
		t.Error("Snapshot.EnteredAt not stamped")
	}
}

func TestController_EnterByChatID_WrongApp(t *testing.T) {
	c, store, _ := newTestController(t)
	store.seedChat(&fakeChat{
		id:       "abc1",
		appID:    "OTHER-APP",
		room:     "meta:story",
		scopeKey: "main",
	})
	_, err := c.EnterByChatID(context.Background(), makeSnapshot("main"), "story", "abc1")
	if err == nil {
		t.Fatal("expected error for cross-app resume, got nil")
	}
	if !contains(err.Error(), "belongs to app") {
		t.Errorf("error = %q, want mention of cross-app", err.Error())
	}
}

func TestController_EnterByChatID_NotMetaRoom(t *testing.T) {
	c, store, _ := newTestController(t)
	store.seedChat(&fakeChat{
		id:       "abc1",
		appID:    c.AppDef.App.ID,
		room:     "freeform",
		scopeKey: "main",
	})
	_, err := c.EnterByChatID(context.Background(), makeSnapshot("main"), "story", "abc1")
	if err == nil {
		t.Fatal("expected error for non-meta room, got nil")
	}
	if !contains(err.Error(), "not a meta chat") {
		t.Errorf("error = %q, want mention of non-meta", err.Error())
	}
}

func TestController_EnterByChatID_ModeMismatch(t *testing.T) {
	c, store, _ := newTestController(t)
	// Add a second "extra" agent + mode so the controller resolves both
	// modes. Synthetic key — no overlap with builtin namespaces.
	c.Agents.Register(agents.Agent{Name: "extra-agent", SystemPrompt: "extra."})
	c.AppDef.MetaModes["extra"] = &app.MetaModeDef{Trigger: "meta-extra", Agent: "extra-agent"}
	store.seedChat(&fakeChat{
		id:       "abc1",
		appID:    c.AppDef.App.ID,
		room:     "meta:extra",
		scopeKey: "main",
	})
	_, err := c.EnterByChatID(context.Background(), makeSnapshot("main"), "story", "abc1")
	if err == nil {
		t.Fatal("expected mode-mismatch error")
	}
	if !contains(err.Error(), "mode mismatch") {
		t.Errorf("error = %q, want mode-mismatch", err.Error())
	}
}

// TestController_NewChat_ArchivesAndOpensFresh archives the active
// chat and asserts the new session points at a fresh row.
func TestController_NewChat_ArchivesAndOpensFresh(t *testing.T) {
	c, store, _ := newTestController(t)

	s, err := c.Enter(context.Background(), makeSnapshot("main"), "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	oldID := s.Chat.ID()

	s2, err := c.NewChat(context.Background(), s)
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	if s2.Chat.ID() == oldID {
		t.Errorf("NewChat returned same chat id %q — expected a fresh row", oldID)
	}
	if len(store.archivedIDs) != 1 || store.archivedIDs[0] != oldID {
		t.Errorf("archivedIDs = %v, want [%s]", store.archivedIDs, oldID)
	}
	if s2.Mode != s.Mode || s2.Agent.Name != s.Agent.Name {
		t.Error("NewChat dropped Mode/Agent")
	}
}

func TestController_ResolveChatIDPrefix_Unique(t *testing.T) {
	c, store, _ := newTestController(t)
	store.seedChat(&fakeChat{
		id:       "abc1unique",
		appID:    c.AppDef.App.ID,
		room:     "meta:story",
		scopeKey: "main",
	})
	store.seedChat(&fakeChat{
		id:       "xyz9other",
		appID:    c.AppDef.App.ID,
		room:     "meta:story",
		scopeKey: "alt",
	})
	got, err := c.ResolveChatIDPrefix(context.Background(), c.AppDef.App.ID, "abc1")
	if err != nil {
		t.Fatalf("ResolveChatIDPrefix: %v", err)
	}
	if got != "abc1unique" {
		t.Errorf("got %q, want %q", got, "abc1unique")
	}
}

func TestController_ResolveChatIDPrefix_Ambiguous(t *testing.T) {
	c, store, _ := newTestController(t)
	store.seedChat(&fakeChat{id: "abc1one", appID: c.AppDef.App.ID, room: "meta:story", scopeKey: "a"})
	store.seedChat(&fakeChat{id: "abc1two", appID: c.AppDef.App.ID, room: "meta:story", scopeKey: "b"})

	_, err := c.ResolveChatIDPrefix(context.Background(), c.AppDef.App.ID, "abc1")
	if err == nil {
		t.Fatal("expected ambiguous-prefix error")
	}
	amb, ok := err.(*AmbiguousPrefixError)
	if !ok {
		t.Fatalf("error = %T (%v), want *AmbiguousPrefixError", err, err)
	}
	if len(amb.Matches) != 2 {
		t.Errorf("Matches = %v, want 2 entries", amb.Matches)
	}
}

func TestController_ResolveChatIDPrefix_TooShort(t *testing.T) {
	c, _, _ := newTestController(t)
	_, err := c.ResolveChatIDPrefix(context.Background(), c.AppDef.App.ID, "ab")
	if err == nil {
		t.Fatal("expected too-short error")
	}
	if !contains(err.Error(), "too short") {
		t.Errorf("error = %q, want mention of too-short", err.Error())
	}
}
