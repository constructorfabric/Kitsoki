// Package main — kitsoki replay command (Phase 4 extension).
//
// kitsoki replay <session-id> [--mode file_diff|llm_rerun|hybrid]
//
// Three modes (agent-split proposal §4.3):
//
//	--mode file_diff   (default) — replay Mode A/B spans deterministically from
//	                               (initial_state_hash, final_diff). Skips Mode C
//	                               spans and prints a summary of skipped spans.
//	--mode llm_rerun   — re-ask every recorded LLM prompt to a fresh LLM call
//	                     (configurable model via --model). Diffs the new output
//	                     against the recorded output. Useful for model evaluation.
//	--mode hybrid      — run deterministic spans deterministically; re-run LLM
//	                     spans for divergence comparison.
//
// For host.agent.extract spans, replay can additionally swap tiers: if the
// author has since added a synonym or slot-template that covers a previously
// LLM-resolved input, the replay shows whether the new deterministic tier
// would have matched. Phase 5 adds the CLI surface for suggesting synonyms;
// the replay machinery hooks are present here.
//
// Mode C spans (external_side_effect: true) are always skipped in file_diff
// mode and summarised at the end.
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// replayCmd returns the kitsoki replay command.
func replayCmd() *cobra.Command {
	var (
		mode  string
		model string
	)

	cmd := &cobra.Command{
		Use:   "replay <session-id>",
		Short: "Replay a recorded session's task spans",
		Long: `Replay the host.agent.task spans recorded in a session's event log.

Three modes are supported:

  file_diff  (default) — replay Mode A/B spans deterministically from
                         (initial_state_hash, final_diff). Mode C spans
                         (external_side_effect: true) are skipped and a
                         summary is printed: "skipped N external-side-effect
                         spans." This is the regression-replay mode for code-
                         writing tasks.

  llm_rerun  — re-ask every recorded LLM prompt to a fresh LLM call
               (configurable via --model). The new output is diffed against
               the recorded output so authors can measure model-upgrade impact
               without re-running the full task pipeline.

  hybrid     — replay deterministic spans (Mode A/B), then re-run LLM spans
               for divergence comparison. Combines coverage of both other modes.

For host.agent.extract spans, the replay additionally checks whether a
recently-added synonym or slot-template would have resolved the input that
previously required an LLM call (concept §4 progressive determinism). See
'kitsoki extract suggest-synonym' (Phase 5) for the authoring side.

For host.agent.converse spans (D10), the replayer renders an opaque block
via host.RenderConverseSpan(chatID, seqStart, seqEnd) — conversations are
the artifact and are not deterministically replayable; the ChatStore is the
canonical record. Example output:
  converse(chat=abc, seq=[12..18]) — 6 turns, see ChatStore

Examples:
  kitsoki replay ses-abc123
  kitsoki replay ses-abc123 --mode llm_rerun --model claude-haiku-4-5
  kitsoki replay ses-abc123 --mode hybrid`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			switch mode {
			case "file_diff", "llm_rerun", "hybrid":
				// valid
			default:
				return fmt.Errorf("unknown --mode %q; use file_diff, llm_rerun, or hybrid", mode)
			}
			// Phase 4 delivers the flags and the command structure.
			// The full traversal of journal task.end events and diff-apply
			// mechanics are wired here once the journal reader API is stable
			// (Phase 6 will add the agent-serve surface that makes the reader
			// addressable per session-id + span kind).
			//
			// For now, surface the structured error so the CLI surface is
			// complete and callers get a meaningful response rather than
			// the old "not implemented" panic.
			return fmt.Errorf("replay --mode %s for session %s: journal traversal not yet implemented (Phase 6 wires the full traversal); the task span schema (task.tool / task.acceptance.attempt / task.end) is present in the journal from this phase forward", mode, sessionID)
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "file_diff",
		"replay mode: file_diff (default, deterministic), llm_rerun (re-ask LLM), hybrid")
	cmd.Flags().StringVar(&model, "model", "",
		"model for --mode llm_rerun / hybrid (default: model recorded in the span)")

	return cmd
}
