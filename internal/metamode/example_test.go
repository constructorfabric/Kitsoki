// Runnable godoc example for the [Controller] surface. The Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/metamode/...`.
package metamode_test

import (
	"context"
	"fmt"
	"time"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/metamode"
)

// ExampleController_Send is the typed-seam worked example: enter an
// "edit" mode, send one turn through a fake agent and an in-memory chat
// store, and read the SendResult — the same Enter → Send shape the TUI
// drives, with the orchestrator never touched.
func ExampleController_Send() {
	// A registry with one agent the mode references.
	reg := agents.NewBuiltins()
	reg.Register(agents.Agent{
		Name:         "story-author",
		SystemPrompt: "You edit kitsoki stories.",
	})

	persist := true
	def := &app.AppDef{
		App: app.AppMeta{ID: "demo", Version: "v0"},
		MetaModes: map[string]*app.MetaModeDef{
			"edit": {
				Trigger: "edit",
				Label:   "Edit",
				Agent:   "story-author",
				Persist: &persist,
			},
		},
	}

	ctrl := &metamode.Controller{
		Chats:  &exampleChatStore{},
		Agents: reg,
		AppDef: def,
		Agent:  exampleAgent{reply: "Renamed the foyer to atrium."},
		Clock:  func() time.Time { return time.Unix(0, 0).UTC() },
	}

	ctx := context.Background()
	sess, err := ctrl.Enter(ctx, metamode.Snapshot{State: "main.foyer"}, "edit")
	if err != nil {
		panic(err)
	}

	// Zero TurnContext: no file tree to walk, so no edit/commit — the
	// turn is a pure conversation step.
	res, err := ctrl.Send(ctx, sess, "rename the foyer to atrium", metamode.TurnContext{})
	if err != nil {
		panic(err)
	}

	fmt.Println("room:           ", sess.Chat.Room())
	fmt.Println("scope:          ", sess.Chat.ScopeKey())
	fmt.Println("assistant:      ", res.Assistant)
	fmt.Println("reloadRequested:", res.ReloadRequested)
	// Output:
	// room:            meta:edit
	// scope:           main.foyer
	// assistant:       Renamed the foyer to atrium.
	// reloadRequested: false
}

// exampleAgent is a fake AgentCaller that returns a fixed reply and
// echoes the session id, standing in for the claude shellout.
type exampleAgent struct{ reply string }

func (o exampleAgent) Ask(_ context.Context, in metamode.AskInput) (metamode.AskOutput, error) {
	return metamode.AskOutput{Reply: o.reply, NewClaudeSessionID: in.ClaudeSessionID}, nil
}

// exampleChatStore is a minimal in-memory ChatStore: one lazily-created
// chat row, no locking contention, no archival semantics beyond a flag.
type exampleChatStore struct{ chat *exampleChat }

func (s *exampleChatStore) ResolveMeta(_ context.Context, appID, room, scopeKey, title string) (metamode.ChatHandle, error) {
	if s.chat == nil {
		s.chat = &exampleChat{id: "01HZEXAMPLE", appID: appID, room: room, scopeKey: scopeKey, title: title}
	}
	return s.chat, nil
}

func (s *exampleChatStore) GetMeta(_ context.Context, chatID string) (metamode.ChatHandle, error) {
	if s.chat != nil && s.chat.id == chatID {
		return s.chat, nil
	}
	return nil, fmt.Errorf("not found: %s", chatID)
}

func (s *exampleChatStore) ListMeta(_ context.Context, appID string) ([]metamode.ChatHandle, error) {
	if s.chat != nil && s.chat.appID == appID {
		return []metamode.ChatHandle{s.chat}, nil
	}
	return nil, nil
}

func (s *exampleChatStore) ArchiveMeta(_ context.Context, chatID string) error {
	if s.chat != nil && s.chat.id == chatID {
		s.chat.archived = true
	}
	return nil
}

func (s *exampleChatStore) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

// exampleChat is the matching ChatHandle.
type exampleChat struct {
	id, appID, room, scopeKey, title string
	claudeSessionID                  string
	archived                         bool
}

func (c *exampleChat) ID() string              { return c.id }
func (c *exampleChat) AppID() string           { return c.appID }
func (c *exampleChat) Room() string            { return c.room }
func (c *exampleChat) ScopeKey() string        { return c.scopeKey }
func (c *exampleChat) Title() string           { return c.title }
func (c *exampleChat) UpdatedAt() time.Time    { return time.Unix(0, 0).UTC() }
func (c *exampleChat) ClaudeSessionID() string { return c.claudeSessionID }
func (c *exampleChat) SetClaudeSessionID(id string) error {
	c.claudeSessionID = id
	return nil
}
func (c *exampleChat) AppendMessage(role, text string) error { return nil }
func (c *exampleChat) FirstUserMessage() (string, error)     { return "", nil }
