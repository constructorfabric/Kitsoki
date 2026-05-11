package orchestrator

// Internal tests for completionNotification and truncate — these are in
// package orchestrator (not orchestrator_test) so they can access unexported
// functions.

import (
	"strings"
	"testing"
	"unicode/utf8"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

// TestCompletionNotification_ChatAware exercises the chat-aware branches
// of completionNotification.
func TestCompletionNotification_ChatAware(t *testing.T) {
	t.Run("chat success with answer — title truncated, body present", func(t *testing.T) {
		ev := jobs.JobEvent{
			Status: jobs.JobDone,
			Result: &host.Result{
				Data: map[string]any{
					"chat_id": "chat-123",
					"answer":  "Hello, here is my long answer that goes beyond sixty characters and keeps going",
				},
			},
		}
		j := &jobs.Job{Kind: "host.oracle.talk"}

		sev, title, body := completionNotification(ev, j)

		if sev != jobs.SeveritySuccess {
			t.Errorf("expected SeveritySuccess, got %q", sev)
		}
		if !strings.HasPrefix(title, "Reply ready — Hello") {
			t.Errorf("expected title to start with 'Reply ready — Hello', got %q", title)
		}
		// Title should be truncated (answer > 60 runes → 59 chars + ellipsis)
		runes := []rune(title)
		// "Reply ready — " is 15 runes, truncated answer is at most 60 runes
		if len(runes) > 15+60 {
			t.Errorf("title too long: %d runes (title=%q)", len(runes), title)
		}
		if strings.Contains(title, "\n") {
			t.Error("title must not contain newlines")
		}
		if body == "" {
			t.Error("expected non-empty body for chat-success with answer")
		}
	})

	t.Run("chat success with stdout only — reply ready, body from stdout", func(t *testing.T) {
		ev := jobs.JobEvent{
			Status: jobs.JobDone,
			Result: &host.Result{
				Data: map[string]any{
					"chat_id": "chat-123",
					"stdout":  "some stdout output",
				},
			},
		}
		j := &jobs.Job{Kind: "host.oracle.ask_with_mcp"}

		sev, title, body := completionNotification(ev, j)

		if sev != jobs.SeveritySuccess {
			t.Errorf("expected SeveritySuccess, got %q", sev)
		}
		if title != "Reply ready" {
			t.Errorf("expected 'Reply ready' when no answer, got %q", title)
		}
		if body != "some stdout output" {
			t.Errorf("expected body=stdout, got %q", body)
		}
	})

	t.Run("chat failure — 'Reply failed — <kind>'", func(t *testing.T) {
		ev := jobs.JobEvent{
			Status: jobs.JobFailed,
			Error:  "claude crashed",
			Result: &host.Result{
				Data: map[string]any{
					"chat_id": "chat-123",
				},
			},
		}
		j := &jobs.Job{Kind: "host.oracle.talk", Error: "claude crashed"}

		sev, title, body := completionNotification(ev, j)

		if sev != jobs.SeverityError {
			t.Errorf("expected SeverityError, got %q", sev)
		}
		if !strings.Contains(title, "Reply failed") {
			t.Errorf("expected 'Reply failed' in title, got %q", title)
		}
		if !strings.Contains(title, "host.oracle.talk") {
			t.Errorf("expected kind in title, got %q", title)
		}
		if body != "claude crashed" {
			t.Errorf("expected body=error, got %q", body)
		}
	})

	t.Run("non-chat job done — unchanged 'Job done: <kind>'", func(t *testing.T) {
		ev := jobs.JobEvent{
			Status: jobs.JobDone,
			Result: &host.Result{
				Data: map[string]any{
					"stdout": "hello",
				},
			},
		}
		j := &jobs.Job{Kind: "host.run"}

		sev, title, body := completionNotification(ev, j)

		if sev != jobs.SeveritySuccess {
			t.Errorf("expected SeveritySuccess, got %q", sev)
		}
		if title != "Job done: host.run" {
			t.Errorf("expected 'Job done: host.run', got %q", title)
		}
		if body != "" {
			t.Errorf("expected empty body for non-chat job, got %q", body)
		}
	})

	t.Run("non-chat job failed — unchanged 'Job failed: <kind>'", func(t *testing.T) {
		ev := jobs.JobEvent{
			Status: jobs.JobFailed,
		}
		j := &jobs.Job{Kind: "host.run", Error: "timeout"}

		sev, title, _ := completionNotification(ev, j)

		if sev != jobs.SeverityError {
			t.Errorf("expected SeverityError, got %q", sev)
		}
		if title != "Job failed: host.run" {
			t.Errorf("expected 'Job failed: host.run', got %q", title)
		}
	})
}

// TestCompletionNotification_MultiByteAnswer asserts that a chat-aware
// success notification whose `answer` is 100 multi-byte (CJK / emoji) chars
// gets rune-truncated correctly: the resulting title contains valid UTF-8
// only (no broken multi-byte sequences) and is bounded in rune count.
//
// The truncate() helper takes a rune-count limit (60 for the title), so a
// byte-naive truncation would chop a multi-byte sequence in half. We assert
// utf8.ValidString and a rune-count ceiling of "Reply ready — " (15 runes)
// + 60 = 75 runes for the title.
func TestCompletionNotification_MultiByteAnswer(t *testing.T) {
	// 100 CJK characters — each is 3 bytes in UTF-8, so a byte-truncating
	// implementation would corrupt the output mid-character.
	cjk := strings.Repeat("漢", 100)
	emoji := strings.Repeat("🦀", 100) // 4 bytes each, also exercises supplementary plane

	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"CJK 100 runes", cjk},
		{"emoji 100 runes", emoji},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := jobs.JobEvent{
				Status: jobs.JobDone,
				Result: &host.Result{
					Data: map[string]any{
						"chat_id": "chat-mb",
						"answer":  tc.answer,
					},
				},
			}
			j := &jobs.Job{Kind: "host.oracle.talk"}

			_, title, body := completionNotification(ev, j)

			if !utf8.ValidString(title) {
				t.Fatalf("title is not valid UTF-8: %q", title)
			}
			if !utf8.ValidString(body) {
				t.Fatalf("body is not valid UTF-8: %q", body)
			}
			// Title prefix is "Reply ready — " (15 runes); answer truncated to
			// at most 60 runes → total ceiling 75 runes. Allow ≤80 as a buffer.
			titleRunes := utf8.RuneCountInString(title)
			if titleRunes > 80 {
				t.Errorf("title exceeded 80 runes: %d (%q)", titleRunes, title)
			}
			// Body is truncated to 200 runes; assert the bound and no broken
			// runes (already covered by ValidString).
			bodyRunes := utf8.RuneCountInString(body)
			if bodyRunes > 200 {
				t.Errorf("body exceeded 200 runes: %d", bodyRunes)
			}
		})
	}
}

// TestTruncate verifies the truncate helper.
func TestTruncate(t *testing.T) {
	cases := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"empty", "", 10, ""},
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"one over", "hello!", 5, "hell…"},
		{"strips leading space", "  hi  ", 10, "hi"},
		{"collapses newlines", "a\nb\nc", 20, "a b c"},
		{"multibyte runes", "αβγδε", 3, "αβ…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.input, tc.n)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
			}
		})
	}
}
