package blocks

import (
	"strings"
	"testing"
)

func newTestRenderer(width int) *Renderer {
	r := New(width, "default")
	r.NoColor = true
	return r
}

func TestUserTurnPrefix(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	got := r.UserTurn("hello")
	if !strings.HasPrefix(got, "> hello") {
		t.Errorf("user turn should start with '> hello', got %q", got)
	}
}

// TestRoutingResolvedFormats exercises every settled-line variant from
// the proposal's "Settled-line format" table. One golden per source,
// asserting the visible substring — palette tweaks don't break the
// test.
func TestRoutingResolvedFormats(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	cases := []struct {
		name string
		in   Resolved
		want string
	}{
		{"deterministic", Resolved{Kind: "nav", Intent: "back", Source: SourceDeterministic},
			"→ nav: back   (deterministic · 1.00)"},
		{"synonym", Resolved{Kind: "in-room", Intent: "pick_branch", Source: SourceSynonym},
			"→ in-room: pick_branch   (synonym · 1.00)"},
		{"slot-parser", Resolved{Kind: "in-room", Intent: "set_count", Source: SourceSlotParser, Detail: "slots: {n: 3}"},
			"→ in-room: set_count   (slot-parser)   slots: {n: 3}"},
		{"cache", Resolved{Kind: "in-room", Intent: "pick_branch", Source: SourceCache},
			"→ in-room: pick_branch   (cached)"},
		{"llm", Resolved{Kind: "in-room", Intent: "pick_branch", Source: SourceLLM, Confidence: 0.84, Detail: `slots: {branch: "backup"}`},
			`→ in-room: pick_branch   (LLM · 0.84)   slots: {branch: "backup"}`},
		{"ambiguous", Resolved{Source: SourceAmbiguous},
			"? need clarification:"},
		{"unknown", Resolved{Intent: "/foo", Source: SourceUnknown},
			"(unknown command: /foo) — try /help"},
		{"off-path", Resolved{Source: SourceOffPath},
			"→ off-path message"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := r.RoutingResolved(tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("RoutingResolved %s:\nwant substring: %q\ngot: %q", tc.name, tc.want, got)
			}
		})
	}
}

func TestRoutingStatusPhases(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	for _, p := range RoutingPhases() {
		got := r.RoutingStatus(p)
		if !strings.Contains(got, string(p)) {
			t.Errorf("RoutingStatus(%s) missing phase name; got %q", p, got)
		}
		if !strings.Contains(got, "routing:") {
			t.Errorf("RoutingStatus(%s) missing 'routing:' prefix", p)
		}
	}
}

func TestMenuRendersGuardHint(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	out := r.Menu([]MenuAction{
		{Index: 1, Name: "go", Label: "Go", Available: true},
		{Index: 2, Name: "stop", Label: "Stop", Available: false, GuardHint: "engine cold"},
	})
	if !strings.Contains(out, "engine cold") {
		t.Errorf("menu should render the guard hint, got:\n%s", out)
	}
	if !strings.Contains(out, "1. Go") || !strings.Contains(out, "2. Stop") {
		t.Errorf("menu rows missing, got:\n%s", out)
	}
}

func TestMenuEmpty(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	out := r.Menu(nil)
	if !strings.Contains(out, "no actions") {
		t.Errorf("empty menu should advertise the empty case, got %q", out)
	}
}

func TestPromptPrefixes(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	cases := map[Mode]string{
		ModeNormal:      "> ",
		ModeMeta:        "» ",
		ModeOffPath:     "# ",
		ModeSlotFilling: "? ",
		ModeAwaitingLLM: "… ",
	}
	for mode, want := range cases {
		got := r.Prompt(mode)
		if !strings.Contains(got, want) {
			t.Errorf("mode %d: want prefix %q, got %q", mode, want, got)
		}
	}
}

func TestFooterTruncate(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(20)
	out := r.Footer("very long status line that overflows", "second line short")
	// Width is 20; first line is 36 chars — must be truncated.
	if !strings.Contains(out, "…") {
		t.Errorf("footer should truncate-with-ellipsis at width 20, got:\n%s", out)
	}
}

func TestBackgroundCompleteMentionsRoom(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	got := r.BackgroundComplete("review_pr", "merged PR #4811")
	if !strings.Contains(got, "review_pr") || !strings.Contains(got, "merged PR #4811") {
		t.Errorf("expected room + summary, got %q", got)
	}
	if !strings.HasPrefix(got, "✓") {
		t.Errorf("expected leading ✓, got %q", got)
	}
}

func TestThemeFallback(t *testing.T) {
	t.Parallel()
	if ThemeByName("definitely-not-a-theme").Name != "default" {
		t.Errorf("unknown theme should fall back to default")
	}
	if ThemeByName("meta-blue").Name != "meta-blue" {
		t.Errorf("meta-blue theme should resolve by name")
	}
}

func TestRenderChatViewContainsAllBlocks(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	out := r.RenderChatView(DefaultChatFixture())
	for _, want := range []string{
		"proposing · cypilot",                       // header
		"session resumed",                           // system notice
		"> back to the proposal",                    // user turn 1
		"→ nav: back",                               // resolved
		"> use the backup branch instead",           // user turn 2
		"→ in-room: pick_branch",                    // resolved LLM
		"CI run for PR #4821",                       // inbox
		"✓ review_pr",                               // background complete
		"actions:",                                  // menu header
		"1. Open review",                            // menu row 1
		"3. Approve",                                // menu row 3
		"CI not yet green",                          // guard hint
		"proposing · cypilot · 2 queued",            // footer line 1 (mode moved off line 1 — see footerFrameworkLine)
		"PR #4821 · CI: failing (3) · PLTFRM-90014", // footer line 2
		"> _", // prompt
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chat view missing block content %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderWorldViewHierarchy(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(80)
	out := r.RenderWorldView("cypilot", WorldFixture())
	// Hierarchy markers must be present so the user knows what's
	// expandable.
	for _, want := range []string{"▾ session", "▾ user", "▾ tickets [3]", "▸ flags", "▸ providers"} {
		if !strings.Contains(out, want) {
			t.Errorf("world view missing %q, full:\n%s", want, out)
		}
	}
}
