// YAML walker for the template-syntax codemod. Uses goccy/go-yaml's AST
// to identify the byte-range of every leaf scalar whose path matches the
// §3.1 templated-string allowlist, then applies Rewrite to the raw bytes
// of those leaves in place. Everything outside the rewritten spans
// (comments, blank lines, indentation, quoting style, key order, flow
// vs block style) is preserved verbatim — only the inner `{{ … }}`
// blocks change.
//
// # AST-vs-interface choice
//
// We chose **byte-surgery on top of the AST** over both pure-AST round-
// trip and interface-mode re-marshal:
//
//   - Pure AST round-trip is fragile (LiteralNode.String() reads from
//     token.Origin, which doesn't reflect the modified Value;
//     StringNode.String() panics on indent-zero shapes the parser
//     produces for sequence-element scalars).
//   - Interface-mode (yaml.Unmarshal to ordered-map + yaml.Marshal)
//     loses comments AND normalises quoting, flow style, blank lines —
//     the resulting diff is dominated by cosmetic churn that swamps the
//     actual rewrites. The proposal allows this fallback with a
//     comment-loss caveat (Implementation notes phase C) but the
//     review experience is poor.
//
// Byte-surgery preserves the source byte-identical outside our target
// spans. The AST is used only to enumerate which leaf scalars live at
// allowlisted paths and where they begin/end in the source bytes; the
// actual rewrite is applied to the raw source slice.
//
// # Locating leaf byte ranges
//
// Goccy/go-yaml's Position field reports Line / Column / Offset, but:
//
//   - For inline scalars (plain, single-quoted, double-quoted strings),
//     Position points at the value's first character. We scan forward
//     to find the value's end (end-of-line for plain, matching quote
//     for quoted).
//   - For literal blocks (`|` / `>`), Position on the inner StringNode
//     points at the END of the content, not the start. We instead use
//     the LiteralNode's Start token (the `|`/`>` indicator) to find the
//     content-start: the line after the indicator, with column
//     determined by the first non-empty content line's indentation.
//     The content ends at the next line whose indent column is less
//     than that.
//
// Both shapes resolve to a [byteStart, byteEnd) span we can splice.
//
// # Field allowlist
//
// See isTemplatedField below. Source-of-truth: internal/app/types.go
// `yaml:"…"` struct tags and proposal §3.1.
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// MigrateFile rewrites every templated-string leaf in src whose YAML path
// matches the allowlist (isTemplatedField). Returns the rewritten bytes
// and the count of leaves modified. If src does not parse as YAML the
// error is returned. When the rewrite produces no change, returns
// (src, 0, nil) byte-identical so callers can detect no-op runs.
func MigrateFile(name string, src []byte) ([]byte, int, error) {
	f, err := parser.ParseBytes(src, parser.ParseComments)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: parse: %w", name, err)
	}
	lineIdx := buildLineIndex(src)
	var edits []edit
	for _, doc := range f.Docs {
		w := &walker{
			file:    name,
			src:     src,
			lineIdx: lineIdx,
			edits:   &edits,
		}
		w.visit(doc, "")
	}
	if len(edits) == 0 {
		return src, 0, nil
	}
	// Apply edits in descending start order so earlier offsets remain
	// valid after each splice.
	sort.Slice(edits, func(i, j int) bool {
		return edits[i].start > edits[j].start
	})
	out := make([]byte, len(src))
	copy(out, src)
	count := 0
	for _, e := range edits {
		out = append(append(append([]byte{}, out[:e.start]...), []byte(e.newText)...), out[e.end:]...)
		count++
	}
	return out, count, nil
}

// edit is one in-place byte splice to apply.
type edit struct {
	start, end int
	newText    string
}

// walker carries the per-file state for the AST visitor.
type walker struct {
	file    string
	src     []byte
	lineIdx []int // byte offset of the start of each 1-indexed line
	edits   *[]edit
}

// buildLineIndex returns a slice where idx[N] = byte offset of the start
// of line N+1. So idx[0] is always 0 (start of line 1).
func buildLineIndex(src []byte) []int {
	idx := []int{0}
	for i, b := range src {
		if b == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// lineColToOffset converts a 1-indexed (line, column) into a 0-indexed
// byte offset. Column 1 is the first byte of the line. Returns -1 if
// out of range.
func (w *walker) lineColToOffset(line, col int) int {
	if line < 1 || line > len(w.lineIdx) {
		return -1
	}
	off := w.lineIdx[line-1] + col - 1
	if off < 0 || off > len(w.src) {
		return -1
	}
	return off
}

// visit dispatches on the AST node kind. path is the slash-separated YAML
// path with sequence indices written as `[N]`.
func (w *walker) visit(n ast.Node, path string) {
	switch v := n.(type) {
	case *ast.DocumentNode:
		if v.Body != nil {
			w.visit(v.Body, path)
		}
	case *ast.MappingNode:
		for _, mv := range v.Values {
			w.visit(mv, path)
		}
	case *ast.MappingValueNode:
		key := mapKeyString(v.Key)
		child := path + "/" + key
		w.visit(v.Value, child)
	case *ast.SequenceNode:
		for i, item := range v.Values {
			w.visit(item, fmt.Sprintf("%s[%d]", path, i))
		}
	case *ast.StringNode:
		w.maybeRewriteInline(path, v.GetToken(), v.Value)
	case *ast.LiteralNode:
		if v.Value == nil {
			return
		}
		w.maybeRewriteLiteral(path, v)
	}
}

// maybeRewriteInline handles plain / single-quoted / double-quoted scalar
// leaves. Position.Line / Column point at the first content byte (the
// opening quote for quoted forms; the first non-space byte for plain).
// We compute the value's byte span from (Line, Column) + the token Type
// (which tells us whether to scan for a closing quote or end-of-line).
//
// For multi-line double-quoted strings we don't attempt to handle line
// continuations — they don't appear in the test corpus and adding
// support would balloon the implementation. If a templated string is
// authored across multiple lines via `"..."` it'll fall through
// untouched.
func (w *walker) maybeRewriteInline(path string, tk *token.Token, value string) {
	if !strings.Contains(value, "{{") {
		return // fast path
	}
	if !isTemplatedField(path) {
		return
	}
	start := w.lineColToOffset(tk.Position.Line, tk.Position.Column)
	if start < 0 || start >= len(w.src) {
		return
	}
	end := w.scanInlineEnd(start, tk.Type)
	if end <= start {
		return
	}
	rawSlice := string(w.src[start:end])
	rewritten, err := w.rewriteInlineSlice(rawSlice, tk.Type)
	if err != nil {
		return
	}
	if rewritten == rawSlice {
		return
	}
	*w.edits = append(*w.edits, edit{start: start, end: end, newText: rewritten})
}

// scanInlineEnd returns the exclusive end byte of an inline scalar
// starting at byte `start`. Type tells us the YAML form:
//
//   - DoubleQuoteType: scan for the next unescaped `"`.
//   - SingleQuoteType: scan for the next unescaped `'` (YAML 1.2 uses
//     `”` as the escape, not backslash).
//   - Otherwise: plain scalar — runs to end of line (or YAML comment
//     marker `#` preceded by whitespace, which we leave alone for the
//     codemod).
func (w *walker) scanInlineEnd(start int, typ token.Type) int {
	switch typ {
	case token.DoubleQuoteType:
		i := start + 1
		for i < len(w.src) {
			if w.src[i] == '\\' && i+1 < len(w.src) {
				i += 2
				continue
			}
			if w.src[i] == '"' {
				return i + 1
			}
			i++
		}
		return len(w.src)
	case token.SingleQuoteType:
		i := start + 1
		for i < len(w.src) {
			if w.src[i] == '\'' {
				if i+1 < len(w.src) && w.src[i+1] == '\'' {
					i += 2 // doubled-quote escape
					continue
				}
				return i + 1
			}
			i++
		}
		return len(w.src)
	default:
		// Plain scalar: until newline or trailing-comment ` #` sequence.
		i := start
		for i < len(w.src) && w.src[i] != '\n' {
			i++
		}
		// Trim trailing whitespace before a `#` comment if present.
		j := i
		for k := start; k < i-1; k++ {
			if w.src[k] == ' ' && k+1 < i && w.src[k+1] == '#' {
				j = k
				break
			}
		}
		return j
	}
}

// rewriteInlineSlice applies Rewrite to the inner content of a scalar
// while preserving the surrounding quotes (for quoted forms). For
// double-quoted strings YAML escape sequences inside the inner content
// are passed through to Rewrite unchanged — `{{` and `}}` never appear
// as escape introducers so the rewrite is safe.
func (w *walker) rewriteInlineSlice(raw string, typ token.Type) (string, error) {
	switch typ {
	case token.DoubleQuoteType, token.SingleQuoteType:
		if len(raw) < 2 {
			return raw, nil
		}
		inner := raw[1 : len(raw)-1]
		rewritten, err := Rewrite(inner)
		if err != nil {
			return "", err
		}
		return string(raw[0]) + rewritten + string(raw[len(raw)-1]), nil
	default:
		return Rewrite(raw)
	}
}

// maybeRewriteLiteral handles `|` and `>` block scalars. The inner
// StringNode's Position points at the END of the content (a parser
// quirk); we compute the content-start using the LiteralNode's Start
// token (the `|`/`>` indicator), and the content-end by scanning
// forward to a line whose indent column is less than the content's
// indent.
func (w *walker) maybeRewriteLiteral(path string, n *ast.LiteralNode) {
	if !isTemplatedField(path) {
		return
	}
	// Start: line after the indicator, at the first non-empty content
	// line's column.
	indicatorLine := n.Start.Position.Line
	contentLine, contentCol := w.findLiteralContentStart(indicatorLine + 1)
	if contentLine < 0 {
		return
	}
	start := w.lineColToOffset(contentLine, 1)
	if start < 0 {
		return
	}
	// End: the byte just before the first line whose first
	// non-whitespace character is at column < contentCol, OR EOF.
	end := w.findLiteralContentEnd(contentLine, contentCol)
	if end <= start {
		return
	}
	rawSlice := string(w.src[start:end])
	if !strings.Contains(rawSlice, "{{") {
		return
	}
	rewritten, err := Rewrite(rawSlice)
	if err != nil {
		return
	}
	if rewritten == rawSlice {
		return
	}
	*w.edits = append(*w.edits, edit{start: start, end: end, newText: rewritten})
}

// findLiteralContentStart scans forward from `from` looking for the
// first non-blank line. Returns (line, firstNonSpaceColumn). The column
// is 1-indexed.
func (w *walker) findLiteralContentStart(from int) (int, int) {
	for line := from; line <= len(w.lineIdx); line++ {
		start := w.lineIdx[line-1]
		end := len(w.src)
		if line < len(w.lineIdx) {
			end = w.lineIdx[line]
		}
		row := w.src[start:end]
		col := 1
		for _, c := range row {
			if c == ' ' || c == '\t' {
				col++
				continue
			}
			if c == '\n' {
				break
			}
			return line, col
		}
	}
	return -1, -1
}

// findLiteralContentEnd returns the byte offset (exclusive) of the last
// byte of the literal block, defined as the start of the first line
// whose first non-whitespace character is at column < indentCol. EOF
// also terminates the block.
func (w *walker) findLiteralContentEnd(fromLine, indentCol int) int {
	for line := fromLine + 1; line <= len(w.lineIdx); line++ {
		start := w.lineIdx[line-1]
		end := len(w.src)
		if line < len(w.lineIdx) {
			end = w.lineIdx[line]
		}
		row := w.src[start:end]
		col := 1
		blank := true
		for _, c := range row {
			if c == ' ' || c == '\t' {
				col++
				continue
			}
			if c == '\n' {
				break
			}
			blank = false
			break
		}
		if blank {
			continue // blank lines stay inside the block.
		}
		if col < indentCol {
			return start
		}
	}
	return len(w.src)
}

// mapKeyString stringifies an AST key node for path tracking.
func mapKeyString(k ast.MapKeyNode) string {
	if s, ok := k.(*ast.StringNode); ok {
		return s.Value
	}
	if k == nil {
		return ""
	}
	return strings.TrimSpace(k.String())
}

// isTemplatedField returns true if path points at a YAML field whose
// value is rendered as a pongo2 template (proposal §3.1 / §3.2). Pure-
// expression fields (`when:`, bare-expression `initial:`) and literal-
// list fields (`intents.*.slots.*.values[*]`) return false.
//
// Roots handled:
//   - /states/...                       — app states tree
//   - /proposals/<name>/views/...       — proposal review/applied/etc. views
//   - /phase_templates/<name>/states/.. — phase template state bodies
//     (see internal/app/phases.go — phases expand into states at load time)
func isTemplatedField(path string) bool {
	segs := splitPath(path)
	if len(segs) == 0 {
		return false
	}
	switch segs[0] {
	case "states":
		return isTemplatedUnderStates(segs[1:])
	case "proposals":
		return isTemplatedUnderProposals(segs[1:])
	case "phase_templates":
		// Skip the template name + descend into its `states` subtree.
		// /phase_templates/trail_leg/states/<id>/view → states/<id>/view
		if len(segs) >= 3 && segs[2] == "states" {
			return isTemplatedUnderStates(segs[3:])
		}
	}
	return false
}

// splitPath splits a YAML path like "/states/foyer/on/go[0]/target" into
// segments ["states", "foyer", "on", "go[0]", "target"]. Sequence
// indices remain attached to the segment name so callers can ignore
// them when matching against named keys.
func splitPath(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}

// stripIndex strips a trailing `[N]` from a path segment. Returns
// (name, true) when an index was present, (segment, false) otherwise.
func stripIndex(seg string) (string, bool) {
	if i := strings.IndexByte(seg, '['); i >= 0 && strings.HasSuffix(seg, "]") {
		return seg[:i], true
	}
	return seg, false
}

// isTemplatedUnderStates walks the trailing segments after "/states/" and
// decides templated-vs-not based on the structural position.
func isTemplatedUnderStates(rest []string) bool {
	rest = trimStateNesting(rest)
	if len(rest) == 0 {
		return false
	}
	last := rest[len(rest)-1]
	leaf, _ := stripIndex(last)
	switch leaf {
	case "view":
		return true
	case "initial":
		return len(rest) == 1
	}
	if len(rest) >= 4 && rest[0] == "on" {
		if _, hasIdx := stripIndex(rest[1]); hasIdx {
			return isTemplatedEffectPath(rest[2:], leaf)
		}
	}
	if len(rest) >= 2 {
		if root, hasIdx := stripIndex(rest[0]); hasIdx && root == "on_enter" {
			return isTemplatedEffectPath(rest[1:], leaf)
		}
	}
	if len(rest) >= 3 && rest[0] == "on" {
		if _, hasIdx := stripIndex(rest[1]); hasIdx {
			switch leaf {
			case "target", "guard_hint":
				return len(rest) == 3
			}
		}
	}
	return false
}

// trimStateNesting strips alternating "<state-name>" / "states" pairs
// from the front of rest. Lets nested compound/parallel children
// resolve under the same leaf detection as their parent.
func trimStateNesting(rest []string) []string {
	for len(rest) > 0 {
		head := rest[0]
		if head == "on" || head == "on_enter" || head == "view" ||
			head == "initial" || head == "description" ||
			head == "relevant_world" || head == "relevant_slots" ||
			head == "menu" || head == "intents" || head == "type" ||
			head == "mode" || head == "terminal" || head == "timeout" {
			return rest
		}
		rest = rest[1:]
		if len(rest) > 0 && rest[0] == "states" {
			rest = rest[1:]
		} else {
			return rest
		}
	}
	return rest
}

// isTemplatedEffectPath returns true for the effect-leaf shapes that
// hold *string-output* templated values (rendered via pongo2 / Render).
// rest is the sub-path starting *after* the "effects[N]" or
// "on_enter[N]" segment.
//
// Pragmatic split (see proposal §3, scope clarification): `set:` RHS
// and host-invoke `with:` args are evaluated via expr.RenderValue
// which preserves typed return values (bool, int64) — these stay on
// expr-lang and are NOT rewritten by the codemod. Only string-output
// leaves (`say:`, dynamic `target:` inside on_complete chains) move to
// pongo2 syntax.
//
// CONTRACT WARNING (post-migration fence):
// `with:` args are EXCLUDED here because some host functions today
// accept `body:` and similar text-shaped args that legitimately contain
// legacy `{{ if … }}{{ end }}` block syntax (e.g. host.transport.post's
// body field in stories/oregon-trail/rooms/general_store.yaml and
// river_crossing.yaml). Those values are rendered via expr.RenderValue,
// which falls back to the expr-lang block-template engine. If any host
// function ever migrates its `with: <field>` rendering to pongo2 (e.g.
// because "it's a string-output field"), the legacy block syntax in
// those YAMLs will break silently. Two safe ways out:
//   (1) re-run the codemod with an extended allowlist that names the
//       specific field (e.g. /with/body/), OR
//   (2) keep the host on expr.RenderValue.
// Do not change this fence without also doing one of the above.
func isTemplatedEffectPath(rest []string, leaf string) bool {
	if len(rest) == 0 {
		return false
	}
	switch leaf {
	case "say":
		return true
	case "target":
		return true // only meaningful inside on_complete chains
	}
	if root, hasIdx := stripIndex(rest[0]); hasIdx && root == "on_complete" {
		return isTemplatedEffectPath(rest[1:], leaf)
	}
	return false
}

// isTemplatedUnderProposals handles the small set of templated fields
// inside the top-level proposals: block.
//
// `execute/with/...` args mirror effect `with:` — they're evaluated via
// expr.RenderValue to preserve typed values for host invocations.
// Excluded from the pongo2 rewrite.
func isTemplatedUnderProposals(rest []string) bool {
	if len(rest) < 2 {
		return false
	}
	if len(rest) >= 3 && rest[1] == "views" {
		return true
	}
	return false
}
