package elements

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/expr"
)

// Media renders a media artifact as a labeled pointer line in the TUI.
// The TUI cannot display video or image frames inline, so it shows a
// short block that tells the operator what was produced and where it
// landed:
//
//	╭─ emoji  Label
//	│  → .artifacts/path/to/file
//	╰─ caption (when present)
//
// When MediaPath is empty the handle is shown in place of the path so
// the operator can correlate the element with the artifact record in the
// trace. AnnotateURL is shown as a second pointer line when present so TUI
// operators can open the same spatial annotation surface as web. Kind selects
// the leading emoji; unknown kinds use 📎.
type Media struct {
	// Handle is the artifact id/handle (e.g. "walkthrough#ab12cd34").
	// Required.
	Handle string
	// Caption is the optional one-line description shown beneath the
	// pointer arrow.
	Caption string
	// Kind is the artifact kind (video/image/pdf/html/slideshow).
	// Selects the pointer icon.
	Kind string
	// Path is the resolved .artifacts/ path. When non-empty it is shown
	// on the pointer arrow line instead of the handle.
	Path string
	// AnnotateURL opens the annotation surface for this media when the
	// transport cannot host it inline.
	AnnotateURL string
}

// mediaIcon maps artifact kind strings to display emoji. The default
// for unknown/empty kinds is 📎 (paperclip).
var mediaIcon = map[string]string{
	"video":     "📹",
	"image":     "🖼",
	"pdf":       "📄",
	"html":      "🌐",
	"slideshow": "📊",
}

// mediaDefaultColor is the lipgloss foreground for the pointer block.
// Cyan-600 — readable on both dark and light terminals, distinct from
// the banner emerald and the heading style.
const mediaDefaultColor = "#0891B2"

// Render returns the TUI pointer block for the artifact. Width is
// available for future wrapping but is currently unused — the pointer
// line is short by design (single-line label + single-line path).
func (m Media) Render(_ int, _ expr.Env, _ ViewRenderer) (string, error) {
	icon, ok := mediaIcon[strings.ToLower(m.Kind)]
	if !ok {
		icon = "📎"
	}

	target := m.Path
	if target == "" {
		target = m.Handle
	}

	style := lipgloss.NewStyle().Foreground(lipgloss.Color(mediaDefaultColor))

	label := m.Handle
	if m.Caption != "" {
		label = m.Caption
	}

	var sb strings.Builder
	sb.WriteString(style.Render(icon + "  " + label))
	sb.WriteString("\n")
	sb.WriteString(style.Render("   → " + target))
	if m.AnnotateURL != "" {
		sb.WriteString("\n")
		sb.WriteString(style.Render("   annotate → " + m.AnnotateURL))
	}
	if m.Caption != "" && m.Caption != label {
		sb.WriteString("\n")
		sb.WriteString(style.Render("   " + m.Caption))
	}
	return sb.String(), nil
}
