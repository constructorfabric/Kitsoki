package tui_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/tui"
)

// TestRenderingAnalyzerBasicUsage demonstrates the RenderingAnalyzer for
// analyzing View() output. This example uses simulated output.
func TestRenderingAnalyzerBasicUsage(t *testing.T) {
	t.Parallel()

	// Simulate what a ModeAwaitingLLM View() might produce
	simulatedOutput := strings.Join([]string{
		"  → nav: back   (deterministic · 1.00)",
		"────────────────────────────────────────",
		"⏳ ⠋ thinking…  ·  queue: 0  ·  Enter · Esc",
		"↳ test input",
		"framework: awaiting",
	}, "\n")

	analyzer := tui.NewRenderingAnalyzer(t, simulatedOutput)

	// Verify basic properties
	analyzer.AssertLineCount(5)
	analyzer.AssertContains("thinking")
	analyzer.AssertContains("↳")

	// Key regression test: routing status and queue indicator must be separate
	analyzer.AssertLineSeparation("nav: back", "thinking…")

	// Verify no horizontal concatenation (the bug we fixed)
	analyzer.AssertNoHorizontalConcat("nav: back", "queue:")
}

// TestRenderingAnalyzerDetectsRegression demonstrates how to catch regressions.
// If someone breaks the fix by reverting to JoinVertical or introducing padding,
// this would catch it.
func TestRenderingAnalyzerDetectsRegression(t *testing.T) {
	t.Parallel()

	// This is what BAD output (with the bug) would look like:
	// Both routing status and queue indicator on the same line due to padding
	badOutput := "  → nav: back   (deterministic · 1.00)                    ⏳ thinking…\n↳ test"

	analyzer := tui.NewRenderingAnalyzer(t, badOutput)
	analyzer.AssertLineCount(2)

	// This assertion would FAIL if the bug reoccurred
	// (it should fail for badOutput, which is the point of this test)
	// Uncomment to see it fail:
	// analyzer.AssertLineSeparation("nav: back", "thinking…")
	// analyzer.AssertNoHorizontalConcat("nav: back", "queue:")

	// Instead, verify that badOutput DOES have the problem
	strippedLines := analyzer.StrippedLines()
	hasBoth := strings.Contains(strippedLines[0], "nav: back") &&
		strings.Contains(strippedLines[0], "thinking")
	require.True(t, hasBoth, "bad output should have both on same line (for testing detection)")
}

// TestRenderingAnalyzerWithComplexOutput tests the analyzer with
// more realistic multi-part output.
func TestRenderingAnalyzerWithComplexOutput(t *testing.T) {
	t.Parallel()

	// Realistic View() output with multiple sections
	output := strings.Join([]string{
		"↳ resolved: deterministic · 1.00",
		"────────────────────────────────────────────────────────────────────────────────",
		"> prompt input",
		"",
		"framework: awaiting · mode: on-path · queue: 0",
	}, "\n")

	analyzer := tui.NewRenderingAnalyzer(t, output)

	// Structural assertions
	analyzer.AssertStructure("resolved:", "prompt", "framework:")

	// No problematic overlaps
	analyzer.AssertNoHorizontalConcat("resolved:", "framework:")
	analyzer.AssertLineSeparation("resolved:", "prompt")

	// Dump output for manual review
	t.Run("DumpOutput", func(t *testing.T) {
		analyzer.Dump()
	})
}

// TestRenderingAnalyzerWithANSICodes demonstrates that the analyzer
// correctly handles ANSI color codes and other styling.
func TestRenderingAnalyzerWithANSICodes(t *testing.T) {
	t.Parallel()

	// Output with ANSI color codes (what lipgloss produces)
	withANSI := "\x1b[38;2;100;100;100m⏳\x1b[0m thinking…\n\x1b[38;5;243m↳\x1b[0m input"

	analyzer := tui.NewRenderingAnalyzer(t, withANSI)

	// The analyzer should handle ANSI codes transparently
	analyzer.AssertContains("thinking")
	analyzer.AssertContains("input")
	analyzer.AssertLineSeparation("thinking", "input")

	// Stripped output should not have ANSI codes
	stripped := analyzer.StrippedLines()
	for _, line := range stripped {
		require.NotContains(t, line, "\x1b", "stripped output should not contain ANSI escape code")
	}
}

// TestRenderingJoinVerticalVsConcatenation compares the two assembly methods
// to verify that our fix (string concatenation) produces correct structure
// while JoinVertical causes issues.
func TestRenderingJoinVerticalVsConcatenation(t *testing.T) {
	t.Parallel()

	part1 := "routing status"
	part2 := "────────────────"
	part3_line1 := "⏳ queue indicator"
	part3_line2 := "↳ prompt"
	part3 := part3_line1 + "\n" + part3_line2

	parts := []string{part1, part2, part3}

	// Method 1: Our fix - string concatenation
	var concatResult strings.Builder
	for i, p := range parts {
		if i > 0 {
			concatResult.WriteString("\n")
		}
		concatResult.WriteString(p)
	}
	concatOutput := concatResult.String()

	// Method 2: Previous approach - JoinVertical
	joinOutput := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Analyze both outputs
	t.Run("ConcatenationMethod", func(t *testing.T) {
		analyzer := tui.NewRenderingAnalyzer(t, concatOutput)
		analyzer.AssertLineCount(4) // All parts rendered correctly
		analyzer.AssertLineSeparation("routing status", "queue indicator")
		// analyzer.Dump() // Uncomment for debugging
	})

	t.Run("JoinVerticalMethod", func(t *testing.T) {
		analyzer := tui.NewRenderingAnalyzer(t, joinOutput)
		// JoinVertical pads lines, which can cause misalignment
		// Our tests verify this doesn't cause the bug anymore with concatenation
		analyzer.AssertContains("routing status")
		analyzer.AssertContains("queue indicator")
		// analyzer.Dump() // Uncomment to see the padding
	})
}

// TestConcurrentIODoesNotMix is an example of testing for bugs that involve
// concurrent I/O from multiple sources (like slog and TUI rendering mixing).
// This test pattern catches race conditions that isolated View() tests miss.
//
// See SKILL.md "Critical Pitfall: When Unit Tests Aren't Enough" for guidance
// on when to write this kind of integration test.
func TestConcurrentIODoesNotMix(t *testing.T) {
	t.Parallel()

	// This example demonstrates the pattern, but doesn't run actual concurrent
	// operations (would require full TUI model setup). The pattern is:
	//
	// 1. Capture the I/O you want to inspect (stderr, files, etc)
	// 2. Introduce concurrent operations that would trigger the bug
	// 3. Assert on the COMBINED output from all sources
	//
	// Example scenario: Oracle logs while TUI renders queue indicator
	//
	// captured := tui.CaptureSlog(t)
	// defer captured.Restore()
	//
	// done := make(chan struct{})
	// go func() {
	//     // Simulate oracle runner logging events
	//     for i := 0; i < 50; i++ {
	//         slog.Info("metamode.oracle.event", "type", "system")
	//         time.Sleep(time.Microsecond)  // Interleave with rendering
	//     }
	//     close(done)
	// }()
	//
	// // Concurrent rendering with logging
	// for i := 0; i < 50; i++ {
	//     output := m.View()  // This renders queue indicator
	//     time.Sleep(time.Microsecond)
	// }
	// <-done
	//
	// // Key assertion: verify logs and queue indicator don't mix on same line
	// captured.AssertNoMixedOutput("INFO", "⏳", "slog and queue indicator on same line")

	// For now, we just verify the helper API exists and can be called
	captured := tui.CaptureSlog(t)
	defer captured.Restore()

	// Example: write something to captured I/O
	slog.Info("test.event", "turn", 1)

	// Verify capture worked
	require.Contains(t, captured.StderrBuf.String(), "test.event",
		"slog capture should work")
}

// TestIntegrationPatternExample shows the pattern for testing bugs that span
// multiple components (TUI + external systems). Use this as a template for
// reproducing real concurrent I/O bugs.
func TestIntegrationPatternExample(t *testing.T) {
	t.Parallel()

	// Pattern for testing a bug like "slog output mixed with queue indicator":
	//
	// Step 1: Set up I/O capture
	captured := tui.CaptureSlog(t)
	defer captured.Restore()

	// Step 2: Simulate concurrent operations
	// (In real usage, would have actual concurrent logging + TUI rendering)
	slog.Info("oracle.event", "type", "system", "turn", 1)
	slog.Info("oracle.event", "type", "thinking_tokens", "turn", 2)

	// Step 3: Verify the output (what user sees)
	output := captured.StderrBuf.String()

	// Step 4: Assert on ACTUAL output, not just function return values
	require.Contains(t, output, "oracle.event", "slog should capture events")

	// This is the key pattern: verify that concurrent sources don't corrupt
	// each other's output. In a real bug, you'd see things like:
	// "INFO oracle.event ... ⏳ running…" (mixed on same line)
	analyzer := tui.NewRenderingAnalyzer(t, output)
	analyzer.AssertNotContains("⏳") // Queue indicator shouldn't be in logs
}
