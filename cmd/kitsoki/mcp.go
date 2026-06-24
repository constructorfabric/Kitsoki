package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/kitrepo"
	studio "kitsoki/internal/mcp/studio"
	rsserver "kitsoki/internal/runstatus/server"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
	"kitsoki/internal/webshot"
)

// studioImportResolver returns the import resolver used by `kitsoki mcp`.
// Without --stories-dir it is exactly the CLI resolver used by `kitsoki test
// flows`; with --stories-dir it treats that directory as an explicit
// @kitsoki/<name> story root before falling back to the normal embedded-library
// resolver.
func studioImportResolver(storiesDir string) app.ImportResolver {
	base := buildImportResolver()
	if storiesDir == "" {
		return base
	}
	return func(name, importerDir string, override bool) (string, error) {
		if override {
			candidate := filepath.Join(storiesDir, name, "app.yaml")
			if _, err := os.Stat(candidate); err != nil {
				return "", fmt.Errorf("--stories-dir=%s: story %q not found (looked for %s): %w",
					storiesDir, name, candidate, err)
			}
			return candidate, nil
		}
		return base(name, importerDir, override)
	}
}

// studioHarnessBuilder is the production studio harness seam. Replay mode
// delegates to studio.DefaultHarnessBuilder (a no-LLM ReplayHarness over the
// recording). Live mode loads the driven story's def for prompt context and
// constructs a direct-API LiveHarness from on-disk credentials — the same
// resolution `kitsoki drive --harness live` uses — so an MCP-driven
// session.new(harness:live) routes through a real LLM. The studio core keeps
// live OUT of its in-package default (DefaultHarnessBuilder refuses it); wiring
// it here makes "the MCP is the first-class LLM interface" hold without the CLI.
func studioHarnessBuilder(mode studio.HarnessMode, recordingPath, storyPath string) (harness.Harness, error) {
	if mode != studio.HarnessLive {
		return studio.DefaultHarnessBuilder(mode, recordingPath, storyPath)
	}
	if storyPath == "" {
		return nil, fmt.Errorf("studio: harness:live requires a story_path for prompt context")
	}
	// session.new accepts either a story directory or an app.yaml; loadAppWithEnv
	// wants the file, so resolve a directory to its conventional app.yaml entry
	// (matching the runtime's dir-capable app.Load).
	appPath := storyPath
	if fi, statErr := os.Stat(storyPath); statErr == nil && fi.IsDir() {
		appPath = filepath.Join(storyPath, "app.yaml")
	}
	def, err := loadAppWithEnv(appPath)
	if err != nil {
		return nil, fmt.Errorf("studio live harness: %w", err)
	}
	// Build the claude-CLI routing harness (the same one `kitsoki web` builds):
	// it authenticates via the claude subscription / CLI — no direct-API key — and
	// the per-session harness profile remaps host.agent dispatch each turn
	// (host_dispatch wraps the dispatch ctx WithActiveProfile). Free-text routing
	// is unused by explicit-intent (maker) driving, so a live session opens on
	// subscription auth alone, where the SDK direct-API path needed an ANTHROPIC_*
	// key the machine may not have. buildHarness lives in this package.
	h, err := buildHarness("claude", "", "", "", "", def)
	if err != nil {
		return nil, fmt.Errorf("studio live harness: %w", err)
	}
	return h, nil
}

// mcpCmd starts the kitsoki studio MCP server on stdio. Unlike `kitsoki serve`
// (which exposes one app's `transition` tool), `kitsoki mcp` exposes the studio
// facade: a server whose state is an authoring workspace plus 0..n driving
// sessions, defaulting every session to the no-LLM replay harness.
//
// This slice ships the server core (transport, handle model, tool registry) with
// a trivial studio.ping / studio.handles pair; the domain tools (story.* /
// session.* / render.*) land in later slices.
//
// Example (smoke test via shell):
//
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}' \
//	  | kitsoki mcp --stories-dir ./stories
func mcpCmd() *cobra.Command {
	var (
		storiesDir  string
		dbPath      string
		harnessType string
		workspace   string
		flowPath    string
		readOnly    bool
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the kitsoki studio MCP server on stdio",
		Long: `Start the kitsoki studio MCP server on stdin/stdout. An external coding
agent (Claude Code, Claude Desktop) attaches and authors a story, drives a
session, and sees the result through a single studio facade.

Unlike 'kitsoki serve' (one app's 'transition' tool), the studio server holds an
authoring workspace handle and 0..n driving-session handles, and defaults every
session to the no-LLM replay harness (--harness replay). Pass --harness live to
opt a session into a real LLM.

Tools: the studio.ping liveness probe and studio.handles lister (server core);
the deterministic story.read/write/validate/graph/test authoring tools; and the
session.new/attach/drive/submit/continue/inspect/trace driving tools,
render.tui/tui_png/web, and issue.create (file a GitHub issue via gh, bundling
rendered assets + a handle's trace/inspect). Driving defaults to harness:replay (no LLM); render.tui
and render.tui_png return the terminal Frame / PNG, while render.web screenshots
the current browser view for a live handle when the local web-shot helper and
Playwright dependencies are available.

Attach by adding to a client's .mcp.json (see 'kitsoki docs' once the studio
docs land):

  { "mcpServers": { "kitsoki": { "command": "kitsoki",
      "args": ["mcp", "--stories-dir", "<dir>"] } } }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbPath == "" {
				dbPath = defaultDBPath()
			}

			// Build the studio session with the live-capable production builder:
			// replay stays no-LLM (DefaultHarnessBuilder), and harness:live
			// resolves on-disk credentials to a direct-API LiveHarness so the MCP
			// can drive a real LLM with no CLI.
			sess := studio.NewStudioSession(studioHarnessBuilder)
			chatStore, chatCleanup, chatErr := openChatStore(dbPath)
			if chatErr != nil {
				return fmt.Errorf("mcp: open chat store: %w", chatErr)
			}
			defer chatCleanup()
			sess.SetChatStore(chatStore)
			if flowPath != "" {
				abs, aerr := filepath.Abs(flowPath)
				if aerr != nil {
					return fmt.Errorf("resolve --flow path: %w", aerr)
				}
				data, rerr := os.ReadFile(abs)
				if rerr != nil {
					return fmt.Errorf("read --flow %q: %w", flowPath, rerr)
				}
				var fixture testrunner.FlowFixture
				if uerr := yaml.Unmarshal(data, &fixture); uerr != nil {
					return fmt.Errorf("parse --flow %q: %w", flowPath, uerr)
				}
				if fixture.HostCassette != "" || fixture.StarlarkHTTPCassette != "" || fixture.StarlarkInspectCassette != "" {
					return fmt.Errorf("mcp --flow currently supports host_handlers stubs only; use a flow without host_cassette or starlark cassettes")
				}
				sess.SetHostRegistryConfigurer(func(reg *host.Registry) error {
					testrunner.RegisterHostStubs(reg, fixture.HostHandlers)
					return nil
				})
			}

			// Seed operator-declared harness profiles (synthetic, codex, …) from
			// the project webconfig so a session.new(profile:…) can route a live
			// session's agent dispatch through a named backend — the studio twin of
			// `kitsoki turn --profile`. Best-effort: a missing/invalid config leaves
			// the session on the legacy default-backend path rather than aborting
			// boot (a story.* / replay session needs no profiles).
			if webCfg, cfgErr := webconfig.Load(webconfig.DefaultConfigFile); cfgErr == nil {
				if profiles, defaultProfile := harnessProfilesFromConfig(webCfg); len(profiles) > 0 {
					sess.SetHarnessProfiles(profiles, defaultProfile)
				}
			}

			// Optionally bind an initial authoring workspace. Loading is
			// best-effort: a load/validation error is cached on the handle (so a
			// story.* tool can surface it) rather than aborting the server boot —
			// the agent may want to attach precisely to fix a broken story.
			if workspace != "" {
				def, loadErr := app.Load(workspace)
				if _, err := sess.OpenWorkspace(studio.OpenWorkspaceParams{
					Dir:     workspace,
					Def:     def,
					LoadErr: loadErr,
				}); err != nil {
					return fmt.Errorf("mcp: open workspace %q: %w", workspace, err)
				}
			}

			// Wire issue.create to file via gh (the operator's authenticated CLI)
			// and write rendered assets under the default artifacts dir. The
			// studio package stays exec/network-free; this is the only production
			// seam that shells out.
			srvOpts := []studio.ServerOption{
				studio.WithIssueFiler(ghIssueFiler),
				studio.WithImportResolver(studioImportResolver(storiesDir)),
			}
			if readOnly {
				srvOpts = append(srvOpts, studio.ReadOnly())
			}
			srv := studio.NewServer(sess, srvOpts...)
			srv.SetWebShot(mcpWebShotFunc(sess, ""))

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			fmt.Fprintf(os.Stderr, "kitsoki: studio MCP server on stdio (harness default %q, stories %q)\n",
				harnessType, storiesDir)
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&storiesDir, "stories-dir", "",
		"root for story.* workspace resolution (mirrors `kitsoki web`)")
	cmd.Flags().StringVar(&dbPath, "db", "",
		"path to the SQLite session database for driving handles (default: the shared kitsoki sessions.db)")
	cmd.Flags().StringVar(&harnessType, "harness", "replay",
		"default harness for driving sessions: replay|live (default replay → no LLM; per-session override on session.new)")
	cmd.Flags().StringVar(&workspace, "workspace", "",
		"optional initial authoring workspace (a story dir or app.yaml) bound as the workspace handle on boot")
	cmd.Flags().StringVar(&flowPath, "flow", "",
		"deterministic flow fixture whose host_handlers stub host.* calls for every driving session (no LLM)")
	cmd.Flags().BoolVar(&readOnly, "read-only", false,
		"omit the story-mutating tool (story.write); read + replay-driving tools stay available (the meta-mode Q&A surface)")
	return cmd
}

// mcpAttachEntry builds the .mcp.json mcpServers entry that drops this binary in
// as the "kitsoki" studio server. It mirrors the writeMCPConfigTempfile shape in
// internal/host/agent_helpers.go: {"mcpServers": {"kitsoki": {"command": ...,
// "args": ["mcp", "--stories-dir", ...]}}}. command is the kitsoki binary path;
// storiesDir is forwarded as --stories-dir (omitted when empty).
func mcpAttachEntry(command, storiesDir string) map[string]any {
	args := []any{"mcp"}
	if storiesDir != "" {
		args = append(args, "--stories-dir", storiesDir)
	}
	return map[string]any{
		"mcpServers": map[string]any{
			"kitsoki": map[string]any{
				"command": command,
				"args":    args,
			},
		},
	}
}

// mcpAttachJSON marshals the .mcp.json attach entry (mcpAttachEntry) to indented
// JSON — the snippet a user pastes into a client's config.
func mcpAttachJSON(command, storiesDir string) ([]byte, error) {
	return json.MarshalIndent(mcpAttachEntry(command, storiesDir), "", "  ")
}

type mcpWebShotOptions struct {
	RepoRoot      string
	Browser       webshot.BrowserInvoker
	Server        func(http.Handler) webshot.ServerProvider
	HealthTimeout time.Duration
}

func mcpWebShotFunc(sess *studio.StudioSession, repoRoot string) studio.WebShotFunc {
	return mcpWebShotFuncWithOptions(sess, mcpWebShotOptions{RepoRoot: repoRoot})
}

func mcpWebShotFuncWithOptions(sess *studio.StudioSession, opts mcpWebShotOptions) studio.WebShotFunc {
	return func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		wsSpec := spec.ToWebshotSpec()
		if wsSpec.SessionID == "" {
			return nil, fmt.Errorf("kitsoki mcp render.web currently supports live handles; use kitsoki web-shot with a no-LLM flow for story/state screenshots")
		}
		repoRoot := opts.RepoRoot
		if repoRoot == "" {
			repoRoot = os.Getenv(kitrepo.EnvVar)
		}
		if repoRoot == "" {
			repoRoot = kitrepo.Resolve()
		}
		if repoRoot == "" {
			return nil, fmt.Errorf("could not locate the kitsoki checkout for tools/runstatus/web-shot.ts; set %s", kitrepo.EnvVar)
		}
		helper := filepath.Join(repoRoot, "tools", "runstatus", "web-shot.ts")
		if _, err := os.Stat(helper); err != nil {
			return nil, fmt.Errorf("web-shot helper not found at %s: %w", helper, err)
		}

		provider := studio.NewRunstatusProvider(sess)
		handler := rsserver.NewMulti(provider).Handler()
		serverFactory := opts.Server
		if serverFactory == nil {
			serverFactory = func(h http.Handler) webshot.ServerProvider {
				return &webshot.HandlerServer{Handler: h, HealthTimeout: opts.HealthTimeout}
			}
		}
		browser := opts.Browser
		if browser == nil {
			browser = &webshot.NodeInvoker{RepoRoot: repoRoot}
		}
		return webshot.Shot(ctx, wsSpec, webshot.Options{
			Server:        serverFactory(handler),
			Browser:       browser,
			HealthTimeout: opts.HealthTimeout,
		})
	}
}
