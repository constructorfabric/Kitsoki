package tui_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/journal"
	tuipkg "kitsoki/internal/tui"
)

// TestWelcomeBlockPrintsAtStartup confirms the welcome banner is
// queued onto the transcript for the first FlushPending. The chat
// view contract (post-scrollback refactor): users see a Claude-Code-
// style intro that scrolls off as they take turns.
func TestWelcomeBlockPrintsAtStartup(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// The welcome lives in m.transcript.pending until the first
	// Update flushes it. AllContent doesn't see pending (renders
	// from entries only), so we ask the model for its pending dump
	// via a test seam.
	rm, _ := tuipkg.ExtractRootModel(m)
	pending := tuipkg.PendingTranscriptForTest(rm)
	joined := strings.Join(pending, "\n")
	require.Contains(t, joined, "kitsoki",
		"welcome block should advertise the kitsoki name; got %q", joined)
	require.Contains(t, joined, "/help",
		"welcome block should hint at /help")
	require.Contains(t, joined, "session",
		"welcome block should show session/state status")
}

// TestWelcomeBlockSuppressedOnResume confirms the welcome banner is NOT
// re-emitted on a --continue resume. It is start-of-session boilerplate
// already in the prior session's scrollback; re-printing it after the
// reconstructed transcript dropped a mis-styled box (it inherited the
// trailing agent body's background) into the middle of the history.
func TestWelcomeBlockSuppressedOnResume(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)

	// A single reconstructed turn stands in for resumed history; the
	// resume options mark the model as resumed.
	entries := []journal.Entry{{
		Kind: journal.KindViewRendered,
		Turn: 1,
		Body: []byte(`{"view_text":"a prior view","user_input":"look"}`),
	}}
	m := tuipkg.NewRootModel(orch, sid, "", "",
		tuipkg.WithResumedTranscript(entries),
	)

	rm, _ := tuipkg.ExtractRootModel(m)
	pending := strings.Join(tuipkg.PendingTranscriptForTest(rm), "\n")
	require.NotContains(t, pending, "/help",
		"welcome banner must be suppressed on resume; pending = %q", pending)
	require.NotContains(t, pending, "list commands",
		"welcome banner must be suppressed on resume")
}

func TestStartupNoticePrintsAtStartup(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := tuipkg.NewRootModel(orch, sid, "", "",
		tuipkg.WithStartupNotice("(kitsoki: project files may need refresh)"),
	)

	rm, _ := tuipkg.ExtractRootModel(m)
	pending := strings.Join(tuipkg.PendingTranscriptForTest(rm), "\n")
	require.Contains(t, pending, "project files may need refresh")
	require.Contains(t, pending, "!")
}

func TestStartupNoticePrintsAfterInitialView(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	m := tuipkg.NewRootModel(orch, sid, "", "opening room body",
		tuipkg.WithStartupNotice("(kitsoki: run setup story)"),
	)

	rm, _ := tuipkg.ExtractRootModel(m)
	pending := strings.Join(tuipkg.PendingTranscriptForTest(rm), "\n")
	bodyIdx := strings.Index(pending, "opening room body")
	noticeIdx := strings.Index(pending, "run setup story")
	require.NotEqual(t, -1, bodyIdx, "pending transcript missing initial view:\n%s", pending)
	require.NotEqual(t, -1, noticeIdx, "pending transcript missing startup notice:\n%s", pending)
	require.Greater(t, noticeIdx, bodyIdx, "startup notice should remain visible after the initial room body:\n%s", pending)
}
