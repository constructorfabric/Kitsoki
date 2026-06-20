package chats_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/jobs"
	"kitsoki/internal/store"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) (*chats.Store, *clock.Fake) {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	fake := clock.NewFake(time.Unix(0, 0))
	cs, err := chats.NewStore(s.DB(), chats.WithClock(fake))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return cs, fake
}

func TestStore_CreateAndGet(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "My Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if c.Status != string(chats.ChatActive) {
		t.Errorf("expected status active, got %q", c.Status)
	}
	if c.Title != "My Chat" {
		t.Errorf("expected title 'My Chat', got %q", c.Title)
	}

	got, err := cs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("expected id %s, got %s", c.ID, got.ID)
	}
	if got.AppID != "app1" {
		t.Errorf("expected app_id 'app1', got %q", got.AppID)
	}
}

func TestStore_GetNotFound(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	_, err := cs.Get(ctx, "NONEXISTENT")
	if err == nil {
		t.Fatal("expected error for nonexistent chat")
	}
}

func TestStore_List(t *testing.T) {
	cs, fake := openTestStore(t)
	ctx := context.Background()

	fake.Advance(time.Second)
	c1, err := cs.Create(ctx, "app1", "agent", "", "Chat 1")
	if err != nil {
		t.Fatalf("Create c1: %v", err)
	}
	fake.Advance(time.Second)
	c2, err := cs.Create(ctx, "app1", "agent", "", "Chat 2")
	if err != nil {
		t.Fatalf("Create c2: %v", err)
	}
	// Different app — should not appear.
	_, err = cs.Create(ctx, "app2", "agent", "", "Other")
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}

	chatsApp1, err := cs.List(ctx, "app1", "agent", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(chatsApp1) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chatsApp1))
	}
	// Ordered by last_active_at DESC — c2 first.
	if chatsApp1[0].ID != c2.ID {
		t.Errorf("expected c2 first, got %s", chatsApp1[0].ID)
	}
	if chatsApp1[1].ID != c1.ID {
		t.Errorf("expected c1 second, got %s", chatsApp1[1].ID)
	}
}

func TestStore_Resolve_GetOrCreate(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	// First call: creates a new chat.
	c1, created1, err := cs.Resolve(ctx, "app1", "agent", "proj", "Initial Title")
	if err != nil {
		t.Fatalf("Resolve (create): %v", err)
	}
	if !created1 {
		t.Errorf("expected created=true on first Resolve, got false")
	}

	// Second call: returns the same chat.
	c2, created2, err := cs.Resolve(ctx, "app1", "agent", "proj", "Different Title")
	if err != nil {
		t.Fatalf("Resolve (get): %v", err)
	}
	if created2 {
		t.Errorf("expected created=false on second Resolve, got true")
	}
	if c1.ID != c2.ID {
		t.Errorf("expected same chat ID; got %s and %s", c1.ID, c2.ID)
	}
	// Title is the original one, not the new one.
	if c2.Title != "Initial Title" {
		t.Errorf("expected original title, got %q", c2.Title)
	}
}

func TestStore_Resolve_SkipsArchived(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c1, _, err := cs.Resolve(ctx, "app1", "meta:story", "main.foyer", "First")
	if err != nil {
		t.Fatalf("Resolve initial: %v", err)
	}
	if err := cs.Archive(ctx, c1.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	c2, created, err := cs.Resolve(ctx, "app1", "meta:story", "main.foyer", "Second")
	if err != nil {
		t.Fatalf("Resolve after archive: %v", err)
	}
	if !created {
		t.Fatal("expected created=true after archiving prior chat")
	}
	if c2.ID == c1.ID {
		t.Fatalf("expected fresh chat after archive; got same ID %s", c1.ID)
	}
}

func TestStore_Resolve_ForkDoesNotBlock(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	// Create a chat, then fork it.
	original, err := cs.Create(ctx, "app1", "agent", "sk", "Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = cs.Fork(ctx, original.ID, "Fork")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Resolve should still return the original non-fork chat.
	resolved, created, err := cs.Resolve(ctx, "app1", "agent", "sk", "New")
	if err != nil {
		t.Fatalf("Resolve after fork: %v", err)
	}
	if created {
		t.Errorf("expected created=false (original chat exists), got true")
	}
	if resolved.ID != original.ID {
		t.Errorf("expected original chat, got %s", resolved.ID)
	}
}

func TestStore_AppendMessageAndTranscript(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m0, err := cs.AppendMessage(ctx, c.ID, "user", "Hello", nil)
	if err != nil {
		t.Fatalf("AppendMessage 0: %v", err)
	}
	if m0.Seq != 0 {
		t.Errorf("expected seq 0, got %d", m0.Seq)
	}

	m1, err := cs.AppendMessage(ctx, c.ID, "assistant", "Hi there", map[string]any{"confidence": 0.9})
	if err != nil {
		t.Fatalf("AppendMessage 1: %v", err)
	}
	if m1.Seq != 1 {
		t.Errorf("expected seq 1, got %d", m1.Seq)
	}

	msgs, err := cs.Transcript(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected msgs[0].Role='user', got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("expected msgs[1].Role='assistant', got %q", msgs[1].Role)
	}
	if msgs[1].Metadata == nil {
		t.Error("expected metadata to be non-nil")
	}
}

func TestStore_Transcript_SinceSeq(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := cs.AppendMessage(ctx, c.ID, "user", "msg", nil); err != nil {
			t.Fatalf("AppendMessage %d: %v", i, err)
		}
	}

	msgs, err := cs.Transcript(ctx, c.ID, 3)
	if err != nil {
		t.Fatalf("Transcript since 3: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (seq 3,4), got %d", len(msgs))
	}
	if msgs[0].Seq != 3 {
		t.Errorf("expected first seq=3, got %d", msgs[0].Seq)
	}
}

func TestStore_LatestSeq(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	seq, err := cs.LatestSeq(ctx, c.ID)
	if err != nil {
		t.Fatalf("LatestSeq (empty): %v", err)
	}
	if seq != -1 {
		t.Errorf("expected -1 for empty chat, got %d", seq)
	}

	if _, err := cs.AppendMessage(ctx, c.ID, "user", "hi", nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := cs.AppendMessage(ctx, c.ID, "assistant", "hey", nil); err != nil {
		t.Fatalf("AppendMessage 2: %v", err)
	}

	seq, err = cs.LatestSeq(ctx, c.ID)
	if err != nil {
		t.Fatalf("LatestSeq: %v", err)
	}
	if seq != 1 {
		t.Errorf("expected seq=1, got %d", seq)
	}
}

func TestStore_SetClaudeSessionID(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ClaudeSessionID != "" {
		t.Errorf("expected empty claude_session_id on create, got %q", c.ClaudeSessionID)
	}

	if err := cs.SetClaudeSessionID(ctx, c.ID, "session-abc-123"); err != nil {
		t.Fatalf("SetClaudeSessionID: %v", err)
	}

	got, err := cs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after SetClaudeSessionID: %v", err)
	}
	if got.ClaudeSessionID != "session-abc-123" {
		t.Errorf("expected claude_session_id 'session-abc-123', got %q", got.ClaudeSessionID)
	}
}

func TestStore_Archive(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "Chat")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := cs.Archive(ctx, c.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	got, err := cs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after Archive: %v", err)
	}
	if got.Status != string(chats.ChatArchived) {
		t.Errorf("expected status 'archived', got %q", got.Status)
	}
}

func TestStore_Fork(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	parent, err := cs.Create(ctx, "app1", "agent", "sk", "Parent Chat")
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := cs.AppendMessage(ctx, parent.ID, "user", "Hello", nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := cs.AppendMessage(ctx, parent.ID, "assistant", "World", nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := cs.SetClaudeSessionID(ctx, parent.ID, "orig-session"); err != nil {
		t.Fatalf("SetClaudeSessionID: %v", err)
	}

	fork, err := cs.Fork(ctx, parent.ID, "Branch A")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	if fork.ID == parent.ID {
		t.Error("fork should have a different ID than parent")
	}
	if fork.ParentChatID != parent.ID {
		t.Errorf("expected parent_chat_id=%s, got %s", parent.ID, fork.ParentChatID)
	}
	if fork.ClaudeSessionID != "" {
		t.Errorf("expected empty claude_session_id on fork, got %q", fork.ClaudeSessionID)
	}
	if fork.Title != "Branch A" {
		t.Errorf("expected title 'Branch A', got %q", fork.Title)
	}

	msgs, err := cs.Transcript(ctx, fork.ID, 0)
	if err != nil {
		t.Fatalf("Transcript on fork: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 copied messages, got %d", len(msgs))
	}
}

// TestStore_RestartPersistence verifies that chats and messages persist when
// the SQLite DB is closed and reopened — i.e. that a fresh process can read
// a transcript a previous process wrote.
func TestStore_RestartPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chats.db")

	// Phase 1: open the store, write a chat + messages, set a claude session,
	// then close everything cleanly.
	var (
		chatID   string
		claudeID = "11111111-2222-4333-8444-555555555555"
	)
	{
		s, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		cs, err := chats.NewStore(s.DB())
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		ctx := context.Background()
		c, err := cs.Create(ctx, "app1", "agent", "scope-a", "Persistent Chat")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		chatID = c.ID
		if _, err := cs.AppendMessage(ctx, chatID, "user", "first question", nil); err != nil {
			t.Fatalf("AppendMessage user: %v", err)
		}
		if _, err := cs.AppendMessage(ctx, chatID, "assistant", "first answer", map[string]any{"exit_code": 0}); err != nil {
			t.Fatalf("AppendMessage assistant: %v", err)
		}
		if err := cs.SetClaudeSessionID(ctx, chatID, claudeID); err != nil {
			t.Fatalf("SetClaudeSessionID: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	// Phase 2: open a fresh store over the same file, read everything back,
	// and verify it round-tripped.
	{
		s, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("Open (reopen): %v", err)
		}
		defer func() { _ = s.Close() }()
		cs, err := chats.NewStore(s.DB())
		if err != nil {
			t.Fatalf("NewStore (reopen): %v", err)
		}
		ctx := context.Background()

		got, err := cs.Get(ctx, chatID)
		if err != nil {
			t.Fatalf("Get after reopen: %v", err)
		}
		if got.Title != "Persistent Chat" {
			t.Errorf("title not persisted: %q", got.Title)
		}
		if got.ClaudeSessionID != claudeID {
			t.Errorf("claude_session_id not persisted: got %q want %q", got.ClaudeSessionID, claudeID)
		}

		msgs, err := cs.Transcript(ctx, chatID, 0)
		if err != nil {
			t.Fatalf("Transcript after reopen: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages after reopen, got %d", len(msgs))
		}
		if msgs[0].Content != "first question" {
			t.Errorf("user msg not persisted: %q", msgs[0].Content)
		}
		if msgs[1].Content != "first answer" {
			t.Errorf("assistant msg not persisted: %q", msgs[1].Content)
		}
		if got, ok := msgs[1].Metadata["exit_code"]; !ok || got != float64(0) {
			t.Errorf("metadata not persisted (json round-trip yields float64): got %v ok=%v", got, ok)
		}
	}
}

// TestStore_SchemaMigration_OnExistingDB verifies that the chats schema is
// applied idempotently to an existing DB that already has the session and jobs
// schemas in place.  Sequence:
//  1. Open a fresh in-memory store (applies sessions schema).
//  2. Apply jobs schema via jobs.NewJobStore.
//  3. Confirm chat tables do NOT yet exist.
//  4. Apply chats schema via chats.NewStore — chat tables now exist.
//  5. Insert a chat and read it back.
//  6. Re-call chats.NewStore on the same *sql.DB — must be idempotent.
func TestStore_SchemaMigration_OnExistingDB(t *testing.T) {
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Apply jobs schema.
	if _, err := jobs.NewJobStore(s.DB()); err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	// Verify the chat tables don't yet exist.
	chatTables := []string{"chats", "chat_messages", "chat_locks"}
	for _, name := range chatTables {
		var cnt int
		err := s.DB().QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
		).Scan(&cnt)
		if err != nil {
			t.Fatalf("query sqlite_master for %s: %v", name, err)
		}
		if cnt != 0 {
			t.Fatalf("expected table %q to not exist before chats.NewStore; got count=%d", name, cnt)
		}
	}

	// Apply chats schema.
	cs, err := chats.NewStore(s.DB())
	if err != nil {
		t.Fatalf("chats.NewStore (first call): %v", err)
	}

	// Now all chat tables must exist.
	for _, name := range chatTables {
		var cnt int
		err := s.DB().QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
		).Scan(&cnt)
		if err != nil {
			t.Fatalf("query sqlite_master for %s after migration: %v", name, err)
		}
		if cnt != 1 {
			t.Fatalf("expected table %q to exist after chats.NewStore; got count=%d", name, cnt)
		}
	}

	// Smoke-test: insert and read back.
	ctx := context.Background()
	c, err := cs.Create(ctx, "app1", "agent", "", "Migration Smoke")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := cs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Migration Smoke" {
		t.Errorf("expected title 'Migration Smoke', got %q", got.Title)
	}

	// Idempotency: a second NewStore call on the same DB must succeed.
	if _, err := chats.NewStore(s.DB()); err != nil {
		t.Fatalf("chats.NewStore (second call) must be idempotent: %v", err)
	}

	// And the existing data is still readable.
	got2, err := cs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after second NewStore: %v", err)
	}
	if got2.ID != c.ID {
		t.Fatalf("data lost after second NewStore call: %+v", got2)
	}
}

func TestStore_Fork_DefaultTitle(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	parent, err := cs.Create(ctx, "app1", "agent", "", "Parent")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fork, err := cs.Fork(ctx, parent.ID, "")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if fork.Title != "Parent (fork)" {
		t.Errorf("expected title 'Parent (fork)', got %q", fork.Title)
	}
}

// TestStore_Fork_ClearsActiveClaudeSessionID verifies the fork explicitly
// gets an empty claude_session_id even when the parent has one set, so the
// next turn against the fork allocates a fresh Claude session (the design
// requirement from the plan: "Fork — copy chat + all messages,
// parent_chat_id set, NEW (empty) claude_session_id.").
func TestStore_Fork_ClearsActiveClaudeSessionID(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	parent, err := cs.Create(ctx, "app1", "agent", "", "Parent")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const parentSession = "11111111-2222-4333-8444-555555555555"
	if err := cs.SetClaudeSessionID(ctx, parent.ID, parentSession); err != nil {
		t.Fatalf("SetClaudeSessionID: %v", err)
	}

	fork, err := cs.Fork(ctx, parent.ID, "Fork")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if fork.ClaudeSessionID != "" {
		t.Errorf("fork claude_session_id must be empty even when parent has one set; got %q", fork.ClaudeSessionID)
	}

	// Sanity-check: parent still has its claude_session_id (fork did not mutate it).
	parentReread, err := cs.Get(ctx, parent.ID)
	if err != nil {
		t.Fatalf("Get parent: %v", err)
	}
	if parentReread.ClaudeSessionID != parentSession {
		t.Errorf("parent claude_session_id mutated by Fork: %q", parentReread.ClaudeSessionID)
	}
}

func TestStore_Rename_HappyPath(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "Original Title")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := cs.Rename(ctx, c.ID, "New Title"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	got, err := cs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after Rename: %v", err)
	}
	if got.Title != "New Title" {
		t.Errorf("expected title 'New Title', got %q", got.Title)
	}
}

func TestStore_Rename_NotFound(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	err := cs.Rename(ctx, "nonexistent-id", "Some Title")
	if err == nil {
		t.Fatal("expected error for nonexistent chat")
	}
	if !errors.Is(err, chats.ErrChatNotFound) {
		t.Errorf("expected ErrChatNotFound, got: %v", err)
	}
}

func TestStore_Rename_EmptyTitle(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, err := cs.Create(ctx, "app1", "agent", "", "Original Title")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = cs.Rename(ctx, c.ID, "   ")
	if err == nil {
		t.Fatal("expected error for empty/whitespace title")
	}
	if errors.Is(err, chats.ErrChatNotFound) {
		t.Errorf("expected validation error, not ErrChatNotFound")
	}
}

// TestStore_Create_ValidatesArgs checks I4: empty app or empty room must be
// rejected before any INSERT runs. Whitespace-only is treated as empty.
func TestStore_Create_ValidatesArgs(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	t.Run("empty app", func(t *testing.T) {
		_, err := cs.Create(ctx, "", "agent", "", "title")
		if err == nil {
			t.Fatal("expected error for empty app")
		}
		if !strings.Contains(err.Error(), "empty app") {
			t.Errorf("expected 'empty app' in error, got %q", err.Error())
		}
	})
	t.Run("whitespace app", func(t *testing.T) {
		_, err := cs.Create(ctx, "   ", "agent", "", "title")
		if err == nil {
			t.Fatal("expected error for whitespace app")
		}
	})
	t.Run("empty room", func(t *testing.T) {
		_, err := cs.Create(ctx, "app1", "", "", "title")
		if err == nil {
			t.Fatal("expected error for empty room")
		}
		if !strings.Contains(err.Error(), "empty room") {
			t.Errorf("expected 'empty room' in error, got %q", err.Error())
		}
	})
}

// TestStore_Resolve_ReturnsCreatedTrue_OnNew pins down I5: the bool return
// is true only when a new row was inserted.
func TestStore_Resolve_ReturnsCreatedTrue_OnNew(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	c, created, err := cs.Resolve(ctx, "app1", "agent", "scope", "Title")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !created {
		t.Error("expected created=true for first Resolve")
	}
	if c == nil || c.ID == "" {
		t.Fatal("expected non-nil chat with non-empty ID")
	}
	// And the row really exists.
	got, err := cs.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	if got.Title != "Title" {
		t.Errorf("expected title 'Title', got %q", got.Title)
	}
}

// TestStore_Resolve_ReturnsCreatedFalse_OnExisting pins down I5: the bool
// is false when an existing row matches.
func TestStore_Resolve_ReturnsCreatedFalse_OnExisting(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	first, err := cs.Create(ctx, "app1", "agent", "scope", "Original")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, created, err := cs.Resolve(ctx, "app1", "agent", "scope", "Different Title")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if created {
		t.Error("expected created=false when chat exists")
	}
	if c.ID != first.ID {
		t.Errorf("expected existing ID %s, got %s", first.ID, c.ID)
	}
	if c.Title != "Original" {
		t.Errorf("title must remain 'Original' (Resolve never overwrites): got %q", c.Title)
	}
}

// TestStore_SchemaVersion_RejectsUnknown checks C3: opening a DB whose
// PRAGMA user_version doesn't match expectedSchemaVersion fails loudly.
// We pre-poison user_version to 99 before NewStore so the version check
// must reject the open.
func TestStore_SchemaVersion_RejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schema-mismatch.db")

	// Open the DB directly and set user_version=99 (skipping NewStore).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Now open via the real path. NewStore must reject the schema version.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	_, err = chats.NewStore(s.DB())
	if err == nil {
		t.Fatal("expected error for mismatched schema version, got nil")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Errorf("expected 'schema version' in error, got %q", err.Error())
	}
}

// TestStore_SchemaVersion_PreservedOnReopen checks the happy path: open,
// close, reopen — the version stays at chats.ExpectedSchemaVersion and
// NewStore succeeds without complaint.
func TestStore_SchemaVersion_PreservedOnReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schema-roundtrip.db")

	// First open: applies schema, stamps user_version.
	{
		s, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		if _, err := chats.NewStore(s.DB()); err != nil {
			t.Fatalf("first NewStore: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	// Reopen: NewStore must succeed and user_version is preserved.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open (reopen): %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := chats.NewStore(s.DB()); err != nil {
		t.Fatalf("NewStore on reopen: %v", err)
	}

	var v int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("PRAGMA scan: %v", err)
	}
	if v != chats.ExpectedSchemaVersion {
		t.Errorf("expected user_version=%d, got %d", chats.ExpectedSchemaVersion, v)
	}
}

// TestStore_SchemaVersion_MigratesV1ToCurrent simulates opening a DB written
// by a pre-v2 build: pre-stamp user_version=1, create only the v1 tables
// (chats, chat_messages, chat_locks), and verify NewStore upgrades the DB
// to ExpectedSchemaVersion by adding chat_pty_sessions and
// chat_input_queue without losing the prior rows.
func TestStore_SchemaVersion_MigratesV1ToCurrent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schema-v1.db")

	// Pre-stamp the DB as v1 with only the v1 tables present, so the
	// migration path actually has work to do (the v1 DDL is a subset
	// of the current embedded DDL).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	v1DDL := `
		CREATE TABLE chats (
		    id TEXT NOT NULL PRIMARY KEY,
		    app_id TEXT NOT NULL,
		    room TEXT NOT NULL,
		    scope_key TEXT NOT NULL DEFAULT '',
		    title TEXT NOT NULL,
		    status TEXT NOT NULL,
		    claude_session_id TEXT,
		    parent_chat_id TEXT,
		    session_id TEXT,
		    created_at INTEGER NOT NULL,
		    updated_at INTEGER NOT NULL,
		    last_active_at INTEGER NOT NULL
		) STRICT;
		CREATE TABLE chat_messages (
		    chat_id TEXT NOT NULL,
		    seq INTEGER NOT NULL,
		    role TEXT NOT NULL CHECK (role IN ('user','assistant','system','tool')),
		    content TEXT NOT NULL,
		    metadata TEXT,
		    created_at INTEGER NOT NULL,
		    PRIMARY KEY (chat_id, seq)
		) STRICT;
		CREATE TABLE chat_locks (
		    chat_id TEXT NOT NULL PRIMARY KEY,
		    owner_pid INTEGER NOT NULL,
		    owner_host TEXT NOT NULL,
		    acquired_at INTEGER NOT NULL,
		    heartbeat_at INTEGER NOT NULL
		) STRICT;
		INSERT INTO chats (id, app_id, room, scope_key, title, status,
		    claude_session_id, parent_chat_id, session_id,
		    created_at, updated_at, last_active_at)
		VALUES ('LEGACY01', 'app1', 'agent', '', 'pre-migration', 'active',
		    '', NULL, NULL, 1, 1, 1);
		PRAGMA user_version = 1;`
	if _, err := db.Exec(v1DDL); err != nil {
		t.Fatalf("seed v1 DB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Now open via the real path. NewStore must upgrade in place.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	if err != nil {
		t.Fatalf("NewStore on v1 DB: %v", err)
	}

	// Version stamped forward.
	var v int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("PRAGMA scan: %v", err)
	}
	if v != chats.ExpectedSchemaVersion {
		t.Errorf("expected user_version=%d after migration, got %d", chats.ExpectedSchemaVersion, v)
	}

	// Pre-existing row survived.
	got, err := cs.Get(context.Background(), "LEGACY01")
	if err != nil {
		t.Fatalf("Get legacy chat: %v", err)
	}
	if got.Title != "pre-migration" {
		t.Errorf("legacy row lost: title=%q", got.Title)
	}

	// New tables exist.
	for _, table := range []string{"chat_pty_sessions", "chat_input_queue"} {
		var name string
		err := s.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("expected table %s to exist post-migration: %v", table, err)
		}
	}
}

// TestStore_AppendMessage_ConcurrentSeqUniqueness exercises the seq-allocation
// transaction under concurrency: 8 goroutines each append 50 messages to the
// same chat. We assert no errors, total messages == 400, and that the
// resulting seqs cover [0, 399] with no gaps or duplicates. Run with -race
// to also catch any latent unsynchronised access in the Store.
//
// SQLite serialises writes (busy_timeout=5000ms is configured by the parent
// store package), so contention is expected — but the AppendMessage
// transaction must atomically read MAX(seq) and INSERT under that
// serialisation, otherwise two goroutines can pick the same seq and one of
// the inserts will violate the (chat_id, seq) PK.
func TestStore_AppendMessage_ConcurrentSeqUniqueness(t *testing.T) {
	cs, _ := openTestStore(t)
	ctx := context.Background()

	chat, err := cs.Create(ctx, "app1", "agent", "", "concurrent-seq")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const goroutines = 8
	const perGoroutine = 50
	const total = goroutines * perGoroutine

	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, aerr := cs.AppendMessage(ctx, chat.ID, "user",
					"msg", map[string]any{"g": g, "i": i}); aerr != nil {
					errsMu.Lock()
					errs = append(errs, aerr)
					errsMu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("expected no errors from concurrent AppendMessage, got %d (first: %v)", len(errs), errs[0])
	}

	// Verify total count and seq integrity by reading the transcript back.
	msgs, err := cs.Transcript(ctx, chat.ID, 0)
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(msgs) != total {
		t.Fatalf("expected %d messages, got %d", total, len(msgs))
	}
	seqs := make([]int, len(msgs))
	for i, m := range msgs {
		seqs[i] = m.Seq
	}
	sort.Ints(seqs)
	for i, s := range seqs {
		if s != i {
			t.Fatalf("seq gap or duplicate at index %d: got %d, want %d (full sorted run starts %v)",
				i, s, i, seqs[:min(20, len(seqs))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
