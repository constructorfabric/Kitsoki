// Markdown → Jira-wiki sanitiser used by the Jira transport.
//
// Pipeline LLMs emit Markdown by default (``**bold**``, `# heading`,
// ``- bullet``, fenced code, etc.).  Jira Server renders those as literal
// escaped text which is unreadable.  This converter turns common Markdown
// idioms into the wiki-markup Jira understands.
//
// The conversion rules and ordering mirror tools/loopy/bugfix/lib/jira.py
// `_sanitize_for_jira` so the two surfaces (loopy direct-post and kitsoki
// transport) produce identical output for the same input.

package transport

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// wideCodepointBoundary is the first code point (U+10000) that UTF-8
	// encodes in four bytes. Jira Server's MySQL `utf8` charset (not
	// `utf8mb4`) rejects four-byte sequences, so everything at or above this
	// boundary — emoji, astral-plane symbols — is replaced before posting.
	wideCodepointBoundary = 0x10000

	// tabWidth is the number of columns a tab expands to when measuring list
	// indentation. It matches Python's str.expandtabs(4) so kitsoki and
	// tools/loopy compute the same nesting depth for the same input.
	tabWidth = 4

	// indentFallback is the secondary indentation stop (in columns) used when
	// an indent is not a clean multiple of tabWidth. Two-space indents are a
	// common LLM convention, so they must still nest rather than collapse to
	// depth 1.
	indentFallback = 2
)

// sanitizeForJira converts Markdown to Jira Server wiki markup and strips
// codepoints that Jira's MySQL utf8 (not utf8mb4) backend rejects.
//
// Conversion order is important — fenced code blocks and inline code are
// extracted to placeholders first so their contents are not rewritten by
// the bullet/bold/heading rules.
func sanitizeForJira(text string) string {
	if text == "" {
		return text
	}

	// 1. Strip 4-byte UTF-8 codepoints (emojis) — Jira MySQL utf8 rejects them.
	text = stripWideCodepoints(text)

	// 1a. Unescape common Markdown backslash escapes that LLMs emit
	//     defensively when they think they're "safely" producing Markdown
	//     for downstream rendering.  Without this, `\*\*bold\*\*` survives
	//     past the bold regex (which expects literal `**...**`) and lands
	//     in Jira as backslash-asterisk pairs that render as plain text.
	//     The eight characters here are the Markdown special set; we strip
	//     the leading backslash, leaving the literal char.
	text = mdEscapeRe.ReplaceAllString(text, "$1")

	// 1b. Defensively close any unclosed ``` block so the regex below
	//     wraps the whole block in {code} instead of leaking backticks.
	text = closeUnclosedFences(text)

	// 2. Pull fenced code blocks out so we don't mutate their contents.
	var fencePlaceholders []string
	text = fenceRe.ReplaceAllStringFunc(text, func(match string) string {
		m := fenceRe.FindStringSubmatch(match)
		lang := strings.TrimSpace(m[1])
		content := strings.TrimRight(m[2], "\n")
		var wiki string
		if lang != "" {
			wiki = fmt.Sprintf("{code:%s}\n%s\n{code}", lang, content)
		} else {
			wiki = fmt.Sprintf("{code}\n%s\n{code}", content)
		}
		fencePlaceholders = append(fencePlaceholders, wiki)
		return fmt.Sprintf("\x00FENCE%d\x00", len(fencePlaceholders)-1)
	})

	// 2b. Markdown tables → Jira tables.  Done before inline code/bold so
	//     pipe-delimited cells are not corrupted by other rules.
	text = convertMarkdownTables(text)

	// 3. Inline code: `foo` → {{foo}}.  Pull into placeholders so a stray
	//    `*` inside (e.g. `*url.URL`) doesn't break the bold regex below.
	var inlineCodes []string
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(match string) string {
		m := inlineCodeRe.FindStringSubmatch(match)
		inlineCodes = append(inlineCodes, "{{"+m[1]+"}}")
		return fmt.Sprintf("\x00INLINE%d\x00", len(inlineCodes)-1)
	})

	// 4. Headings: `# H` → `h1. H` (up to h6).  Match longest first so
	//    `###` doesn't accidentally match the leading `#` of `### `.
	text = applyMultiline(text, headingRe, func(line string) string {
		m := headingRe.FindStringSubmatch(line)
		level := len(m[1])
		if level > 6 {
			level = 6
		}
		return fmt.Sprintf("h%d. %s", level, m[2])
	})

	// 5. Bullets: leading `- ` or `* ` → `* ` (Jira uses `*`).  Indent
	//    depth uses 4-space stops with a 2-space fallback so both LLM
	//    conventions (4-indent and 2-indent) nest correctly.
	text = applyMultiline(text, bulletRe, func(line string) string {
		m := bulletRe.FindStringSubmatch(line)
		indent := tabExpandedLen(m[1])
		depth := depthForIndent(indent)
		return strings.Repeat("*", depth) + " " + m[2]
	})

	// 6. Numbered lists: `1. x` → `# x`.
	text = applyMultiline(text, numberedRe, func(line string) string {
		m := numberedRe.FindStringSubmatch(line)
		indent := tabExpandedLen(m[1])
		depth := depthForIndent(indent)
		return strings.Repeat("#", depth) + " " + m[2]
	})

	// 6b. Blockquote: `> text` → `bq. text`.
	text = applyMultiline(text, blockquoteRe, func(line string) string {
		m := blockquoteRe.FindStringSubmatch(line)
		return "bq. " + m[1]
	})

	// 6c. Horizontal rule: a line containing only `---`/`***`/`___` →
	//     Jira's `----`.  Markdown allows 3+ chars; Jira renders 4.
	text = applyMultiline(text, hrRe, func(_ string) string { return "----" })

	// 7. Bold: `**x**` → `*x*`.  Allow a single soft-wrap newline inside
	//    the bold so prose like `**no rendered-URL\n   validation**`
	//    (LLM output that wraps at 80 cols) still converts.
	text = boldRe.ReplaceAllStringFunc(text, func(match string) string {
		m := boldRe.FindStringSubmatch(match)
		inner := strings.ReplaceAll(m[1], "\n", " ")
		return "*" + inner + "*"
	})

	// 7b. Strikethrough: `~~x~~` → `-x-`.
	text = strikeRe.ReplaceAllString(text, "-$1-")

	// 8. Markdown links: `[text](url)` → `[text|url]`.
	text = linkRe.ReplaceAllString(text, "[$1|$2]")

	// 9. Restore inline-code, then fenced code blocks.
	text = inlinePlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		m := inlinePlaceholderRe.FindStringSubmatch(match)
		var idx int
		_, _ = fmt.Sscanf(m[1], "%d", &idx)
		if idx < 0 || idx >= len(inlineCodes) {
			return match
		}
		return inlineCodes[idx]
	})
	text = fencePlaceholderRe.ReplaceAllStringFunc(text, func(match string) string {
		m := fencePlaceholderRe.FindStringSubmatch(match)
		var idx int
		_, _ = fmt.Sscanf(m[1], "%d", &idx)
		if idx < 0 || idx >= len(fencePlaceholders) {
			return match
		}
		return fencePlaceholders[idx]
	})

	return text
}

// stripWideCodepoints replaces every codepoint at U+10000 or above with
// the literal "(emoji)".  Jira Server's MySQL `utf8` charset (not
// `utf8mb4`) rejects 4-byte UTF-8 sequences with a 500.
func stripWideCodepoints(s string) string {
	if !containsWideRune(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= wideCodepointBoundary {
			b.WriteString("(emoji)")
		} else {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func containsWideRune(s string) bool {
	for _, r := range s {
		if r >= wideCodepointBoundary {
			return true
		}
	}
	return false
}

// closeUnclosedFences appends a closing ``` if the text ends inside an
// open fenced block.  LLM bodies are sometimes truncated mid-fence;
// without this, the regex below leaves the loose backticks as literals
// and the body inside is mangled by the bullet/bold rules.
func closeUnclosedFences(text string) string {
	if text == "" {
		return text
	}
	opens := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			opens++
		}
	}
	if opens%2 == 1 {
		sep := ""
		if !strings.HasSuffix(text, "\n") {
			sep = "\n"
		}
		text = text + sep + "```\n"
	}
	return text
}

// depthForIndent maps a leading whitespace count (in spaces, tabs
// expanded to 4) to a Jira-list nesting depth.  4-space stops first,
// 2-space fallback to support both LLM conventions:
//
//	""       → 1
//	"  "     → 2
//	"    "   → 2
//	"      " → 3
//	"        " → 3
func depthForIndent(indent int) int {
	if indent >= tabWidth && indent%tabWidth == 0 {
		return indent/tabWidth + 1
	}
	if indent >= indentFallback && indent%indentFallback == 0 {
		return indent/indentFallback + 1
	}
	d := indent/tabWidth + 1
	if d < 1 {
		return 1
	}
	return d
}

// tabExpandedLen returns the visual width of `s` with tabs expanded as 4
// spaces.  Matches Python's str.expandtabs(4).
func tabExpandedLen(s string) int {
	n := 0
	for _, r := range s {
		if r == '\t' {
			n += tabWidth - (n % tabWidth)
		} else {
			n++
		}
	}
	return n
}

// applyMultiline runs `fn` on every line whose contents match `re`.  It
// preserves line endings and avoids the Go regexp engine's lack of true
// multiline anchors when alternation interacts with `\n`.
func applyMultiline(text string, re *regexp.Regexp, fn func(line string) string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if re.MatchString(line) {
			lines[i] = fn(line)
		}
	}
	return strings.Join(lines, "\n")
}

// convertMarkdownTables converts `| a | b |\n|---|---|\n| 1 | 2 |` to
// Jira's `||a||b||\n|1|2|`.  Only contiguous blocks where the second
// line is a separator (`|---|`) are converted; everything else is left
// alone so prose with stray pipes is not mangled.
func convertMarkdownTables(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		if i+1 < len(lines) && tableRowRe.MatchString(lines[i]) && tableSepRe.MatchString(lines[i+1]) {
			header := splitTableRow(lines[i])
			out = append(out, "||"+strings.Join(header, "||")+"||")
			i += 2
			for i < len(lines) && tableRowRe.MatchString(lines[i]) {
				cells := splitTableRow(lines[i])
				for j, c := range cells {
					if c == "" {
						cells[j] = " "
					}
				}
				out = append(out, "|"+strings.Join(cells, "|")+"|")
				i++
			}
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return strings.Join(out, "\n")
}

func splitTableRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// ── compiled patterns ────────────────────────────────────────────────────────

var (
	// Fenced code block: ```lang\ncontent\n```  (lang optional, single line of opener)
	// `(?s)` so `.` matches newlines.  Lang must not contain whitespace
	// or backticks.
	fenceRe = regexp.MustCompile("(?s)```([^\\s`]*)\\n(.*?)```")

	// Inline code: `foo` (single backtick, no embedded newline).  Greedy
	// is fine because [^`\n] forbids the closer.
	inlineCodeRe = regexp.MustCompile("`([^`\n]+)`")

	headingRe    = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	bulletRe     = regexp.MustCompile(`^([ \t]*)[-*]\s+(.*)$`)
	numberedRe   = regexp.MustCompile(`^([ \t]*)\d+\.\s+(.*)$`)
	blockquoteRe = regexp.MustCompile(`^>\s?(.*)$`)
	hrRe         = regexp.MustCompile(`^[ \t]*[-*_]{3,}[ \t]*$`)

	// Bold with optional single soft-wrap newline inside.  Non-greedy
	// inner so adjacent `**...**` pairs don't fuse.
	boldRe = regexp.MustCompile(`(?s)\*\*([^*\n]+(?:\n[^*\n]+)?)\*\*`)

	strikeRe = regexp.MustCompile(`~~([^~\n]+?)~~`)
	linkRe   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)

	inlinePlaceholderRe = regexp.MustCompile(`\x00INLINE(\d+)\x00`)
	fencePlaceholderRe  = regexp.MustCompile(`\x00FENCE(\d+)\x00`)

	tableRowRe = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	tableSepRe = regexp.MustCompile(`^\s*\|?\s*:?-+:?(\s*\|\s*:?-+:?)+\s*\|?\s*$`)

	// Markdown backslash escapes — strip the leading `\` and keep the
	// literal special character.  Covers the canonical CommonMark set
	// that LLMs over-escape when emitting Markdown inside JSON strings.
	mdEscapeRe = regexp.MustCompile("\\\\([*_#\\[\\]()`~|>!\\-])")
)
