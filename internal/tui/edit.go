// edit.go — the Esc-menu "Edit mode" overlay (LLM-driven authoring).
//
// Flow:
//
//   editPhaseInput     → user types a free-text proposal in the prompt
//   editPhaseThinking  → Claude is rewriting app.yaml (spinner)
//   editPhaseReview    → diff is shown; keys: a=apply, r=refine, c=cancel
//   editPhaseApplying  → file is being written + orchestrator reloaded
//
// The edit model is a tiny state holder. The root model owns the
// prompt textinput, the spinner, and the transcript, so this file is
// mostly enums + a couple of tea.Cmd factories. Async work is wrapped
// in editProposalReadyMsg / editApplyDoneMsg so the root can react.
package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/authoring"
)

type editPhase int

const (
	editPhaseInput editPhase = iota
	editPhaseThinking
	editPhaseReview
	editPhaseApplying
)

// editModel is the per-edit-session state. Reset on every Open().
type editModel struct {
	phase    editPhase
	proposal *authoring.Proposal
}

func newEditModel() editModel { return editModel{phase: editPhaseInput} }

// Open resets the model so a fresh proposal can begin. If a previous
// Proposal is still attached (user pressed Esc / 'c' / 'r' before
// applying), its shadow directory is cleaned up first so we don't
// leak temp dirs.
func (e *editModel) Open() {
	if e.proposal != nil {
		_ = authoring.Discard(e.proposal)
	}
	e.phase = editPhaseInput
	e.proposal = nil
}

// editProposalReadyMsg is emitted when authoring.Propose returns.
type editProposalReadyMsg struct {
	proposal *authoring.Proposal
	err      error
}

// editApplyDoneMsg is emitted when authoring.Apply finishes (the
// orchestrator reload happens in the root, not in the cmd).
type editApplyDoneMsg struct {
	err error
}

// proposeCmd asynchronously calls authoring.Propose. Cancellation
// must be handled by the caller via the supplied context. runCtx
// carries the player's current state + rendered view so Claude can
// pin the proposal to the right file.
func proposeCmd(ctx context.Context, appPath, proposalText string, runCtx *authoring.Context) tea.Cmd {
	return func() tea.Msg {
		p, err := authoring.Propose(ctx, appPath, proposalText, runCtx)
		return editProposalReadyMsg{proposal: p, err: err}
	}
}

// applyCmd asynchronously calls authoring.Apply. Reload happens in
// the root after this returns so the orchestrator swap stays on the
// TUI's main goroutine.
func applyCmd(p *authoring.Proposal) tea.Cmd {
	return func() tea.Msg {
		return editApplyDoneMsg{err: authoring.Apply(p)}
	}
}

// renderDiffForTranscript wraps a unified diff in a Markdown ```diff
// fence so Glamour highlights it naturally inside the transcript.
func renderDiffForTranscript(diff string) string {
	if strings.TrimSpace(diff) == "" {
		return "_(no changes — Claude returned the file unchanged)_"
	}
	return "```diff\n" + diff + "```"
}

// editReviewHint is the prompt-line caption shown during review.
var editReviewHint = lipgloss.NewStyle().Foreground(colorMuted).Render(
	"apply [a] · refine [r] · cancel [c]")

// editInputHint is the prompt-line caption shown during input.
var editInputHint = lipgloss.NewStyle().Foreground(colorMuted).Render(
	"describe a change (Enter to send, Esc to cancel)")
