package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"kitsoki/internal/tui/blocks"
)

// Frame is "the full screen a human sees" captured as a value instead of
// emitted to the terminal. It is the single seam every headless consumer
// (kitsoki drive's per-turn JSONL, kitsoki shot's screenshots, the MCP
// render.* tools) reads, and it is the same paint the live TUI performs
// for its own bottom region — so the two cannot drift.
//
// Text is the ANSI-stripped, agent-readable projection; ANSI is the
// styled, screenshot-ready twin. They are produced from the same compose
// pass: ansi.Strip(ANSI) == Text by construction (see ComposeFrame and
// its rendering test).
type Frame struct {
	// Text is the ANSI-stripped screen — what an agent reads.
	Text string
	// ANSI is the styled screen — what a screenshot renders.
	ANSI string
	// Width is the column count the frame was composed at.
	Width int
	// Height is the row budget the frame was composed at. The composer
	// does not clip to Height (scrollback, not the frame, owns history);
	// it is recorded so a consumer knows the terminal geometry the frame
	// was paint-equivalent to.
	Height int
	// Metadata is the machine-derived state the agent can reason on
	// without re-parsing Text.
	Metadata FrameMeta
}

// FrameMeta is the typed, machine-derived sidecar to a Frame: the state,
// mode, and allowed-intent menu the operator could pick, plus a small
// world digest. Every field is read straight from the model and the
// machine — no text re-parsing — so a consumer can trust it.
type FrameMeta struct {
	// State is the current state path (e.g. "foyer", "bar.lit").
	State string
	// Mode is the human-readable mode label (normal | meta | off-path |
	// slot-fill | awaiting | …) — the same string the footer status row
	// shows via modeLabel.
	Mode string
	// AllowedIntents is the menu of intents the machine reports valid for
	// the current state+world — the canonical menu an operator could pick.
	AllowedIntents []string
	// WorldDigest is a small snapshot of the current world vars for the
	// agent to reason on. It is the live world map; callers that mutate it
	// must copy first.
	WorldDigest map[string]any
}

// ComposeFrame assembles "the full screen a human sees" at the given
// width/height into a Frame, by calling the EXISTING renderers — the
// transcript's last flushed body for the room region and the same
// chrome assembly RootModel.View() uses for the bottom region. It is
// width-parameterised: a screenshot at 100 cols is byte-identical to a
// real 100-col terminal because the same resize()/compose path runs.
//
// The model is taken by value (RootModel methods are value receivers),
// so resizing the copy to the requested geometry does not disturb live
// state. ANSI is the styled output; Text is its ansi.Strip twin.
func ComposeFrame(m *RootModel, width, height int) Frame {
	// Width fidelity: re-flow the body, prompt, divider, and status row
	// to the requested geometry by routing through the live resize seam
	// on a copy. The live caller passes its own width/height, so this is
	// idempotent for it; a headless caller (--cols/--rows) gets a frame
	// paint-equivalent to a real terminal of that size.
	mm := *m
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	mm.width = width
	mm.height = height
	mm = mm.resize()

	promptLine, bannerLine := composePromptAndBanner(mm)

	// Body region: the last flushed room body (headless callers want the
	// body in the still frame; the live View() leaves it in scrollback).
	var sb strings.Builder
	if body := strings.TrimRight(mm.transcript.LastBody(), "\n"); body != "" {
		sb.WriteString(body)
		sb.WriteString("\n")
	}
	chrome := joinChromeParts(composeChromeParts(mm, width, promptLine, bannerLine))
	sb.WriteString(chrome)

	ansiText := sb.String()
	return Frame{
		Text:     ansi.Strip(ansiText),
		ANSI:     ansiText,
		Width:    width,
		Height:   height,
		Metadata: composeFrameMeta(mm),
	}
}

// composeFrameMeta reads the typed sidecar straight from the model and
// the machine. AllowedIntents is the canonical menu the machine reports
// for the current state+world — the same source inspect/turn use — so
// the agent can trust it without re-parsing the rendered menu.
func composeFrameMeta(m RootModel) FrameMeta {
	meta := FrameMeta{
		State: string(m.currentState),
		Mode:  modeLabel(m.mode),
	}
	if m.orch == nil {
		return meta
	}
	w := m.orch.CurrentWorld(m.sid)
	for _, ai := range m.orch.AllowedIntents(m.currentState, w) {
		meta.AllowedIntents = append(meta.AllowedIntents, ai.Name)
	}
	meta.WorldDigest = w.Vars
	return meta
}

// composePromptAndBanner reproduces RootModel.View()'s prompt-line and
// action-required-banner computation for a (possibly resized) model copy.
// Kept as a helper so the live View() and the headless composer build the
// same prompt bytes from one place. The model is a value copy here, so
// the textarea mutations (SetPromptFunc / SetHeight) don't leak.
func composePromptAndBanner(m RootModel) (promptLine, bannerLine string) {
	// Sync the textarea's rendered height to its current value before
	// pulling its view (mirrors View()).
	promptH := promptVisualHeight(m.prompt.Value(), m.prompt.Width())
	m.prompt.SetHeight(promptH)

	if banner := m.inbox.ActionRequiredBanner(); banner != "" {
		bannerLine = banner
	}

	prefix := m.promptPrefix()
	m.prompt.SetPromptFunc(promptPrefixCols, m.promptLineFunc())
	m.prompt.SetHeight(promptHeightFor(&m.prompt))
	switch m.mode {
	case ModeChoosing:
		if m.pendingDraft != "" {
			promptLine = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true).
				Render("(picker active — /input restores your prior draft)")
		} else {
			promptLine = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true).
				Render("(picker active)")
		}
	case ModeMenu:
		promptLine = m.menuSystem.View()
	case ModeMetaSessions:
		promptLine = m.sessionsPanel.View()
	case ModeAwaitingLLM:
		caption := "thinking… (Ctrl+C to cancel)"
		if m.pendingKind == pendingDeterministic {
			caption = "running…  (Ctrl+C to cancel)"
		}
		indicator := lipgloss.NewStyle().
			Foreground(colorMuted).
			Render("⏳ " + m.spinner.View() + " " + caption +
				"  ·  queue: " + queueDepthLabel(len(m.inputQueue)) +
				"  ·  Enter to queue · Esc to cancel queue")
		m.prompt.SetPromptFunc(promptPrefixCols, func(int) string { return "↳ " })
		promptLine = indicator + "\n" + m.prompt.View()
	case ModeMeta:
		if m.metaMode.inFlight {
			promptLine = prefix + m.spinner.View() + " " +
				lipgloss.NewStyle().Foreground(colorMuted).Render("agent is thinking… (Ctrl+C or Esc to cancel)")
		} else {
			promptLine = m.prompt.View()
		}
	default:
		promptLine = m.prompt.View()
	}
	return promptLine, bannerLine
}

// composeChromeParts builds the ordered bottom-chrome part list — the
// single source of truth shared by RootModel.View() and ComposeFrame.
// The ordering and styling are exactly what View() inlined before the
// composer existed:
//
//	[live routing/thinking line, if any]
//	[action-required banner, if any]
//	──────────────────────────────────── (divider)
//	[prompt line]
//	[per-room footer, if the state declares one]
//	room · state · mode · queue           (framework status row)
//
// width parameterises the divider and status row; the live caller passes
// m.width so its output is unchanged, a headless caller passes its cols.
func composeChromeParts(m RootModel, width int, promptLine, bannerLine string) []string {
	r := blocks.New(width, m.currentTheme())
	var parts []string
	if live := m.transcript.LiveLine(); live != "" {
		parts = append(parts, live)
	}
	if bannerLine != "" {
		parts = append(parts, bannerLine)
	}
	parts = append(parts, r.Divider())
	parts = append(parts, promptLine)
	if line2 := footerStoryLine(m); line2 != "" {
		parts = append(parts,
			lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true).
				Render(line2))
	}
	// Faint, right-aligned discoverability hint just above the status
	// row. On its own line so it never competes with the location/mode
	// content for status-row width on narrow terminals.
	hint := lipgloss.NewStyle().Foreground(colorMuted).Render(discoverabilityHint)
	parts = append(parts, lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Right).
		Render(hint))
	parts = append(parts, r.StatusRow(footerFrameworkLine(m), modeLabel(m.mode)))
	return parts
}

// joinChromeParts assembles the final chrome string from the part list.
// It uses simple string concatenation instead of JoinVertical to avoid
// padding issues that horizontally misalign multi-line parts like the
// awaiting-LLM prompt (indicator\n + prompt). Trailing newlines are
// trimmed per part to prevent double-spacing — identical to the join
// RootModel.View() performed inline before the composer existed.
func joinChromeParts(parts []string) string {
	var output strings.Builder
	for _, part := range parts {
		trimmed := strings.TrimRight(part, "\n")
		if trimmed == "" {
			continue
		}
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString(trimmed)
	}
	return output.String()
}
