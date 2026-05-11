package chathost_test

import (
	"context"
	"errors"
	"testing"

	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"

	"database/sql"
	_ "modernc.org/sqlite"
)

// openMemDB opens an in-memory SQLite database for testing.
func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAdapter_RoundTrip(t *testing.T) {
	db := openMemDB(t)
	s, err := chats.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	a := chathost.NewAdapter(s)
	ctx := context.Background()

	// Create via adapter
	c, err := a.Create(ctx, "my-app", "oracle", "", "Test Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Title != "Test Chat" {
		t.Fatalf("expected title 'Test Chat', got %q", c.Title)
	}

	// Get via adapter
	got, err := a.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("Get returned wrong ID: got %q want %q", got.ID, c.ID)
	}

	// SetClaudeSessionID
	if err := a.SetClaudeSessionID(ctx, c.ID, "claude-session-123"); err != nil {
		t.Fatalf("SetClaudeSessionID: %v", err)
	}
	got2, _ := a.Get(ctx, c.ID)
	if got2.ClaudeSessionID != "claude-session-123" {
		t.Fatalf("expected ClaudeSessionID='claude-session-123', got %q", got2.ClaudeSessionID)
	}

	// AppendMessage
	msg, err := a.AppendMessage(ctx, c.ID, "user", "Hello", nil)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if msg.Role != "user" {
		t.Fatalf("expected role 'user', got %q", msg.Role)
	}
	if msg.Content != "Hello" {
		t.Fatalf("expected content 'Hello', got %q", msg.Content)
	}

	// Transcript
	msgs, err := a.Transcript(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	// LatestSeq
	seq, err := a.LatestSeq(ctx, c.ID)
	if err != nil {
		t.Fatalf("LatestSeq: %v", err)
	}
	if seq != 0 {
		t.Fatalf("expected seq=0, got %d", seq)
	}

	// List
	chatsOut, err := a.List(ctx, "my-app", "oracle", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(chatsOut) != 1 {
		t.Fatalf("expected 1 chat in list, got %d", len(chatsOut))
	}

	// Fork
	forked, err := a.Fork(ctx, c.ID, "Forked")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if forked.ParentChatID != c.ID {
		t.Fatalf("fork ParentChatID wrong: got %q want %q", forked.ParentChatID, c.ID)
	}

	// Archive
	if err := a.Archive(ctx, c.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	archived, _ := a.Get(ctx, c.ID)
	if archived.Status != string(chats.ChatArchived) {
		t.Fatalf("expected status='archived', got %q", archived.Status)
	}
}

func TestAdapter_Resolve(t *testing.T) {
	db := openMemDB(t)
	s, err := chats.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	a := chathost.NewAdapter(s)
	ctx := context.Background()

	// First resolve creates
	c1, created1, err := a.Resolve(ctx, "app", "room", "", "My Chat")
	if err != nil {
		t.Fatalf("Resolve first: %v", err)
	}
	if !created1 {
		t.Fatalf("expected created=true on first Resolve")
	}
	// Second resolve returns same
	c2, created2, err := a.Resolve(ctx, "app", "room", "", "My Chat")
	if err != nil {
		t.Fatalf("Resolve second: %v", err)
	}
	if created2 {
		t.Fatalf("expected created=false on second Resolve")
	}
	if c1.ID != c2.ID {
		t.Fatalf("Resolve should return same ID: %q vs %q", c1.ID, c2.ID)
	}
}

func TestAdapter_WithLock_TranslatesErrChatBusy(t *testing.T) {
	db := openMemDB(t)
	s, err := chats.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	a := chathost.NewAdapter(s)
	ctx := context.Background()

	// Create a chat to get a valid ID
	c, err := a.Create(ctx, "app", "room", "", "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Acquire lock in outer call; inner call should see ErrChatBusy
	var innerErr error
	err = a.WithLock(ctx, c.ID, func(ctx context.Context) error {
		// Attempt to acquire the same lock (same process/host — always busy)
		innerErr = a.WithLock(ctx, c.ID, func(context.Context) error {
			return nil
		})
		return nil
	})
	if err != nil {
		t.Fatalf("outer WithLock failed: %v", err)
	}
	if innerErr == nil {
		t.Fatal("expected inner WithLock to fail with ErrChatBusy")
	}
	if !errors.Is(innerErr, host.ErrChatBusy) {
		t.Fatalf("expected errors.Is(err, host.ErrChatBusy), got: %v", innerErr)
	}
}
