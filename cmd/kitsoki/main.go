// Command kitsoki is the CLI entrypoint for the Kitsoki deterministic LLM orchestrator.
// Subcommands: run, viz, trace, replay, test, serve (§9a, §12).
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/kitrepo"
	"kitsoki/internal/machine"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/tui"
	"kitsoki/internal/viz"
)

const version = "0.0.1-scaffold"

// newRootCmd builds the top-level cobra command tree. Extracted from main()
// so tests can construct an isolated root and call Execute() against captured
// I/O without running the real os.Args/os.Exit dance.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "kitsoki",
		Short: "Kitsoki — deterministic LLM orchestrator",
		Long: `Kitsoki lets a human drive a structured application with free-text input.
The LLM translates natural language into a finite alphabet of intents defined
by the application; the state machine decides what happens next.

Embedded documentation (ships inside this binary):
  kitsoki docs             list available topics
  kitsoki docs llm-guide   condensed manual for an LLM driving kitsoki
  kitsoki docs app-schema  authoritative reference for app.yaml
  kitsoki docs all         print every topic, concatenated

See docs/ in the repo for the narrative documentation.`,
		// Resolve the kitsoki source repo once per invocation and export it
		// into the environment so every downstream consumer — the
		// kitsoki.* meta-mode injection gate, expandMetaCwd, the
		// kitsoki-engineer/explainer/bug-reporter agents' DefaultCwd, and
		// the `kitsoki bug create --target kitsoki` subprocess the agent
		// spawns — keeps reading $KITSOKI_REPO unchanged. kitrepo.Resolve
		// remembers the location under ~/.kitsoki/repo, so after the first
		// run from a dev checkout the engine-targeting features work from
		// any directory without the operator setting the env var. Runs for
		// every subcommand (no child overrides PersistentPreRun).
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(kitrepo.EnvVar) == "" {
				if repo := kitrepo.Resolve(); repo != "" {
					_ = os.Setenv(kitrepo.EnvVar, repo)
				}
			}
			return nil
		},
	}

	root.AddCommand(versionCmd())
	root.AddCommand(runCmd())
	root.AddCommand(vizCmd())
	root.AddCommand(traceCmd())
	root.AddCommand(replayCmd())
	root.AddCommand(replayRoutingCmd())
	root.AddCommand(testCmd())
	root.AddCommand(serveCmd())
	root.AddCommand(renderCmd())
	root.AddCommand(docsCmd())
	root.AddCommand(recordCmd())
	root.AddCommand(inspectCmd())
	root.AddCommand(turnCmd())
	root.AddCommand(sessionCmd())
	root.AddCommand(chatCmd())
	root.AddCommand(mcpValidatorCmd())
	root.AddCommand(mcpBashCmd())
	root.AddCommand(bugCmd())
	root.AddCommand(uiCmd())
	root.AddCommand(extractCmd())
	root.AddCommand(promptsCmd())
	root.AddCommand(oracleCmd())
	root.AddCommand(oracleServeCmd())
	root.AddCommand(migrateOracleCmd())
	root.AddCommand(cassetteCmd())
	root.AddCommand(exportStatusCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(webCmd())

	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Sentinel error: translate to EX_TEMPFAIL=75 (chat-busy / session-busy)
		// so wrappers like loop.py can back off and retry.  The user-facing
		// reason was already written to stderr by the subcommand.
		if IsTempFail(err) {
			os.Exit(EX_TEMPFAIL)
		}
		// kitsoki turn --trace exit codes:
		//   0: accepted, 1: rejected, 2: terminal, 3: infra error.
		// For exit 0–2 the outcome is self-describing (JSONL events on stdout).
		// For exit 3 (infra) print the message to stderr so the driver can log it.
		if code, ok := IsTurnExitError(err); ok {
			if code == turnExitInfraError {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			os.Exit(code)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the kitsoki version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kitsoki %s\n", version)
		},
	}
}

func runCmd() *cobra.Command {
	var (
		harnessType      string
		claudeModel      string
		oracleBackend    string
		recordingPath    string
		recordPath       string
		dbPath           string
		continueFlag     bool
		continueID       string
		continueKey      string
		noImplicitResume bool
		warpBasisPath    string
		execModeFlag     string
		promptOverlay    string
	)

	cmd := &cobra.Command{
		Use:   "run <app.yaml>",
		Short: "Start an interactive session for an app (TUI)",
		Long: `Load an app definition and open an interactive TUI session. The user
types free text; an LLM harness maps it to one of the app's intents; the
state machine applies the transition; the view is re-rendered.

Harness auto-selection (when --harness is omitted):
  1. 'claude' binary on PATH       → claude harness (no API key needed)
  2. Anthropic credential found    → live harness (direct SDK)
  3. otherwise                     → replay (requires --recording)

A live credential is resolved from (first hit wins): ANTHROPIC_API_KEY,
ANTHROPIC_AUTH_TOKEN, ~/.claude/settings.json (env block), or ~/.claude.json
(primaryApiKey) — so '--harness live' works without exporting a key.

Examples:
  kitsoki run testdata/apps/cloak/app.yaml
  kitsoki run myapp.yaml --harness claude --claude-model opus
  kitsoki run myapp.yaml --harness replay --recording recording.yaml
  kitsoki run myapp.yaml --harness recording --record /tmp/rec.jsonl

Session traces are written automatically to the nearest .kitsoki/sessions/
folder (walking up from cwd). Use 'kitsoki trace <path>' to pretty-print.

See 'kitsoki docs llm-guide' for the full operator guide.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Restore terminal modes on any exit path so a panic before
			// tea.Program.Run installs its own recovery — or a prior crash
			// that already left the terminal in alt-screen / mouse-reporting
			// mode — doesn't leave the user staring at escape sequences.
			// Tea cleans up on normal Run() return; this defer covers the
			// gaps before/after Run and on panic.
			defer restoreTerminal()
			defer func() {
				if r := recover(); r != nil {
					restoreTerminal()
					panic(r) // re-raise so the runtime still prints the trace
				}
			}()

			// Force a colour profile so lipgloss/glamour render with
			// ANSI escapes regardless of how termenv classifies stdout
			// once Bubble Tea has set up its renderer. Without this,
			// tea.Println (no-alt-screen mode) sometimes received
			// already-stripped strings — lipgloss had detected the
			// program's output as non-TTY and produced plain text
			// from Render(). Honour NO_COLOR / TERM=dumb so user
			// preferences still win.
			if os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
				lipgloss.SetColorProfile(termenv.TrueColor)
			}

			appPath := args[0]

			// Load app definition. loadAppWithEnv publishes
			// KITSOKI_APP_DIR FIRST so the loader's env-var validator
			// can resolve `${KITSOKI_APP_DIR}` references in cwd: and
			// other env-expanded fields. (Setting the env var after
			// Load returned was the bug-2 ordering issue.)
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			// Determine DB path.
			if dbPath == "" {
				dbPath = defaultDBPath()
			}
			if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
				return fmt.Errorf("create db directory: %w", err)
			}

			// Resolve the execution mode (execution-modes proposal). The
			// TUI defaults to staged so multi-way decision gates pause for
			// the operator rather than auto-advancing silently.
			var execMode orchestrator.ExecutionMode
			switch execModeFlag {
			case "staged":
				execMode = orchestrator.ExecStaged
			case "one-shot", "oneshot":
				execMode = orchestrator.ExecOneShot
			default:
				return fmt.Errorf("--mode %q is invalid (want \"staged\" or \"one-shot\")", execModeFlag)
			}

			// Allocate the room-enter sink up-front so it can be passed into the
			// orchestrator AND held by the rootModel. Bound to the tea.Program
			// below via sink.Attach(p) after tea.NewProgram exists.
			roomEnterSink := tui.NewRoomEnterSink()

			// ── Orchestrator construction (shared with `kitsoki web`) ───────
			rt, err := buildSessionRuntime(runtimeConfig{
				AppPath:       appPath,
				Def:           def,
				DBPath:        dbPath,
				ExecMode:      execMode,
				HarnessType:   harnessType,
				ClaudeModel:   claudeModel,
				OracleBackend: resolveOracleBackend(oracleBackend),
				RecordingPath: recordingPath,
				RecordPath:    recordPath,
				PromptOverlay: promptOverlay,
				RoomEnterSink: roomEnterSink,
			})
			if err != nil {
				return err
			}
			defer rt.Close()

			// Re-bind the locals the rest of runCmd's TUI / resume code uses.
			s := rt.Store
			jw := rt.Journal
			jobStore := rt.JobStore
			rawChatStore := rt.ChatStore
			orch := rt.Orch

			ctx := context.Background()

			// ── Flag validation ────────────────────────────────────────────
			if continueID != "" && !continueFlag {
				return fmt.Errorf("--id requires --continue")
			}
			if continueKey != "" && !continueFlag {
				return fmt.Errorf("--key requires --continue")
			}
			if continueID != "" && continueKey != "" {
				return fmt.Errorf("--id and --key are mutually exclusive")
			}

			// ── Determine session ID (resume or fresh) ─────────────────────
			var (
				sid        app.SessionID
				resumeMode bool
				tuiOptions []tui.RootModelOption
			)

			if continueFlag {
				// Explicit --continue path.
				switch {
				case continueID != "":
					sid = app.SessionID(continueID)
				case continueKey != "":
					t, thread, kErr := parseExternalKey(continueKey)
					if kErr != nil {
						return kErr
					}
					sid, err = s.LookupByKey(ctx, t, thread)
					if errors.Is(err, store.ErrSessionNotFound) {
						return fmt.Errorf("no session bound to %s", continueKey)
					}
					if err != nil {
						return fmt.Errorf("lookup key %s: %w", continueKey, err)
					}
				default:
					// No selector — present numbered list picker.
					summaries, lErr := s.ListSessions(ctx, def.App.ID, 0)
					if lErr != nil {
						return fmt.Errorf("list sessions: %w", lErr)
					}
					keys := make([][]store.ExternalKey, len(summaries))
					for i, sum := range summaries {
						keys[i], _ = s.ListExternalKeys(ctx, sum.ID)
					}
					sid, err = pickSession(summaries, keys, cmd.ErrOrStderr(), cmd.InOrStdin())
					if errors.Is(err, errPickerAborted) {
						return errTempFail
					}
					if err != nil {
						return err
					}
				}
				resumeMode = true
			} else if !noImplicitResume {
				// Implicit-resume path: prompt with the most recent active
				// session as the default. ListSessions returns rows ordered
				// by started_at DESC, so activeSessions[0] is the newest.
				// The earlier "exactly one active" guard surprised users
				// who accumulated a pile of sessions across restarts: the
				// prompt silently disappeared the moment they had two,
				// so each restart spun up a fresh session and the loop
				// felt amnesiac. Now any number of active sessions
				// surfaces the prompt; the picker is one keystroke away
				// for users who want to resume an OLDER session.
				summaries, lErr := s.ListSessions(ctx, def.App.ID, 0)
				if lErr != nil {
					return fmt.Errorf("list sessions: %w", lErr)
				}
				activeSessions := summaries[:0]
				for _, sum := range summaries {
					if sum.Status == "active" {
						activeSessions = append(activeSessions, sum)
					}
				}
				if len(activeSessions) >= 1 {
					sum := activeSessions[0]
					age := time.Since(sum.StartedAt).Truncate(time.Second)
					stateLabel := "unknown"
					if jPreview, jErr := orch.LoadJourney(sum.ID); jErr == nil {
						stateLabel = string(jPreview.State)
					}
					var pickerHint string
					if len(activeSessions) > 1 {
						pickerHint = fmt.Sprintf(" · [p] pick from %d active",
							len(activeSessions))
					}
					fmt.Fprintf(cmd.ErrOrStderr(),
						"You have an active session for %s from %s ago, turn %d (in %s).\n"+
							"[Enter] to continue · [n] start fresh%s · [q] quit\n",
						def.App.ID,
						humanizeAge(age),
						sum.LastTurn,
						stateLabel,
						pickerHint,
					)
					scanner := bufio.NewScanner(cmd.InOrStdin())
					if !scanner.Scan() {
						// EOF / I/O error (e.g. piped or closed stdin): we
						// cannot prompt for a choice, so don't silently fall
						// into the default (resume) branch. Surface the
						// condition and abort rather than guessing intent.
						if err := scanner.Err(); err != nil {
							fmt.Fprintf(cmd.ErrOrStderr(),
								"Aborted: cannot read choice from stdin: %v\n", err)
						} else {
							fmt.Fprintln(cmd.ErrOrStderr(),
								"Aborted: no input on stdin (EOF).")
						}
						return errTempFail
					}
					choice := strings.TrimSpace(scanner.Text())
					switch strings.ToLower(choice) {
					case "q":
						return errTempFail
					case "n", "no":
						// Fall through: fresh session.
					case "p", "pick":
						// Open the numbered-list picker over all active
						// sessions so the user can resume a specific one.
						keys := make([][]store.ExternalKey, len(activeSessions))
						for i, sum := range activeSessions {
							keys[i], _ = s.ListExternalKeys(ctx, sum.ID)
						}
						chosen, pErr := pickSession(activeSessions, keys, cmd.ErrOrStderr(), cmd.InOrStdin())
						if errors.Is(pErr, errPickerAborted) {
							return errTempFail
						}
						if pErr != nil {
							return pErr
						}
						sid = chosen
						resumeMode = true
					default:
						// Empty line (Enter) or any other input → resume the
						// most recent active session.
						sid = sum.ID
						resumeMode = true
					}
				}
			}

			// ── Acquire writer lock for resume ─────────────────────────────
			// For a resumed session we wrap p.Run() inside WithWriterLock so
			// the lock is held for the entire TUI lifetime (§5.3).
			// For fresh sessions we create the session normally (no lock needed
			// at this stage; individual turns take their own locks internally).
			var (
				initialView string
			)

			if resumeMode {
				// Hard-error for typo'd --id: verify the session exists before
				// attempting rehydration.  LoadHistory returns an empty slice
				// (not an error) for unknown sessions, so we probe by listing.
				// Use the explicit-ID path for the check: --key and picker paths
				// already fail fast above if the session is not found.
				if continueID != "" {
					sum, getErr := s.GetSession(ctx, sid)
					if errors.Is(getErr, store.ErrSessionNotFound) {
						fmt.Fprintf(cmd.ErrOrStderr(), "error: no session with id %s\n", sid)
						return fmt.Errorf("no session with id %s", sid)
					}
					if getErr != nil {
						return fmt.Errorf("lookup session %s: %w", sid, getErr)
					}
					if sum.AppID != def.App.ID {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"error: session %s belongs to app %q, not %q\n",
							sid, sum.AppID, def.App.ID)
						return fmt.Errorf("session app-id mismatch")
					}
				}

				// Wire EventSink for resumed TUI session.
				// Use "tui:<session_id>" as the virtual transport:thread key so
				// each session gets a stable, unique, human-readable trace path.
				// The EventSink JSONL is the only trace — no slog file.
				tuiTracePath := store.DefaultTracePath(def.App.ID, "tui", string(sid))
				var tuiMetaTracePath string
				if mkErr := os.MkdirAll(filepath.Dir(tuiTracePath), 0o755); mkErr == nil {
					if tuiSink, sinkErr := store.OpenJSONL(tuiTracePath); sinkErr == nil {
						orch.SetEventSink(tuiSink)
						defer func() { _ = tuiSink.Close() }()
						tuiMetaTracePath = tuiTracePath
					}
					// Failure to open is non-fatal: events still land in SQLite.
				}

				// Rehydrate the session via AttachSession (journal read path §4.5).
				bundle, attachErr := orch.AttachSession(sid)
				if attachErr != nil {
					return fmt.Errorf("attach session %s: %w", sid, attachErr)
				}

				// Reconcile the story into the (appended-to) trace: backfill a
				// base snapshot for an older trace that lacks one, or record a
				// diff if the on-disk story drifted since the prior session.
				if err := orch.RecordEffectiveStory(ctx, sid); err != nil {
					return fmt.Errorf("record effective story (resume): %w", err)
				}

				// Use the journal's last view.rendered as the initial TUI frame.
				// Fall back to RenderState only when no journal entry exists yet
				// (e.g. session created before journal writes were enabled).
				if bundle.InitialView != "" {
					initialView = bundle.InitialView
				} else {
					initialView, err = orch.RenderState(bundle.Journey.State, bundle.Journey.World)
					if err != nil {
						return fmt.Errorf("render resumed state: %w", err)
					}
				}

				// Print pre-resume status header (§5.5).
				clarifyNote := ""
				if bundle.PendingClarify != nil {
					clarifyNote = " (1 pending clarify rehydrated)"
				}
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Resuming %s (%s, turn %d, state %s): transcript: %d rows reconstructed%s\n",
					sid, def.App.ID, bundle.Journey.Turn, bundle.Journey.State,
					len(bundle.TranscriptEntries), clarifyNote,
				)

				tuiOptions = append(tuiOptions,
					tui.WithResumedJourney(bundle.Journey.State, bundle.Journey.World, bundle.Journey.Turn),
					// Pass an empty initial view to NewRootModel because we seed
					// the transcript from journal entries below; passing the view
					// here too would duplicate the last turn.
					tui.WithResumedTranscript(bundle.TranscriptEntries),
				)

				// Build the TUI model now so we can pass it to tea.NewProgram
				// before acquiring the lock.  Pass the initialView as the
				// NewRootModel arg only when there are no transcript entries to
				// replay (e.g. first-turn resume), so the TUI shows something.
				effectiveInitialView := ""
				if len(bundle.TranscriptEntries) == 0 {
					effectiveInitialView = initialView
				}
				tuiOptions = append([]tui.RootModelOption{
					tui.WithJobStore(jobStore),
					tui.WithChatStore(rawChatStore),
					tui.WithJournalWriter(jw),
				}, tuiOptions...)
				if tuiMetaTracePath != "" {
					tuiOptions = append(tuiOptions, tui.WithExternalTraceFile(tuiMetaTracePath))
				}
				// Allocate the meta-mode stream sink up-front so the
				// model can hold a reference; bind it to the program
				// post-construction via sink.Attach(p) below.
				metaSink := tui.NewMetaStreamSink()
				tuiOptions = append(tuiOptions, tui.WithMetaStreamSink(metaSink))
				rootModel := tui.NewRootModel(orch, sid, appPath, effectiveInitialView, tuiOptions...)
				// Single-pane redesign: no alt-screen + no mouse capture.
				// Output prints into the terminal's normal scrollback so
				// the header scrolls off naturally as content grows
				// (Claude Code's model). The View() output is just the
				// bottom chrome — footer + prompt — which Bubble Tea
				// re-renders in place at the cursor row.

				// Suppress slog output during TUI operation to prevent log lines
				// from mixing with the queue indicator on the same terminal line.
				// Issue: oracle runner emits slog records while TUI is rendering,
				// causing "2026-05-29 ... INFO ... ⏳ running…" on same line.
				oldLogger := slog.Default()
				suppressedLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
				slog.SetDefault(suppressedLogger)
				defer slog.SetDefault(oldLogger)

				p := tea.NewProgram(rootModel)
				metaSink.Attach(p)
				defer metaSink.Detach()
				roomEnterSink.Attach(p)
				defer roomEnterSink.Detach()
				detach := tui.AttachOrchestratorObserver(orch, p, sid)
				defer detach()

				lockErr := s.WithWriterLock(ctx, sid, func() error {
					_, runErr := p.Run()
					return runErr
				})
				if errors.Is(lockErr, store.ErrSessionBusy) {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"session busy: another process holds the writer lock for %s\n"+
							"Either close that attached session or run:\n"+
							"    kitsoki session detach --id %s\n"+
							"to break a stale lock.\n",
						sid, sid,
					)
					return errTempFail
				}
				return lockErr
			}

			// ── Fresh session path ─────────────────────────────────────────
			sid, err = orch.NewSession(ctx)
			if err != nil {
				return fmt.Errorf("create session: %w", err)
			}

			// Wire EventSink for fresh TUI session.
			// freshMetaTracePath is the path handed to the meta-mode agent.
			var freshMetaTracePath string
			{
				freshTracePath := store.DefaultTracePath(def.App.ID, "tui", string(sid))
				if mkErr := os.MkdirAll(filepath.Dir(freshTracePath), 0o755); mkErr == nil {
					if freshSink, sinkErr := store.OpenJSONL(freshTracePath); sinkErr == nil {
						orch.SetEventSink(freshSink)
						defer func() { _ = freshSink.Close() }()
						freshMetaTracePath = freshTracePath
					}
					// Failure to open is non-fatal: events still land in SQLite.
				}
			}

			// Record the effective story as the first event after the header,
			// before any turn-0 on_enter events — so the trace self-describes
			// the story it replays against (see store.StorySnapshot).
			if err := orch.RecordEffectiveStory(ctx, sid); err != nil {
				return fmt.Errorf("record effective story: %w", err)
			}

			// Fire the initial state's on_enter chain BEFORE rendering
			// the first frame. Machine.Turn already runs on_enter for a
			// transition that lands in a new state, but the initial
			// state isn't entered via a transition — without this call
			// any app whose root room has on_enter (e.g. dev-story's
			// main view that invokes iface.ticket.list_mine to
			// populate its ticket queue) renders the first frame
			// against default-empty world keys, and the user sees a
			// blank list until they navigate away and back.
			if err := orch.RunInitialOnEnter(ctx, sid); err != nil {
				return fmt.Errorf("run initial on_enter: %w", err)
			}

			// Reload the journey so InitialViewTyped renders against
			// the post-on_enter world.
			j, jerr := orch.LoadJourney(sid)
			if jerr != nil {
				return fmt.Errorf("load journey post-on_enter: %w", jerr)
			}
			w := j.World

			// Get initial view. Capture the typed-view payload alongside
			// the rendered fallback string so the TUI's initial-paint
			// seam can route through AppendSystemTyped when the root
			// state's view is a typed element-array — otherwise the
			// pre-rendered ANSI would be re-routed through Glamour by
			// AppendSystem, which strips the ESC bytes and surfaces
			// literal `[1;…m` codes in the rendered output.
			initialView, initialTypedView, initialTypedEnv, initialTypedRR, err := orch.InitialViewTyped(w)
			if err != nil {
				return fmt.Errorf("initial view: %w", err)
			}

			// --warp: bootstrap teleport. Applied BEFORE the TUI starts so
			// the operator lands at the primed state on the first frame.
			// Errors abort with a clear message (no half-warped session).
			// The teleport's returned outcome carries the post-warp View,
			// which we feed into the TUI's initialView so the first frame
			// matches the post-warp state.
			if warpBasisPath != "" {
				resolved, basis, basisErr := tui.LoadWarpBasis(warpBasisPath, appPath)
				if basisErr != nil {
					return fmt.Errorf("--warp %q: %w", warpBasisPath, basisErr)
				}
				if basis.State == "" {
					return fmt.Errorf("--warp %s: missing required `state:` field", resolved)
				}
				slots := make(map[string]any, len(basis.World))
				for k, v := range basis.World {
					slots[k] = v
				}
				out, warpErr := orch.Teleport(ctx, sid, inbox.TeleportTarget{
					State: app.StatePath(basis.State),
					Slots: slots,
				})
				if warpErr != nil {
					return fmt.Errorf("--warp %s: teleport: %w", resolved, warpErr)
				}
				if out != nil && out.View != "" {
					initialView = out.View
					initialTypedView = out.TypedView
					initialTypedEnv = out.RenderEnv
					initialTypedRR = out.Renderer
				}
			}

			// Launch TUI.
			// WithMouseCellMotion enables scroll-wheel events on the
			// transcript viewport. Copying text then requires Option
			// (macOS) or Shift (Linux) held during selection to bypass
			// mouse capture.
			tuiOptions = []tui.RootModelOption{
				tui.WithJobStore(jobStore),
				tui.WithChatStore(rawChatStore),
				tui.WithJournalWriter(jw),
				tui.WithInitialTypedView(initialTypedView, initialTypedEnv, initialTypedRR),
			}
			if freshMetaTracePath != "" {
				tuiOptions = append(tuiOptions, tui.WithExternalTraceFile(freshMetaTracePath))
			}
			// Allocate the meta-mode stream sink up-front so the
			// model can hold a reference; bind it to the program
			// post-construction via sink.Attach(p) below. This is
			// what lets the user see live agent progress (tool calls,
			// narration, retries) in the transcript while a meta-mode
			// Send is in flight, instead of a buffered spinner.
			metaSink := tui.NewMetaStreamSink()
			tuiOptions = append(tuiOptions, tui.WithMetaStreamSink(metaSink))
			rootModel := tui.NewRootModel(orch, sid, appPath, initialView, tuiOptions...)
			// Single-pane redesign: no alt-screen + no mouse capture.
			// Output prints to normal scrollback so the terminal's
			// native scroll (wheel / Cmd+↑) walks history; the prompt
			// re-renders at the bottom in place.

			// Suppress slog output during TUI operation to prevent log lines
			// from mixing with the queue indicator on the same terminal line.
			oldLogger := slog.Default()
			suppressedLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
			slog.SetDefault(suppressedLogger)
			defer slog.SetDefault(oldLogger)

			p := tea.NewProgram(rootModel)
			metaSink.Attach(p)
			defer metaSink.Detach()
			roomEnterSink.Attach(p)
			defer roomEnterSink.Detach()
			// Bridge orchestrator background-turn notifications into
			// the Bubble Tea message loop so the main transcript
			// re-renders when a background job's on_complete fires —
			// without this, the inbox badge ticks but the transcript
			// stays frozen until the next keystroke.
			detach := tui.AttachOrchestratorObserver(orch, p, sid)
			defer detach()
			_, err = p.Run()
			return err
		},
	}

	cmd.Flags().StringVar(&harnessType, "harness", "",
		"harness type: claude|live|replay|recording (default: claude if `claude` binary on PATH, else live if an Anthropic credential is found, else replay)")
	cmd.Flags().StringVar(&claudeModel, "claude-model", "",
		fmt.Sprintf("model passed to claude -p --model (default: %s); use 'opus' for higher quality at higher cost", harness.DefaultClaudeModel))
	cmd.Flags().StringVar(&oracleBackend, "oracle", "",
		"coding-agent CLI backend for host.oracle.* calls: claude|copilot (default: claude, or $KITSOKI_ORACLE)")
	cmd.Flags().StringVar(&recordingPath, "recording", "",
		"path to recording YAML file (required for --harness replay)")
	cmd.Flags().StringVar(&recordPath, "record", "",
		"path to output JSONL recording (for --harness recording)")
	cmd.Flags().StringVar(&dbPath, "db", "",
		"path to SQLite session database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")

	cmd.Flags().BoolVar(&continueFlag, "continue", false,
		"resume an existing session instead of starting a fresh one")
	cmd.Flags().StringVar(&continueID, "id", "",
		"resume a specific session by ID (requires --continue)")
	cmd.Flags().StringVar(&continueKey, "key", "",
		"resume a specific session by external key transport:thread (requires --continue)")
	cmd.Flags().BoolVar(&noImplicitResume, "no-implicit-resume", false,
		"always start a fresh session even if exactly one active session exists for this app")

	cmd.Flags().StringVar(&promptOverlay, "prompt-overlay", "",
		"project prompt-overlay dir: its prompts shadow the story's and may {% extends \"@story/…\" %} to specialize without forking (see docs/stories/prompts.md)")
	cmd.Flags().StringVar(&execModeFlag, "mode", "staged",
		`execution mode: "staged" (stop at each decision gate for the operator) or "one-shot" (auto-advance, LLM/default deciders)`)
	cmd.Flags().StringVar(&warpBasisPath, "warp", "",
		"path to a warp-basis YAML (state + world overrides); applied as the first action after session create. Same file the TUI's /warp file:<path> loads. See stories/oregon-trail/scenarios/ for examples.")

	return cmd
}

// setHarnessLogger wires the logger into harness implementations that support it.
func setHarnessLogger(h harness.Harness, l *slog.Logger) {
	type withLogger interface{ WithLogger(*slog.Logger) }
	if wl, ok := h.(withLogger); ok {
		wl.WithLogger(l)
	}
}

// autoSelectHarness returns the harness type to use when --harness is not explicitly set.
//
// Precedence:
//  1. `claude` binary on PATH    → use ClaudeCLIHarness (no API key needed).
//  2. Anthropic credential found → use LiveHarness (direct SDK). See
//     resolveAnthropicCredential for the credential chain.
//  3. Otherwise                  → use "replay" (requires --recording) or error.
func autoSelectHarness() string {
	if _, err := exec.LookPath("claude"); err == nil {
		return "claude"
	}
	if hasAnthropicCredential() {
		return "live"
	}
	// Fall back to replay; the caller will error if --recording is not set.
	return "replay"
}

// resolveOracleBackend resolves the oracle backend selector with precedence
// flag → $KITSOKI_ORACLE → "" (claude default). The runtime treats "" / "claude"
// identically (the default backend), so an empty result is fine.
func resolveOracleBackend(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	return os.Getenv("KITSOKI_ORACLE")
}

// buildHarness constructs the appropriate harness based on the harness type flag.
// If harnessType is empty, autoSelectHarness() is called to pick one.
// claudeModel is the model name for the ClaudeCLIHarness; pass "" to use the default.
func buildHarness(harnessType, claudeModel, oracleBackend, recordingPath, recordPath string, def *app.AppDef) (harness.Harness, error) {
	if harnessType == "" {
		harnessType = autoSelectHarness()
	}

	switch harnessType {
	case "claude":
		// Intent routing reuses the claude-CLI harness shell even for the
		// copilot backend: it builds a claude-shaped invocation and the
		// runner's TranslateInvocation (installed via the copilot backend on
		// the Exec context) rewrites it onto copilot's flags. Point the harness
		// at the copilot binary and tag the Exec context so the one engine that
		// forks the subprocess uses the copilot backend.
		if oracleBackend == "copilot" {
			copilotBin, err := exec.LookPath("copilot")
			if env := os.Getenv(host.CopilotBinEnv); env != "" {
				copilotBin, err = env, nil
			}
			if err != nil {
				return nil, fmt.Errorf("--oracle copilot: %w", host.ErrOracleUnavailable)
			}
			copilotExec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
				return host.RunClaudeOneShotForHarness(host.WithOracleBackendNamed(ctx, "copilot"), bin, args, stdin, workingDir)
			}
			return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{
				Model:         claudeModel,
				ClaudeBin:     copilotBin,
				Exec:          copilotExec,
				ValidatorTool: "kitsoki-validator-submit",
			})
		}
		return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{Model: claudeModel, Exec: host.RunClaudeOneShotForHarness})

	case "replay":
		if recordingPath == "" {
			return nil, fmt.Errorf("--recording is required when --harness replay is set")
		}
		return harness.NewReplay(recordingPath)

	case "live":
		client, source, err := newLiveClient()
		if err != nil {
			return nil, err
		}
		slog.Debug("harness/live: credential resolved", "source", source)
		return harness.NewLive(&client, "", def)

	case "recording":
		if recordingPath != "" {
			// Wrap replay with recording.
			replay, err := harness.NewReplay(recordingPath)
			if err != nil {
				return nil, fmt.Errorf("replay harness for recording: %w", err)
			}
			if recordPath == "" {
				recordPath = "recording.jsonl"
			}
			return harness.NewRecording(replay, recordPath)
		}
		// Wrap live with recording.
		client, source, err := newLiveClient()
		if err != nil {
			return nil, fmt.Errorf("recording mode without a recording requires a live credential: %w", err)
		}
		slog.Debug("harness/recording: live credential resolved", "source", source)
		live, err := harness.NewLive(&client, "", def)
		if err != nil {
			return nil, err
		}
		if recordPath == "" {
			recordPath = "recording.jsonl"
		}
		return harness.NewRecording(live, recordPath)

	default:
		return nil, fmt.Errorf("unknown harness type %q (use claude|live|replay|recording)", harnessType)
	}
}

// defaultDBPath returns the default SQLite database path.
func defaultDBPath() string {
	// Use $XDG_DATA_HOME/kitsoki/sessions.db or ~/.local/share/kitsoki/sessions.db.
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "kitsoki", "sessions.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "kitsoki-sessions.db")
	}
	return filepath.Join(home, ".local", "share", "kitsoki", "sessions.db")
}

func vizCmd() *cobra.Command {
	var (
		outPath     string
		doMermaid   bool
		byRoom      bool
		doFlowchart bool
		detailLevel string
		filterRoom  string
		filterFrom  string
		filterTo    string
	)

	cmd := &cobra.Command{
		Use:   "viz <app.yaml>",
		Short: "Emit a graph diagram (Graphviz DOT or Mermaid) for an app",
		Long: `Emit a graph diagram for the given app definition. Useful for
getting a visual overview of a state machine before authoring/debugging.

Default: Graphviz DOT to <appname>-viz.dot.
--mermaid: Mermaid stateDiagram-v2 to <appname>-viz.mmd (or '-' for stdout).
--rooms (with --mermaid): split into one diagram per room + an overview,
    written to a directory (default <appname>-viz/). A "room" is the
    top-level compound state if any, else the prefix before the first '_'
    in the state name. Useful for apps with many states (devstory, etc.)
    where the single all-up diagram is unreadable.
--flowchart: Mermaid flowchart LR (data-flow view) to <appname>-flow.mmd.
    Shows rooms as subgraphs, on_enter effects as hex nodes, world writes
    as cylinder nodes — styled like the bugfix pipeline diagrams.
    Use --detail to control verbosity:
      rooms  — one node per room, cross-room transitions only
      states — states in room subgraphs, all transitions (default)
      steps  — + on_enter effect chains (shell/llm/work hex nodes)
      full   — + world writes (bind/set cylinders) and error targets
    Use --room or --from/--to to scope the diagram to a subset of rooms:
      --room <name>: limit flowchart to a single room (stub nodes for external exits)
      --from <room> --to <room>: limit flowchart to rooms on any path between the two
          (includes both endpoints; stub nodes for exits outside the slice)

Examples:
  kitsoki viz testdata/apps/cloak/app.yaml
  kitsoki viz myapp.yaml --out /tmp/g.dot && dot -Tsvg /tmp/g.dot -o /tmp/g.svg
  kitsoki viz testdata/apps/cloak/app.yaml --mermaid --out -
  kitsoki viz myapp.yaml --mermaid --rooms --out viz/
  kitsoki viz myapp.yaml --mermaid | mmdc -i - -o graph.svg
  kitsoki viz myapp.yaml --flowchart --detail steps
  kitsoki viz myapp.yaml --flowchart --detail full --out flow.mmd
  kitsoki viz myapp.yaml --flowchart --detail full | mmdc -i - -o flow.svg
  kitsoki viz myapp.yaml --flowchart --detail steps --room reproducing
  kitsoki viz myapp.yaml --flowchart --detail full --from reproducing --to testing`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			// loadAppWithEnv publishes KITSOKI_APP_DIR so the loader's
			// env-var validator (e.g. cwd: "${KITSOKI_APP_DIR}/foo")
			// can resolve references at validate time.
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			if doFlowchart {
				dl, err := viz.ParseDetailLevel(detailLevel)
				if err != nil {
					return err
				}
				filter := viz.FlowchartFilter{Room: filterRoom, From: filterFrom, To: filterTo}
				if err := filter.Validate(); err != nil {
					return err
				}
				if outPath == "" {
					outPath = def.App.ID + "-flow.mmd"
				}
				var w io.Writer
				if outPath == "-" {
					w = cmd.OutOrStdout()
				} else {
					f, err := os.Create(outPath)
					if err != nil {
						return fmt.Errorf("create %q: %w", outPath, err)
					}
					defer func() { _ = f.Close() }()
					w = f
				}
				if err := viz.ExportFlowchart(def, dl, filter, w); err != nil {
					return fmt.Errorf("export flowchart: %w", err)
				}
				if outPath != "-" {
					fmt.Printf("wrote %s\n", outPath)
					fmt.Printf("render: mmdc -i %s -o flow.svg\n", outPath)
				}
				return nil
			}

			if byRoom {
				if !doMermaid {
					return fmt.Errorf("--rooms requires --mermaid")
				}
				if outPath == "" {
					outPath = def.App.ID + "-viz"
				}
				err := viz.ExportMermaidRooms(def, outPath,
					func(p string) error { return os.MkdirAll(p, 0755) },
					func(p string, data []byte) error { return os.WriteFile(p, data, 0644) },
				)
				if err != nil {
					return fmt.Errorf("export rooms: %w", err)
				}
				fmt.Printf("wrote %s/{index.md,_overview.mmd,*.mmd}\n", outPath)
				fmt.Printf("render: see %s/index.md for the per-room render command\n", outPath)
				return nil
			}

			ext := ".dot"
			if doMermaid {
				ext = ".mmd"
			}
			if outPath == "" {
				outPath = def.App.ID + "-viz" + ext
			}

			var w io.Writer
			if outPath == "-" {
				w = cmd.OutOrStdout()
			} else {
				f, err := os.Create(outPath)
				if err != nil {
					return fmt.Errorf("create %q: %w", outPath, err)
				}
				defer func() { _ = f.Close() }()
				w = f
			}

			if doMermaid {
				if err := viz.ExportMermaid(def, w); err != nil {
					return fmt.Errorf("export Mermaid: %w", err)
				}
			} else {
				if err := viz.Export(def, w); err != nil {
					return fmt.Errorf("export DOT: %w", err)
				}
			}

			if outPath != "-" {
				fmt.Printf("wrote %s\n", outPath)
				if doMermaid {
					fmt.Printf("render: mmdc -i %s -o graph.svg\n", outPath)
					fmt.Printf("        # for large apps, raise mermaid-cli's text/edge caps:\n")
					fmt.Printf("        # mmdc -c <(echo '{\"maxTextSize\":5000000,\"maxEdges\":50000}') -i %s -o graph.svg\n", outPath)
				} else {
					fmt.Printf("render: dot -Tpng %s -o graph.png\n", outPath)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&outPath, "out", "", `output file or directory (default: <appid>-viz.{dot,mmd} or <appid>-viz/ with --rooms; "-" for stdout)`)
	cmd.Flags().BoolVar(&doMermaid, "mermaid", false, "emit Mermaid stateDiagram-v2 instead of Graphviz DOT")
	cmd.Flags().BoolVar(&byRoom, "rooms", false, "split into per-room files plus an overview (requires --mermaid)")
	cmd.Flags().BoolVar(&doFlowchart, "flowchart", false, "emit Mermaid flowchart LR (data-flow view) instead of stateDiagram")
	cmd.Flags().StringVar(&detailLevel, "detail", "states", "detail level for --flowchart: rooms|states|steps|full")
	cmd.Flags().StringVar(&filterRoom, "room", "", "filter flowchart to a single room (--flowchart only)")
	cmd.Flags().StringVar(&filterFrom, "from", "", "start room for a range filter (--flowchart only; requires --to)")
	cmd.Flags().StringVar(&filterTo, "to", "", "end room for a range filter (--flowchart only; requires --from)")
	return cmd
}

// replayCmd is defined in replay.go (oracle-split Phase 4).

func testCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run Mode 1 and Mode 2 tests for an app",
		Long: `Test sub-commands:
  kitsoki test flows   <app.yaml>   — Mode 2: deterministic flow tests (no LLM)
  kitsoki test intents <app.yaml>   — Mode 1: intent pass-rate tests

Fixture layout (defaults):
  <app-dir>/flows/*.yaml      — flow fixtures (run under 'test flows')
  <app-dir>/intents/*.yaml    — intent fixtures (run under 'test intents')
  <app-dir>/recording.yaml       — recording YAML (seeds replay/static harness)

See 'kitsoki docs llm-guide' §7 for fixture shape.`,
	}
	cmd.AddCommand(testFlowsCmd())
	cmd.AddCommand(testIntentsCmd())
	return cmd
}

// serveCmd starts the kitsoki MCP server on stdio for a given app.
// Usage: kitsoki serve <app.yaml> [--db <path>]
//
// The server exposes the single `transition` tool to any MCP client
// (Claude Desktop, Claude Code, etc.) that connects via stdio.
//
// Example (smoke test via shell):
//
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}' | kitsoki serve cloak.yaml
func serveCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "serve <app.yaml>",
		Short: "Start the MCP server on stdio for an app",
		Long: `Start the kitsoki MCP server on stdin/stdout. External MCP clients
(Claude Desktop, Claude Code) can connect and drive the app via the
single 'transition' tool.

The server reads MCP JSON-RPC messages from stdin and writes responses
to stdout. It blocks until stdin is closed.

The 'transition' tool accepts:
  { intent: <string>, slots: <object?>, confidence: <float?>, session_id: <string> }

and returns either:
  { ok: true,  state: <path>, view: <string>, menu: [<intent>,...], world: <obj> }
or:
  { ok: false, error: { code: <string>, message: <string>, ... } }

Without --db, sessions are in-memory and lost on exit.

See 'kitsoki docs llm-guide' for the full operator guide.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			// Load the app definition. loadAppWithEnv publishes
			// KITSOKI_APP_DIR first so env-expanded fields validate.
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			// Open the session store.
			var s store.Store
			if dbPath == "" {
				// Default: in-memory (ephemeral session for this serve invocation).
				s, err = store.OpenMemory()
			} else {
				s, err = store.Open(dbPath)
			}
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = s.Close() }()

			// Build the machine.
			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine for %q: %w", def.App.ID, err)
			}

			// Construct the MCP server.
			srv := kitsokimcp.NewServer(m, s, def)

			// Run until stdin closes or signal received.
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			fmt.Fprintf(os.Stderr, "kitsoki: serving app %q via MCP stdio\n", def.App.ID)
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to the SQLite session database (default: in-memory)")
	return cmd
}

// restoreTerminal emits the escape sequences that disable mouse reporting and
// leave the alternate screen, in case a prior crash (or one in this run before
// tea.NewProgram's own recovery kicked in) left the terminal in those modes.
// Idempotent: safe to call from multiple defer paths.
//
//   - CSI ?1000 l — disable X10 mouse reporting
//   - CSI ?1002 l — disable cell-motion mouse reporting (matches tea.WithMouseCellMotion)
//   - CSI ?1003 l — disable any-motion mouse reporting
//   - CSI ?1006 l — disable SGR mouse mode
//   - CSI ?1049 l — leave alternate screen buffer
//
// Written to stderr so it doesn't interleave with structured stdout output
// (e.g. JSON traces piped to a file). A bare terminal will render the
// sequence; a pipe (no terminal) will silently absorb it.
func restoreTerminal() {
	const seq = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1049l"
	_, _ = fmt.Fprint(os.Stderr, seq)
}
