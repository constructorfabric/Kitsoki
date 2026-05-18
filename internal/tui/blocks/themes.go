package blocks

import "github.com/charmbracelet/lipgloss"

// Theme is the palette every block renderer reaches for. Per-room theme
// swaps (meta mode accent colours, off-path amber, etc.) are realised
// by swapping the active theme, not by per-call colour overrides.
type Theme struct {
	Name string

	// Primary accent — used by the prompt prefix, user-turn header,
	// location bar background.
	Primary lipgloss.Color
	// Accent — the "available action" colour. Green by default.
	Accent lipgloss.Color
	// Info — the slash-command output colour. Blue by default.
	Info lipgloss.Color
	// Muted — system notices, hints, faded text.
	Muted lipgloss.Color
	// Warning — guard hints, off-path banner, amber tints.
	Warning lipgloss.Color
	// Error — rejection messages.
	Error lipgloss.Color
	// Text — near-white default text.
	Text lipgloss.Color
	// Border — pane border colour.
	Border lipgloss.Color
}

// themeDefault matches today's tui/styles.go palette so the redesign
// renders against the same baseline before per-room themes diverge.
var themeDefault = &Theme{
	Name:    "default",
	Primary: lipgloss.Color("#7C3AED"), // violet
	Accent:  lipgloss.Color("#10B981"), // emerald
	Info:    lipgloss.Color("#3B82F6"), // blue
	Muted:   lipgloss.Color("#6B7280"), // gray
	Warning: lipgloss.Color("#F59E0B"), // amber
	Error:   lipgloss.Color("#EF4444"), // red
	Text:    lipgloss.Color("#F9FAFB"), // near white
	Border:  lipgloss.Color("#4B5563"), // dark gray
}

// themeMetaBlue is a deeper-blue accent for meta-mode rooms so the
// transcript reads visibly different from on-path.
var themeMetaBlue = &Theme{
	Name:    "meta-blue",
	Primary: lipgloss.Color("#2563EB"), // strong blue
	Accent:  lipgloss.Color("#22D3EE"), // cyan
	Info:    lipgloss.Color("#60A5FA"), // light blue
	Muted:   lipgloss.Color("#94A3B8"), // slate
	Warning: lipgloss.Color("#F59E0B"),
	Error:   lipgloss.Color("#EF4444"),
	Text:    lipgloss.Color("#F1F5F9"),
	Border:  lipgloss.Color("#1E40AF"),
}

// themeMetaAmber is a warm accent for self-improvement / authoring meta
// rooms.
var themeMetaAmber = &Theme{
	Name:    "meta-amber",
	Primary: lipgloss.Color("#D97706"), // amber-700
	Accent:  lipgloss.Color("#FBBF24"), // amber-400
	Info:    lipgloss.Color("#F59E0B"),
	Muted:   lipgloss.Color("#78716C"),
	Warning: lipgloss.Color("#F59E0B"),
	Error:   lipgloss.Color("#EF4444"),
	Text:    lipgloss.Color("#FEF3C7"),
	Border:  lipgloss.Color("#92400E"),
}

// themeOffPath leans amber for the off-path banner / answers.
var themeOffPath = &Theme{
	Name:    "off-path",
	Primary: lipgloss.Color("#F59E0B"),
	Accent:  lipgloss.Color("#F59E0B"),
	Info:    lipgloss.Color("#3B82F6"),
	Muted:   lipgloss.Color("#6B7280"),
	Warning: lipgloss.Color("#F59E0B"),
	Error:   lipgloss.Color("#EF4444"),
	Text:    lipgloss.Color("#F9FAFB"),
	Border:  lipgloss.Color("#B45309"),
}

// ThemeByName returns the named theme, falling back to default on
// unknown names. The fallback is intentional — callers shouldn't have
// to error-check a theme lookup.
func ThemeByName(name string) *Theme {
	switch name {
	case "meta-blue":
		return themeMetaBlue
	case "meta-amber":
		return themeMetaAmber
	case "off-path":
		return themeOffPath
	default:
		return themeDefault
	}
}

// AllThemes returns every shipped theme in stable order — used by the
// preview CLI's bake-off mode (--theme all).
func AllThemes() []*Theme {
	return []*Theme{themeDefault, themeMetaBlue, themeMetaAmber, themeOffPath}
}
