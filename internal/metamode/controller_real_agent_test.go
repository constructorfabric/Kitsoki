package metamode_test

// End-to-end wire test for the meta-mode Controller against the real
// AgentCaller adapter. The fake `claude` binary
// (internal/host/testdata/fake-oneshot-mcp.sh) is invoked as a real
// subprocess via host.AgentBinEnv — this proves the controller +
// adapter + host stack actually round-trips a turn without mocking the
// adapter seam.
//
// The fake script echoes its stdin back as `prompt=...` text. We assert
// the assistant reply is non-empty, that user + assistant messages
// landed on the chat, that the controller persisted the freshly minted
// claude_session_id, and that ReloadRequested is false (no story-dir
// edits happen — the fake never touches the filesystem outside of its
// own argv).

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/metamode"
	"kitsoki/internal/world"
)

// fakeBinPath resolves the path to fake-oneshot-mcp.sh relative to the
// directory holding this test file. Mirrors fakeOneShotMCPBin in
// internal/host/agent_ask_with_mcp_test.go.
func fakeBinPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/metamode/ → ../host/testdata/fake-oneshot-mcp.sh
	return filepath.Join(filepath.Dir(thisFile), "..", "host", "testdata", "fake-oneshot-mcp.sh")
}

// ─── Minimal in-test ChatStore + ChatHandle ──────────────────────────────────
//
// Smaller than controller_test.go's fakeMetaChatStore — we only need to
// observe AppendMessage / SetClaudeSessionID on a single resolved chat.

type wireChatStore struct {
	mu   sync.Mutex
	chat *wireChat
}

func (s *wireChatStore) ResolveMeta(_ context.Context, appID, room, scopeKey, title string) (metamode.ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chat == nil {
		s.chat = &wireChat{
			id:       "wire-chat-1",
			appID:    appID,
			room:     room,
			scopeKey: scopeKey,
			title:    title,
		}
	}
	return s.chat, nil
}

func (s *wireChatStore) GetMeta(_ context.Context, chatID string) (metamode.ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chat != nil && s.chat.id == chatID {
		return s.chat, nil
	}
	return nil, fmt.Errorf("wireChatStore.GetMeta: %q not found", chatID)
}

func (s *wireChatStore) ListMeta(_ context.Context, _ string) ([]metamode.ChatHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chat == nil {
		return nil, nil
	}
	return []metamode.ChatHandle{s.chat}, nil
}

func (s *wireChatStore) ArchiveMeta(_ context.Context, _ string) error { return nil }

func (s *wireChatStore) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	// real-agent wire test doesn't simulate lock contention; just
	// pass through to fn so the original behaviour is preserved.
	return fn(ctx)
}

type wireAppend struct {
	Role string
	Text string
}

type wireChat struct {
	mu              sync.Mutex
	id              string
	appID           string
	room            string
	scopeKey        string
	title           string
	updatedAt       time.Time
	claudeSessionID string
	appends         []wireAppend
	sessionIDSets   []string
}

func (c *wireChat) ID() string              { return c.id }
func (c *wireChat) AppID() string           { return c.appID }
func (c *wireChat) Room() string            { return c.room }
func (c *wireChat) ScopeKey() string        { return c.scopeKey }
func (c *wireChat) Title() string           { return c.title }
func (c *wireChat) UpdatedAt() time.Time    { return c.updatedAt }
func (c *wireChat) ClaudeSessionID() string { return c.claudeSessionID }

func (c *wireChat) SetClaudeSessionID(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionIDSets = append(c.sessionIDSets, id)
	c.claudeSessionID = id
	return nil
}

func (c *wireChat) AppendMessage(role, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appends = append(c.appends, wireAppend{Role: role, Text: text})
	return nil
}

func (c *wireChat) FirstUserMessage() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.appends {
		if a.Role == "user" {
			return a.Text, nil
		}
	}
	return "", nil
}

func (c *wireChat) recordedAppends() []wireAppend {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]wireAppend, len(c.appends))
	copy(out, c.appends)
	return out
}

func (c *wireChat) recordedSessionIDSets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.sessionIDSets))
	copy(out, c.sessionIDSets)
	return out
}

// Compile-time interface check.
var _ metamode.ChatStore = (*wireChatStore)(nil)
var _ metamode.ChatHandle = (*wireChat)(nil)

// TestController_Send_RealAgentAdapter_RoundTrip is the end-to-end
// wire test: real metamode.NewAgentCallerAdapter() in front of a fake
// `claude` subprocess launched via host.AgentBinEnv. The fake echoes
// its stdin so we get a non-empty reply, and the host mints a fresh
// claude session id since no prior one is set on the chat.
func TestController_Send_RealAgentAdapter_RoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot-mcp.sh requires bash")
	}

	// Point the real agent adapter at the fake claude binary.
	t.Setenv(host.AgentBinEnv, fakeBinPath(t))

	store := &wireChatStore{}
	reg := agents.NewBuiltins()

	// Pin Cwd to a tempdir so the spawned subprocess has a valid
	// working directory regardless of where `go test` is run from.
	// The adapter would otherwise fall back to "" (no AppFile), which
	// exec.Cmd treats as cwd-of-parent and can race against test
	// parallelism on some hosts.
	tempCwd := t.TempDir()

	def := &app.AppDef{
		App: app.AppMeta{ID: "wire-test-app"},
		MetaModes: map[string]*app.MetaModeDef{
			"story": {
				Trigger: "meta",
				Label:   "improve the story",
				Agent:   "story-author",
				Cwd:     tempCwd,
			},
		},
	}

	c := &metamode.Controller{
		Chats:  store,
		Agents: reg,
		AppDef: def,
		Agent:  metamode.NewAgentCallerAdapter(),
		Clock:  func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}

	snap := metamode.Snapshot{
		SessionID: app.SessionID("sess-wire-1"),
		State:     app.StatePath("main"),
		World:     world.New(),
	}

	s, err := c.Enter(context.Background(), snap, "story")
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}

	res, err := c.Send(context.Background(), s, "hello from the wire test", metamode.TurnContext{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("Send returned res.Err: %v", res.Err)
	}
	if res.Assistant == "" {
		t.Errorf("res.Assistant empty; want non-empty echo from fake claude (got %q)", res.Assistant)
	}
	if res.ReloadRequested {
		t.Errorf("res.ReloadRequested = true; want false (no story-dir edits in this path)")
	}
	if len(res.ChangedFiles) != 0 {
		t.Errorf("res.ChangedFiles = %v; want empty (no AppFile in TurnContext)", res.ChangedFiles)
	}

	// User + assistant must have been appended in that order.
	appends := store.chat.recordedAppends()
	if len(appends) != 2 {
		t.Fatalf("appends = %d entries (%+v), want 2", len(appends), appends)
	}
	if appends[0].Role != "user" || appends[0].Text != "hello from the wire test" {
		t.Errorf("appends[0] = %+v, want user/hello from the wire test", appends[0])
	}
	if appends[1].Role != "assistant" {
		t.Errorf("appends[1].Role = %q, want assistant", appends[1].Role)
	}
	if appends[1].Text == "" {
		t.Errorf("appends[1].Text empty; expected fake's echo body")
	}

	// The host mints a UUID when no prior session id exists. Send
	// must call SetClaudeSessionID with that minted value exactly once.
	sets := store.chat.recordedSessionIDSets()
	if len(sets) != 1 {
		t.Fatalf("sessionIDSets = %v; want exactly one entry (the minted UUID)", sets)
	}
	if sets[0] == "" {
		t.Errorf("sessionIDSets[0] empty; host should have minted a UUID")
	}
	if len(sets[0]) < 16 {
		// UUIDs are 36 chars; allow any non-trivial id here so a future
		// host change to a shorter scheme doesn't break the test.
		t.Errorf("sessionIDSets[0] = %q looks too short to be a real session id", sets[0])
	}
	if got := store.chat.ClaudeSessionID(); got != sets[0] {
		t.Errorf("ClaudeSessionID() = %q, want %q (round-trip into chat row)", got, sets[0])
	}
}
