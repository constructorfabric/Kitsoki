package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/kitrepo"
	"kitsoki/internal/kittrial"
	"kitsoki/internal/kitverify"
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
				// storiesDir is a project-story root, not a repo-wide override.
				// Absence from it is not an error — delegate to base so that
				// kitdev overrides, $KITSOKI_REPO, and the embedded library can
				// satisfy @kitsoki/* imports that storiesDir does not cover.
				return base(name, importerDir, override)
			}
			return candidate, nil
		}
		return base(name, importerDir, override)
	}
}

// studioKitTrialDeps assembles the cmd-layer seams the studio kit.* tools
// need (studio.WithKitTrialDeps): the same resolver pair, project-checks
// sweep, and extends resolver `kitsoki kit trial`/`kit update` wire for
// the CLI verbs — so a lifecycle driven over MCP is the same computation
// as one driven from the shell. checkProjectUpgrade stays in this package
// (it is the CLI's project-tools sweep); only a closure crosses the seam.
func studioKitTrialDeps() studio.KitTrialDeps {
	return studio.KitTrialDeps{
		BaseResolver:  buildImportResolver(),
		PlainResolver: buildPlainImportResolver(),
		ProjectChecks: func(ctx context.Context, projectRoot string, resolver app.ImportResolver) ([]kittrial.Check, error) {
			rep, err := checkProjectUpgrade(ctx, projectUpgradeOptions{Target: projectRoot, Resolver: resolver})
			if err != nil {
				return nil, err
			}
			checks := make([]kittrial.Check, 0, len(rep.Checks))
			for _, c := range rep.Checks {
				checks = append(checks, kittrial.Check{ID: c.ID, Status: c.Status, Detail: c.Detail})
			}
			return checks, nil
		},
		Extends: func(ctx context.Context, projectRoot string) kitverify.ExtendsResolver {
			return lockfileExtendsResolver(ctx, projectRoot)
		},
		ResolveEntry: resolveKitEntry,
	}
}

// mcpOperatingSystemPaths resolves the trusted managed-workspace assets used
// by the Studio operating-system plane. A downstream project intentionally
// keeps its own process working directory for project config and story paths,
// but it does not carry Kitsoki's scripts/dev-workspace.sh. The root command
// canonicalizes --kitsoki-repo / $KITSOKI_REPO into the environment before the
// MCP command runs, so a configured source checkout owns both the lifecycle
// script and its .capsules/workspaces root; kitsokiRepo carries that value in.
// KITSOKI_MCP_REPO_ROOT is a narrower override that takes precedence when set:
// Codex currently starts stdio MCP servers from its own working directory even
// when an MCP configuration includes cwd, and that env var keeps the
// managed-workspace guard anchored to the intended checkout in that case.
// Empty keeps the source-checkout development fallback relative to the
// current working directory.
func mcpOperatingSystemPaths(kitsokiRepo string) (workspaceRoot, workspaceScript string) {
	root := strings.TrimSpace(os.Getenv("KITSOKI_MCP_REPO_ROOT"))
	if root == "" {
		root = strings.TrimSpace(kitsokiRepo)
	}
	if root == "" {
		return filepath.Join(".capsules", "workspaces"), filepath.Join("scripts", "dev-workspace.sh")
	}
	return filepath.Join(root, ".capsules", "workspaces"), filepath.Join(root, "scripts", "dev-workspace.sh")
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
		storiesDir              string
		dbPath                  string
		harnessType             string
		workspace               string
		flowPath                string
		corpusRuntimeConfigPath string
		issueSink               string
		readOnly                bool
		operatingProfile        string
		graphCatalogs           []string
		graphScopes             []string
		graphSteward            bool
		graphActor              string
		graphFeedbackSink       string
		graphWriteVia           string
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
render.tui/tui_png/web, and issue.create (file a local artifact ticket by
default, or a GitHub issue via the native ticket provider when requested,
bundling rendered assets + a handle's trace/inspect). Driving defaults to harness:replay (no LLM); render.tui
and render.tui_png return the terminal Frame / PNG, while render.web screenshots
the current browser view for a live handle when the local web-shot helper and
Playwright dependencies are available.

Attach by adding to a client's .mcp.json (see 'kitsoki docs' once the studio
docs land):

  { "mcpServers": { "kitsoki": { "command": "kitsoki",
      "args": ["mcp", "--stories-dir", "<dir>"] } } }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			defer func() {
				if shouldWriteMCPStartupError(err) {
					writeMCPStartupError(err, dbPath, storiesDir, workspace, flowPath)
				}
			}()
			if dbPath == "" {
				dbPath = defaultDBPath()
			}

			// Export the semantic-routing default so the studio's per-session
			// orchestrators match the CLI: exact deterministic first, then the
			// selected harness/model unless the operator opts the semantic stack
			// back in.
			if enabled, ok := semanticRoutingOverride(); ok {
				_ = os.Setenv("KITSOKI_SEMANTIC_ROUTING", strconv.FormatBool(enabled))
			} else if _, hadEnv := os.LookupEnv("KITSOKI_SEMANTIC_ROUTING"); !hadEnv {
				_ = os.Setenv("KITSOKI_SEMANTIC_ROUTING", "false")
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
			if flowPath != "" && corpusRuntimeConfigPath != "" {
				return fmt.Errorf("mcp: --flow cannot be combined with --corpus-runtime-config; flow stubs are not evaluation proof")
			}
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
			if corpusRuntimeConfigPath != "" {
				configure, configErr := loadCorpusRuntimeConfigurer(corpusRuntimeConfigPath, nil)
				if configErr != nil {
					return fmt.Errorf("mcp: load --corpus-runtime-config: %w", configErr)
				}
				sess.SetHostRegistryConfigurer(configure)
			}

			var studioBugPrivacyChecker bugprivacy.Checker
			// Seed operator-declared harness profiles and launch policy from the
			// project webconfig. A missing config contributes nothing; a present
			// but invalid config is an operator error, and for launch policy must
			// not silently drop a guard.
			webCfg, cfgErr := webconfig.Load(webconfig.DefaultConfigFile)
			if cfgErr != nil {
				return fmt.Errorf("mcp: load %s: %w", webconfig.DefaultConfigFile, cfgErr)
			}
			if profiles, defaultProfile := harnessProfilesFromConfig(webCfg); len(profiles) > 0 {
				sess.SetHarnessProfiles(profiles, defaultProfile)
			}
			if policy := agentLaunchPolicyFromConfig(webCfg); policy.Enabled {
				sess.SetAgentLaunchPolicy(policy)
			}
			studioBugPrivacyChecker = bugPrivacyCheckerFromConfig(webCfg, "")

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

			// The operating-system graph is a server-held authority graph. Strict
			// is the default; legacy and escape are explicit compatibility paths.
			workspaceRoot, workspaceScript := mcpOperatingSystemPaths(os.Getenv(kitrepo.EnvVar))
			operatingServices, osErr := studio.NewOperatingSystemServices(
				studio.StudioOperatingProfile(operatingProfile),
				workspaceRoot,
				workspaceScript,
			)
			if osErr != nil {
				return fmt.Errorf("mcp: configure operating-system profile %q: %w", operatingProfile, osErr)
			}

			// Wire issue.create to file via host.gh.ticket.create and write
			// rendered assets under the default artifacts dir. The default sink is
			// local-artifact so local dogfood does not burn GitHub issues; GitHub
			// I/O remains available through the injected filer seam.
			srvOpts := []studio.ServerOption{
				studio.WithIssueFiler(ghIssueFiler),
				studio.WithIssueSink(issueSink),
				studio.WithImportResolver(studioImportResolver(storiesDir)),
				studio.WithKitTrialDeps(studioKitTrialDeps()),
				studio.WithOperatingSystemServices(studio.StudioOperatingProfile(operatingProfile), operatingServices),
				studio.WithGraphCatalogs(graphCatalogs),
				studio.WithGraphScopes(graphScopes),
				studio.WithGraphSteward(graphSteward),
				studio.WithGraphActor(graphActor),
				studio.WithGraphFeedbackSink(graphFeedbackSink),
				studio.WithGraphWriteVia(graphWriteVia),
				studio.WithGraphIssueFiler(ghGraphIssueFiler),
			}
			if studioBugPrivacyChecker != nil {
				srvOpts = append(srvOpts, studio.WithBugPrivacyChecker(studioBugPrivacyChecker))
			}
			if readOnly {
				srvOpts = append(srvOpts, studio.ReadOnly())
			}
			srv := studio.NewServer(sess, srvOpts...)
			srv.SetWebShotResult(mcpWebShotResultFunc(sess, ""))
			srv.SetWebAct(mcpWebActFunc(sess, ""))

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
	cmd.Flags().StringVar(&corpusRuntimeConfigPath, "corpus-runtime-config", "",
		"local corpus-runtime/v1 YAML that installs local proof and durable receipt handlers (requires Bubblewrap network isolation)")
	cmd.Flags().StringVar(&issueSink, "issue-sink", studio.IssueSinkLocalArtifact,
		"default issue.create sink: local-artifact writes .artifacts/issues/bugs; github files through the native GitHub issue provider")
	cmd.Flags().BoolVar(&readOnly, "read-only", false,
		"omit the story-mutating tool (story.write); read + replay-driving tools stay available (the meta-mode Q&A surface)")
	cmd.Flags().StringVar(&operatingProfile, "operating-profile", string(studio.DefaultStudioOperatingProfile),
		"Studio operating-system profile: strict (default), legacy (explicit compatibility), or escape (audited exception)")
	cmd.Flags().StringArrayVar(&graphCatalogs, "catalog", nil,
		"[alias=]path to a bound catalog for the mounted graph.*/feedback.* tool family; repeatable, first is default (mirrors `kitsoki mcp-graph --catalog`; omit to leave the family catalog-less, degrading every call to NO_CATALOG)")
	cmd.Flags().StringArrayVar(&graphScopes, "graph-scope", nil,
		"[alias=]path to a scope-spec YAML baking a deterministic catalog subset into the mounted graph tool family (mirrors `kitsoki mcp-graph --scope`; a malformed spec fails the mount closed, never unscoped)")
	cmd.Flags().BoolVar(&graphSteward, "graph-steward", false,
		"Grant graph.authorize and live graph.apply on the studio-mounted graph tool family (default: propose-only, per the graph-mcp plan's studio-second-door gate).")
	cmd.Flags().StringVar(&graphActor, "graph-actor", "",
		"actor name stamped on studio-mounted graph write-tool calls (mirrors `kitsoki mcp-graph --actor`)")
	cmd.Flags().StringVar(&graphFeedbackSink, "graph-feedback-sink", "",
		"one of: local, catalog, github — sink for the studio-mounted feedback.report (mirrors `kitsoki mcp-graph --feedback-sink`; default local)")
	cmd.Flags().StringVar(&graphWriteVia, "graph-write-via", "",
		"one of: auto, direct, capsule — write routing for the studio-mounted graph write family (mirrors `kitsoki mcp-graph --write-via`; default auto: consult the catalog repo's .kitsoki/project-profile.yaml graph.write_via, else direct)")
	return cmd
}

func shouldWriteMCPStartupError(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled)
}

func writeMCPStartupError(err error, dbPath, storiesDir, workspace, flowPath string) {
	if err == nil {
		return
	}
	const dir = ".artifacts/logs"
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		fmt.Fprintf(os.Stderr, "kitsoki: mcp startup failed: %v (also failed to create %s: %v)\n", err, dir, mkErr)
		return
	}
	now := time.Now().UTC()
	body := fmt.Sprintf(
		"timestamp: %s\nerror: %v\ncwd: %s\ndb: %s\nstories_dir: %s\nworkspace: %s\nflow: %s\n",
		now.Format(time.RFC3339Nano),
		err,
		mcpGetwd(),
		dbPath,
		storiesDir,
		workspace,
		flowPath,
	)
	name := filepath.Join(dir, "mcp-startup-"+now.Format("20060102-150405")+".log")
	_ = os.WriteFile(name, []byte(body), 0o644)
	latest := filepath.Join(dir, "mcp-startup-latest.log")
	_ = os.WriteFile(latest, []byte(body), 0o644)
	fmt.Fprintf(os.Stderr, "kitsoki: mcp startup failed; wrote %s: %v\n", latest, err)
}

func mcpGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "(unknown: " + err.Error() + ")"
	}
	return wd
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
	resultFn := mcpWebShotResultFuncWithOptions(sess, mcpWebShotOptions{RepoRoot: repoRoot})
	return func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		res, err := resultFn(ctx, spec)
		return res.PNG, err
	}
}

func mcpWebShotResultFunc(sess *studio.StudioSession, repoRoot string) studio.WebShotResultFunc {
	return mcpWebShotResultFuncWithOptions(sess, mcpWebShotOptions{RepoRoot: repoRoot})
}

func mcpWebShotFuncWithOptions(sess *studio.StudioSession, opts mcpWebShotOptions) studio.WebShotFunc {
	resultFn := mcpWebShotResultFuncWithOptions(sess, opts)
	return func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		res, err := resultFn(ctx, spec)
		return res.PNG, err
	}
}

func mcpWebShotResultFuncWithOptions(sess *studio.StudioSession, opts mcpWebShotOptions) studio.WebShotResultFunc {
	return func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		wsSpec := spec.ToWebshotSpec()
		if wsSpec.SessionID == "" {
			return studio.WebShotResult{}, fmt.Errorf("kitsoki mcp render.web currently supports live handles; use kitsoki web-shot with a no-LLM flow for story/state screenshots")
		}
		repoRoot := opts.RepoRoot
		if repoRoot == "" {
			repoRoot = os.Getenv(kitrepo.EnvVar)
		}
		if repoRoot == "" {
			repoRoot = kitrepo.Resolve()
		}
		if repoRoot == "" {
			return studio.WebShotResult{}, fmt.Errorf("could not locate the kitsoki checkout for tools/runstatus/web-shot.ts; set %s", kitrepo.EnvVar)
		}
		helper := filepath.Join(repoRoot, "tools", "runstatus", "web-shot.ts")
		if _, err := os.Stat(helper); err != nil {
			return studio.WebShotResult{}, fmt.Errorf("web-shot helper not found at %s: %w", helper, err)
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
		res, err := webshot.ShotWithSemantic(ctx, wsSpec, webshot.Options{
			Server:        serverFactory(handler),
			Browser:       browser,
			HealthTimeout: opts.HealthTimeout,
		})
		return studio.WebShotResult{PNG: res.PNG, SemanticJSON: res.SemanticJSON, RRWebJSON: res.RRWebJSON}, err
	}
}

func mcpWebActFunc(sess *studio.StudioSession, repoRoot string) studio.WebActFunc {
	return mcpWebActFuncWithOptions(sess, mcpWebShotOptions{RepoRoot: repoRoot})
}

func mcpWebActFuncWithOptions(sess *studio.StudioSession, opts mcpWebShotOptions) studio.WebActFunc {
	return func(ctx context.Context, spec studio.WebRenderSpec, action studio.WebActionSpec) (studio.WebShotResult, error) {
		wsSpec := spec.ToWebshotSpec()
		if wsSpec.SessionID == "" {
			return studio.WebShotResult{}, fmt.Errorf("kitsoki mcp visual.act currently supports live handles")
		}
		repoRoot := opts.RepoRoot
		if repoRoot == "" {
			repoRoot = os.Getenv(kitrepo.EnvVar)
		}
		if repoRoot == "" {
			repoRoot = kitrepo.Resolve()
		}
		if repoRoot == "" {
			return studio.WebShotResult{}, fmt.Errorf("could not locate the kitsoki checkout for tools/runstatus/web-shot.ts; set %s", kitrepo.EnvVar)
		}
		helper := filepath.Join(repoRoot, "tools", "runstatus", "web-shot.ts")
		if _, err := os.Stat(helper); err != nil {
			return studio.WebShotResult{}, fmt.Errorf("web-shot helper not found at %s: %w", helper, err)
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
		var point *webshot.Point
		if action.Point != nil {
			point = &webshot.Point{X: action.Point.X, Y: action.Point.Y}
		}
		res, err := webshot.ActWithSemantic(ctx, wsSpec, webshot.Action{
			Kind:         action.Kind,
			ActionHandle: action.ActionHandle,
			Point:        point,
			Button:       action.Button,
			Modifiers:    action.Modifiers,
		}, webshot.Options{
			Server:        serverFactory(handler),
			Browser:       browser,
			HealthTimeout: opts.HealthTimeout,
		})
		return studio.WebShotResult{PNG: res.PNG, SemanticJSON: res.SemanticJSON, RRWebJSON: res.RRWebJSON}, err
	}
}
