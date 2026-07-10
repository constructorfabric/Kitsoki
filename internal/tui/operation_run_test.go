package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
)

func TestOperationRunChromeTextRunning(t *testing.T) {
	text := operationRunChromeText(map[string]any{
		app.OperationRunWorldKey: map[string]any{
			"operation_id": "bf__capsule_demo",
			"policy_id":    "bf__capsule_demo",
			"title":        "Capsule bugfix",
			"status":       "running",
			"from":         "idle",
			"to":           "bugfix.reproduce",
		},
	})

	ra := NewRenderingAnalyzer(t, text)
	ra.AssertContains("Capsule bugfix")
	ra.AssertContains("running")
	ra.AssertContains("idle -> bugfix.reproduce")
}

func TestOperationRunChromeTextRunningShowsPhase(t *testing.T) {
	text := operationRunChromeText(map[string]any{
		app.OperationRunWorldKey: map[string]any{
			"operation_id": "bf__capsule_demo",
			"title":        "Capsule bugfix",
			"status":       "running",
			"phase":        "testing_artifact",
			"from":         "idle",
			"to":           "bugfix.reproduce",
		},
	})

	ra := NewRenderingAnalyzer(t, text)
	ra.AssertContains("Capsule bugfix")
	ra.AssertContains("running")
	ra.AssertContains("phase testing")
	ra.AssertNotContains("idle -> bugfix.reproduce")
}

func TestOperationRunChromeTextCompleted(t *testing.T) {
	text := operationRunChromeText(map[string]any{
		app.OperationRunWorldKey: map[string]any{
			"operation_id":      "bf__capsule_demo",
			"title":             "Capsule bugfix",
			"status":            "completed",
			"terminal_state":    "__exit__shipped",
			"terminal_artifact": "bf__done_artifact",
		},
	})

	ra := NewRenderingAnalyzer(t, text)
	ra.AssertContains("Capsule bugfix")
	ra.AssertContains("completed")
	ra.AssertContains("terminal __exit__shipped")
	ra.AssertContains("artifact bf__done_artifact")
}

func TestOperationRunChromeTextWaiting(t *testing.T) {
	text := operationRunChromeText(map[string]any{
		app.OperationRunWorldKey: map[string]any{
			"operation_id":   "bf__capsule_demo",
			"title":          "Capsule bugfix",
			"status":         "waiting",
			"stop_reason":    "needs-human",
			"stop_detail":    "Regression gate was never RED.",
			"terminal_state": "__exit__needs-human",
		},
	})

	ra := NewRenderingAnalyzer(t, text)
	ra.AssertContains("Capsule bugfix")
	ra.AssertContains("waiting")
	ra.AssertContains("reason needs-human")
	ra.AssertContains("Regression gate was never RED.")
}

func TestOperationRunChromeLineWidthBehavior(t *testing.T) {
	line := "operation: " + operationRunChromeText(map[string]any{
		app.OperationRunWorldKey: map[string]any{
			"title":  "A very long autonomous capsule bugfix operation",
			"status": "running",
			"from":   "core.bugfix.reproduce",
			"to":     "core.bugfix.implement",
		},
	})

	tests := []struct {
		name         string
		width        int
		wantContains string
	}{
		{
			name:         "normal width keeps operation context",
			width:        132,
			wantContains: "core.bugfix.reproduce -> core.bugfix.implement",
		},
		{
			name:         "narrow width preserves operation label",
			width:        34,
			wantContains: "operation:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncated := truncateFrameCell(line, tt.width)

			require.LessOrEqual(t, ansi.StringWidth(truncated), tt.width)
			require.Contains(t, truncated, tt.wantContains)
		})
	}
}
