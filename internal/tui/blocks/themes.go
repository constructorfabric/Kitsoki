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

// Brand palette — the Kitsoki "Mesa Sun" mark (docs/branding/logo.md).
// These are fixed brand colours, not theme roles: the startup logo renders
// in desert tones regardless of the active room theme, and themeMesa below
// maps them onto the standard Theme slots for the branded welcome banner.
const (
	brandGold   = lipgloss.Color("#e0a23a") // sun
	brandClay   = lipgloss.Color("#a8492b") // building
	brandAdobe  = lipgloss.Color("#c97b4a") // lighter tier / accents
	brandRust   = lipgloss.Color("#7d2e1c") // ground / shadow
	brandShadow = lipgloss.Color("#3a2418") // doorway / deep shadow
	brandPaper  = lipgloss.Color("#f0e3c8") // paper / reversed-mark text
	brandSand   = lipgloss.Color("#b59b76") // muted sand — hints/subtitle
	brandTeal   = lipgloss.Color("#3aa3a0") // optional cool accent
)

// themeMesa is the brand theme used by the startup welcome banner so the
// first thing a user sees matches the Mesa Sun mark — gold sun accent,
// clay building primary, rust border. It is not a per-room theme; only the
// welcome banner reaches for it (see internal/tui/welcome.go).
var themeMesa = &Theme{
	Name:    "mesa",
	Primary: brandClay,
	Accent:  brandGold,
	Info:    brandTeal,
	Muted:   brandSand,
	Warning: brandAdobe,
	Error:   lipgloss.Color("#c84b31"), // desert red — distinct from clay
	Text:    brandPaper,
	Border:  brandRust,
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
	case "mesa":
		return themeMesa
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
	return []*Theme{themeDefault, themeMesa, themeMetaBlue, themeMetaAmber, themeOffPath}
}
