// Command kitsoki is the CLI entrypoint for the Kitsoki deterministic LLM orchestrator.
// Subcommands: run, viz, trace, replay, test, serve (§9a, §12).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/buildinfo"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/kitrepo"
	"kitsoki/internal/kitstage"
	"kitsoki/internal/machine"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/metamode"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/tui"
	"kitsoki/internal/viz"
	"kitsoki/internal/webconfig"
)

// version is stamped at release time via -ldflags "-X main.version=...".
// The default is the dev/unstamped value (`go build`, `go run`, tests).
var version = "0.0.1-scaffold"

// newRootCmd builds the top-level cobra command tree. Extracted from main()
// so tests can construct an isolated root and call Execute() against captured
// I/O without running the real os.Args/os.Exit dance.
func newRootCmd() *cobra.Command {
	buildinfo.Version = version
	// kitsokiRepoFlag backs the persistent --kitsoki-repo override. It points
	// `@kitsoki/<name>` imports at a live kitsoki checkout instead of the
	// embedded story library (see buildImportResolver). Empty → no override;
	// the resolver falls through to on-disk discovery then the embedded copy.
	var kitsokiRepoFlag string
	// stagedFlag backs the persistent --staged toggle. It resolves every kit
	// with a `kitsoki kit update` staged candidate to that candidate instead
	// of the accepted lockfile resolution (see internal/kitstage and
	// buildImportResolver). Exported as $KITSOKI_KIT_STAGED so subprocesses
	// inherit the trial posture, mirroring --kitsoki-repo → $KITSOKI_REPO.
	var stagedFlag bool
	defaultRunCmd := runCmd()
	prepareInvocation := func(cmd *cobra.Command, args []string) error {
		// --kitsoki-repo overrides $KITSOKI_REPO when given; either way the
		// chosen value is exported so every downstream consumer — the
		// import resolver's override branch (buildImportResolver), the
		// engine-targeting meta modes, expandMetaCwd, and the subprocesses
		// the agents spawn — reads one canonical location. The flag wins so
		// an operator can point a single invocation at a checkout without
		// mutating their persisted ~/.kitsoki/repo.
		if kitsokiRepoFlag != "" {
			abs := kitsokiRepoFlag
			if a, err := filepath.Abs(kitsokiRepoFlag); err == nil {
				abs = a
			}
			_ = os.Setenv(kitrepo.EnvVar, abs)
		}
		if os.Getenv(kitrepo.EnvVar) == "" {
			if repo := kitrepo.Resolve(); repo != "" {
				_ = os.Setenv(kitrepo.EnvVar, repo)
			}
		}
		// --staged exports the trial posture the same way: flag wins, env
		// stays authoritative for subprocesses and the import resolver.
		if stagedFlag {
			_ = os.Setenv(kitstage.EnvStaged, "all")
		}
		// Record whether the operator explicitly passed --semantic-routing so
		// semanticRoutingOptions can let it override KITSOKI_SEMANTIC_ROUTING.
		// Persistent flags are inherited, so cmd.Flags() resolves it for every
		// subcommand.
		semanticRoutingFlagSet = cmd.Flags().Changed("semantic-routing")
		return nil
	}

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
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if err := prepareInvocation(cmd, args); err != nil {
				return err
			}
			defaultRunCmd.SetOut(cmd.OutOrStdout())
			defaultRunCmd.SetErr(cmd.ErrOrStderr())
			defaultRunCmd.SetIn(cmd.InOrStdin())
			defaultRunCmd.SetContext(cmd.Context())
			return defaultRunCmd.RunE(defaultRunCmd, nil)
		},
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
		PersistentPreRunE: prepareInvocation,
	}

	// Persistent override for `@kitsoki/<name>` import resolution. Runs for
	// every subcommand; see buildImportResolver for the precedence order.
	root.PersistentFlags().StringVar(&kitsokiRepoFlag, "kitsoki-repo", "",
		"path to a kitsoki source checkout; resolves @kitsoki/NAME imports against <path>/stories/NAME (overrides $KITSOKI_REPO and the embedded story library)")

	// Persistent trial-posture toggle: resolve kits staged by `kitsoki kit
	// update` to their candidate trees (see internal/kitstage).
	root.PersistentFlags().BoolVar(&stagedFlag, "staged", false,
		"resolve kits with a staged update candidate (kitsoki kit update) to the staged version instead of the accepted lockfile resolution (env: KITSOKI_KIT_STAGED)")

	// Global toggle for the deterministic semantic-routing stack. When unset,
	// the CLI keeps the stack off: exact deterministic commands still route, and
	// misses go to the selected harness/model. Passing true opts back into
	// semroute, turn-cache, default_intent, and free-form fallback.
	root.PersistentFlags().BoolVar(&semanticRoutingFlag, "semantic-routing", false,
		"enable the deterministic semantic-routing stack (semroute, turn-cache, default_intent sink, free-form fallback); default off (env: KITSOKI_SEMANTIC_ROUTING)")
	root.Flags().AddFlagSet(defaultRunCmd.Flags())

	root.AddCommand(versionCmd())
	root.AddCommand(defaultRunCmd)
	root.AddCommand(vizCmd())
	root.AddCommand(traceCmd())
	root.AddCommand(replayCmd())
	root.AddCommand(replayRoutingCmd())
	root.AddCommand(testCmd())
	root.AddCommand(serveCmd())
	root.AddCommand(mcpCmd())
	root.AddCommand(mcpTestCmd())
	root.AddCommand(renderCmd())
	root.AddCommand(docsCmd())
	root.AddCommand(recordCmd())
	root.AddCommand(inspectCmd())
	root.AddCommand(turnCmd())
	root.AddCommand(interceptCmd())
	root.AddCommand(hookCmd())
	root.AddCommand(driveCmd())
	root.AddCommand(shotCmd())
	root.AddCommand(webShotCmd())
	root.AddCommand(sessionCmd())
	root.AddCommand(inboxCmd())
	root.AddCommand(chatCmd())
	root.AddCommand(mcpValidatorCmd())
	root.AddCommand(mcpBashCmd())
	root.AddCommand(mcpCodeactCmd())
	root.AddCommand(mcpGraphCmd())
	root.AddCommand(mcpOperatorAskCmd())
	root.AddCommand(bugCmd())
	root.AddCommand(issuesCmd())
	root.AddCommand(uiCmd())
	root.AddCommand(extractCmd())
	root.AddCommand(promptsCmd())
	root.AddCommand(agentCmd())
	root.AddCommand(agentServeCmd())
	root.AddCommand(starlarkCmd())
	root.AddCommand(migrateAgentCmd())
	root.AddCommand(cassetteCmd())
	root.AddCommand(evalCmd())
	root.AddCommand(agentBenchCmd())
	root.AddCommand(personaQACmd())
	root.AddCommand(qaCmd())
	root.AddCommand(exportStatusCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(webCmd())
	root.AddCommand(tourCmd())
	root.AddCommand(tourSpecCmd())
	root.AddCommand(materializeCmd())
	root.AddCommand(newGHAgentCmd())
	root.AddCommand(projectProfileCmd())
	root.AddCommand(projectToolsCmd())
	root.AddCommand(kitCmd())
	root.AddCommand(validateCmd())
	root.AddCommand(storyboardCmd())
	root.AddCommand(workflowCmd())
	root.AddCommand(capsuleCmd())
	root.AddCommand(queueCmd())
	root.AddCommand(gitopsCmd())
	root.AddCommand(ticketProviderCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(graphCmd())
	root.AddCommand(roadmapCmd())
	root.AddCommand(pogCmd())
	root.AddCommand(historyCmd())
	root.AddCommand(initCmd())
	root.AddCommand(tuiServeCmd())
	tierHelp(root)

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
		// kitsoki intercept pass-through: exit 10 is a normal outcome (the prompt
		// proceeds to the LLM), NOT a failure — never print an "error:" line.
		if code, ok := IsInterceptExitError(err); ok {
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
			if rev := strings.TrimSpace(buildinfo.Revision); rev != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "kitsoki %s\nrevision: %s\n", version, rev)
				return
			}
			if rev := strings.TrimSpace(buildinfo.RevisionShort); rev != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "kitsoki %s\nrevision: %s\n", version, rev)
				return
			}
			fmt.Fprintf(cmd.OutOrStdout(), "kitsoki %s\n", version)
		},
	}
}

func tuiMetaAgentCaller(harnessType string) metamode.AgentCaller {
	if strings.TrimSpace(harnessType) == "replay" {
		var opts []metamode.StubOption
		if v := os.Getenv("KITSOKI_META_STREAM_DELAY_MS"); v != "" {
			if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
				opts = append(opts, metamode.WithStubStreamDelay(time.Duration(ms)*time.Millisecond))
			}
		}
		return metamode.NewStubAgentCaller(opts...)
	}
	return metamode.NewAgentCallerAdapter()
}

func tuiMetaController(def *app.AppDef, cs *chats.Store, harnessType string) *metamode.Controller {
	if def == nil || cs == nil || len(def.MetaModes) == 0 {
		return nil
	}
	reg := host.AgentRegistry()
	if reg == nil {
		return nil
	}
	return &metamode.Controller{
		Chats:  metamode.NewChatStoreAdapter(cs),
		Agents: reg,
		AppDef: def,
		Agent:  tuiMetaAgentCaller(harnessType),
	}
}

func tuiStoryOptions(cfg webconfig.WebConfig) ([]tui.StoryOption, error) {
	dirs := webconfig.Resolve(nil, cfg)
	metas, err := webconfig.DiscoverStories(dirs, buildImportResolver())
	if err != nil {
		if defaultStoryDirsMissing(dirs, err) {
			return nil, nil
		}
		return nil, err
	}
	options := make([]tui.StoryOption, 0, len(metas))
	for _, meta := range metas {
		if meta.Def == nil {
			continue
		}
		options = append(options, tui.StoryOption{
			Path:  meta.Path,
			AppID: meta.Def.App.ID,
			Title: storyTitle(meta.Def),
		})
	}
	return options, nil
}

func runCmd() *cobra.Command {
	var (
		harnessType   string
		claudeModel   string
		agentBackend  string
		recordingPath string
		recordPath    string
		hostCassette  string
		dbPath        string
		continueFlag  bool
		continueID    string
		continueKey   string
		warpBasisPath string
		execModeFlag  string
		promptOverlay string
		ticketRepo    string
	)

	cmd := &cobra.Command{
		Use:   "run <app.yaml>",
		Short: "Start an interactive session for an app (TUI)",
		Long: `Load an app definition and open an interactive TUI session. The user
types free text; an LLM harness maps it to one of the app's intents; the
state machine applies the transition; the view is re-rendered.

Harness auto-selection (when --harness is omitted):
  1. non-Claude --agent backend    → CLI harness for that backend
  2. 'claude' binary on PATH       → claude harness (no API key needed)
  3. Anthropic credential found    → live harness (direct SDK)
  4. otherwise                     → setup error; replay requires --recording

A live credential is resolved from (first hit wins): ANTHROPIC_API_KEY,
ANTHROPIC_AUTH_TOKEN, ~/.claude/settings.json (env block), or ~/.claude.json
(primaryApiKey) — so '--harness live' works without exporting a key.

Examples:
  kitsoki run testdata/apps/cloak/app.yaml
  kitsoki run myapp.yaml --harness claude --claude-model opus
  kitsoki run myapp.yaml --harness replay --recording recording.yaml
  kitsoki run myapp.yaml --harness replay --recording recording.yaml --host-cassette host.cassette.yaml
  kitsoki run myapp.yaml --harness recording --record /tmp/rec.jsonl

Session traces are written automatically to the nearest .kitsoki/sessions/
folder (walking up from cwd). Use 'kitsoki trace <path>' to pretty-print.

See 'kitsoki docs llm-guide' for the full operator guide.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// `run` owns the terminal. Silence the process-wide default logger
			// from the start of command execution so loader advisories and
			// validation warnings cannot interleave with Cobra errors/usage
			// before Bubble Tea has a chance to install its own rendering loop.
			oldRunLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer slog.SetDefault(oldRunLogger)

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

			runArgs := append([]string(nil), args...)
			for {
				var selectedStoryPath string
				runOnce := func(args []string) error {
					// Load machine-global config from .kitsoki.yaml in the cwd. This
					// carries harness profiles (/provider /model parity with the web)
					// AND the implicit-root `root:` block. A missing file is not an
					// error; an invalid profile or root block fails fast here.
					webCfg, err := webconfig.Load(webconfig.DefaultConfigFile)
					if err != nil {
						return err
					}
					storyOptions, storyDiscoverErr := tuiStoryOptions(webCfg)
					storySwitch := func(story tui.StoryOption) {
						selectedStoryPath = story.Path
					}
					harnessProfiles, defaultProfile := harnessProfilesFromConfig(webCfg)
					bugPrivacyRuntime := bugPrivacyRuntimeConfig{
						AgentBackend:         resolveAgentBackend(agentBackend),
						ClaudeModel:          claudeModel,
						UseDefaultLiveLadder: strings.TrimSpace(harnessType) != "replay",
					}

					// Resolve the app definition. With a path arg, load it from disk
					// (the historical rung-2 path). With NO arg, synthesize the implicit
					// project root from .kitsoki.yaml `root:` (rung 0/1) — a dev-story
					// instance with no file on disk. See docs/stories/imports.md
					// "The blank root that grows".
					var (
						def                  *app.AppDef
						appPath              string
						reloader             func() (*app.AppDef, error)
						projectStartupNotice string
					)
					if len(args) == 1 {
						appPath = args[0]
						// loadAppWithEnv publishes KITSOKI_APP_DIR FIRST so the loader's
						// env-var validator can resolve `${KITSOKI_APP_DIR}` references
						// in cwd: and other env-expanded fields.
						def, err = loadAppWithEnv(appPath)
						if err != nil {
							return err
						}
					} else {
						repoRoot, rrErr := os.Getwd()
						if rrErr != nil {
							return fmt.Errorf("resolve working directory for implicit root: %w", rrErr)
						}
						rootSpec := webCfg.Root.RootSpec()
						def, err = app.SynthesizeRootWithResolver(rootSpec, repoRoot, buildImportResolver())
						if err != nil {
							return fmt.Errorf("synthesize implicit root: %w", err)
						}
						projectStartupNotice = projectUpgradeNoticeForRoot(repoRoot)
						// A synthesized root has no app.yaml to re-read on /reload, so
						// inject a reloader that re-reads .kitsoki.yaml and
						// re-synthesizes — a rung-1 overrides edit takes effect on the
						// same Reload + RerunOnEnter path a rung-2 file edit travels.
						reloader = func() (*app.AppDef, error) {
							cfg, cfgErr := webconfig.Load(webconfig.DefaultConfigFile)
							if cfgErr != nil {
								return nil, cfgErr
							}
							return app.SynthesizeRootWithResolver(cfg.Root.RootSpec(), repoRoot, buildImportResolver())
						}
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
						AppPath:           appPath,
						Def:               def,
						DBPath:            dbPath,
						ExecMode:          execMode,
						HarnessType:       harnessType,
						ClaudeModel:       claudeModel,
						AgentBackend:      resolveAgentBackend(agentBackend),
						HarnessProfiles:   harnessProfiles,
						DefaultProfile:    defaultProfile,
						HarnessLadder:     webCfg.HarnessLadder.ToHostLadderConfig(),
						AgentLaunchPolicy: agentLaunchPolicyFromConfig(webCfg),
						RecordingPath:     recordingPath,
						RecordPath:        recordPath,
						HostCassette:      hostCassette,
						PromptOverlay:     promptOverlay,
						RoomEnterSink:     roomEnterSink,
						Reloader:          reloader,
						Mining:            webCfg.Mining,
					})
					if err != nil {
						return err
					}
					defer rt.Close()

					// Re-bind the locals the rest of runCmd's TUI / resume code uses.
					s := rt.Store
					jw := rt.Journal
					jr := rt.JournalRead
					jobStore := rt.JobStore
					rawChatStore := rt.ChatStore
					orch := rt.Orch

					ctx := context.Background()
					bugFilingNotice := bugFilingAuthStartupNotice(ctx, ticketRepo)
					bugPrivacyNotice := bugPrivacyStartupNotice(webCfg, bugPrivacyRuntime, ticketRepo)
					runAsUserNotice := runAsUserStartupNotice(webCfg, runtime.GOOS)

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
						bugPrivacyResolver := bugPrivacyCheckerResolverFromConfig(webCfg, appPath, bugPrivacyRuntime)
						tuiOptions = append([]tui.RootModelOption{
							tui.WithJobStore(jobStore),
							tui.WithChatStore(rawChatStore),
							tui.WithJournalWriter(jw),
							tui.WithJournalReader(jr),
							tui.WithStorySelector(storyOptions, storyDiscoverErr, storySwitch),
							tui.WithTraceHistory(func() (store.History, error) { return s.LoadHistory(sid) }),
							tui.WithBugTicketRepo(ticketRepo),
							tui.WithBugPrivacyChecker(bugPrivacyResolver(orchestrator.ProfileSelection{})),
							tui.WithBugPrivacyCheckerResolver(bugPrivacyResolver),
						}, tuiOptions...)
						if projectStartupNotice != "" {
							tuiOptions = append(tuiOptions, tui.WithStartupNotice(projectStartupNotice))
						}
						if bugFilingNotice != "" {
							tuiOptions = append(tuiOptions, tui.WithStartupNotice(bugFilingNotice))
						}
						if runAsUserNotice != "" {
							tuiOptions = append(tuiOptions, tui.WithStartupNotice(runAsUserNotice))
						}
						if bugPrivacyNotice != "" {
							tuiOptions = append(tuiOptions, tui.WithStartupNotice(bugPrivacyNotice))
						}
						if tuiMetaTracePath != "" {
							tuiOptions = append(tuiOptions, tui.WithExternalTraceFile(tuiMetaTracePath))
						}
						if metaController := tuiMetaController(def, rawChatStore, harnessType); metaController != nil {
							tuiOptions = append(tuiOptions, tui.WithMetaController(metaController))
						}
						// Allocate the meta-mode stream sink up-front so the
						// model can hold a reference; bind it to the program
						// post-construction via sink.Attach(p) below.
						metaSink := tui.NewMetaStreamSink()
						tuiOptions = append(tuiOptions, tui.WithMetaStreamSink(metaSink))
						// Allocate the operator prompter up-front so a forwarded agent
						// question surfaces as an inline widget; bind it to the program
						// post-construction via prompter.Attach(p) below.
						operatorPrompter := tui.NewTUIOperatorPrompter()
						tuiOptions = append(tuiOptions, tui.WithOperatorPrompter(operatorPrompter))
						// Allocate the spatial prompter up-front so a request for a spatial
						// ambient surfaces an OSC 8 link to a transient `/point` window;
						// bind it post-construction via prompter.Attach(p) below.
						spatialPrompter := tui.NewTUISpatialPrompter()
						tuiOptions = append(tuiOptions, tui.WithSpatialPrompter(spatialPrompter))
						rootModel := tui.NewRootModel(orch, sid, appPath, effectiveInitialView, tuiOptions...)
						// Single-pane redesign: no alt-screen + no mouse capture.
						// Output prints into the terminal's normal scrollback so
						// the header scrolls off naturally as content grows
						// (Claude Code's model). The View() output is just the
						// bottom chrome — footer + prompt — which Bubble Tea
						// re-renders in place at the cursor row.

						// Suppress slog output during TUI operation to prevent log lines
						// from mixing with the queue indicator on the same terminal line.
						// Issue: agent runner emits slog records while TUI is rendering,
						// causing "2026-05-29 ... INFO ... ⏳ running…" on same line.
						oldLogger := slog.Default()
						suppressedLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
						slog.SetDefault(suppressedLogger)
						defer slog.SetDefault(oldLogger)

						p := tea.NewProgram(rootModel)
						metaSink.Attach(p)
						defer metaSink.Detach()
						operatorPrompter.Attach(p)
						defer operatorPrompter.Detach()
						spatialPrompter.Attach(p)
						defer spatialPrompter.Detach()
						roomEnterSink.Attach(p)
						defer roomEnterSink.Detach()
						detach := tui.AttachOrchestratorObserver(orch, p, sid)
						defer detach()
						restoreProcessOutput, captureErr := captureTUIProcessOutput(cmd, p)
						if captureErr != nil {
							return fmt.Errorf("isolate TUI process output: %w", captureErr)
						}
						defer restoreProcessOutput()

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
					bugPrivacyResolver := bugPrivacyCheckerResolverFromConfig(webCfg, appPath, bugPrivacyRuntime)
					tuiOptions = []tui.RootModelOption{
						tui.WithJobStore(jobStore),
						tui.WithChatStore(rawChatStore),
						tui.WithJournalWriter(jw),
						tui.WithJournalReader(jr),
						tui.WithInitialTypedView(initialTypedView, initialTypedEnv, initialTypedRR),
						tui.WithStorySelector(storyOptions, storyDiscoverErr, storySwitch),
						tui.WithTraceHistory(func() (store.History, error) { return s.LoadHistory(sid) }),
						tui.WithBugTicketRepo(ticketRepo),
						tui.WithBugPrivacyChecker(bugPrivacyResolver(orchestrator.ProfileSelection{})),
						tui.WithBugPrivacyCheckerResolver(bugPrivacyResolver),
					}
					if projectStartupNotice != "" {
						tuiOptions = append(tuiOptions, tui.WithStartupNotice(projectStartupNotice))
					}
					if bugFilingNotice != "" {
						tuiOptions = append(tuiOptions, tui.WithStartupNotice(bugFilingNotice))
					}
					if runAsUserNotice != "" {
						tuiOptions = append(tuiOptions, tui.WithStartupNotice(runAsUserNotice))
					}
					if bugPrivacyNotice != "" {
						tuiOptions = append(tuiOptions, tui.WithStartupNotice(bugPrivacyNotice))
					}
					if freshMetaTracePath != "" {
						tuiOptions = append(tuiOptions, tui.WithExternalTraceFile(freshMetaTracePath))
					}
					if metaController := tuiMetaController(def, rawChatStore, harnessType); metaController != nil {
						tuiOptions = append(tuiOptions, tui.WithMetaController(metaController))
					}
					// Allocate the meta-mode stream sink up-front so the
					// model can hold a reference; bind it to the program
					// post-construction via sink.Attach(p) below. This is
					// what lets the user see live agent progress (tool calls,
					// narration, retries) in the transcript while a meta-mode
					// Send is in flight, instead of a buffered spinner.
					metaSink := tui.NewMetaStreamSink()
					tuiOptions = append(tuiOptions, tui.WithMetaStreamSink(metaSink))
					// Allocate the operator prompter up-front so a forwarded agent
					// question surfaces as an inline widget; bind it post-construction
					// via prompter.Attach(p) below.
					operatorPrompter := tui.NewTUIOperatorPrompter()
					tuiOptions = append(tuiOptions, tui.WithOperatorPrompter(operatorPrompter))
					// Allocate the spatial prompter up-front so a request for a spatial
					// ambient surfaces an OSC 8 link to a transient `/point` window; bind
					// it post-construction via prompter.Attach(p) below.
					spatialPrompter := tui.NewTUISpatialPrompter()
					tuiOptions = append(tuiOptions, tui.WithSpatialPrompter(spatialPrompter))
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
					operatorPrompter.Attach(p)
					defer operatorPrompter.Detach()
					spatialPrompter.Attach(p)
					defer spatialPrompter.Detach()
					roomEnterSink.Attach(p)
					defer roomEnterSink.Detach()
					// Bridge orchestrator background-turn notifications into
					// the Bubble Tea message loop so the main transcript
					// re-renders when a background job's on_complete fires —
					// without this, the inbox badge ticks but the transcript
					// stays frozen until the next keystroke.
					detach := tui.AttachOrchestratorObserver(orch, p, sid)
					defer detach()
					restoreProcessOutput, captureErr := captureTUIProcessOutput(cmd, p)
					if captureErr != nil {
						return fmt.Errorf("isolate TUI process output: %w", captureErr)
					}
					defer restoreProcessOutput()
					_, err = p.Run()
					if selectedStoryPath != "" && err == nil {
						return nil
					}
					return err
				}
				if err := runOnce(runArgs); err != nil {
					return err
				}
				if selectedStoryPath == "" {
					return nil
				}
				runArgs = []string{selectedStoryPath}
				continueFlag = false
				continueID = ""
				continueKey = ""
				warpBasisPath = ""
			}
		},
	}

	cmd.Flags().StringVar(&harnessType, "harness", "",
		"harness type: claude|live|replay|recording (default: selected from --agent, then claude on PATH, then Anthropic credential)")
	cmd.Flags().StringVar(&claudeModel, "claude-model", "",
		fmt.Sprintf("model passed to claude -p --model (default: %s); use 'opus' for higher quality at higher cost", harness.DefaultClaudeModel))
	cmd.Flags().StringVar(&agentBackend, "agent", "",
		"coding-agent CLI backend for host.agent.* calls: claude|copilot|codex (default: claude, or $KITSOKI_AGENT)")
	cmd.Flags().StringVar(&recordingPath, "recording", "",
		"path to recording YAML file (required for --harness replay)")
	cmd.Flags().StringVar(&recordPath, "record", "",
		"path to output JSONL recording (for --harness recording)")
	cmd.Flags().StringVar(&hostCassette, "host-cassette", "",
		"host cassette backing host.* calls (deterministic, no LLM); combinable with --harness replay")
	cmd.Flags().StringVar(&dbPath, "db", "",
		"path to SQLite session database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")

	cmd.Flags().BoolVar(&continueFlag, "continue", false,
		"resume an existing session instead of starting a fresh one")
	cmd.Flags().StringVar(&continueID, "id", "",
		"resume a specific session by ID (requires --continue)")
	cmd.Flags().StringVar(&continueKey, "key", "",
		"resume a specific session by external key transport:thread (requires --continue)")

	cmd.Flags().StringVar(&promptOverlay, "prompt-overlay", "",
		"project prompt-overlay dir: its prompts shadow the story's and may {% extends \"@story/…\" %} to specialize without forking (see docs/stories/prompts.md)")
	cmd.Flags().StringVar(&execModeFlag, "mode", "staged",
		`execution mode: "staged" (stop at each decision gate for the operator) or "one-shot" (auto-advance, LLM/default deciders)`)
	cmd.Flags().StringVar(&warpBasisPath, "warp", "",
		"path to a warp-basis YAML (state + world overrides); applied as the first action after session create. Same file the TUI's /warp file:<path> loads. See stories/oregon-trail/scenarios/ for examples.")
	cmd.Flags().StringVar(&ticketRepo, "ticket-repo", "",
		"file TUI /bug reports as GitHub issues on this owner/repo with uploaded evidence; requires GitHub auth from `kitsoki gh-agent login`, `kitsoki gh-agent token`, or GH_TOKEN/GITHUB_TOKEN. Default empty value writes local artifact tickets under .artifacts/issues/bugs instead")

	return cmd
}

// captureTUIProcessOutput quarantines process-level stdout/stderr while Bubble
// Tea owns the terminal. tea.NewProgram must be constructed first so its
// renderer retains the real terminal writer; everything else is redirected to
// a pipe and returned to the model as managed terminalOutputMsg values.
func captureTUIProcessOutput(cmd *cobra.Command, program *tea.Program) (func(), error) {
	capture, err := tui.NewTerminalOutputCapture()
	if err != nil {
		return nil, err
	}

	priorStdout, priorStderr := os.Stdout, os.Stderr
	priorCmdOut, priorCmdErr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	capture.Attach(program)
	os.Stdout = capture.Writer()
	os.Stderr = capture.Writer()
	cmd.SetOut(capture.Writer())
	cmd.SetErr(capture.Writer())

	var once sync.Once
	return func() {
		once.Do(func() {
			os.Stdout = priorStdout
			os.Stderr = priorStderr
			cmd.SetOut(priorCmdOut)
			cmd.SetErr(priorCmdErr)
			_ = capture.Close()
		})
	}, nil
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
//  1. non-Claude agent backend   → use the CLI routing harness for that backend.
//  2. `claude` binary on PATH    → use ClaudeCLIHarness (no API key needed).
//  3. Anthropic credential found → use LiveHarness (direct SDK). See
//     resolveAnthropicCredential for the credential chain.
//  4. Otherwise                  → error; replay requires an explicit recording.
func autoSelectHarness(agentBackend string) (string, error) {
	agentBackend = strings.TrimSpace(agentBackend)
	if agentBackend != "" && agentBackend != "claude" {
		return "claude", nil
	}
	claude := false
	if _, err := exec.LookPath("claude"); err == nil {
		claude = true
	}
	cred := hasAnthropicCredential()
	if claude {
		return "claude", nil
	}
	if cred {
		return "live", nil
	}
	return "", errors.New(strings.TrimSpace(firstRunProviderHint(false, false)))
}

// firstRunProviderHint returns an actionable message when NO agent provider is
// configured (no `claude` binary on PATH and no Anthropic credential), so a
// fresh `kitsoki` run does not silently fall back to the replay harness. Returns
// "" when a provider exists. Change 0.4 (G1: "no silent replay fallback on
// first run").
func firstRunProviderHint(hasClaude, hasCred bool) string {
	if hasClaude || hasCred {
		return ""
	}
	return "kitsoki: no agent provider found for the default harness.\n" +
		"To run kitsoki live, configure one of:\n" +
		"  - install the `claude` CLI (Claude Code) so it is on your PATH, or\n" +
		"  - set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN (direct Anthropic SDK), or\n" +
		"  - select another agent backend, such as `--agent codex` or KITSOKI_AGENT=codex, or\n" +
		"  - select a harness profile in .kitsoki.local.yaml (see docs/guide/agents/harness-profiles.md).\n" +
		"To run deterministic replay instead, pass --harness replay --recording <recording.yaml>.\n"
}

func bugFilingAuthStartupNotice(ctx context.Context, ticketRepo string) string {
	repo := strings.TrimSpace(ticketRepo)
	if repo == "" {
		return ""
	}
	if host.GitHubWriteAuthStatus(ctx).Configured {
		return ""
	}
	return fmt.Sprintf("(warning: GitHub bug filing is unavailable for %s because auth is missing. Filing bugs is critical; run `kitsoki gh-agent login` or set GH_TOKEN/GITHUB_TOKEN.)", repo)
}

func runAsUserStartupNotice(cfg webconfig.WebConfig, goos string) string {
	warning := runAsUserSetupWarning(cfg, goos)
	if warning == nil {
		return ""
	}
	return fmt.Sprintf("(warning: %s. %s Command: `%s`.)", warning.Title, warning.Body, warning.ActionCommand)
}

// resolveAgentBackend resolves the agent backend selector with precedence
// flag → $KITSOKI_AGENT → "" (claude default). The runtime treats "" / "claude"
// identically (the default backend), so an empty result is fine.
func resolveAgentBackend(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	return os.Getenv("KITSOKI_AGENT")
}

// buildHarness constructs the appropriate harness based on the harness type flag.
// If harnessType is empty, autoSelectHarness() is called to pick one from the
// selected agent backend and available credentials.
// claudeModel is the model name for the ClaudeCLIHarness; pass "" to use the default.
func buildHarness(harnessType, claudeModel, agentBackend, recordingPath, recordPath string, def *app.AppDef) (harness.Harness, error) {
	return buildHarnessWithActiveProfile(harnessType, claudeModel, agentBackend, recordingPath, recordPath, def, host.ActiveProfile{})
}

func buildHarnessWithActiveProfile(harnessType, claudeModel, agentBackend, recordingPath, recordPath string, def *app.AppDef, activeProfile host.ActiveProfile) (harness.Harness, error) {
	if activeProfile.Provider.Model != "" {
		claudeModel = activeProfile.Provider.Model
	}
	if harnessType == "" {
		var err error
		harnessType, err = autoSelectHarness(agentBackend)
		if err != nil {
			return nil, err
		}
	}
	withProfile := func(ctx context.Context) context.Context {
		return host.WithActiveProfile(ctx, activeProfile)
	}

	switch harnessType {
	case "claude":
		// Intent routing reuses the claude-CLI harness shell even for the
		// copilot backend: it builds a claude-shaped invocation and the
		// runner's TranslateInvocation (installed via the copilot backend on
		// the Exec context) rewrites it onto copilot's flags. Point the harness
		// at the copilot binary and tag the Exec context so the one engine that
		// forks the subprocess uses the copilot backend.
		if agentBackend == "copilot" {
			copilotBin, err := exec.LookPath("copilot")
			if env := os.Getenv(host.CopilotBinEnv); env != "" {
				copilotBin, err = env, nil
			}
			if err != nil {
				return nil, fmt.Errorf("--agent copilot: %w", host.ErrAgentUnavailable)
			}
			copilotExec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
				return host.RunClaudeOneShotForHarness(host.WithAgentBackendNamed(withProfile(ctx), "copilot"), bin, args, stdin, workingDir)
			}
			return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{
				Model:         claudeModel,
				ClaudeBin:     copilotBin,
				Exec:          copilotExec,
				ValidatorTool: "kitsoki-validator-submit",
			})
		}
		if agentBackend == "codex" {
			codexBin, err := exec.LookPath("codex")
			if env := os.Getenv(host.CodexBinEnv); env != "" {
				codexBin, err = env, nil
			}
			if err != nil {
				return nil, fmt.Errorf("--agent codex: %w", host.ErrAgentUnavailable)
			}
			codexExec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
				return host.RunClaudeOneShotForHarness(host.WithAgentBackendNamed(withProfile(ctx), "codex"), bin, args, stdin, workingDir)
			}
			return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{
				Model:         claudeModel,
				ClaudeBin:     codexBin,
				Exec:          codexExec,
				ValidatorTool: host.CodexValidatorToolName("kitsoki-validator"),
			})
		}
		if agentBackend == "agy" {
			agyBin, err := exec.LookPath("agy")
			if env := os.Getenv(host.AgyBinEnv); env != "" {
				agyBin, err = env, nil
			}
			if err != nil {
				return nil, fmt.Errorf("--agent agy: %w", host.ErrAgentUnavailable)
			}
			agyExec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
				return host.RunClaudeOneShotForHarness(host.WithAgentBackendNamed(withProfile(ctx), "agy"), bin, args, stdin, workingDir)
			}
			return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{
				Model:         claudeModel,
				ClaudeBin:     agyBin,
				Exec:          agyExec,
				ValidatorTool: "mcp__kitsoki-validator__submit",
			})
		}
		claudeExec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
			return host.RunClaudeOneShotForHarness(withProfile(ctx), bin, args, stdin, workingDir)
		}
		return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{Model: claudeModel, Exec: claudeExec})

	case "replay":
		if recordingPath == "" {
			return nil, fmt.Errorf("--recording is required when --harness replay is set")
		}
		return harness.NewReplay(recordingPath)

	case "live":
		client, source, err := newLiveClientWithEnv(activeProfile.Provider.Env)
		if err != nil {
			return nil, err
		}
		slog.Debug("harness/live: credential resolved", "source", source)
		return harness.NewLive(&client, claudeModel, def)

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

// replayCmd is defined in replay.go (agent-split Phase 4).

func testCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run Mode 1 and Mode 2 tests for an app",
		Long: `Test sub-commands:
  kitsoki test flows         <app.yaml>   — Mode 2: deterministic flow tests (no LLM)
  kitsoki test flow-coverage <app.yaml>   — static flow fixture coverage ledger
  kitsoki test intents       <app.yaml>   — Mode 1: intent pass-rate tests (harness/recording, may use an LLM)
  kitsoki test routing       <app.yaml>   — Mode 0: no-LLM routing-tier fixture tests (semroute/deterministic only)

Fixture layout (defaults):
  <app-dir>/flows/*.yaml      — flow fixtures (run under 'test flows')
  <app-dir>/intents/*.yaml    — intent fixtures (run under 'test intents')
  <app-dir>/recording.yaml       — recording YAML (seeds replay/static harness)

See 'kitsoki docs llm-guide' §7 for fixture shape.`,
	}
	cmd.AddCommand(testFlowsCmd())
	cmd.AddCommand(testFlowCoverageCmd())
	cmd.AddCommand(testIntentsCmd())
	cmd.AddCommand(testRoutingCmd())
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
	var kitsDir string
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

			// Construct the MCP server (kit_call is enabled when --kits-dir
			// discovers one or more installed kits, S3b).
			kits, err := buildKitDispatcher(kitsDir)
			if err != nil {
				return fmt.Errorf("load installed kits from %q: %w", kitsDir, err)
			}
			srv := kitsokimcp.NewServer(m, s, def, mcpKitOption(kits))

			// Run until stdin closes or signal received.
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			fmt.Fprintf(os.Stderr, "kitsoki: serving app %q via MCP stdio\n", def.App.ID)
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to the SQLite session database (default: in-memory)")
	cmd.Flags().StringVar(&kitsDir, "kits-dir", "", "directory of installed kit.yaml roots (enables the kit_call MCP tool, S3b)")
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
// (e.g. JSON traces piped to a file). When stderr is not a terminal, skip the
// sequence so captured startup errors stay readable.
func restoreTerminal() {
	if !isatty(os.Stderr) {
		return
	}
	const seq = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1049l"
	_, _ = fmt.Fprint(os.Stderr, seq)
}
