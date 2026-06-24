package tui

import "github.com/charmbracelet/lipgloss"

// All lipgloss.Style values in one place — easy to tune.

const (
	colorPrimary  = lipgloss.Color("#7C3AED") // violet
	colorAccent   = lipgloss.Color("#10B981") // emerald
	colorWarning  = lipgloss.Color("#F59E0B") // amber
	colorError    = lipgloss.Color("#EF4444") // red
	colorMuted    = lipgloss.Color("#6B7280") // gray
	colorInfo     = lipgloss.Color("#3B82F6") // blue
	colorOnPath   = lipgloss.Color("#10B981") // green
	colorOffPath  = lipgloss.Color("#F59E0B") // amber
	colorBorder   = lipgloss.Color("#4B5563") // dark gray
	colorSelected = lipgloss.Color("#7C3AED") // violet
	colorText     = lipgloss.Color("#F9FAFB") // near white
)

var (
	// locationStyle is the top location bar.
	locationStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Background(colorPrimary).
			Padding(0, 1).
			Bold(true)

	// locationOffPathStyle is the location bar in off-path mode.
	locationOffPathStyle = lipgloss.NewStyle().
				Foreground(colorText).
				Background(colorOffPath).
				Padding(0, 1).
				Bold(true)

	// transcriptStyle is the main transcript pane (on-path).
	transcriptStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// transcriptOffPathStyle is the transcript pane in off-path mode.
	transcriptOffPathStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(colorWarning).
				Padding(0, 1)

	// menuStyle is the right-side menu pane.
	menuStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// promptStyle is the bottom input line.
	promptStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	// promptOffPathStyle is the prompt in off-path mode.
	promptOffPathStyle = lipgloss.NewStyle().
				Foreground(colorWarning).
				Bold(true)

	// errorStyle renders rejection messages.
	errorStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Italic(true)

	// guardHintStyle renders guard failure hints.
	guardHintStyle = lipgloss.NewStyle().
			Foreground(colorWarning).
			Italic(true)

	// warningStyle renders a non-fatal warning — something didn't take, but
	// the session is fine. Amber (not error-red) so the user reads it as
	// "heads up", not "everything broke".
	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarning).
			Italic(true)

	// clarificationStyle renders a soft "I didn't catch that" prompt. It is
	// deliberately muted (gray, not warning-amber) — the user didn't fail,
	// the router did, and the next line is the recovery menu.
	clarificationStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	// menuItemStyle renders an available (guard-passing) menu item. Soft
	// green tint signals "you can do this right now" without shouting.
	menuItemStyle = lipgloss.NewStyle().
			Foreground(colorAccent)

	// menuItemSelectedStyle renders the selected/highlighted menu item.
	menuItemSelectedStyle = lipgloss.NewStyle().
				Foreground(colorSelected).
				Bold(true)

	// menuItemBlockedStyle renders a menu item whose guards currently fail.
	// Red signals "the intent is declared in this state but its preconditions
	// aren't met yet" — paired with the captured guard_hint inline so the
	// player knows what to do first.
	menuItemBlockedStyle = lipgloss.NewStyle().
				Foreground(colorError).
				Italic(true)

	// offPathBannerStyle renders the off-path banner.
	offPathBannerStyle = lipgloss.NewStyle().
				Foreground(colorWarning).
				Bold(true).
				Italic(true)

	// transcriptOffPathAnswerStyle renders an agent answer surfaced in
	// off-path mode. Soft amber to match the off-path framing without
	// shouting; not italic because the body is normal prose.
	transcriptOffPathAnswerStyle = lipgloss.NewStyle().
					Foreground(colorOffPath)

	// turnHeaderStyle renders the user-turn header in the transcript.
	turnHeaderStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	// metaListHeaderStyle renders the /meta list separator banner.
	metaListHeaderStyle = lipgloss.NewStyle().
				Foreground(colorInfo).
				Bold(true)

	// metaListItemStyle renders each row of /meta list output.
	metaListItemStyle = lipgloss.NewStyle().
				Foreground(colorInfo)

	// slashOutputStyle renders the "(...)"-style transcript lines that
	// slash commands emit as feedback. Always bold blue so meta-info
	// notes — /meta reload signals, /onpath exit summary, /trace ping
	// messages — are unambiguously distinct from narrative content.
	slashOutputStyle = lipgloss.NewStyle().
				Foreground(colorInfo).
				Bold(true)
)
