// intercept_drive.go — the cmd-layer half of the conflict-capable intercept:
// when the pre-LLM gate matches a command in a binding whose app declares a
// room flagged intercept_drive: rest, it does NOT run the stateless OneShot but
// drives a real, PERSISTED session to rest synchronously (the agent's hook
// blocks for the duration) via Orchestrator.DriveToRest.
//
// Two caps govern this path, both lifted from the 5s fast-path budget:
//   - kitsoki's own: the drive runs under interceptDriveBudget, far past 5s;
//   - Claude's: `kitsoki hook install` writes a raised `timeout` on the
//     UserPromptSubmit entry (see hook.go) so Claude doesn't kill the hook
//     mid-rebase. kitsoki's budget sits under the installed Claude timeout so
//     it always reaches safe-abort first.
//
// The drive uses the SAME on-disk session store the rest of kitsoki opens, so
// the persisted session is watchable in `kitsoki web` / `kitsoki trace --follow`
// while the hook blocks — the live progress surface that keeps the synchronous
// wait from being a degraded experience (the in-Claude account is the final
// block report; the spike proved there is no live stream into Claude itself).

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

// interceptDriveBudget is kitsoki's wall-clock cap for a synchronous multi-turn
// intercept drive. It is deliberately generous — an oracle reading and editing
// conflicted files is minutes, not the 5s fast-path cap — and MUST sit under the
// Claude hook `timeout` the installer writes (interceptHookTimeoutSeconds) so
// kitsoki always reaches safe-abort before Claude kills the hook.
const interceptDriveBudget = 15 * time.Minute

// driveConfig is the resolved input for a cmd-layer intercept drive.
type driveConfig struct {
	// AppPath is the resolved path to the bound app.yaml.
	AppPath string
	// Def is the pre-loaded app definition (the gate already loaded it).
	Def *app.AppDef
	// DBPath is the on-disk session store (the default store the web/TUI read),
	// so the driven session is watchable live.
	DBPath string
	// Input is the original free-text prompt (for the trace + report).
	Input string
	// Intent / Slots are the matched command to drive.
	Intent string
	Slots  map[string]any
	// WorkingDir pins the binding's repo so git commands run in the right tree;
	// seeded as world.working_dir. Empty leaves the app's default (".").
	WorkingDir string
}

// driveInterceptToRest builds a real, oracle-capable session runtime against the
// on-disk store and drives the matched command to rest. It returns the
// structured DriveOutcome; a non-nil error is an infrastructure failure (the
// caller fails open, letting the prompt reach the model untouched).
func driveInterceptToRest(ctx context.Context, dc driveConfig) (orchestrator.DriveOutcome, error) {
	rt, err := buildSessionRuntime(runtimeConfig{
		AppPath: dc.AppPath,
		Def:     dc.Def,
		DBPath:  dc.DBPath,
		// One-shot execution: auto-advance the multi-way gates so the settle loop
		// drives the conflict sub-flow to completion without stopping for an
		// operator (there is no live operator on a blocking hook).
		ExecMode: orchestrator.ExecOneShot,
		// A real claude harness so host.oracle.task reaches the model to resolve
		// conflicts — the acknowledged, recorded LLM use of this path (the gate's
		// no-LLM promise holds only for the single-command subset).
		HarnessType: "claude",
	})
	if err != nil {
		return orchestrator.DriveOutcome{}, fmt.Errorf("build drive runtime: %w", err)
	}
	defer rt.Close()

	var initialWorld map[string]any
	if dc.WorkingDir != "" {
		initialWorld = map[string]any{"working_dir": dc.WorkingDir}
	}

	return rt.Orch.DriveToRest(ctx, dc.Intent, dc.Slots, orchestrator.DriveOptions{
		Input:        dc.Input,
		InitialWorld: initialWorld,
	})
}

// composeDriveReport renders the marked block report for a settled multi-turn
// drive — the in-the-moment, in-Claude account of what kitsoki did while the
// hook blocked. It mirrors composeInterceptReport's shape (attribution line +
// outcome + trace note + optional escape line) but reports the drive's
// disposition (resolved / safe-aborted), its hop count, and the durable
// session pointer the user can replay live.
//
//	⌁ kitsoki drove this to completion — rebase (session abc123, 4 rounds)
//	Rebased onto main; resolved 1 conflict.   ·   ⟲ kitsoki trace --follow abc123
//	↳ prefix "//" to skip kitsoki and send the prompt to the agent
func composeDriveReport(intent, escapePrefix string, out orchestrator.DriveOutcome) string {
	var b strings.Builder
	verb := "drove this to completion"
	if out.Aborted {
		verb = "could not complete this — safely aborted"
	}
	fmt.Fprintf(&b, "⌁ kitsoki %s — %s (session %s, %d round%s)\n",
		verb, intent, out.SessionID, out.Rounds, plural(out.Rounds))

	outcome := firstNonEmptyLine(out.View, driveOutcomeFallback(out))
	fmt.Fprintf(&b, "%s   ·   ⟲ kitsoki trace --follow %s", outcome, out.SessionID)

	if escapePrefix != "" {
		fmt.Fprintf(&b, "\n↳ prefix %q to skip kitsoki and send the prompt to the agent", escapePrefix)
	}
	return b.String()
}

// driveOutcomeFallback is the one-line outcome shown when the final view is
// empty — keyed on the machine-readable disposition so the user always gets a
// truthful summary even with no rendered view.
func driveOutcomeFallback(out orchestrator.DriveOutcome) string {
	switch out.Outcome {
	case "resolved":
		return "Completed."
	case "escalation":
		return "The oracle could not resolve the conflict — the rebase was aborted; the tree is clean."
	case "budget":
		return "Timed out — the operation was aborted; the tree is clean."
	case "error", "panic":
		return "The operation failed — it was aborted; the tree is clean."
	default:
		return "Done."
	}
}

// plural returns "s" unless n == 1 (so "1 round" / "2 rounds").
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
