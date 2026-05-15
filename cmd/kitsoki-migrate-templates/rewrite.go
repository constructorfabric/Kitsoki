// Pure-string translation logic for the legacy expr-lang `{{ }}` template
// syntax → pongo2/v6 (Django) syntax (see proposal §3.1).
//
// This file is intentionally I/O-free and YAML-free: it operates on a single
// template string at a time. The caller (walk.go) decides *which* strings to
// rewrite based on a YAML field allowlist. Idempotency is guaranteed by the
// detector: pongo2 syntax uses `{% %}` for block keywords, so a second pass
// over already-migrated source finds no `{{ if|else|end|range … }}` tokens
// and leaves the input untouched.
//
// # Pongo2/v6 quirks the rewriter accounts for
//
//   - Django filter arg syntax uses a colon, not parentheses:
//     `x|default:"foo"` (NOT `x|default('foo')`). The `??` rewrite
//     emits the colon form.
//   - There is no inline ternary. `{{ x ? a : b }}` cannot become
//     `{{ a if x else b }}` (pongo2 parse error). Instead the ENTIRE
//     `{{ }}` is replaced by a block: `{% if x %}a{% else %}b{% endif %}`.
//     String-literal branches lose their quotes (they become literal
//     template text); other branches are wrapped in `{{ }}` and
//     nested ternaries recurse to nested blocks.
package main

import (
	"fmt"
	"strings"
)

// Rewrite translates a single template string from the legacy expr-lang
// `{{ }}` syntax to pongo2/v6 (Django) syntax per the §3.1 table. If the
// input contains no `{{` markers, it is returned unchanged (fast path).
//
// Idempotency: Rewrite is a no-op on already-migrated input because
//   - block keywords (`if|else|end|range`) live behind `{% %}` after the
//     first pass, which this tokeniser never sees as a `{{ }}` block;
//   - the ternary case replaces the `{{ }}` outright with `{% if %}`,
//     leaving no `?` + `:` shape inside any `{{ }}`;
//   - the `??` detector matches the literal two-character operator,
//     which pongo2's `|default:` filter does not emit.
func Rewrite(src string) (string, error) {
	if !strings.Contains(src, "{{") {
		return src, nil
	}
	tokens, err := tokeniseSrc(src)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.Grow(len(src) + 16)
	stack := []frame{}
	for _, t := range tokens {
		if !t.isBlock {
			sb.WriteString(t.raw)
			continue
		}
		body := strings.TrimSpace(t.inner)
		kw, rest := splitKeywordRW(body)
		inRange := false
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].kind == "range" {
				inRange = true
				break
			}
		}
		switch kw {
		case "if":
			condExpr := strings.TrimSpace(rest)
			if inRange {
				condExpr = rewriteDotsForRange(condExpr)
			}
			sb.WriteString("{% if ")
			sb.WriteString(condExpr)
			sb.WriteString(" %}")
			stack = append(stack, frame{kind: "if"})
		case "else":
			sb.WriteString("{% else %}")
			if len(stack) > 0 {
				stack[len(stack)-1].elseSeen = true
			}
		case "end":
			if len(stack) == 0 {
				return "", fmt.Errorf("template: unexpected {{ end }} (no open block)")
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.kind == "range" {
				sb.WriteString("{% endfor %}")
			} else {
				sb.WriteString("{% endif %}")
			}
		case "range":
			listExpr := strings.TrimSpace(rest)
			if inRange {
				listExpr = rewriteDotsForRange(listExpr)
			}
			sb.WriteString("{% for item in ")
			sb.WriteString(listExpr)
			sb.WriteString(" %}")
			stack = append(stack, frame{kind: "range"})
		default:
			// Plain expression interpolation. Pongo2 has no inline
			// ternary, so a top-level `<cond> ? <a> : <b>` must escape
			// the surrounding `{{ }}` and become a block. Other shapes
			// rewrite their inner expression and stay inside `{{ }}`.
			expr := body
			if inRange {
				expr = rewriteDotsForRange(expr)
			}
			if blk, ok := tryConvertTernaryToBlock(strings.TrimSpace(expr)); ok {
				sb.WriteString(blk)
				continue
			}
			expr = rewriteExpression(expr)
			sb.WriteString("{{ ")
			sb.WriteString(expr)
			sb.WriteString(" }}")
		}
	}
	if len(stack) != 0 {
		return "", fmt.Errorf("template: unclosed block (%d level(s) of nesting still open)", len(stack))
	}
	return sb.String(), nil
}

// frame is one entry on the open-block stack used during rewrite.
type frame struct {
	kind     string // "if" | "range"
	elseSeen bool
}

// tokRW is the tokeniser's output: alternating literal-text and {{ … }}
// block tokens. raw preserves the original {{…}} bytes so non-block-keyword
// expressions can be re-emitted byte-identical when no inner rewrite applies.
type tokRW struct {
	isBlock bool
	raw     string // original full text including delimiters
	inner   string // for blocks: the bytes between {{ and }} (verbatim)
}

// tokeniseSrc splits src into alternating literal-text and {{ … }} tokens.
// Mirrors expr.tokenise (internal/expr/expr.go) but preserves the raw
// `{{ … }}` slice so non-block tokens can pass through unchanged on a
// no-op rewrite path.
func tokeniseSrc(src string) ([]tokRW, error) {
	var out []tokRW
	for len(src) > 0 {
		start := strings.Index(src, "{{")
		if start < 0 {
			out = append(out, tokRW{raw: src})
			break
		}
		if start > 0 {
			out = append(out, tokRW{raw: src[:start]})
		}
		rest := src[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return nil, fmt.Errorf("template: unclosed {{ block")
		}
		inner := rest[:end]
		out = append(out, tokRW{
			isBlock: true,
			raw:     src[start : start+2+end+2],
			inner:   inner,
		})
		src = rest[end+2:]
	}
	return out, nil
}

// splitKeywordRW classifies a block body. Returns (keyword, rest) where
// keyword is one of "if" | "else" | "end" | "range" | "" — empty means
// this is a plain expression interpolation, not a block keyword.
//
// Detection is whitespace-delimited at the start; an expression that
// happens to start with the letters "if" but is actually `iface.foo` (no
// boundary) is NOT classified as a block.
func splitKeywordRW(body string) (string, string) {
	body = strings.TrimSpace(body)
	for _, kw := range []string{"if", "else", "end", "range"} {
		if body == kw {
			return kw, ""
		}
		if strings.HasPrefix(body, kw) && len(body) > len(kw) {
			c := body[len(kw)]
			if c == ' ' || c == '\t' {
				return kw, body[len(kw):]
			}
		}
	}
	return "", body
}

// rewriteExpression applies per-expression rewrites to the body of a
// plain `{{ … }}` interpolation. After this runs, the result is wrapped
// back in `{{ … }}` by the caller (so any rewrite that needs to escape
// the `{{ }}` — like a top-level ternary — must be handled BEFORE this
// is called, via tryConvertTernaryToBlock).
//
//   - `<expr> ?? <fallback>` → `<expr>|default:<fallback>` (Django filter)
//
// Other shapes pass through unchanged. The rewriter scans byte-by-byte and
// tracks string-literal state so a `?` inside a quoted value never trips
// the parser. Parenthesised subexpressions are processed recursively so
// `int(slots.foo ?? 0)` rewrites to `int(slots.foo|default:0)` and any
// nested ternaries inside parens become nested blocks via the recursive
// call into tryConvertTernaryToBlock from branchToTemplateFragment.
func rewriteExpression(expr string) string {
	expr = strings.TrimSpace(expr)
	expr = rewriteInParens(expr)
	expr = rewriteNullishCoalesceTopLevel(expr)
	return expr
}

// rewriteInParens scans expr for parenthesised subexpressions at any depth
// and recursively rewrites their contents. Walks left to right, tracking
// string-literal state, and on each top-level `(` emits the rewritten
// inner expression in place. Brackets and braces (`[...]`, `{...}`) are
// treated as opaque — pongo2 filter syntax doesn't use them in shapes
// that would benefit from inner ternary/`??` rewriting, and we don't
// want to disturb list/index lookups.
func rewriteInParens(expr string) string {
	var sb strings.Builder
	sb.Grow(len(expr) + 8)
	i := 0
	for i < len(expr) {
		c := expr[i]
		// Skip over string literals verbatim.
		if c == '\'' || c == '"' {
			j := scanStringLiteral(expr, i)
			sb.WriteString(expr[i:j])
			i = j
			continue
		}
		if c != '(' {
			sb.WriteByte(c)
			i++
			continue
		}
		// Found a `(`; find the matching `)`.
		end := matchClosingParen(expr, i)
		if end < 0 {
			// Unbalanced — write the rest verbatim and stop.
			sb.WriteString(expr[i:])
			return sb.String()
		}
		inner := expr[i+1 : end]
		inner = rewriteExpression(inner)
		sb.WriteByte('(')
		sb.WriteString(inner)
		sb.WriteByte(')')
		i = end + 1
	}
	return sb.String()
}

// scanStringLiteral returns the index one past the end of the string
// literal that starts at expr[start]. Handles backslash escapes.
func scanStringLiteral(expr string, start int) int {
	quote := expr[start]
	i := start + 1
	for i < len(expr) {
		if expr[i] == '\\' && i+1 < len(expr) {
			i += 2
			continue
		}
		if expr[i] == quote {
			return i + 1
		}
		i++
	}
	return len(expr)
}

// matchClosingParen returns the index of the `)` that closes the `(` at
// expr[start]. Tracks nested parens and string literals. Returns -1 if
// unbalanced.
func matchClosingParen(expr string, start int) int {
	depth := 1
	i := start + 1
	for i < len(expr) {
		c := expr[i]
		if c == '\'' || c == '"' {
			i = scanStringLiteral(expr, i)
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// rewriteNullishCoalesceTopLevel rewrites every top-level `<lhs> ?? <rhs>`
// in expr to `<lhs>|default:<rhs>` (Django filter-arg syntax).
// Chains like `a ?? b ?? c` are folded left-associatively to
// `a|default:b|default:c` — chained Django filters apply left-to-right,
// so the second `default:` runs on the result of the first, matching the
// short-circuit semantics of `??`. Top-level means depth-0 outside string
// literals.
func rewriteNullishCoalesceTopLevel(expr string) string {
	for {
		idx := findFirstTopLevelOp(expr, "??")
		if idx < 0 {
			return expr
		}
		lhs := strings.TrimRight(expr[:idx], " \t")
		// rhs runs from idx+2 to the next top-level `??`, or to the end.
		tail := expr[idx+2:]
		next := findFirstTopLevelOp(tail, "??")
		var rhs, rest string
		if next < 0 {
			rhs = strings.TrimSpace(tail)
			rest = ""
		} else {
			rhs = strings.TrimSpace(tail[:next])
			rest = tail[next:]
		}
		expr = lhs + "|default:" + rhs + rest
	}
}

// findFirstTopLevelOp returns the first byte index of op at depth-0
// outside string literals, or -1.
func findFirstTopLevelOp(expr, op string) int {
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if inStr != 0 {
			if c == '\\' && i+1 < len(expr) {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			inStr = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if depth == 0 && i+len(op) <= len(expr) && expr[i:i+len(op)] == op {
				return i
			}
		}
	}
	return -1
}

// tryConvertTernaryToBlock attempts to expand a top-level ternary as a
// pongo2 `{% if cond %}A{% else %}B{% endif %}` block. The caller emits
// the returned text in place of the surrounding `{{ }}` so the block
// replaces the interpolation outright (pongo2 has no inline ternary).
//
// Returns ("", false) when expr is not a pure top-level ternary —
// callers fall back to rewriteExpression + `{{ }}` wrapping.
func tryConvertTernaryToBlock(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	qIdx := findTopLevelTernaryQuestion(expr)
	if qIdx < 0 {
		return "", false
	}
	cIdx := findTopLevelByteFrom(expr, ':', qIdx+1)
	if cIdx < 0 {
		return "", false
	}
	cond := strings.TrimSpace(expr[:qIdx])
	a := strings.TrimSpace(expr[qIdx+1 : cIdx])
	b := strings.TrimSpace(expr[cIdx+1:])
	var sb strings.Builder
	sb.WriteString("{% if ")
	sb.WriteString(cond)
	sb.WriteString(" %}")
	sb.WriteString(branchToTemplateFragment(a))
	sb.WriteString("{% else %}")
	sb.WriteString(branchToTemplateFragment(b))
	sb.WriteString("{% endif %}")
	return sb.String(), true
}

// branchToTemplateFragment converts a ternary branch expression into its
// pongo2 template-text form, suitable for emission between `{% if %}`
// and `{% else %}` / `{% endif %}`. Three shapes:
//
//   - Bare string literal (`'foo'` or `"foo"`):
//     emit unquoted as literal text.
//   - Parenthesised top-level ternary:
//     strip the parens and recurse into tryConvertTernaryToBlock,
//     producing a nested block.
//   - Anything else:
//     wrap in `{{ … }}` after running rewriteExpression on it
//     (handles nested `??` and other in-expression rewrites).
func branchToTemplateFragment(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ""
	}
	if len(branch) >= 2 && branch[0] == '(' && branch[len(branch)-1] == ')' {
		if matchClosingParen(branch, 0) == len(branch)-1 {
			inner := strings.TrimSpace(branch[1 : len(branch)-1])
			if blk, ok := tryConvertTernaryToBlock(inner); ok {
				return blk
			}
			branch = inner
		}
	}
	if len(branch) >= 2 {
		first := branch[0]
		last := branch[len(branch)-1]
		if (first == '\'' || first == '"') && first == last {
			if scanStringLiteral(branch, 0) == len(branch) {
				return branch[1 : len(branch)-1]
			}
		}
	}
	return "{{ " + rewriteExpression(branch) + " }}"
}

// findTopLevelTernaryQuestion returns the index of the first `?` at depth
// 0 outside string literals that is NOT part of a `??` operator.
func findTopLevelTernaryQuestion(expr string) int {
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if inStr != 0 {
			if c == '\\' && i+1 < len(expr) {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			inStr = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '?':
			if depth == 0 {
				if i+1 < len(expr) && expr[i+1] == '?' {
					i++ // skip the second `?` of `??`
					continue
				}
				return i
			}
		}
	}
	return -1
}

// findTopLevelByteFrom returns the first byte index of c at depth-0 outside
// string literals, starting the search at byte index start. depth/quote
// state is recomputed from byte 0 so the caller doesn't have to thread
// context.
func findTopLevelByteFrom(expr string, c byte, start int) int {
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if inStr != 0 {
			if ch == '\\' && i+1 < len(expr) {
				i++
				continue
			}
			if ch == inStr {
				inStr = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			inStr = ch
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if i >= start && depth == 0 && ch == c {
				return i
			}
		}
	}
	return -1
}

// rewriteDotsForRange rewrites leading-dot identifiers inside a range body
// expression to use the `item` binding pongo2 expects.
//
//	.foo      → item.foo
//	.foo.bar  → item.foo.bar
//	a.foo     → a.foo            (unchanged — only leading-dot tokens rewrite)
//
// Mirrors internal/expr/expr.go rewriteDotsForRange — kept self-contained
// here so the codemod has no dependency on the runtime expr package.
func rewriteDotsForRange(src string) string {
	if !strings.Contains(src, ".") {
		return src
	}
	var sb strings.Builder
	sb.Grow(len(src) + 8)
	prevBoundary := true
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '.' && prevBoundary && i+1 < len(src) && isIdentStart(src[i+1]) {
			sb.WriteString("item.")
			prevBoundary = false
			continue
		}
		sb.WriteByte(c)
		prevBoundary = !isIdentCont(c)
	}
	return sb.String()
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
