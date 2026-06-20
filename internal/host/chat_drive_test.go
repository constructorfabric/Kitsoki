package host_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/clock"
	"kitsoki/internal/host"
)

func TestChatDrive_NoStore(t *testing.T) {
	t.Parallel()
	res, err := host.ChatDriveHandler(context.Background(), map[string]any{
		"chat_id": "X", "payload": "y",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "no chat store wired") {
		t.Errorf("expected 'no chat store wired' error, got %q", res.Error)
	}
}

func TestChatDrive_MissingArgs(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	ctx := host.WithChatStore(context.Background(), cs)
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no chat_id", map[string]any{"payload": "y"}, "chat_id"},
		{"no payload", map[string]any{"chat_id": "X"}, "payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := host.ChatDriveHandler(ctx, tc.args)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if !strings.Contains(res.Error, tc.want) {
				t.Errorf("expected error mentioning %q, got %q", tc.want, res.Error)
			}
		})
	}
}

func TestChatDrive_ChatNotFound(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	ctx := host.WithChatStore(context.Background(), cs)
	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id": "NOPE", "payload": "y",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "chat_not_found") {
		t.Errorf("expected chat_not_found, got %q", res.Error)
	}
}

// TestChatDrive_AsyncReturnsDriveID is the await:false happy path: the
// handler enqueues and returns drive_id without running the turn.
func TestChatDrive_AsyncReturnsDriveID(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id":   "chat-1",
		"payload":   "do the thing",
		"transport": "jira",
		"thread":    "PROJ-1#42",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	driveID, _ := res.Data["drive_id"].(string)
	if driveID == "" {
		t.Fatal("expected drive_id in Result.Data")
	}
	if res.Data["chat_id"] != "chat-1" {
		t.Errorf("chat_id = %v, want chat-1", res.Data["chat_id"])
	}
	if _, ok := res.Data["enqueued_at"].(int64); !ok {
		t.Errorf("enqueued_at should be int64, got %T", res.Data["enqueued_at"])
	}
	// Drive row exists and is pending — async path does NOT dispatch.
	d, err := cs.GetDrive(ctx, driveID)
	if err != nil {
		t.Fatalf("GetDrive: %v", err)
	}
	if d.Status != "pending" {
		t.Errorf("drive status = %q, want pending (async path leaves it pending)", d.Status)
	}
	if d.Transport != "jira" || d.Thread != "PROJ-1#42" {
		t.Errorf("transport/thread not propagated: transport=%q thread=%q", d.Transport, d.Thread)
	}
	// Async result must NOT carry status/result_text/error.
	for _, key := range []string{"status", "result_seq", "result_text", "error"} {
		if _, ok := res.Data[key]; ok {
			t.Errorf("async result should not include %q", key)
		}
	}
}

// TestChatDrive_AwaitSuccess runs the await:true path against the fake
// claude binary and verifies the handler returns the run result inline.
func TestChatDrive_AwaitSuccess(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithClaudeRunner(host.WithChatStore(context.Background(), cs), stubAgentRunner())

	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id": "chat-1",
		"payload": "hi",
		"await":   true,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if res.Data["status"] != "done" {
		t.Errorf("status = %v, want done", res.Data["status"])
	}
	if text, _ := res.Data["result_text"].(string); text == "" {
		t.Error("result_text should be populated on done")
	}
	if _, ok := res.Data["result_seq"].(int); !ok {
		t.Errorf("result_seq should be int, got %T", res.Data["result_seq"])
	}
	// Drive row reflects terminal done.
	driveID, _ := res.Data["drive_id"].(string)
	d, _ := cs.GetDrive(ctx, driveID)
	if d.Status != "done" {
		t.Errorf("drive status = %q, want done", d.Status)
	}
}

// TestChatDrive_AwaitChatBusyReportsBusy: await:true with the lock held
// elsewhere returns chat_busy in Result.Error and leaves the drive
// pending.
//
// Uses an injected fake clock so the dispatcher's lock-contention
// retry loop completes deterministically without burning the
// production-default 300 s on real wall-clock sleeps. The fake never
// frees the lock, so we drive the clock forward in 1 s ticks until
// the retry budget elapses and the handler surfaces chat_busy. The
// companion TestDispatchDriveWithTimeout_TimesOutWithoutFree exercises
// the dispatcher directly with the same pattern.
func TestChatDrive_AwaitChatBusyReportsBusy(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	cs.withLockErr = host.NewChatBusyError(errors.New("chats: chat busy"))

	clk := clock.NewFake(time.Unix(0, 0))
	ctx := host.WithClock(host.WithChatStore(context.Background(), cs), clk)

	type result struct {
		res host.Result
		err error
	}
	done := make(chan result, 1)
	go func() {
		// timeout_seconds: 2 → at most a couple of retry ticks before
		// the dispatcher gives up and surfaces ErrChatBusy. Kept small
		// so the advance-loop below is bounded.
		r, err := host.ChatDriveHandler(ctx, map[string]any{
			"chat_id":         "chat-1",
			"payload":         "hi",
			"await":           true,
			"timeout_seconds": 2,
		})
		done <- result{r, err}
	}()

	// Drive the dispatcher's retry loop forward in 1-second ticks. Each
	// iteration of DispatchDriveWithTimeout parks on clk.After exactly
	// once, so BlockUntilContext(1) succeeds as long as the handler is
	// still retrying. Once the budget elapses the goroutine returns and
	// stops parking — BlockUntilContext then times out and we stop.
	advanceCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	for i := 0; i < 5; i++ {
		if err := clk.BlockUntilContext(advanceCtx, 1); err != nil {
			break
		}
		clk.Advance(1 * time.Second)
	}

	var got result
	select {
	case got = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ChatDriveHandler did not return after retry budget elapsed")
	}

	if got.err != nil {
		t.Fatalf("unexpected Go error: %v", got.err)
	}
	if !strings.Contains(got.res.Error, "chat_busy") {
		t.Errorf("expected chat_busy in Result.Error, got %q", got.res.Error)
	}
	driveID, _ := got.res.Data["drive_id"].(string)
	if driveID == "" {
		t.Fatal("drive_id should still be returned on chat_busy")
	}
	d, _ := cs.GetDrive(ctx, driveID)
	if d.Status != "pending" {
		t.Errorf("drive status after chat_busy = %q, want pending", d.Status)
	}
}

// TestChatDrive_AwaitDriveFailedSurfacesError: claude exits non-zero →
// Result.Error contains drive_failed and Data.status == failed.
func TestChatDrive_AwaitDriveFailedSurfacesError(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithClaudeRunner(host.WithChatStore(context.Background(), cs), stubOneShotRunner())

	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id": "chat-1",
		"payload": "FAIL please",
		"await":   true,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "drive_failed") {
		t.Errorf("expected drive_failed in Result.Error, got %q", res.Error)
	}
	if res.Data["status"] != "failed" {
		t.Errorf("status = %v, want failed", res.Data["status"])
	}
	if msg, _ := res.Data["error"].(string); msg == "" {
		t.Error("Result.Data.error should be populated on failed")
	}
}

func TestChatDrive_RegisteredAsBuiltin(t *testing.T) {
	t.Parallel()
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.chat.drive"); !ok {
		t.Fatal("host.chat.drive was not registered by RegisterBuiltins")
	}
}

// TestChatDrive_ChatRefResolvesByPosition exercises the chat_ref →
// resolve_ref → chat_id path with a simple positional input.
func TestChatDrive_ChatRefResolvesByPosition(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{
		ID: "chat-1", AppID: "bugfix", Room: "live", Status: "active",
		Title: "first",
	})
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_ref": "1",
		"app":      "bugfix",
		"room":     "live",
		"payload":  "x",
		"skip_llm": true,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if res.Data["chat_id"] != "chat-1" {
		t.Errorf("chat_id = %v, want chat-1", res.Data["chat_id"])
	}
}

func TestChatDrive_ChatRefAndChatIDMutuallyExclusive(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1"})
	ctx := host.WithChatStore(context.Background(), cs)
	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id":  "chat-1",
		"chat_ref": "1",
		"payload":  "x",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got %q", res.Error)
	}
}

func TestChatDrive_ChatRefRequiresAppRoom(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	ctx := host.WithChatStore(context.Background(), cs)
	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_ref": "1",
		"payload":  "x",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "app and room") {
		t.Errorf("expected 'app and room' guidance, got %q", res.Error)
	}
}

// TestChatDrive_PersistsOnCompleteMetadata: when the orchestrator
// pre-injects __on_complete / __origin_*, the handler records them on
// the drive row so a future consumer can fire the chain.
func TestChatDrive_PersistsOnCompleteMetadata(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithChatStore(context.Background(), cs)

	chain := `[{"set":{"foo":"bar"}}]`
	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id":             "chat-1",
		"payload":             "drive me",
		"__on_complete":       chain,
		"__origin_session_id": "SESS-A",
		"__origin_state":      "bugfix.phase_7",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	driveID, _ := res.Data["drive_id"].(string)
	d, err := cs.GetDrive(ctx, driveID)
	if err != nil {
		t.Fatalf("GetDrive: %v", err)
	}
	if d.OnCompleteJSON != chain {
		t.Errorf("OnCompleteJSON = %q, want %q", d.OnCompleteJSON, chain)
	}
	if d.OriginSessionID != "SESS-A" {
		t.Errorf("OriginSessionID = %q, want SESS-A", d.OriginSessionID)
	}
	if d.OriginState != "bugfix.phase_7" {
		t.Errorf("OriginState = %q, want bugfix.phase_7", d.OriginState)
	}
}

// TestChatDrive_OnCompleteAcceptsParsedSlice: the orchestrator may
// pass the on_complete chain as []any (the parsed []app.Effect form)
// rather than a JSON string. The handler must marshal it.
func TestChatDrive_OnCompleteAcceptsParsedSlice(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id": "chat-1",
		"payload": "drive me",
		"__on_complete": []any{
			map[string]any{"set": map[string]any{"x": 1}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	driveID, _ := res.Data["drive_id"].(string)
	d, _ := cs.GetDrive(ctx, driveID)
	if d.OnCompleteJSON == "" {
		t.Errorf("OnCompleteJSON should be non-empty after slice input, got empty")
	}
	if !strings.Contains(d.OnCompleteJSON, `"set"`) {
		t.Errorf("OnCompleteJSON should contain 'set', got %q", d.OnCompleteJSON)
	}
}

// TestChatDrive_OnCompleteEmptySliceLeavesBlank: an empty []any from
// the orchestrator (e.g. an effect with no on_complete declared)
// must not populate the drive's on_complete column.
func TestChatDrive_OnCompleteEmptySliceLeavesBlank(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Status: "active"})
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.ChatDriveHandler(ctx, map[string]any{
		"chat_id":       "chat-1",
		"payload":       "x",
		"__on_complete": []any{},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	driveID, _ := res.Data["drive_id"].(string)
	d, _ := cs.GetDrive(ctx, driveID)
	if d.OnCompleteJSON != "" {
		t.Errorf("OnCompleteJSON should be empty for []any{}, got %q", d.OnCompleteJSON)
	}
}
