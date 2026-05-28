package tui_test

// TKT-001 reproduction: pressing ESC to dismiss an action_required banner while
// ModeAwaitingLLM is active blocks the Bubble Tea Update() loop for the full
// duration of the SQLite write that MarkNotificationRead performs.
//
// Root cause: tui.go banner-dismiss ESC handler calls
// m.jobStore.MarkNotificationRead(ctx, n.ID) synchronously inside Update().
//
// Fix: move the call into a tea.Cmd closure so Update() returns immediately
// and the DB write happens outside the event-loop frame.
//
// Run:
//
//	cd internal/tui && go test -v -run TestTKT001 .

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/jobs"
	tuipkg "kitsoki/internal/tui"
)

// TestTKT001_EscBannerDismiss_BlocksEventLoop is the reproduction test.
//
// It puts the model in ModeAwaitingLLM with an action_required notification
// banner visible, acquires the single SQLite connection via db.BeginTx so that
// any synchronous MarkNotificationRead call inside Update() will block, then
// presses ESC and measures elapsed time.
//
// While the bug is present (synchronous call), elapsed >= 150 ms and the
// assertion passes. After the correct fix (async tea.Cmd), Update() returns in
// microseconds and the assertion fails — proving the fix is in place.
func TestTKT001_EscBannerDismiss_BlocksEventLoop(t *testing.T) {
	db, js := openInboxTestDB(t)
	// Force a single connection so BeginTx blocks any concurrent DB write.
	db.SetMaxOpenConns(1)

	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tuipkg.NewRootModel(orch, sid, "", initialView, tuipkg.WithJobStore(js))

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	// Insert an action_required notification so the banner is visible.
	n := jobs.Notification{
		ID:        "tkt001-action",
		SessionID: sid,
		CreatedAt: time.Now(),
		Severity:  jobs.SeverityActionRequired,
		Title:     "confirm deploy",
	}
	ctx := context.Background()
	require.NoError(t, js.InsertNotification(ctx, &n))

	// Feed the notification into the model's inbox sub-model.
	updated, _ := rm.Update(tuipkg.InboxRefreshedMsg([]jobs.Notification{n}))
	rm, ok = tuipkg.ExtractRootModel(updated)
	require.True(t, ok)

	// Verify the banner is live before entering the timed section.
	require.NotEmpty(t, tuipkg.InboxActionRequiredBannerForTest(28, 14, []jobs.Notification{n}),
		"precondition: action_required notification must produce a visible banner")

	// Put the model in ModeAwaitingLLM — banner dismiss ESC is active in this mode.
	rm = tuipkg.SimulateSlowHarnessTurnStart(rm)
	require.Equal(t, tuipkg.ModeAwaitingLLM, tuipkg.GetMode(rm),
		"precondition: model must be in ModeAwaitingLLM")

	// Acquire the single SQLite connection so that any synchronous DB write
	// inside Update() blocks until this transaction is released.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Press ESC — the banner dismiss path — and time how long Update() takes.
	start := time.Now()
	rm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	elapsed := time.Since(start)

	// Regression guard: Update() must return in microseconds because
	// MarkNotificationRead is dispatched as a tea.Cmd, not called synchronously.
	assert.Less(t, elapsed, 10*time.Millisecond,
		"TKT-001 regression: Update() returned in %v; expected <10ms (sync DB call would block on held transaction)", elapsed)
}
