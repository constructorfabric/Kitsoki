// web.go — implements `kitsoki web`, the multi-story interactive browser surface.
//
// Where `kitsoki status serve` is a read-only observer that tails a JSONL
// trace another process writes, `kitsoki web` hosts LIVE orchestrators in the
// same process and serves the runstatus SPA/RPC/SSE surface against them. This
// is the multi-story evolution of docs/proposals/web-ui.md: one process serves
// a story browser (the SPA home screen), and the operator starts as many live
// sessions as they like — each its own in-process orchestrator. The read side
// lets the browser observe a session render and trace; the write RPCs let the
// browser DRIVE it (turn / submit / continue / offpath) and read the current
// room (session.view); the lifecycle RPCs (stories.list/rescan, session.new/
// reload) let the home screen discover stories and spin sessions up.
//
// web.go no longer takes a positional <app.yaml>; it starts story-less. It
// resolves the story directories (flags > .kitsoki.yaml > ./stories), builds a
// SessionRegistry over a session-invariant runtimeBase (so the deterministic
// --flow / --host-cassette no-LLM posture applies to EVERY session a Playwright
// demo opens), seeds the catalogue with a rescan, and serves the
// session-routing server (server.NewMulti). Per-session construction —
// buildSessionRuntime, NewSession, on_enter, Reload — lives in the registry
// (registry.go / runtime.go), not here.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
)

func webCmd() *cobra.Command {
	var (
		addr             string
		harnessType      string
		claudeModel      string
		agentBackend     string
		recordingPath    string
		recordPath       string
		dbPath           string
		execModeFlag     string
		flowPath         string
		hostCassette     string
		configPath       string
		storyDirs        []string
		actor            string
		ticketRepo       string
		agentEvidenceDir string
		improveProvider  string
		maxSessions      int
		kitsDir          string
	)

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve the multi-story interactive browser UI (live sessions)",
		Long: `Discover stories and serve the runstatus web UI over HTTP. The home screen
lists the discovered stories and any live sessions; the operator starts a fresh
session from a story or opens an existing one.

Unlike 'kitsoki status serve' — which tails a JSONL trace another process
writes, read-only — 'kitsoki web' hosts the orchestrators itself, so the browser
observes (and drives) sessions running in this process. Sessions are in-memory
only and die with the process.

Story directories resolve with the precedence flags > .kitsoki.yaml > ./stories:

  kitsoki web                              # walk ./stories (or .kitsoki.yaml's story_dirs)
  kitsoki web --stories-dir stories --stories-dir testdata/apps
  kitsoki web --config ./my-kitsoki.yaml --addr 127.0.0.1:7777

Deterministic (no-LLM) posture for UI development and Playwright tests — applies
to EVERY session started from the home screen:

  kitsoki web --stories-dir stories/prd --flow stories/prd/flows/happy_path.yaml

With --flow, the flow fixture's host_handlers stub back every host.* call and
the harness is nil — the browser drives each session by submitting intents
(runstatus.session.submit) explicitly, with no LLM. --host-cassette backs host.*
calls from a recorded cassette and is combinable with --flow.

The runstatus SPA must be bundled into the binary (run 'make build', which runs
'pnpm build' under tools/runstatus/); otherwise the page reports the UI as
unbuilt. Assumes a trusted localhost / internal network; there is no
authentication.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the execution mode (execution-modes proposal). Staged by
			// default, matching the TUI. Applies to every session.
			var execMode orchestrator.ExecutionMode
			switch execModeFlag {
			case "staged":
				execMode = orchestrator.ExecStaged
			case "one-shot", "oneshot":
				execMode = orchestrator.ExecOneShot
			default:
				return fmt.Errorf("--mode %q is invalid (want \"staged\" or \"one-shot\")", execModeFlag)
			}

			// ── Load the flow fixture (deterministic posture) if requested ──
			// The fixture / FlowFilePath go into runtimeBase, so EVERY session
			// the registry spins up inherits the nil-harness, cassette/stub
			// posture — the no-LLM end-to-end Playwright demo depends on this.
			var (
				fixture      *testrunner.FlowFixture
				flowFilePath string
			)
			// liveCassette layers a host cassette over the LIVE-harness posture
			// (--harness replay/recording) instead of the nil-harness flow
			// posture. When the operator asks for a real interpreter harness AND
			// a host cassette, free-text routing must stay live (replay drives
			// it) while specific host.* calls — the agent off-ramp's
			// host.agent.converse — are stubbed by the cassette. A flow fixture
			// would force a nil harness and reject every free-text turn, so we do
			// NOT build one in that case; the cassette rides runtimeBase.HostCassette.
			liveHarness := harnessType == "replay" || harnessType == "recording" || harnessType == "live" || harnessType == "claude"
			var (
				liveCassette string
				seedFixture  *testrunner.FlowFixture
			)

			if flowPath != "" && liveHarness {
				// --flow WITH a live harness (e.g. --harness replay --recording):
				// the flow is NOT the nil-harness driver here — the recording
				// routes free text and the host cassette backs host.* calls. We
				// parse the flow ONLY for its initial_state / initial_world to seed
				// the session onto its mid-graph start, leaving the live harness in
				// place. host_handlers are ignored (the cassette owns host.*).
				abs, aerr := filepath.Abs(flowPath)
				if aerr != nil {
					return fmt.Errorf("resolve --flow path: %w", aerr)
				}
				data, rerr := os.ReadFile(abs)
				if rerr != nil {
					return fmt.Errorf("read --flow %q: %w", flowPath, rerr)
				}
				var f testrunner.FlowFixture
				if uerr := yaml.Unmarshal(data, &f); uerr != nil {
					return fmt.Errorf("parse --flow %q: %w", flowPath, uerr)
				}
				seedFixture = &f
				if hostCassette != "" {
					if abs2, aerr2 := filepath.Abs(hostCassette); aerr2 == nil {
						liveCassette = abs2
					} else {
						liveCassette = hostCassette
					}
				}
			} else if flowPath != "" {
				abs, aerr := filepath.Abs(flowPath)
				if aerr != nil {
					return fmt.Errorf("resolve --flow path: %w", aerr)
				}
				flowFilePath = abs
				data, rerr := os.ReadFile(abs)
				if rerr != nil {
					return fmt.Errorf("read --flow %q: %w", flowPath, rerr)
				}
				var f testrunner.FlowFixture
				if uerr := yaml.Unmarshal(data, &f); uerr != nil {
					return fmt.Errorf("parse --flow %q: %w", flowPath, uerr)
				}
				fixture = &f
			} else if hostCassette != "" && liveHarness {
				// --host-cassette WITH an interpreter harness: keep the harness
				// live (it routes free text) and layer the cassette over the real
				// host registry. No flow fixture (a flow forces a nil harness).
				if abs, aerr := filepath.Abs(hostCassette); aerr == nil {
					liveCassette = abs
				} else {
					liveCassette = hostCassette
				}
			} else if hostCassette != "" {
				// --host-cassette without --flow and without an interpreter
				// harness: a minimal deterministic fixture carrying only the
				// cassette, so the nil-harness posture applies and cassette
				// episodes back every host.* call (intents submitted explicitly).
				fixture = &testrunner.FlowFixture{}
			}

			// --host-cassette overrides / sets the FLOW fixture's cassette path
			// (flow posture only). The path is resolved relative to the flow file
			// (when --flow is set) or the cwd (when standalone) inside
			// buildSessionRuntime. The live-harness path uses liveCassette above.
			if hostCassette != "" && fixture != nil {
				fixture.HostCassette = hostCassette
				if flowFilePath == "" {
					if abs, aerr := filepath.Abs(hostCassette); aerr == nil {
						fixture.HostCassette = abs
					}
				}
			}

			if dbPath == "" {
				dbPath = defaultDBPath()
			}
			if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
				return fmt.Errorf("create db directory: %w", err)
			}

			// ── Story discovery config (flags > .kitsoki.yaml > ./stories) ──
			// Loading the config (root-story world-key resolution) and the
			// story catalogue below both run per-story loader lint (deprecated
			// agent fields, missing on_enter bind fallbacks, etc.) via
			// slog.Warn. Across dozens of transitively-imported stories that's
			// hundreds of startup lines with no bearing on `web` actually
			// starting — real failures still surface as returned errors, not
			// through this logger. Suppress the default logger for the whole
			// load+scan phase, matching the same precedent `run` uses to keep
			// TUI/output rendering clean (main.go's oldLogger/io.Discard
			// swaps). slog.SetDefault also redirects the stdlib "log"
			// package's writer/flags as a side effect when swapping to a
			// non-default handler (see log/slog's SetDefault doc) and does
			// NOT undo that on restore (the original default is specially
			// exempted) — so log.Writer()/log.Flags() are saved and restored
			// explicitly too, or the stdlib log package would stay silenced
			// for the rest of the process.
			restoreDefaultLogging := suppressDefaultLogging()
			loggingRestored := false
			restoreLogging := func() {
				if loggingRestored {
					return
				}
				restoreDefaultLogging()
				loggingRestored = true
			}
			defer restoreLogging()
			cfg, err := webconfig.Load(configPath)
			if err != nil {
				return err
			}
			dirs := webconfig.Resolve(storyDirs, cfg)
			harnessProfiles, defaultProfile := harnessProfilesFromConfig(cfg)
			bugPrivacyRuntime := bugPrivacyRuntimeConfig{
				AgentBackend:         resolveAgentBackend(agentBackend),
				ClaudeModel:          claudeModel,
				UseDefaultLiveLadder: fixture == nil,
			}

			// ── Operator identity ────────────────────────────────────────────
			// An explicit --actor wins; otherwise fall back to the configured
			// git user so browser-driven turns on a dev machine record a real
			// principal (and stories with an author ACL can start) without the
			// operator having to pass the flag. The X-Kitsoki-Actor header and
			// an explicit actor RPC param still override this per turn.
			if actor == "" {
				if u := gitOutput("config", "user.name"); u != "" {
					actor = u
					fmt.Fprintf(cmd.ErrOrStderr(), "kitsoki: no --actor; using git user.name %q as operator identity\n", actor)
				}
			}

			// ── Session-invariant construction posture every session inherits ──
			base := runtimeBase{
				DBPath:            dbPath,
				ExecMode:          execMode,
				HarnessType:       harnessType,
				ClaudeModel:       claudeModel,
				AgentBackend:      resolveAgentBackend(agentBackend),
				HarnessProfiles:   harnessProfiles,
				DefaultProfile:    defaultProfile,
				HarnessLadder:     cfg.HarnessLadder.ToHostLadderConfig(),
				AgentLaunchPolicy: agentLaunchPolicyFromConfig(cfg),
				RecordingPath:     recordingPath,
				RecordPath:        recordPath,
				Flow:              fixture,
				FlowFilePath:      flowFilePath,
				SeedFixture:       seedFixture,
				HostCassette:      liveCassette,
				DefaultActor:      actor,
				Mining:            cfg.Mining,
				ConnectIDEFromEnv: true,
			}

			// ── Registry + initial story catalogue ──────────────────────────
			registry := NewRegistry(cfg, dirs, base)
			if maxSessions > 0 {
				registry.SetMaxSessions(maxSessions)
			}
			defer registry.Close()
			stories, err := registry.Rescan()
			restoreLogging()
			if err != nil {
				return fmt.Errorf("discover stories: %w", err)
			}

			// ── Serve (session-routing) ──────────────────────────────────────
			// Web-filed bugs (runstatus.bug.report) land under the same repo the
			// stories live in: the git toplevel of the first resolved story dir,
			// falling back to that dir, then $PWD. This mirrors `kitsoki bug
			// --target story` (which writes under $PWD) while preferring the repo
			// root so the .artifacts/issues/bugs/ pile is shared across story subdirs.
			// --kits-dir enables the kit.<kit>.<iface>.<op> JSON-RPC fallback +
			// runstatus.kits.list (S3b); empty (the default) leaves both
			// reporting "no kits installed" — most instances have none yet.
			kits, err := buildKitDispatcher(kitsDir)
			if err != nil {
				return fmt.Errorf("load installed kits from %q: %w", kitsDir, err)
			}
			bugRoot := resolveWebBugRoot(dirs)
			bugPrivacyResolver := bugPrivacyCheckerResolverFromConfig(cfg, bugRoot, bugPrivacyRuntime)
			srv := server.NewMulti(registry,
				server.WithDefaultActor(actor),
				server.WithBugRoot(bugRoot),
				server.WithWorkflowRoot(bugRoot),
				server.WithTicketRepo(ticketRepo),
				server.WithAgentEvidenceDir(agentEvidenceDir),
				server.WithImproveTicketProvider(improveProvider),
				server.WithBugPrivacyChecker(bugPrivacyResolver(orchestrator.ProfileSelection{})),
				server.WithBugPrivacyCheckerResolver(bugPrivacyServerResolver(bugPrivacyResolver)),
				server.WithSetupWarnings(setupWarningsFromRuntimeConfig(cfg, runtime.GOOS, bugPrivacyRuntime, ticketRepo != "")),
				server.WithProjectOnboarded(projectOnboardedForRoot(bugRoot)),
				server.WithKits(kits),
			)
			// Attach the cross-session notification relay sink so each new
			// session's background-turn fan-out reaches the runstatus.notification
			// SSE feed. Set before any session.new call.
			registry.SetNotifier(srv)
			httpSrv := &http.Server{
				Addr:    addr,
				Handler: srv.Handler(),
				// ReadHeaderTimeout guards against Slowloris. Keep short.
				ReadHeaderTimeout: 10 * time.Second,
				// IdleTimeout recycles keep-alive connections that go quiet.
				// No WriteTimeout: LLM agent calls (turn/submit/continue) can
				// block for 30-120s; a WriteTimeout would kill those responses
				// mid-flight. SSE streams (/rpc/events) also require no write
				// deadline — they hold the connection open indefinitely.
				IdleTimeout: 120 * time.Second,
			}

			serveCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			go func() {
				<-serveCtx.Done()
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutCancel()
				_ = httpSrv.Shutdown(shutCtx)
			}()

			fmt.Fprintf(cmd.ErrOrStderr(), "kitsoki: web UI (%d stories across %d dir(s)) on http://%s\n", len(stories), len(dirs), addr)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", webconfig.DefaultConfigFile, "path to the web config file (story_dirs)")
	cmd.Flags().StringArrayVar(&storyDirs, "stories-dir", nil, "story directory to walk for app.yaml (repeatable; overrides .kitsoki.yaml story_dirs)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7777", "HTTP listen address")
	cmd.Flags().StringVar(&harnessType, "harness", "", "harness: claude | live | replay | recording (default: auto-select; ignored with --flow)")
	cmd.Flags().StringVar(&claudeModel, "claude-model", "", "claude model when --harness=claude (e.g. opus, sonnet)")
	cmd.Flags().StringVar(&agentBackend, "agent", "", "coding-agent CLI backend for host.agent.* calls: claude|copilot (default: claude, or $KITSOKI_AGENT)")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "path to recording YAML (for --harness replay)")
	cmd.Flags().StringVar(&recordPath, "record", "", "path to output JSONL recording (for --harness recording)")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite session store path (default: nearest .kitsoki/sessions.db)")
	cmd.Flags().StringVar(&execModeFlag, "mode", "staged", "execution mode: staged | one-shot")
	cmd.Flags().StringVar(&flowPath, "flow", "", "drive every session deterministically from a flow fixture (no LLM; host_handlers stub host.* calls, intents are submitted explicitly)")
	cmd.Flags().StringVar(&hostCassette, "host-cassette", "", "host cassette file backing host.* calls (deterministic, no LLM); combinable with --flow")
	cmd.Flags().StringVar(&actor, "actor", "", "operator identity recorded on browser-driven turns as slots.author (default: git config user.name; the X-Kitsoki-Actor header and an explicit actor RPC param override it)")
	cmd.Flags().StringVar(&ticketRepo, "ticket-repo", "", "file Report-bug reports as GitHub issues on this owner/repo (evidence saved under .artifacts/bug-reports for developer review); requires GitHub auth from `kitsoki gh-agent login`, `kitsoki gh-agent token`, or GH_TOKEN/GITHUB_TOKEN. Default empty value writes local artifact tickets under .artifacts/issues/bugs instead")
	cmd.Flags().StringVar(&agentEvidenceDir, "agent-evidence-dir", "", "after a GitHub bug filing, also deposit the scrubbed rrweb+HAR here under <DeckID>/ so the kitsoki gh-agent can auto-build a hosted deck without re-downloading; point at the agent's --evidence-dir")
	cmd.Flags().StringVar(&improveProvider, "improve-ticket-provider", "", "ticket_provider/v1 .star script used by meta-improve reports after writing a local evidence bundle; useful for private story ticket systems. Empty uses --ticket-repo when set, otherwise local artifacts")
	cmd.Flags().IntVar(&maxSessions, "max-sessions", 0, "cap on concurrently live in-memory sessions before idle eviction kicks in (default: $KITSOKI_WEB_MAX_SESSIONS or a generous built-in default; 0 means use that default)")
	cmd.Flags().StringVar(&kitsDir, "kits-dir", "", "directory of installed kit.yaml roots (enables kit.<kit>.<iface>.<op> + runstatus.kits.list, S3b)")

	return cmd
}

func suppressDefaultLogging() func() {
	oldLogger := slog.Default()
	oldLogOutput := log.Writer()
	oldLogFlags := log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return func() {
		slog.SetDefault(oldLogger)
		log.SetOutput(oldLogOutput)
		log.SetFlags(oldLogFlags)
	}
}

// resolveWebBugRoot picks the repo root under which web-filed bug reports
// (runstatus.bug.report) write .artifacts/issues/bugs/. It walks up from the first
// resolved story dir to the nearest ancestor containing a .git entry; if none
// is found it returns that story dir, and if there are no story dirs it falls
// back to the process cwd. Empty means "let the server resolve per request".
func resolveWebBugRoot(dirs []string) string {
	if len(dirs) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return ""
	}
	start := dirs[0]
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return start
}
