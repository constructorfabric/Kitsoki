// sessions_panel_flow_test.go — end-to-end TUI integration tests for
// the foyer "meta sessions" overlay.
//
// These tests drive the full keystroke flow through RootModel.Update,
// rather than poking at the panel model in isolation:
//
//	Esc            → openSessionsPanel
//	(async)        → sessionsPanelLoadCmd → handleSessionsPanelLoaded
//	arrow / Enter  → updateSessionsPanel → handleSessionsPanelChoice
//	(async)        → metaResumeCmd → handleMetaEnterDone (→ ModeMeta)
//
// The intent is to catch regressions where the panel model itself is
// fine but the RootModel wiring (mode flip, command routing, async
// message handlers) drifts away from what the panel needs.
package tui_test

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

// seedSessionsPanelChat populates the fake chat store with one meta
// chat keyed against the cloak orchestrator's AppID and the "story"
// mode declared by singleStoryMode(). Returns the seeded chat ID so
// tests can assert it round-trips through the resume flow.
func seedSessionsPanelChat(t *testing.T, m tea.Model, store *fakeMetaChatStore, id, preview string) string {
	t.Helper()
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	appID := rm.AppID()
	store.seedChat(&fakeMetaChat{
		id:        id,
		appID:     appID,
		room:      "meta:story",
		scopeKey:  "main",
		title:     "improve the story",
		updatedAt: time.Now(),
		appends: []struct{ Role, Text string }{
			{"user", preview},
		},
	})
	return id
}

// TestSessionsPanelFlow_HappyPathResume drives the entire keystroke
// sequence end-to-end:
//
//  1. Esc opens the system menu (ModeMenu).
//  2. Hotkey '3' picks "Meta sessions" — the entry order under
//     cloak's singleStoryMode is [Exit, meta:story, Meta sessions],
//     so the panel sits at row 3.
//  3. Processing the resulting cmd runs sessionsPanelLoadCmd, which
//     emits sessionsPanelLoadedMsg with the seeded listing.
//  4. handleSessionsPanelLoaded flips the mode to ModeMetaSessions
//     and opens the panel — verified via SessionsPanelActive +
//     SessionsPanelView.
//  5. Enter on the highlighted row emits sessionsPanelChoiceMsg
//     carrying the seeded chatID + modeName.
//  6. handleSessionsPanelChoice fires metaResumeCmd; processing it
//     calls Controller.EnterByChatID through the fake store and
//     emits metaEnterDoneMsg.
//  7. handleMetaEnterDone activates ModeMeta with the resumed
//     session's banner.
//
// We never reach a real DB or a real claude: the fake chat store and
// fake oracle (wired via buildMetaModeModel) cover both seams.
func TestSessionsPanelFlow_HappyPathResume(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")
	chatID := seedSessionsPanelChat(t, m, store, "panelchat01", "what should I do about the foyer?")

	// 1. Esc opens the system menu.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, tuipkg.ModeMenu, extractMode(t, m),
		"Esc from ModeOnPath should enter ModeMenu")
	require.Contains(t, tuipkg.MenuSystemView(mustRoot(t, m)), "Meta sessions",
		"the 'Meta sessions' row must appear once an app declares meta_modes")

	// 2. Hotkey 3 = Meta sessions row. Picking it closes the menu
	// overlay and returns a cmd that runs sessionsPanelLoadCmd
	// (which calls Controller.ListChats off the UI goroutine).
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	// 3. Drain commands until the panel is loaded. processCommands
	// chases menuSystemChoiceMsg → handleMenuSystemChoice →
	// openSessionsPanel → sessionsPanelLoadCmd → handleSessionsPanelLoaded.
	m = processCommands(m, cmd, 10)

	// 4. The handler should have flipped the mode and opened the panel
	// with the seeded row in it.
	require.Equal(t, tuipkg.ModeMetaSessions, extractMode(t, m),
		"handleSessionsPanelLoaded should switch the RootModel into ModeMetaSessions")
	require.True(t, tuipkg.SessionsPanelActive(mustRoot(t, m)),
		"panel must be active after the async load completes")
	panelView := tuipkg.SessionsPanelView(mustRoot(t, m))
	require.Contains(t, panelView, "meta sessions",
		"the panel header should render")
	require.Contains(t, panelView, "panelcha",
		"the 8-char ID prefix for the seeded chat should appear in the panel")
	require.Contains(t, panelView, "what should I do",
		"the seeded preview should appear in the panel")
	require.Contains(t, panelView, "story",
		"the mode name should appear in the panel")

	// 5. With the only seeded row already highlighted, Enter emits a
	// sessionsPanelChoiceMsg and the panel closes itself.
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd, "Enter on a populated panel must emit a choice cmd")

	// 6. Processing that cmd routes the choice through
	// handleSessionsPanelChoice → metaResumeCmd →
	// EnterByChatID → metaEnterDoneMsg → handleMetaEnterDone.
	m = processCommands(m, cmd, 10)

	// 7. We should now be in ModeMeta with the seeded chat resumed.
	require.Equal(t, tuipkg.ModeMeta, extractMode(t, m),
		"a successful resume must land in ModeMeta")
	require.False(t, tuipkg.SessionsPanelActive(mustRoot(t, m)),
		"the panel overlay must be torn down once resume completes")
	transcript := extractTranscript(t, m)
	require.Contains(t, transcript, "meta:story",
		"the resumed mode's banner must be appended to the transcript")

	// The resume targeted the right chat. We assert against the
	// resumed session's chat ID rather than the fake store's `chat`
	// shortcut field — that shortcut is set by ResolveMeta's auto-
	// create branch, not by the seeded-row GetMeta path the resume
	// flow takes, so checking it would be the wrong observable.
	require.Equal(t, chatID, tuipkg.MetaSessionChatID(mustRoot(t, m)),
		"resume should have targeted exactly the seeded chat")
	// store.chat must NOT have been populated — seedChat appends
	// directly to rows, and EnterByChatID looks up via GetMeta. If
	// store.chat became non-nil we'd know the resume took the
	// legacy auto-create path instead of the seeded row.
	require.Nil(t, store.chat,
		"resume must hit GetMeta, not ResolveMeta's auto-create branch")
}

// TestSessionsPanelFlow_EscClosesPanel covers the dismiss path: open
// the panel as above, then press Esc inside it. The mode should fall
// back to ModeOnPath and no resume command should fire.
func TestSessionsPanelFlow_EscClosesPanel(t *testing.T) {
	m, store, _ := buildMetaModeModel(t, singleStoryMode(), "")
	seedSessionsPanelChat(t, m, store, "panelchat02", "preview text")

	// Open the menu and pick "Meta sessions" (row 3 after the
	// legacy 'Report bug' stub at row 2 was removed).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = processCommands(m, cmd, 10)
	require.Equal(t, tuipkg.ModeMetaSessions, extractMode(t, m),
		"panel must be open before we test the Esc dismiss")
	require.True(t, tuipkg.SessionsPanelActive(mustRoot(t, m)))

	// Esc inside the panel: the panel's Update marks itself inactive,
	// updateSessionsPanel notices the mismatch and flips mode back to
	// ModeOnPath. No cmd should be returned (nothing async to chase).
	var escCmd tea.Cmd
	m, escCmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.Nil(t, escCmd, "Esc inside the panel must not emit a follow-up command")

	require.Equal(t, tuipkg.ModeOnPath, extractMode(t, m),
		"Esc should drop us back into ModeOnPath")
	require.False(t, tuipkg.SessionsPanelActive(mustRoot(t, m)),
		"the panel must be closed after Esc")

	// The seeded row has no transcript replay — i.e., handleMetaEnterDone
	// never ran — confirming resume did not fire.
	require.NotContains(t, extractTranscript(t, m), "preview text",
		"the seeded chat's preview must not appear in the transcript when the user dismisses with Esc")
}
