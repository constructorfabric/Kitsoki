package tui

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// RenderingAnalyzer provides tools to analyze and assert on View() output.
// Use it to write regression tests for rendering problems.
type RenderingAnalyzer struct {
	raw      string
	lines    []string
	stripped string // ANSI codes removed
	t        *testing.T
}

// NewRenderingAnalyzer creates an analyzer for the given View() output.
func NewRenderingAnalyzer(t *testing.T, viewOutput string) *RenderingAnalyzer {
	lines := strings.Split(viewOutput, "\n")
	return &RenderingAnalyzer{
		raw:      viewOutput,
		lines:    lines,
		stripped: stripANSI(viewOutput),
		t:        t,
	}
}

// Dump prints the full output with line numbers for debugging.
func (ra *RenderingAnalyzer) Dump() {
	ra.t.Logf("\n=== Rendered Output (with ANSI codes) ===\n")
	for i, line := range ra.lines {
		ra.t.Logf("Line %2d (bytes=%3d): %q\n", i, len(line), truncateStr(line, 70))
	}

	ra.t.Logf("\n=== Stripped Output (ANSI removed) ===\n")
	strippedLines := strings.Split(ra.stripped, "\n")
	for i, line := range strippedLines {
		ra.t.Logf("Line %2d (len=%3d): %q\n", i, len(line), truncateStr(line, 70))
	}
}

// LineCount returns the number of lines in the output.
func (ra *RenderingAnalyzer) LineCount() int {
	return len(ra.lines)
}

// Lines returns all lines in the output (with ANSI codes).
func (ra *RenderingAnalyzer) Lines() []string {
	return ra.lines
}

// StrippedLines returns all lines with ANSI codes removed.
func (ra *RenderingAnalyzer) StrippedLines() []string {
	return strings.Split(ra.stripped, "\n")
}

// AssertLineCount verifies the output has exactly n lines.
func (ra *RenderingAnalyzer) AssertLineCount(n int) {
	require.Equal(ra.t, n, len(ra.lines),
		"expected %d lines, got %d", n, len(ra.lines))
}

// AssertContains checks if any line contains the given text (ANSI-stripped).
func (ra *RenderingAnalyzer) AssertContains(text string) {
	require.Contains(ra.t, ra.stripped, text,
		"output should contain %q", text)
}

// AssertNotContains checks that no line contains the given text.
func (ra *RenderingAnalyzer) AssertNotContains(text string) {
	require.NotContains(ra.t, ra.stripped, text,
		"output should NOT contain %q", text)
}

// AssertLineSeparation verifies that text1 and text2 are on different lines.
// This is the key test for preventing overlap bugs.
func (ra *RenderingAnalyzer) AssertLineSeparation(text1, text2 string) {
	strippedLines := ra.StrippedLines()
	var line1Idx, line2Idx int
	found1, found2 := false, false

	for i, line := range strippedLines {
		if strings.Contains(line, text1) {
			line1Idx = i
			found1 = true
		}
		if strings.Contains(line, text2) {
			line2Idx = i
			found2 = true
		}
	}

	require.True(ra.t, found1, "should find %q in output", text1)
	require.True(ra.t, found2, "should find %q in output", text2)
	require.NotEqual(ra.t, line1Idx, line2Idx,
		"%q (line %d) and %q (line %d) must be on different lines",
		text1, line1Idx, text2, line2Idx)
}

// AssertNoHorizontalConcat verifies that text1 and text2 don't appear
// on the same line (a symptom of horizontal concatenation bugs).
func (ra *RenderingAnalyzer) AssertNoHorizontalConcat(text1, text2 string) {
	strippedLines := ra.StrippedLines()
	for i, line := range strippedLines {
		hasBoth := strings.Contains(line, text1) && strings.Contains(line, text2)
		require.False(ra.t, hasBoth,
			"line %d should NOT contain both %q and %q (would indicate horizontal concatenation): %q",
			i, text1, text2, truncateStr(line, 80))
	}
}

// AssertMaxLineWidth returns the maximum visible width (ANSI-stripped)
// of any line. Useful for detecting padding issues.
func (ra *RenderingAnalyzer) AssertMaxLineWidth() int {
	strippedLines := ra.StrippedLines()
	maxWidth := 0
	for _, line := range strippedLines {
		if len(line) > maxWidth {
			maxWidth = len(line)
		}
	}
	return maxWidth
}

// AssertLineByteSizes prints the byte size of each line. Useful for
// detecting padding or encoding issues.
func (ra *RenderingAnalyzer) AssertLineByteSizes() map[int]int {
	sizes := make(map[int]int)
	for i, line := range ra.lines {
		sizes[i] = len(line)
	}
	return sizes
}

// AssertStructure verifies the output has the expected overall structure.
// Useful for catching regressions when layout changes.
func (ra *RenderingAnalyzer) AssertStructure(expectedElements ...string) {
	strippedLines := ra.StrippedLines()
	var found []bool
	for i, elem := range expectedElements {
		found = append(found, false)
		for _, line := range strippedLines {
			if strings.Contains(line, elem) {
				found[i] = true
				break
			}
		}
	}

	for i, elem := range expectedElements {
		require.True(ra.t, found[i],
			"expected output to contain %q", elem)
	}
}

// truncateStr returns the first n runes of s, or s if shorter.
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return s
}

// stripANSI removes ANSI escape codes from a string.
// Used to analyze visible output independent of terminal colors/styles.
func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '@' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

// RenderingTestScenario defines a controlled test scenario for View() rendering.
// Use it to set up specific states and verify rendering output.
type RenderingTestScenario struct {
	Mode             Mode
	LiveLineContent  string
	PromptContent    string
	InputQueueDepth  int
	BannerContent    string
	FooterContent    string
	ThemeVariant     string // "dark" or "light" for testing theme sensitivity
	TerminalWidth    int    // simulate different terminal widths
}

// DefaultScenario returns a basic scenario for testing.
func DefaultScenario() RenderingTestScenario {
	return RenderingTestScenario{
		Mode:          ModeOnPath,
		PromptContent: "test input",
		TerminalWidth: 80,
		ThemeVariant:  "dark",
	}
}

// ModeAwaitingLLMScenario creates a scenario for testing ModeAwaitingLLM rendering.
func ModeAwaitingLLMScenario() RenderingTestScenario {
	return RenderingTestScenario{
		Mode:            ModeAwaitingLLM,
		LiveLineContent: "",
		PromptContent:   "",
		InputQueueDepth: 0,
		TerminalWidth:   80,
		ThemeVariant:    "dark",
	}
}

// WithRoutingStatus sets the live routing status text.
func (s RenderingTestScenario) WithRoutingStatus(status string) RenderingTestScenario {
	s.LiveLineContent = status
	return s
}

// WithPrompt sets the prompt input text.
func (s RenderingTestScenario) WithPrompt(prompt string) RenderingTestScenario {
	s.PromptContent = prompt
	return s
}

// WithQueueDepth sets the input queue depth (for ModeAwaitingLLM).
func (s RenderingTestScenario) WithQueueDepth(depth int) RenderingTestScenario {
	s.InputQueueDepth = depth
	return s
}

// WithMode sets the TUI mode.
func (s RenderingTestScenario) WithMode(mode Mode) RenderingTestScenario {
	s.Mode = mode
	return s
}

// WithTerminalWidth simulates a specific terminal width.
func (s RenderingTestScenario) WithTerminalWidth(width int) RenderingTestScenario {
	s.TerminalWidth = width
	return s
}

// RenderingTestHelper is a helper to write regression tests.
// Example usage:
//
//	NewRenderingTestHelper(t, ModeAwaitingLLMScenario().
//		WithRoutingStatus("↳ resolved: view").
//		WithPrompt("test").
//		WithQueueDepth(1)).
//		AssertNoHorizontalConcat("resolved", "queue:").
//		AssertLineSeparation("resolved", "⏳")
type RenderingTestHelper struct {
	t        *testing.T
	scenario RenderingTestScenario
}

// NewRenderingTestHelper creates a helper for testing the given scenario.
// Note: This is a placeholder - full implementation would require
// access to RootModel internals or a test double.
func NewRenderingTestHelper(t *testing.T, scenario RenderingTestScenario) *RenderingTestHelper {
	return &RenderingTestHelper{
		t:        t,
		scenario: scenario,
	}
}

// Render simulates rendering the scenario. Returns an analyzer for assertions.
// This is a placeholder - actual implementation would call RootModel.View().
func (rth *RenderingTestHelper) Render() *RenderingAnalyzer {
	// TODO: Implement actual rendering simulation or integration
	// For now, return a placeholder analyzer
	return NewRenderingAnalyzer(rth.t, "")
}

// AssertNoHorizontalConcat is a convenience method for regression testing.
func (rth *RenderingTestHelper) AssertNoHorizontalConcat(text1, text2 string) *RenderingTestHelper {
	rth.Render().AssertNoHorizontalConcat(text1, text2)
	return rth
}

// AssertLineSeparation is a convenience method for regression testing.
func (rth *RenderingTestHelper) AssertLineSeparation(text1, text2 string) *RenderingTestHelper {
	rth.Render().AssertLineSeparation(text1, text2)
	return rth
}

// === Integration Test Helpers ===
// Use these when testing bugs that involve concurrent I/O, external systems,
// or interactions between the TUI and other components (slog, files, etc).
// See SKILL.md "Critical Pitfall: When Unit Tests Aren't Enough" for when to use these.

// CapturedIO holds the output from systems that write concurrently (slog, stderr, etc).
// Use this to test bugs where multiple sources write at the same time.
type CapturedIO struct {
	StderrBuf  strings.Builder
	StdoutBuf  strings.Builder
	oldDefault *slog.Logger
	t          *testing.T
}

// CaptureSlog redirects slog to a buffer so you can inspect what actually gets logged.
// Call Restore() in a defer to restore the original logger.
//
// Usage:
//
//	captured := CaptureSlog(t)
//	defer captured.Restore()
//
//	// Now slog writes go to captured.StderrBuf
//	slog.Info("test event")
//
//	// Assert on actual logged output
//	if strings.Contains(captured.StderrBuf.String(), "⏳") {
//	    t.Fatal("slog output mixed with queue indicator")
//	}
func CaptureSlog(t *testing.T) *CapturedIO {
	captured := &CapturedIO{t: t}
	captured.oldDefault = slog.Default()
	suppressedLogger := slog.New(slog.NewTextHandler(&captured.StderrBuf, nil))
	slog.SetDefault(suppressedLogger)
	return captured
}

// Restore restores the original slog logger.
func (c *CapturedIO) Restore() {
	if c.oldDefault != nil {
		slog.SetDefault(c.oldDefault)
	}
}

// AssertNoMixedOutput checks that output from two sources (like slog logs and queue indicator)
// don't appear on the same line. This catches race conditions where concurrent I/O mixes.
//
// Example:
//
//	captured := CaptureSlog(t)
//	defer captured.Restore()
//
//	// Concurrent goroutine logging while TUI renders
//	go func() {
//	    for i := 0; i < 50; i++ {
//	        slog.Info("oracle.event", "type", "system")
//	        time.Sleep(time.Microsecond)
//	    }
//	}()
//
//	// TUI rendering
//	for i := 0; i < 50; i++ {
//	    m.View()
//	    time.Sleep(time.Microsecond)
//	}
//
//	// Verify logs didn't mix with queue indicator
//	captured.AssertNoMixedOutput("INFO", "⏳", "indicator in log lines")
func (c *CapturedIO) AssertNoMixedOutput(sourceAMarker, sourceBMarker, description string) {
	lines := strings.Split(c.StderrBuf.String(), "\n")
	for _, line := range lines {
		hasA := strings.Contains(line, sourceAMarker)
		hasB := strings.Contains(line, sourceBMarker)
		require.False(c.t, hasA && hasB,
			"concurrent I/O bug (%s): line has both %q and %q: %q",
			description, sourceAMarker, sourceBMarker, truncateStr(line, 100))
	}
}

// SimulateConurrentIO is a helper for testing race conditions between slog and TUI rendering.
// It runs a logging goroutine concurrently with rendering to simulate the timing where
// logs and UI output mix on the same terminal line.
//
// Example:
//
//	captured := CaptureSlog(t)
//	defer captured.Restore()
//
//	helper := NewConcurrentIOTester(t, captured)
//	helper.LogConcurrently(func() {
//	    for i := 0; i < 50; i++ {
//	        slog.Info("oracle.event", "turn", i)
//	    }
//	}).
//	RenderConcurrently(func() {
//	    for i := 0; i < 50; i++ {
//	        m.View()
//	    }
//	})
//
//	// After concurrent operations, verify the result
//	captured.AssertNoMixedOutput("INFO", "⏳", "queue indicator")
type ConcurrentIOTester struct {
	t        *testing.T
	captured *CapturedIO
}

// NewConcurrentIOTester creates a helper for testing concurrent I/O scenarios.
func NewConcurrentIOTester(t *testing.T, captured *CapturedIO) *ConcurrentIOTester {
	return &ConcurrentIOTester{t: t, captured: captured}
}

// LogConcurrently runs the given function in a goroutine to simulate concurrent logging.
// Use this with RenderConcurrently to test race conditions.
func (cit *ConcurrentIOTester) LogConcurrently(fn func()) *ConcurrentIOTester {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	// Give logging goroutine a moment to interleave with rendering
	time.Sleep(time.Microsecond)
	// Wait for logging to complete
	<-done
	return cit
}

// RenderConcurrently runs rendering concurrent with logging.
// The logging goroutine started by LogConcurrently runs in parallel with this.
func (cit *ConcurrentIOTester) RenderConcurrently(fn func()) *ConcurrentIOTester {
	fn()
	return cit
}
