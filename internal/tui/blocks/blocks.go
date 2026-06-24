package blocks

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Renderer carries the per-frame rendering context: width to wrap at,
// theme palette to colour with, and whether colour is suppressed
// entirely (golden tests, NO_COLOR, pipes).
type Renderer struct {
	Width   int
	Theme   *Theme
	NoColor bool
}

// New builds a Renderer with the given width and theme name. An unknown
// theme name falls back to the default theme.
func New(width int, themeName string) *Renderer {
	t := ThemeByName(themeName)
	return &Renderer{
		Width:   width,
		Theme:   t,
		NoColor: noColorEnabled(),
	}
}

// WithNoColor returns a copy of r with NoColor forced to v. Used by
// golden tests to pin output.
func (r *Renderer) WithNoColor(v bool) *Renderer {
	cp := *r
	cp.NoColor = v
	return &cp
}

// style applies fg/bg/bold/italic from the theme entry, honouring
// NoColor by returning a bare style when colour is suppressed.
func (r *Renderer) style(fg, bg lipgloss.TerminalColor, bold, italic bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	if !r.NoColor {
		if fg != nil {
			s = s.Foreground(fg)
		}
		if bg != nil {
			s = s.Background(bg)
		}
	}
	if bold {
		s = s.Bold(true)
	}
	if italic {
		s = s.Italic(true)
	}
	return s
}

// noColorEnabled honours the standard NO_COLOR convention plus
// KITSOKI_NO_COLOR for explicit override. Matches routing_chip's
// noColourEnabled so the chat view renders consistently with the chip.
func noColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return true
	}
	if os.Getenv("KITSOKI_NO_COLOR") != "" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return true
	}
	return false
}
