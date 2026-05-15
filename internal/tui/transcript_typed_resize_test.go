// transcript_typed_resize_test.go — coverage for Issue 4 of the
// view-elements code review: typed-view entries must re-render at the
// new viewport width on tea.WindowSizeMsg / SetSize.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// TestTypedView_ReflowsOnWindowSizeMsg verifies that a typed-view entry
// appended via AppendTurnTyped re-renders its prose body at the
// post-resize viewport width. The before/after rendered strings must
// differ — at the very least, the longest line width must change.
func TestTypedView_ReflowsOnWindowSizeMsg(t *testing.T) {
	tm := newTranscriptModel(200, 40)
	// A prose paragraph that easily exceeds 30 chars but fits on one
	// line at width 200. Reflow visibility check: at width 200 the
	// body is one line; at width 30 it must wrap to multiple lines.
	long := strings.Repeat("word ", 20) // 100 chars
	typed := &app.View{
		Elements: []app.ViewElement{
			{Kind: "prose", Source: long},
		},
	}
	tm.AppendTurnTyped("user input", "", typed, expr.Env{}, nil)

	wide := tm.AllContent()
	wideLines := strings.Split(strings.TrimRight(wide, "\n"), "\n")
	wideBody := wideLines[len(wideLines)-1]
	// At width 200, the body should fit on one line.
	if strings.Count(wideBody, " word") < 10 {
		t.Fatalf("at width 200 expected the body to mostly fit on one line; got:\n%s", wide)
	}

	// Now resize to 30 chars wide. After the WindowSizeMsg fires,
	// the typed view re-renders and the body must wrap.
	tm2, _ := tm.Update(tea.WindowSizeMsg{Width: 30, Height: 40})
	tm = tm2

	narrow := tm.AllContent()
	// Body should now wrap. Look at the lines after the header for
	// the prose body.
	narrowBodyLines := 0
	for _, line := range strings.Split(narrow, "\n") {
		if strings.Contains(line, "word") {
			narrowBodyLines++
		}
	}
	if narrowBodyLines < 2 {
		t.Errorf("after resize to width 30, expected the prose to wrap to multiple lines; got:\n%s", narrow)
	}
	if narrow == wide {
		t.Errorf("rendered content did not change on resize; expected a different layout at width 30 vs 200")
	}
}

// TestTypedView_SetSizeReflows is the same assertion via SetSize (the
// path the RootModel takes when resize() fires). The transcript model
// recomputes both Glamour and typed-view entries on this seam.
func TestTypedView_SetSizeReflows(t *testing.T) {
	tm := newTranscriptModel(200, 40)
	long := strings.Repeat("word ", 20)
	typed := &app.View{
		Elements: []app.ViewElement{
			{Kind: "prose", Source: long},
		},
	}
	tm.AppendTurnTyped("user input", "", typed, expr.Env{}, nil)
	wide := tm.AllContent()

	tm.SetSize(30, 40, 30, 40)
	narrow := tm.AllContent()
	if narrow == wide {
		t.Errorf("typed-view entry did not reflow on SetSize")
	}
	narrowBodyLines := 0
	for _, line := range strings.Split(narrow, "\n") {
		if strings.Contains(line, "word") {
			narrowBodyLines++
		}
	}
	if narrowBodyLines < 2 {
		t.Errorf("expected prose to wrap to multiple lines after SetSize(30); got:\n%s", narrow)
	}
}
