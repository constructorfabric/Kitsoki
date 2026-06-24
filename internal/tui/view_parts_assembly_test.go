package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

// TestJoinVerticalWithMixedLineCounts verifies that JoinVertical
// correctly handles parts with different line counts, particularly
// when one part is a single line and another is multi-line.
//
// This tests the hypothesis that the rendering bug might be caused
// by JoinVertical's padding behavior with mismatched line counts.
func TestJoinVerticalWithMixedLineCounts(t *testing.T) {
	t.Parallel()

	// Simulate the parts that appear in ModeAwaitingLLM
	routingStatus := "  → nav: back   (deterministic · 1.00)"         // 1 line
	divider := "────────────────────────────────────────────────────" // 1 line
	indicator := "⏳ ⠋ thinking…  ·  queue: 0  ·  Enter · Esc"         // 1 line
	prompt := "↳ test input"                                          // 1 line
	promptLine := indicator + "\n" + prompt                           // 2 lines

	parts := []string{routingStatus, divider, promptLine}

	// Trim trailing newlines as the real code does
	for i := range parts {
		parts[i] = strings.TrimRight(parts[i], "\n")
	}

	// Call JoinVertical with Left alignment
	result := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Split result by newlines to analyze
	lines := strings.Split(result, "\n")

	t.Logf("\nResult has %d lines\n", len(lines))
	for i, line := range lines {
		t.Logf("Line %d: bytes=%d content=%q\n",
			i, len(line), truncateForLog(line, 60))
	}

	t.Logf("\nParts before JoinVertical:\n")
	for i, p := range []string{routingStatus, divider, promptLine} {
		lines := strings.Count(p, "\n")
		if !strings.HasSuffix(p, "\n") {
			lines++
		}
		t.Logf("Part %d: lines=%d bytes=%d content=%q\n",
			i, lines, len(p), truncateForLog(p, 60))
	}

	// Key assertion: routing status and indicator MUST be on separate lines
	require.GreaterOrEqual(t, len(lines), 3,
		"result should have at least 3 lines (routing status, divider, indicator)")

	// Find which lines contain routing status vs indicator
	var routingLineIdx, indicatorLineIdx int
	for i, line := range lines {
		if strings.Contains(line, "nav: back") {
			routingLineIdx = i
		}
		if strings.Contains(line, "⏳") {
			indicatorLineIdx = i
		}
	}

	// They must be different lines
	if routingLineIdx > 0 && indicatorLineIdx > 0 {
		require.NotEqual(t, routingLineIdx, indicatorLineIdx,
			"routing status and queue indicator must be on separate lines")
	}

	// The routing status line should NOT contain the queue indicator
	if routingLineIdx >= 0 && routingLineIdx < len(lines) {
		require.NotContains(t, lines[routingLineIdx], "queue:",
			"routing status line must not contain queue indicator")
	}
}

// TestPromptLineEmbeddedNewlines verifies that a promptLine with an
// embedded newline (indicator\n + prompt) is correctly handled through
// TrimRight and JoinVertical.
func TestPromptLineEmbeddedNewlines(t *testing.T) {
	t.Parallel()

	indicator := "⏳ thinking…  ·  queue: 0"
	prompt := "↳ test input"
	promptLine := indicator + "\n" + prompt

	// Apply TrimRight as the real code does
	promptLine = strings.TrimRight(promptLine, "\n")

	// Should still have the embedded newline
	require.Contains(t, promptLine, "\n",
		"TrimRight should preserve embedded newlines")

	// Split should give us both parts
	parts := strings.Split(promptLine, "\n")
	require.Len(t, parts, 2, "should have 2 parts after split")
	require.Equal(t, indicator, parts[0])
	require.Equal(t, prompt, parts[1])
}

// TestJoinVerticalPaddingWidth verifies the width calculation behavior
// of JoinVertical to understand if padding might cause visual overlap.
func TestJoinVerticalPaddingWidth(t *testing.T) {
	t.Parallel()

	short := "short"                                                   // 5 chars
	long := "this is a very long line that should determine max width" // 58 chars
	parts := []string{short, long}

	result := lipgloss.JoinVertical(lipgloss.Left, parts...)
	lines := strings.Split(result, "\n")

	t.Logf("Short line: %q (len=%d)", short, len(short))
	t.Logf("Long line: %q (len=%d)", long, len(long))
	t.Logf("Result line 0: %q (len=%d)", lines[0], len(lines[0]))
	t.Logf("Result line 1: %q (len=%d)", lines[1], len(lines[1]))

	// JoinVertical pads all lines to the same width
	// So line 0 should be padded to match the width of line 1
	require.Greater(t, len(lines[0]), len(short),
		"short line should be padded")

	// Both lines should be approximately the same byte length
	require.Equal(t, len(lines[0]), len(lines[1]),
		"JoinVertical should pad all lines to same width")
}

func truncateForLog(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}
