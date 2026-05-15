package elements

import (
	"kitsoki/internal/expr"
)

// Template is the escape hatch into the caller's Glamour pipeline. The
// renderer expands pongo2 syntax in Source and hands the result to the
// caller-supplied Glamour callback.
//
// This is the kind that preserves today's behaviour for the legacy
// scalar `view: <markdown>` form (the loader normalises it to a single
// {Kind: "template", Source: <original>} element). Until Phase E/F
// migrate apps to typed elements, every view that flows through the
// dispatcher takes this path.
type Template struct {
	Source  string
	Glamour GlamourFunc
}

// Render expands templates in Source and delegates to the Glamour
// callback. A nil Glamour is normalised to identity (the post-Pongo
// source returned verbatim) so tests can exercise the substitution path
// without spinning up a Glamour renderer.
func (t Template) Render(_ int, env expr.Env, rr ViewRenderer) (string, error) {
	body, err := renderLeaf(rr, t.Source, env)
	if err != nil {
		return "", err
	}
	if body == "" {
		return "", nil
	}
	gl := t.Glamour
	if gl == nil {
		gl = IdentityGlamour
	}
	return gl(body), nil
}
