package elements

import (
	"strings"

	"github.com/muesli/reflow/wordwrap"

	"kitsoki/internal/expr"
)

// Prose is one reflowable narration paragraph. Source may carry pongo2
// template syntax; Render expands it through render.Pongo before
// reflowing. Author line breaks inside Source are collapsed into spaces —
// prose is a single logical paragraph that the renderer is free to
// rewrap to fit the viewport.
//
// Authors who want hard line breaks should use `code` (preserves layout
// exactly) or split into multiple `prose:` elements (each renders as its
// own paragraph with a blank line in between, courtesy of the dispatcher).
type Prose struct {
	Source string
}

// Render reflows the prose body to the supplied width.
func (p Prose) Render(width int, env expr.Env, rr ViewRenderer) (string, error) {
	body, err := renderLeaf(rr, p.Source, env)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	// Collapse author-authored whitespace runs (including newlines) into
	// single spaces so the wrapper sees a single paragraph. YAML folded
	// scalars (`>`) already do this on load; this guard also handles
	// the literal (`|`) and quoted-string forms where the author may
	// have hand-wrapped at a width different from the viewport.
	body = collapseWhitespace(body)
	if width < 1 {
		return body, nil
	}
	return wordwrap.String(body, width), nil
}

// collapseWhitespace replaces runs of whitespace (including '\n') with a
// single ASCII space and trims the result. Mirrors the behaviour an
// author gets from a YAML folded scalar (`view: >`) — the contract a
// `prose:` element promises.
func collapseWhitespace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		sb.WriteRune(r)
		inSpace = false
	}
	return strings.TrimSpace(sb.String())
}
