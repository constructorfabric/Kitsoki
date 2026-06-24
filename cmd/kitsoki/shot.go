// shot.go — the `kitsoki shot` subcommand: rasterise a Frame to a PNG.
//
// shot turns a slice-1 Frame's ANSI string into a faithful monospace +
// ANSI-colour PNG so any state is a reviewable image — the visual twin of the
// agent-readable Frame.Text that `kitsoki drive` already emits. Rendering bugs
// (overlap, a banner colliding with the divider, a status row wider than the
// terminal) survive a text read but jump out of an image; shot is how the QA
// agent (and a human) gets one for any state.
//
// Two input forms:
//
//	shot --frame frame.json -o out.png
//	    Rasterise the ANSI of a Frame JSON object emitted by `kitsoki drive`
//	    (the whole driveFrame object, or a bare Frame — both are accepted).
//	    This is the agent's primary path: drive already composed the frame.
//
//	shot <app.yaml> --trace t.jsonl --turn N -o out.png
//	    Re-compose a historical turn's frame from a trace via the slice-1
//	    composer, then rasterise it. Replays the trace's recorded intents
//	    (deterministically, no LLM) up to turn N onto a throwaway temp trace —
//	    the source trace is never mutated.
//
// shot paints the already-composed bytes; it never wraps, truncates, or
// re-lays-out. An over-long line paints past --cols (visibly the bug). See
// internal/tui/shot for the parser/rasteriser and docs/proposals/qa-screenshot.md.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"kitsoki/internal/store"
	"kitsoki/internal/tui"
	"kitsoki/internal/tui/blocks"
	"kitsoki/internal/tui/shot"
)

func shotCmd() *cobra.Command {
	var (
		framePath string
		tracePath string
		turn      int
		outPath   string
		cols      int
		rows      int
		themeName string
	)

	cmd := &cobra.Command{
		Use:   "shot [<app.yaml>]",
		Short: "Rasterise a Frame (or a past turn) to a monospace + ANSI-colour PNG",
		Long: `Rasterise a slice-1 Frame's ANSI to a faithful monospace + ANSI-colour
PNG — the visual twin of the agent-readable Frame.Text.

Two forms:
  kitsoki shot --frame frame.json -o shot.png
      Rasterise a Frame emitted by 'kitsoki drive' (the per-turn JSON object
      or a bare Frame).

  kitsoki shot <app.yaml> --trace t.jsonl --turn N -o shot.png
      Re-compose a past turn's frame from a trace (deterministic replay of the
      recorded intents, no LLM) and rasterise it. The source trace is read
      only — replay happens on a throwaway temp trace.

shot paints already-composed bytes; it does NOT wrap, truncate, or re-flow. An
over-long line paints past --cols — that overflow is the bug shot lets you see.

Examples:
  kitsoki drive app.yaml --trace t.jsonl ... | head -1 > frame.json
  kitsoki shot --frame frame.json -o shot.png --theme molokai
  kitsoki shot testdata/apps/cloak/app.yaml --trace t.jsonl --turn 2 -o turn2.png`,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := ""
			if len(args) == 1 {
				appPath = args[0]
			}
			if outPath == "" {
				return infraError("-o/--out is required")
			}

			theme := blocks.ThemeByName(themeName)
			opts := shot.Options{Theme: theme, Cols: cols, Rows: rows}

			var ansi string
			var err error
			switch {
			case framePath != "":
				ansi, err = ansiFromFrameFile(framePath)
			case tracePath != "":
				ansi, err = ansiFromTraceTurn(cmd.Context(), appPath, tracePath, turn, cols, rows)
			default:
				return infraError("provide --frame <frame.json> or --trace <t.jsonl> --turn N")
			}
			if err != nil {
				return err
			}

			f, ferr := os.Create(outPath)
			if ferr != nil {
				return infraError("create %q: %v", outPath, ferr)
			}
			defer func() { _ = f.Close() }()

			if err := shot.RenderPNG(f, ansi, opts); err != nil {
				return infraError("%v", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "kitsoki shot: wrote %s\n", outPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&framePath, "frame", "", "Frame JSON file emitted by 'kitsoki drive' (one form)")
	cmd.Flags().StringVar(&tracePath, "trace", "", "trace JSONL to re-compose a past turn from (other form)")
	cmd.Flags().IntVar(&turn, "turn", 0, "turn number to re-compose (with --trace); 0 means the initial frame")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output PNG path (required)")
	cmd.Flags().IntVar(&cols, "cols", 100, "frame width in columns")
	cmd.Flags().IntVar(&rows, "rows", 30, "frame height in rows")
	cmd.Flags().StringVar(&themeName, "theme", "default", "theme palette: default|mesa|meta-blue|meta-amber|off-path")

	return cmd
}

// ansiFromFrameFile reads a Frame JSON file and returns its ANSI string. It
// accepts either a bare tui.Frame ({"ANSI":...}) or the whole driveFrame object
// ({"frame":{...}}) `kitsoki drive` writes per turn, so a user can pipe a drive
// line straight in without unwrapping it.
func ansiFromFrameFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", infraError("read --frame %q: %v", path, err)
	}

	// Try the driveFrame wrapper first (the common case: a drive output line).
	var wrapped struct {
		Frame *tui.Frame `json:"frame"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Frame != nil && wrapped.Frame.ANSI != "" {
		return wrapped.Frame.ANSI, nil
	}

	// Fall back to a bare Frame.
	var frame tui.Frame
	if err := json.Unmarshal(data, &frame); err != nil {
		return "", infraError("parse --frame %q: %v", path, err)
	}
	if frame.ANSI == "" {
		return "", infraError("--frame %q has no ANSI field (is it a Frame?)", path)
	}
	return frame.ANSI, nil
}

// ansiFromTraceTurn re-composes the frame for a historical turn and returns its
// ANSI. It reads the source trace's recorded turns, replays them deterministically
// (each turn's recorded intent+slots via RunIntentWithInput — no LLM) onto a
// throwaway temp trace up to turnN, folds each outcome through the same
// ApplyTurnOutcome path the live TUI uses, and composes the slice-1 Frame at the
// requested geometry. turnN==0 yields the initial frame (after on_enter, before
// any turn).
func ansiFromTraceTurn(ctx context.Context, appPath, srcTrace string, turnN, cols, rows int) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if turnN < 0 {
		return "", infraError("--turn must be >= 0")
	}

	// Read the recorded turns from the SOURCE trace (read-only).
	turns, err := recordedTurns(srcTrace)
	if err != nil {
		return "", err
	}
	if turnN > len(turns) {
		return "", infraError("--turn %d exceeds the %d recorded turn(s) in %q", turnN, len(turns), srcTrace)
	}

	// Replay onto a THROWAWAY temp trace so the source file is never mutated.
	// The story is loaded from appPath, or reconstructed from the source trace
	// when appPath is "" (mirrors setupTraceSession's own fallback, but we feed
	// it the source history by pointing it at the source trace for the def and
	// a temp trace for the writes — so we pass appPath through and require it
	// when the source trace can't self-describe).
	tmpDir, err := os.MkdirTemp("", "kitsoki-shot-")
	if err != nil {
		return "", infraError("create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	tmpTrace := filepath.Join(tmpDir, "replay.jsonl")

	// When no app is given, seed the temp trace's story snapshot from the source
	// trace so setupTraceSession can reconstruct the effective story from it.
	if appPath == "" {
		if err := seedStoryFromTrace(srcTrace, tmpTrace); err != nil {
			return "", err
		}
	}

	ts, err := setupTraceSession(ctx, appPath, tmpTrace, &noRunHarness{})
	if err != nil {
		return "", err
	}
	defer ts.Close()

	if err := ts.Orch.RunInitialOnEnter(ctx, ts.SID); err != nil {
		return "", infraError("run initial on_enter: %v", err)
	}

	model, err := newDriveModel(ts)
	if err != nil {
		return "", err
	}

	// Replay turns 1..turnN. turnN==0 leaves the model at the initial frame.
	for i := 0; i < turnN; i++ {
		tr := turns[i]
		outcome, turnErr := ts.Orch.RunIntentWithInput(ctx, ts.SID, tr.intent, tr.slots, tr.input)
		model = model.ApplyTurnOutcome(outcome, tr.input, turnErr)
	}

	frame := tui.ComposeFrame(&model, cols, rows)
	return frame.ANSI, nil
}

// recordedTurn is one replayable turn pulled from a trace: the routed intent,
// its slots, and the operator's original free-text input (for the display echo).
type recordedTurn struct {
	intent string
	slots  map[string]any
	input  string
}

// recordedTurns reads a trace JSONL and projects its TransitionApplied events
// into the ordered replayable turns. The machine.transition payload carries
// {intent, slots}; the paired turn.input carries the operator's words. Turns
// that never produced a transition (pure rejections) are skipped — they have no
// intent to replay.
func recordedTurns(tracePath string) ([]recordedTurn, error) {
	sink, err := store.OpenJSONL(tracePath)
	if err != nil {
		return nil, infraError("open trace %q: %v", tracePath, err)
	}
	defer func() { _ = sink.Close() }()

	history := sink.History()

	// Index the operator input per turn for the display echo.
	inputByTurn := map[int]string{}
	for _, ev := range history {
		if ev.Kind == store.UserInputReceived {
			var p struct {
				Input string `json:"input"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Input != "" {
				inputByTurn[int(ev.Turn)] = p.Input
			}
		}
	}

	var turns []recordedTurn
	for _, ev := range history {
		if ev.Kind != store.TransitionApplied {
			continue
		}
		var p struct {
			Intent string         `json:"intent"`
			Slots  map[string]any `json:"slots"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil || p.Intent == "" {
			continue
		}
		turns = append(turns, recordedTurn{
			intent: p.Intent,
			slots:  p.Slots,
			input:  inputByTurn[int(ev.Turn)],
		})
	}
	return turns, nil
}

// seedStoryFromTrace copies the source trace's turn-0 StorySnapshot event into a
// fresh temp trace so setupTraceSession (with appPath=="") can reconstruct the
// effective story from it without the on-disk story files. Only the story
// snapshot is copied — not the turn events — so the temp trace starts empty of
// turns and replay drives them cleanly. The JSONL session header is written
// automatically by OpenJSONL on the new file, so it is not copied here.
func seedStoryFromTrace(srcTrace, dstTrace string) error {
	sink, err := store.OpenJSONL(srcTrace)
	if err != nil {
		return infraError("open trace %q: %v", srcTrace, err)
	}
	history := sink.History()
	_ = sink.Close()

	var snapshots []store.Event
	for _, ev := range history {
		if ev.Kind == store.StorySnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		return infraError("trace %q has no story snapshot; pass <app.yaml> so shot can load the story", srcTrace)
	}

	dst, err := store.OpenJSONL(dstTrace)
	if err != nil {
		return infraError("open temp trace: %v", err)
	}
	defer func() { _ = dst.Close() }()

	for _, ev := range snapshots {
		if err := dst.Append(ev); err != nil {
			return infraError("seed temp trace: %v", err)
		}
	}
	return nil
}
