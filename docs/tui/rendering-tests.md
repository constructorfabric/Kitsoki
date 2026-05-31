# TUI Rendering Test Guide

This guide explains how to use the rendering test apparatus to debug and prevent TUI layout issues.

## Overview

The rendering test apparatus (`internal/tui/rendering_test_utils.go`) provides tools to:
- Analyze and verify View() output structure
- Write regression tests for rendering bugs
- Detect overlaps, misalignment, and other layout problems
- Simulate different terminal conditions

## Core Components

### RenderingAnalyzer

Analyzes a View() output string to verify structure and catch regressions.

**Key Methods:**
- `AssertLineCount(n)` — verify exact line count
- `AssertContains(text)` — verify text appears in output
- `AssertLineSeparation(text1, text2)` — verify text1 and text2 are on different lines (crucial for overlap bugs!)
- `AssertNoHorizontalConcat(text1, text2)` — verify text1 and text2 don't appear on the same line
- `AssertStructure(elements...)` — verify output contains expected elements
- `Dump()` — print full output with line numbers for manual review
- `StrippedLines()` — get output with ANSI codes removed

**Example:**

```go
func TestQueueIndicatorNotOverlapped(t *testing.T) {
    output := /* your View() output */
    analyzer := tui.NewRenderingAnalyzer(t, output)
    
    // Verify the bug doesn't regress:
    analyzer.AssertLineSeparation("routing status", "⏳")
    analyzer.AssertNoHorizontalConcat("resolved:", "queue:")
}
```

## Real-World Example: The Overlap Bug

The bug we just fixed: queue indicator appearing on the same terminal line as routing status.

**Test that catches this bug:**

```go
func TestModeAwaitingLLMQueueIndicatorNotOverlapped(t *testing.T) {
    // Set up the scenario
    m := newTestModel()
    m.mode = ModeAwaitingLLM
    m.transcript.AppendLive("↳ resolved: view")
    m.transcript.FinalizeLive("")
    
    // Render
    output := m.View()
    analyzer := tui.NewRenderingAnalyzer(t, output)
    
    // Regression test - if someone breaks this, the test fails
    analyzer.AssertLineSeparation("resolved", "⏳")
    analyzer.AssertNoHorizontalConcat("resolved", "queue:")
}
```

## Common Rendering Problems and How to Test Them

### 1. Status Row Bleeding Into Scrollback

**Symptom:** A status line appears on the same terminal line as content above.

**Root Cause:** JoinVertical's padding or double newlines causing cursor misalignment.

**Test:**

```go
func TestStatusRowDoesNotBleed(t *testing.T) {
    analyzer := tui.NewRenderingAnalyzer(t, output)
    
    // Find the status row and the line before it
    analyzer.AssertLineSeparation("last content line", "framework: awaiting")
}
```

### 2. Horizontal Concatenation When Should Be Vertical

**Symptom:** Two parts appear on the same line with padding between them.

**Root Cause:** Using JoinVertical with mismatched line counts, or padding being applied incorrectly.

**Test:**

```go
func TestNoUnintendedHorizontalLayout(t *testing.T) {
    analyzer := tui.NewRenderingAnalyzer(t, output)
    
    // These should never appear on the same line
    analyzer.AssertNoHorizontalConcat("indicator", "status_row")
}
```

### 3. Embedded Newlines Being Stripped or Doubled

**Symptom:** Multi-line parts (like `indicator\n + prompt`) render incorrectly.

**Root Cause:** TrimRight or padding affecting embedded newlines.

**Test:**

```go
func TestEmbeddedNewlinesPreserved(t *testing.T) {
    analyzer := tui.NewRenderingAnalyzer(t, output)
    
    // Multi-line prompt should still have both lines
    analyzer.AssertLineCount(expectedLines)
    analyzer.AssertLineSeparation("⏳ queue indicator", "↳ prompt")
}
```

## Writing Your Own Regression Tests

### Step 1: Identify the Bug Symptoms

Example: "Queue indicator appears on same line as routing status"

### Step 2: Create a Test That Reproduces the Bug

```go
func TestBugName(t *testing.T) {
    // Set up the exact conditions that trigger the bug
    m := setupModelForBug()
    
    // Render
    output := m.View()
    
    // Verify the bug would be caught
    analyzer := tui.NewRenderingAnalyzer(t, output)
    
    // These assertions SHOULD FAIL if the bug exists
    // If they pass, the bug is fixed
    analyzer.AssertExpectedStructure()
}
```

### Step 3: Add to View Rendering Tests

Place your test in `rendering_regression_test.go` or create a new `*_rendering_test.go` file.

### Step 4: Verify the Test Catches Regressions

Temporarily revert the fix, run the test, and confirm it fails. Then re-apply the fix and confirm it passes.

## Debugging Rendering Issues

### 1. Use Dump() to See Full Output

```go
analyzer.Dump()  // Prints line-by-line output with byte counts
```

This shows:
- Raw output with ANSI codes
- Line numbers and byte lengths (useful for width issues)
- Stripped output for reading visible content

### 2. Check for ANSI Code Artifacts

ANSI codes affect byte length but not visible width. The analyzer handles this automatically:

```go
analyzer.AssertLineSeparation(...) // Works correctly with ANSI codes
analyzer.StrippedLines()           // Access ANSI-free content
```

### 3. Test Different Terminal Widths

If the bug only manifests at certain widths:

```go
for _, width := range []int{80, 120, 200} {
    t.Run(fmt.Sprintf("Width%d", width), func(t *testing.T) {
        m.SetWidth(width)
        output := m.View()
        analyzer := tui.NewRenderingAnalyzer(t, output)
        analyzer.AssertLineSeparation("text1", "text2")
    })
}
```

## Integration with Main Test Suite

All rendering tests are in `internal/tui/*_test.go` files:

```bash
# Run all rendering tests
go test ./internal/tui -run Rendering -v

# Run a specific regression test
go test ./internal/tui -run TestQueueIndicatorNotOverlapped -v

# Show full output for debugging
go test ./internal/tui -run TestName -v -args -test.v
```

## Common Mistakes to Avoid

1. **Not comparing lines correctly** — use `AssertLineSeparation()` not string contains checks
2. **Forgetting ANSI codes** — the analyzer handles them, but stripped content is what users see
3. **Testing the implementation, not the behavior** — test what users see, not internal line counts
4. **Making tests too specific** — focus on the critical structure, not exact byte positions

## Key Assertion Patterns

### Pattern 1: No Overlap

```go
analyzer.AssertLineSeparation("status", "indicator")
analyzer.AssertNoHorizontalConcat("status", "indicator")
```

### Pattern 2: Correct Structure

```go
analyzer.AssertStructure("header", "content", "footer")
```

### Pattern 3: Multi-line Parts Work

```go
analyzer.AssertLineCount(expectedCount)
analyzer.AssertLineSeparation("line1_of_part", "line2_of_part")
```

### Pattern 4: Content is Present

```go
analyzer.AssertContains("expected_text")
analyzer.AssertNotContains("error_marker")
```

## Performance Considerations

- `RenderingAnalyzer` is lightweight - suitable for every test
- ANSI stripping is O(n) in output length
- All assertions are fast (string operations)
- No external dependencies

## Future Enhancements

The framework is designed to be extended:

1. **Full RootModel Integration** — `RenderingTestHelper` can be expanded to actually construct and render RootModel instances
2. **Visual Regression** — add image comparison for theme/style testing
3. **Performance Metrics** — measure rendering time under different conditions
4. **Snapshot Testing** — compare full output against golden files
5. **Terminal Emulation** — simulate actual terminal rendering edge cases

## See Also

- `rendering_test_utils.go` — implementation and API
- `rendering_regression_test.go` — example tests
- `view_parts_assembly_test.go` — JoinVertical/concatenation comparison
- Main TUI code: `tui.go` (View() method)
