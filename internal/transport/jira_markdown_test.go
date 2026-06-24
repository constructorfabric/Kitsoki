package transport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each subtest covers one conversion rule.  Order roughly mirrors the
// pipeline in sanitizeForJira so failures point at the right step.
func TestSanitizeForJira_ConversionRules(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain prose untouched", "plain prose with no markdown.", "plain prose with no markdown."},

		{"h1", "# Hello", "h1. Hello"},
		{"h2", "## Hello", "h2. Hello"},
		{"h3", "### Hello", "h3. Hello"},
		{"h6", "###### Hello", "h6. Hello"},

		{"bold simple", "**bold**", "*bold*"},
		{"bold inside text", "see **this**", "see *this*"},
		{"bold with softwrap", "**no rendered-URL\n   validation**", "*no rendered-URL    validation*"},

		{"strike", "~~old~~", "-old-"},

		{"bullets dash", "- one\n- two", "* one\n* two"},
		{"bullets star", "* one\n* two", "* one\n* two"},
		{"bullets nested 2sp", "- top\n  - child", "* top\n** child"},
		{"bullets nested 4sp", "- top\n    - child", "* top\n** child"},

		{"numbered", "1. first\n2. second", "# first\n# second"},

		{"blockquote", "> quoted line", "bq. quoted line"},

		{"hr dashes", "---", "----"},
		{"hr stars", "***", "----"},
		{"hr underscores", "___", "----"},

		{"inline code", "use `foo` here", "use {{foo}} here"},
		{"inline code with star inside", "see `*url.URL` type", "see {{*url.URL}} type"},

		{"link", "see [docs](https://example.com)", "see [docs|https://example.com]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeForJira(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeForJira(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

// Fenced code blocks: `lang` should map to `{code:lang}`; bare fences to
// `{code}`.  Inner content (including stars/headings) must NOT be
// rewritten.
func TestSanitizeForJira_FencedCode(t *testing.T) {
	t.Run("with lang", func(t *testing.T) {
		in := "```go\nfunc f() { *x = 1 }\n```"
		want := "{code:go}\nfunc f() { *x = 1 }\n{code}"
		got := sanitizeForJira(in)
		if got != want {
			t.Fatalf("\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("no lang", func(t *testing.T) {
		in := "```\nplain code\n```"
		want := "{code}\nplain code\n{code}"
		got := sanitizeForJira(in)
		if got != want {
			t.Fatalf("\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("inner markdown preserved", func(t *testing.T) {
		in := "```\n# not a heading\n- not a bullet\n**not bold**\n```"
		want := "{code}\n# not a heading\n- not a bullet\n**not bold**\n{code}"
		got := sanitizeForJira(in)
		if got != want {
			t.Fatalf("\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("unclosed fence is closed", func(t *testing.T) {
		// LLM truncated mid-block — sanitiser must close the fence so
		// the regex still wraps the content in {code} cleanly.
		in := "```\nstart of block\nno closer"
		got := sanitizeForJira(in)
		if !strings.Contains(got, "{code}") {
			t.Fatalf("expected {code} wrapper, got: %q", got)
		}
		if strings.Contains(got, "```") {
			t.Fatalf("expected backticks gone, got: %q", got)
		}
	})

	t.Run("multiple blocks distinct", func(t *testing.T) {
		in := "```go\nA\n```\n\n```py\nB\n```"
		got := sanitizeForJira(in)
		if !strings.Contains(got, "{code:go}") || !strings.Contains(got, "{code:py}") {
			t.Fatalf("missing both langs: %q", got)
		}
		if !strings.Contains(got, "A") || !strings.Contains(got, "B") {
			t.Fatalf("missing content: %q", got)
		}
	})
}

// Emojis (4-byte UTF-8) get replaced with `(emoji)` to dodge Jira MySQL
// utf8 (not utf8mb4) rejection.
func TestSanitizeForJira_StripsEmojis(t *testing.T) {
	in := "all good ✨" // U+2728 is 3-byte, must be preserved
	got := sanitizeForJira(in)
	if got != in {
		t.Fatalf("3-byte rune got mutated:\n got: %q\nwant: %q", got, in)
	}

	in = "all good \U0001F600 done" // U+1F600 is 4-byte
	got = sanitizeForJira(in)
	want := "all good (emoji) done"
	if got != want {
		t.Fatalf("4-byte rune not stripped:\n got: %q\nwant: %q", got, want)
	}
}

// Markdown tables convert to Jira's `||header||` / `|cell|` shape.
func TestSanitizeForJira_Tables(t *testing.T) {
	in := "| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |"
	got := sanitizeForJira(in)
	want := "||a||b||\n|1|2|\n|3|4|"
	if got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
}

// Combined realistic LLM output: bold + bullets + fenced code + link.
// This mirrors what `summary_markdown` looks like for a coverage-review
// (phase_1_7) artifact.
func TestSanitizeForJira_RealisticSummary(t *testing.T) {
	in := strings.Join([]string{
		"# PLTFRM-89912 — reproduction checkpoint",
		"",
		"**Result: PASS**",
		"",
		"Reproduced the bug on `mc-clean-25134`.",
		"",
		"- step 1: hit the endpoint",
		"- step 2: observe SSRF",
		"",
		"```go",
		"customRequest := request.CustomMethod(v.Method, restContextURL+renderedURL)",
		"```",
		"",
		"See [docs](https://example.com/x).",
	}, "\n")

	got := sanitizeForJira(in)

	// Spot-check key conversions.
	for _, want := range []string{
		"h1. PLTFRM-89912 — reproduction checkpoint",
		"*Result: PASS*",
		"{{mc-clean-25134}}",
		"* step 1: hit the endpoint",
		"* step 2: observe SSRF",
		"{code:go}",
		"customRequest := request.CustomMethod(v.Method, restContextURL+renderedURL)",
		"{code}",
		"[docs|https://example.com/x]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got:\n%s", want, got)
		}
	}

	// Negative checks: source markdown must NOT survive verbatim.
	for _, leak := range []string{"**Result", "```go", "[docs](https"} {
		if strings.Contains(got, leak) {
			t.Errorf("unexpected raw markdown %q leaked through:\n%s", leak, got)
		}
	}
}

// Snapshot test: feed each on-disk PLTFRM-89912 fixture through the
// sanitiser and assert the output (a) is non-empty and (b) carries no
// raw `**bold**` / `# heading` artefacts.
func TestSanitizeForJira_Fixtures(t *testing.T) {
	dir := "testdata"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if len(entries) == 0 {
		t.Skip("no fixtures present")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			path := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			out := sanitizeForJira(string(raw))
			if out == "" {
				t.Fatalf("sanitised output is empty")
			}
			// Confirm no Markdown bold survives at line scope.  We
			// intentionally do NOT check `# ` because fenced code
			// blocks legitimately contain `#` characters (Go code,
			// shell prompts, etc.) and the regex pulls them into a
			// {code} block first.
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "**") &&
					!strings.HasPrefix(line, "{code") {
					// Bold takes the form `**…**`; bail only when both
					// markers present on the same line (so single-`**`
					// inside `{code}` blocks doesn't false-positive).
					if strings.Count(line, "**") >= 2 {
						t.Errorf("raw **bold** survived in %s:\n  %s", e.Name(), line)
						break
					}
				}
			}
		})
	}
}

// depthForIndent must implement the 4-stop-then-2-stop fallback that the
// Python reference uses.  Drives bullet/numbered nesting depth.
func TestDepthForIndent(t *testing.T) {
	cases := []struct {
		in, out int
	}{
		{0, 1},
		{2, 2},
		{4, 2},
		{6, 4}, // 6/2 + 1 = 4 (matches Python ref)
		{8, 3},
	}
	for _, c := range cases {
		if got := depthForIndent(c.in); got != c.out {
			t.Errorf("depthForIndent(%d) = %d, want %d", c.in, got, c.out)
		}
	}
}

// tabExpandedLen mirrors Python's str.expandtabs(4).
func TestTabExpandedLen(t *testing.T) {
	cases := []struct {
		in  string
		out int
	}{
		{"", 0},
		{"    ", 4},
		{"\t", 4},
		{"\t\t", 8},
		{"  \t", 4},
		{"   \t", 4}, // 3 spaces → next 4-stop is +1
	}
	for _, c := range cases {
		if got := tabExpandedLen(c.in); got != c.out {
			t.Errorf("tabExpandedLen(%q) = %d, want %d", c.in, got, c.out)
		}
	}
}
