package tui

import "github.com/charmbracelet/lipgloss"

// All lipgloss.Style values in one place — easy to tune.

const (
	colorPrimary  = lipgloss.Color("#7C3AED") // violet
	colorAccent   = lipgloss.Color("#10B981") // emerald
	colorWarning  = lipgloss.Color("#F59E0B") // amber
	colorError    = lipgloss.Color("#EF4444") // red
	colorMuted    = lipgloss.Color("#6B7280") // gray
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

	// menuItemStyle renders a menu item.
	menuItemStyle = lipgloss.NewStyle().
			Foreground(colorText)

	// menuItemSelectedStyle renders the selected/highlighted menu item.
	menuItemSelectedStyle = lipgloss.NewStyle().
				Foreground(colorSelected).
				Bold(true)

	// menuItemBlockedStyle renders a blocked menu item.
	menuItemBlockedStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	// offPathBannerStyle renders the off-path banner.
	offPathBannerStyle = lipgloss.NewStyle().
				Foreground(colorWarning).
				Bold(true).
				Italic(true)

	// turnHeaderStyle renders the user-turn header in the transcript.
	turnHeaderStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)
)
