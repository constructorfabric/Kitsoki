package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"kitsoki/internal/app"
	"kitsoki/internal/baseskills"
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/webconfig"
)

type agentLaunchOptions struct {
	AppPath                 string
	AgentFile               string
	AgentTemplate           string
	ConfigPath              string
	AgentName               string
	Profile                 string
	Backend                 string
	Mode                    string
	Model                   string
	Effort                  string
	WorkingDir              string
	Task                    string
	TaskFile                string
	PermissionMode          string
	CodeactCapabilitiesJSON string
	CodeactCapabilitiesFile string
	AddDirs                 []string
	Env                     []string
	RawArgs                 []string
	Exec                    bool
	Interactive             bool
	RawInteractive          bool
}

type agentLaunchPlan struct {
	App          string                    `json:"app,omitempty"`
	AgentFile    string                    `json:"agent_file,omitempty"`
	Agent        string                    `json:"agent,omitempty"`
	Profile      string                    `json:"profile,omitempty"`
	Backend      string                    `json:"backend"`
	Mode         string                    `json:"mode,omitempty"`
	Binary       string                    `json:"binary"`
	WorkingDir   string                    `json:"working_dir"`
	Model        string                    `json:"model,omitempty"`
	Effort       string                    `json:"effort,omitempty"`
	Tools        []string                  `json:"tools,omitempty"`
	Env          map[string]string         `json:"env,omitempty"`
	Command      []string                  `json:"command"`
	Stdin        string                    `json:"stdin,omitempty"`
	RunAsUser    string                    `json:"run_as_user,omitempty"`
	DryRun       bool                      `json:"dry_run"`
	Interactive  bool                      `json:"interactive,omitempty"`
	FutureNotes  []string                  `json:"future_notes,omitempty"`
	LaunchPolicy *host.AgentLaunchDecision `json:"launch_policy,omitempty"`
	// ProfileResolution is the secret-free, local catalog evidence used to
	// construct this dry-run. It lets callers prove that a requested model and
	// effort came from the selected profile rather than an operator-authored
	// receipt. Env values deliberately never leave providerEnv.
	ProfileResolution *agentLaunchProfileResolution `json:"profile_resolution,omitempty"`

	providerEnv map[string]string
	claudeArgs  []string
	cleanups    []func()
}

type agentLaunchProfileResolution struct {
	Name    string                     `json:"name,omitempty"`
	Backend string                     `json:"backend,omitempty"`
	Model   string                     `json:"model,omitempty"`
	Models  []string                   `json:"models,omitempty"`
	Effort  string                     `json:"effort,omitempty"`
	Efforts []string                   `json:"efforts,omitempty"`
	Auth    agentLaunchAuthReadiness   `json:"auth"`
	Quota   *agentLaunchQuotaReadiness `json:"quota,omitempty"`
}

type agentLaunchAuthReadiness struct {
	// Status only describes local configuration. It never claims that a remote
	// provider accepted credentials; that would require a provider call.
	Status          string   `json:"status"`
	EnvironmentKeys []string `json:"environment_keys,omitempty"`
}

// agentLaunchQuotaReadiness deliberately omits the local state path. The
// preflight needs only the configured throttle shape, not filesystem details.
type agentLaunchQuotaReadiness struct {
	Window          string `json:"window,omitempty"`
	TokensPerWindow int64  `json:"tokens_per_window,omitempty"`
	MaxConcurrent   int    `json:"max_concurrent,omitempty"`
	ReserveTokens   int64  `json:"reserve_tokens,omitempty"`
	LeaseTimeout    string `json:"lease_timeout,omitempty"`
}

func launchProfileResolution(name string, profile orchestrator.HarnessProfile) *agentLaunchProfileResolution {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	envKeys := make([]string, 0, len(profile.Env))
	for key := range profile.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	auth := agentLaunchAuthReadiness{Status: "ambient-unverified"}
	if len(envKeys) > 0 {
		auth = agentLaunchAuthReadiness{Status: "configured-unverified", EnvironmentKeys: envKeys}
	}
	resolution := &agentLaunchProfileResolution{
		Name: name, Backend: profile.Backend, Model: profile.Model,
		Models: append([]string(nil), profile.Models...), Effort: profile.Effort,
		Efforts: append([]string(nil), profile.Efforts...), Auth: auth,
	}
	if profile.Quota != (host.QuotaControl{}) {
		resolution.Quota = &agentLaunchQuotaReadiness{
			Window: profile.Quota.Window, TokensPerWindow: profile.Quota.TokensPerWindow,
			MaxConcurrent: profile.Quota.MaxConcurrent, ReserveTokens: profile.Quota.ReserveTokens,
			LeaseTimeout: profile.Quota.LeaseTimeout,
		}
	}
	return resolution
}

type standaloneCodexAgent struct {
	Name                        string
	Description                 string
	DeveloperInstructions       string
	DeveloperInstructionsAppend string
	Extends                     string
	Model                       string
	Effort                      string
	SandboxMode                 string
	MCPServers                  map[string]any
}

type builtInAgentFrontmatter struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Model       string        `yaml:"model"`
	Effort      string        `yaml:"effort"`
	Tools       agentToolList `yaml:"tools"`
}

// agentToolList accepts both frontmatter spellings of a tool list: the YAML
// sequence form and the Claude Code convention of one comma-separated scalar
// (`tools: Bash, Read`), which the embedded agent library is authored in.
type agentToolList []string

func (t *agentToolList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var joined string
		if err := value.Decode(&joined); err != nil {
			return err
		}
		*t = nil
		for _, part := range strings.Split(joined, ",") {
			if part = strings.TrimSpace(part); part != "" {
				*t = append(*t, part)
			}
		}
		return nil
	default:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*t = list
		return nil
	}
}

// materializeBuiltInAgentLibrary is a seam for the embedded agent library. It
// keeps freestanding launch independent of .codex/.kitsoki installation while
// allowing unit tests to supply a tiny deterministic library.
var materializeBuiltInAgentLibrary = baseskills.Materialize

const (
	codexBypassApprovalsAndSandboxFlag = "--dangerously-bypass-approvals-and-sandbox"

	launchModeCodeact                    = "codeact"
	launchCodeactMCPServerName           = "kitsoki-codeact"
	launchCodeactMCPToolName             = "mcp__kitsoki-codeact__codeact_eval"
	launchCodeactDefaultCapabilitiesJSON = `{"fs":true,"vcs":"read"}`
	launchCodexShellToolFeature          = "shell_tool"
	launchCodexAppsFeature               = "apps"
	launchStudioMCPDriverAgentName       = "kitsoki-mcp-driver"
)

func agentLaunchCmd() *cobra.Command {
	var opts agentLaunchOptions
	cmd := &cobra.Command{
		Use:   "launch [--agent <name>|--raw --interactive] [--app <app.yaml>] [--task <text>|--task-file <path>]",
		Short: "Launch a Claude/Codex CLI from an agent definition",
		Long: `Resolve an agent definition plus an optional harness profile and turn it
into a concrete Claude/Codex task-agent launch.

When --app is set, the agent comes from that story's top-level agents: block.
When --app is omitted, the agent is resolved as a freestanding Codex agent from
an optional template overlay or the embedded Kitsoki agent library (or --agent-file).
Freestanding Codex agents may
declare [mcp_servers.*] blocks; launch attaches them exactly like a Claude
--mcp-config, then the Codex backend translates them to codex -c overrides.

Use --raw --interactive to start a normal interactive backend CLI in a working
directory with no app, no agent file, and no Kitsoki replacement system prompt.

Use --mode codeact for a task-backed agent whose only code-action surface is
the kitsoki-codeact MCP server. CodeAct mode removes shell access through the
selected backend's hard controls: Claude allowed/disallowed tools or Codex
--disable shell_tool.

By default this prints a redacted JSON launch plan for task-backed launches.
Freestanding Codex launch with no task opens Codex interactively.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := buildAgentLaunchPlan(opts)
			if err != nil {
				return err
			}
			for _, cleanup := range plan.cleanups {
				defer cleanup()
			}
			if !opts.Exec && !plan.Interactive {
				plan.DryRun = true
				return writeAgentLaunchPlan(cmd.OutOrStdout(), plan)
			}
			ctx := context.Background()
			if len(plan.providerEnv) > 0 {
				ctx = host.WithAgentProviderEnv(ctx, plan.providerEnv)
			}
			ctx = host.WithAgentBackendNamed(ctx, plan.Backend)
			return runAgentLaunchPlan(ctx, cmd, plan)
		},
	}

	cmd.Flags().StringVar(&opts.AppPath, "app", "", "optional story app.yaml whose top-level agents: block declares --agent")
	cmd.Flags().StringVar(&opts.AgentFile, "agent-file", "", "freestanding agent definition file; project overrides and the embedded agent library are used when omitted")
	cmd.Flags().StringVar(&opts.AgentTemplate, "agent-template", "", "optional TOML overlay applied to an embedded agent and rendered only for this launch")
	cmd.Flags().StringVar(&opts.ConfigPath, "config", webconfig.DefaultConfigFile, "kitsoki config file for harness profiles")
	cmd.Flags().StringVar(&opts.AgentName, "agent", "", "agent name to launch")
	cmd.Flags().StringVar(&opts.Profile, "profile", "", "harness profile name; defaults to config default_profile when set")
	cmd.Flags().StringVar(&opts.Backend, "backend", "", "override backend: claude|codex|copilot")
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "launch mode: normal|codeact")
	cmd.Flags().StringVar(&opts.Model, "model", "", "override model for this task")
	cmd.Flags().StringVar(&opts.Effort, "effort", "", "override reasoning effort for this task")
	cmd.Flags().StringVar(&opts.WorkingDir, "working-dir", "", "override agent cwd; story mode defaults to agent cwd then app directory; freestanding mode defaults to the current directory")
	cmd.Flags().StringVar(&opts.Task, "task", "", "task instructions")
	cmd.Flags().StringVar(&opts.TaskFile, "task-file", "", "file containing task instructions")
	cmd.Flags().StringVar(&opts.PermissionMode, "permission-mode", "", "permission mode: ask|bypassPermissions|denyAll")
	cmd.Flags().StringVar(&opts.CodeactCapabilitiesJSON, "codeact-capabilities-json", "", "CodeAct mode Starlark capability ceiling as JSON; defaults to working-directory read/write plus read-only git probes")
	cmd.Flags().StringVar(&opts.CodeactCapabilitiesFile, "codeact-capabilities-file", "", "file containing the CodeAct mode Starlark capability ceiling")
	cmd.Flags().StringArrayVar(&opts.AddDirs, "add-dir", nil, "additional directory made available to the launched agent")
	cmd.Flags().StringArrayVar(&opts.Env, "env", nil, "extra environment override KEY=VALUE; values are redacted from dry-run output")
	cmd.Flags().StringArrayVar(&opts.RawArgs, "raw-arg", nil, "raw backend argument; only for launcher shims used with --raw --interactive")
	_ = cmd.Flags().MarkHidden("raw-arg")
	cmd.Flags().BoolVar(&opts.Exec, "exec", false, "actually run a task-backed external CLI; task-backed default is a no-provider dry run")
	cmd.Flags().BoolVar(&opts.Interactive, "interactive", false, "force an interactive Codex session instead of one-shot codex exec; implied when freestanding launch has no task")
	cmd.Flags().BoolVar(&opts.RawInteractive, "raw", false, "with --interactive, launch the normal backend CLI without app/agent prompt synthesis")

	return cmd
}

func buildAgentLaunchPlan(opts agentLaunchOptions) (agentLaunchPlan, error) {
	mode, err := normalizeAgentLaunchMode(opts.Mode)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	opts.Mode = mode
	if opts.Mode != launchModeCodeact && (strings.TrimSpace(opts.CodeactCapabilitiesJSON) != "" || strings.TrimSpace(opts.CodeactCapabilitiesFile) != "") {
		return agentLaunchPlan{}, fmt.Errorf("--codeact-capabilities-json/--codeact-capabilities-file require --mode codeact")
	}
	if strings.TrimSpace(opts.Task) != "" && strings.TrimSpace(opts.TaskFile) != "" {
		return agentLaunchPlan{}, fmt.Errorf("use only one of --task or --task-file")
	}
	if opts.RawInteractive {
		if opts.Mode == launchModeCodeact {
			return agentLaunchPlan{}, fmt.Errorf("--mode codeact does not support --raw interactive launch because it cannot prove Bash is absent")
		}
		opts.Interactive = true
		return buildRawInteractiveLaunchPlan(opts)
	}
	if strings.TrimSpace(opts.AgentName) == "" {
		return agentLaunchPlan{}, fmt.Errorf("--agent is required unless --raw --interactive is set")
	}
	if opts.Interactive && strings.TrimSpace(opts.AppPath) != "" {
		return agentLaunchPlan{}, fmt.Errorf("--interactive is only supported for freestanding Codex agents; omit --app")
	}
	if strings.TrimSpace(opts.AppPath) == "" {
		return buildStandaloneAgentLaunchPlan(opts)
	}
	def, err := loadAppWithEnv(opts.AppPath)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	decl, ok := def.Agents[opts.AgentName]
	if !ok || decl == nil {
		return agentLaunchPlan{}, fmt.Errorf("app %s has no agent %q", opts.AppPath, opts.AgentName)
	}
	launchCfg, err := loadLaunchConfig(opts.ConfigPath)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	profiles := launchCfg.Profiles
	defaultProfile := launchCfg.DefaultProfile
	profileName := firstLaunchNonEmpty(opts.Profile, defaultProfile)
	profile, hasProfile := profiles[profileName]
	if profileName != "" && !hasProfile {
		return agentLaunchPlan{}, fmt.Errorf("unknown harness profile %q", profileName)
	}

	task, err := readLaunchTask(opts)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	appDir := filepath.Dir(opts.AppPath)
	workingDir, err := resolveLaunchWorkingDir(firstLaunchNonEmpty(opts.WorkingDir, decl.Cwd, "."), appDir)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	launchDecision, err := checkLaunchPolicy(launchCfg.AgentLaunchPolicy, "agent.launch", opts.AgentName, workingDir)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	providerBackend := backendForAgentProvider(def, decl.Provider)
	backend := firstLaunchNonEmpty(opts.Backend, profile.Backend, providerBackend, "claude")
	profileBackendOverride := opts.Backend
	if opts.Mode == launchModeCodeact {
		if err := validateCodeactLaunchOptions(opts); err != nil {
			return agentLaunchPlan{}, err
		}
		if shouldDefaultCodeactToCodex(opts, profile, providerBackend) {
			backend = "codex"
			profileBackendOverride = "codex"
		}
	}
	if _, ok := host.ResolveAgentBackendName(backend); !ok && backend != "claude" {
		return agentLaunchPlan{}, fmt.Errorf("unknown backend %q", backend)
	}
	if opts.Mode == launchModeCodeact && !codeactBackendSupportsHardNoShell(backend) {
		return agentLaunchPlan{}, fmt.Errorf("--mode codeact requires backend claude or codex because backend %q cannot hard-remove shell access", backend)
	}
	profileName, profile, err = launchProfileForBackend(profileName, profile, backend, opts.Profile, profileBackendOverride)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	providerModel, providerEffort := providerDefaultsForAgent(def, decl.Provider)
	if opts.Mode == launchModeCodeact && providerBackend != "" && launchBackendName(providerBackend) != launchBackendName(backend) {
		providerModel, providerEffort = "", ""
	}
	modelInput := firstLaunchNonEmpty(opts.Model, profile.Model, decl.Model, providerModel)
	if opts.Mode == launchModeCodeact {
		var modelErr error
		modelInput, modelErr = codeactLaunchModel(backend, modelInput, opts.Model)
		if modelErr != nil {
			return agentLaunchPlan{}, modelErr
		}
	}
	model := launchModelForBackend(backend, modelInput)
	effort := firstLaunchNonEmpty(opts.Effort, profile.Effort, decl.Effort, providerEffort)
	providerEnv := map[string]string{}
	for k, v := range providerEnvForAgent(def, decl.Provider) {
		providerEnv[k] = v
	}
	for k, v := range profile.Env {
		providerEnv[k] = v
	}
	extraEnv, err := parseLaunchEnv(opts.Env)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	for k, v := range extraEnv {
		providerEnv[k] = v
	}

	bin, err := resolveLaunchBin(backend, opts.Exec)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	bin, runAsUser, err := resolveDelegatedLaunchBin(backend, bin, launchCfg.AgentUserDelegation, opts.Exec)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	launchDecl := *decl
	permissionMode := opts.PermissionMode
	planTools := append([]string(nil), launchDecl.Tools...)
	if opts.Mode == launchModeCodeact {
		server, cfgErr := buildLaunchCodeactMCPServer(opts, workingDir)
		if cfgErr != nil {
			return agentLaunchPlan{}, cfgErr
		}
		launchDecl.SystemPrompt = appendCodeactLaunchSystemPrompt(launchDecl.SystemPrompt)
		launchDecl.Tools = []string{launchCodeactMCPToolName}
		launchDecl.MCP = &app.AgentMCPDecl{
			Servers: map[string]any{launchCodeactMCPServerName: server},
			Tools:   []string{launchCodeactMCPToolName},
		}
		permissionMode = "denyAll"
		planTools = append([]string(nil), launchDecl.Tools...)
	}
	cliArgs := buildLaunchClaudeArgs(&launchDecl, model, effort, permissionMode, opts.AddDirs)
	cliArgs = appendCodeactBackendArgs(cliArgs, opts.Mode, backend)
	// A story agent that declares no native tools and only MCP servers is an
	// MCP-only contract. Codex cannot express Claude's per-tool allowlist, but
	// it can remove its shell surface while retaining the declared MCP servers.
	// Keep this generic rather than special-casing individual drivers (for
	// example, the graph driver hosted by another repository).
	mcpOnlyStoryAgent := opts.Mode != launchModeCodeact && len(launchDecl.Tools) == 0 &&
		launchDecl.MCP != nil && len(launchDecl.MCP.Servers) > 0
	cliArgs = appendCodexShellToolDisableArg(cliArgs, backend, mcpOnlyStoryAgent)
	var cleanups []func()
	if launchDecl.MCP != nil && len(launchDecl.MCP.Servers) > 0 {
		mcpConfigPath, cleanup, cfgErr := writeLaunchMCPConfigTempfile(launchDecl.MCP.Servers, "kitsoki-agent-launch-mcp")
		if cfgErr != nil {
			return agentLaunchPlan{}, cfgErr
		}
		cleanups = append(cleanups, cleanup)
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}
	planCtx := context.Background()
	if len(providerEnv) > 0 {
		planCtx = host.WithAgentProviderEnv(planCtx, providerEnv)
	}
	inv := host.TranslateAgentInvocationForBackend(planCtx, backend, cliArgs, task, workingDir)
	if inv.Cleanup != nil {
		cleanups = append(cleanups, inv.Cleanup)
	}
	command := append([]string{bin}, inv.Args...)
	return agentLaunchPlan{
		App:               opts.AppPath,
		Agent:             opts.AgentName,
		Profile:           profileName,
		Backend:           backend,
		Mode:              opts.Mode,
		Binary:            bin,
		WorkingDir:        workingDir,
		Model:             model,
		Effort:            effort,
		Tools:             planTools,
		Env:               redactEnv(providerEnv),
		Command:           command,
		Stdin:             inv.Stdin,
		RunAsUser:         runAsUser,
		providerEnv:       providerEnv,
		claudeArgs:        cliArgs,
		cleanups:          cleanups,
		LaunchPolicy:      launchDecision,
		FutureNotes:       launchFutureNotes(opts.Mode),
		ProfileResolution: launchProfileResolution(profileName, profile),
	}, nil
}

func buildStandaloneAgentLaunchPlan(opts agentLaunchOptions) (agentLaunchPlan, error) {
	agentPath, renderedCleanup, err := resolveStandaloneAgentFile(opts.AgentName, opts.AgentFile, opts.AgentTemplate)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	cleanupRendered := true
	defer func() {
		if cleanupRendered && renderedCleanup != nil {
			renderedCleanup()
		}
	}()
	agent, err := loadStandaloneCodexAgent(agentPath)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	if agent.Name != "" && agent.Name != opts.AgentName {
		return agentLaunchPlan{}, fmt.Errorf("agent file %s declares name %q, not %q", agentPath, agent.Name, opts.AgentName)
	}

	launchCfg, err := loadLaunchConfig(opts.ConfigPath)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	profiles := launchCfg.Profiles
	defaultProfile := launchCfg.DefaultProfile
	profileName := firstLaunchNonEmpty(opts.Profile, defaultProfile)
	profile, hasProfile := profiles[profileName]
	if profileName != "" && !hasProfile {
		return agentLaunchPlan{}, fmt.Errorf("unknown harness profile %q", profileName)
	}
	task, err := readLaunchTask(opts)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	interactive := opts.Interactive || strings.TrimSpace(task) == ""
	workingDir, err := resolveStandaloneLaunchWorkingDir(opts.WorkingDir)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	launchDecision, err := checkLaunchPolicy(launchCfg.AgentLaunchPolicy, "agent.launch", opts.AgentName, workingDir)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	backend := firstLaunchNonEmpty(opts.Backend, profile.Backend, "codex")
	profileBackendOverride := opts.Backend
	if opts.Mode == launchModeCodeact {
		if err := validateCodeactLaunchOptions(opts); err != nil {
			return agentLaunchPlan{}, err
		}
		if shouldDefaultCodeactToCodex(opts, profile, "") {
			backend = "codex"
			profileBackendOverride = "codex"
		}
	}
	if _, ok := host.ResolveAgentBackendName(backend); !ok && backend != "claude" {
		return agentLaunchPlan{}, fmt.Errorf("unknown backend %q", backend)
	}
	if opts.Mode == launchModeCodeact && !codeactBackendSupportsHardNoShell(backend) {
		return agentLaunchPlan{}, fmt.Errorf("--mode codeact requires backend claude or codex because backend %q cannot hard-remove shell access", backend)
	}
	profileName, profile, err = launchProfileForBackend(profileName, profile, backend, opts.Profile, profileBackendOverride)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	modelInput := firstLaunchNonEmpty(opts.Model, profile.Model, agent.Model)
	if opts.Mode == launchModeCodeact {
		var modelErr error
		modelInput, modelErr = codeactLaunchModel(backend, modelInput, opts.Model)
		if modelErr != nil {
			return agentLaunchPlan{}, modelErr
		}
	}
	model := launchModelForBackend(backend, modelInput)
	effort := firstLaunchNonEmpty(opts.Effort, profile.Effort, agent.Effort)
	providerEnv := map[string]string{}
	for k, v := range profile.Env {
		providerEnv[k] = v
	}
	extraEnv, err := parseLaunchEnv(opts.Env)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	for k, v := range extraEnv {
		providerEnv[k] = v
	}

	requireInstalled := opts.Exec || interactive
	bin, err := resolveLaunchBin(backend, requireInstalled)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	bin, runAsUser, err := resolveDelegatedLaunchBin(backend, bin, launchCfg.AgentUserDelegation, requireInstalled)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	if interactive && backend != "codex" {
		return agentLaunchPlan{}, fmt.Errorf("interactive freestanding launch currently supports --backend codex, got %q", backend)
	}
	decl := &app.AgentDecl{
		SystemPrompt: agent.DeveloperInstructions,
		Model:        model,
		Effort:       effort,
	}
	permissionMode := opts.PermissionMode
	planTools := append([]string(nil), decl.Tools...)
	if opts.Mode == launchModeCodeact {
		server, cfgErr := buildLaunchCodeactMCPServer(opts, workingDir)
		if cfgErr != nil {
			return agentLaunchPlan{}, cfgErr
		}
		decl.SystemPrompt = appendCodeactLaunchSystemPrompt(decl.SystemPrompt)
		decl.Tools = []string{launchCodeactMCPToolName}
		decl.MCP = &app.AgentMCPDecl{
			Servers: map[string]any{launchCodeactMCPServerName: server},
			Tools:   []string{launchCodeactMCPToolName},
		}
		permissionMode = "denyAll"
		planTools = append([]string(nil), decl.Tools...)
	}
	disableCodexShell := shouldDisableStandaloneCodexShell(opts.Mode, backend, opts.AgentName, agent)
	var cleanups []func()
	if renderedCleanup != nil {
		cleanups = append(cleanups, renderedCleanup)
	}
	mcpServers := agent.MCPServers
	if opts.Mode == launchModeCodeact {
		mcpServers = decl.MCP.Servers
	}
	disableCodexApps := shouldDisableCodexAppsForMCPServers(backend, mcpServers)
	cliArgs := buildLaunchClaudeArgs(decl, model, effort, permissionMode, opts.AddDirs)
	cliArgs = appendCodexShellToolDisableArg(cliArgs, backend, disableCodexShell)
	if len(mcpServers) > 0 {
		mcpConfigPath, cleanup, cfgErr := writeLaunchMCPConfigTempfile(mcpServers, "kitsoki-agent-launch-mcp")
		if cfgErr != nil {
			return agentLaunchPlan{}, cfgErr
		}
		cleanups = append(cleanups, cleanup)
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}
	planCtx := context.Background()
	if len(providerEnv) > 0 {
		planCtx = host.WithAgentProviderEnv(planCtx, providerEnv)
	}
	if interactive {
		prompt := composeLaunchPrompt(decl.SystemPrompt, task)
		extraArgs := appendCodexShellToolDisableArg(nil, backend, disableCodexShell)
		extraArgs = appendCodexAppsDisableArg(extraArgs, backend, disableCodexApps)
		command := append([]string{bin}, buildInteractiveCodexArgs(model, effort, workingDir, opts.AddDirs, mcpServers, extraArgs, prompt)...)
		futureNotes := []string{
			"Interactive Codex launch uses top-level `codex`, not `codex exec`, so it opens the TUI with the freestanding agent instructions as the initial prompt.",
		}
		futureNotes = append(futureNotes, standaloneLaunchFutureNotes(opts.Mode, disableCodexShell)...)
		plan := agentLaunchPlan{
			AgentFile:         agentPath,
			Agent:             opts.AgentName,
			Profile:           profileName,
			Backend:           backend,
			Mode:              opts.Mode,
			Binary:            bin,
			WorkingDir:        workingDir,
			Model:             model,
			Effort:            effort,
			Tools:             planTools,
			Env:               redactEnv(providerEnv),
			Command:           command,
			RunAsUser:         runAsUser,
			Interactive:       true,
			providerEnv:       providerEnv,
			cleanups:          cleanups,
			LaunchPolicy:      launchDecision,
			FutureNotes:       futureNotes,
			ProfileResolution: launchProfileResolution(profileName, profile),
		}
		cleanupRendered = false
		return plan, nil
	}
	inv := host.TranslateAgentInvocationForBackend(planCtx, backend, cliArgs, task, workingDir)
	if inv.Cleanup != nil {
		cleanups = append(cleanups, inv.Cleanup)
	}
	command := append([]string{bin}, inv.Args...)
	plan := agentLaunchPlan{
		AgentFile:         agentPath,
		Agent:             opts.AgentName,
		Profile:           profileName,
		Backend:           backend,
		Mode:              opts.Mode,
		Binary:            bin,
		WorkingDir:        workingDir,
		Model:             model,
		Effort:            effort,
		Tools:             planTools,
		Env:               redactEnv(providerEnv),
		Command:           command,
		Stdin:             inv.Stdin,
		RunAsUser:         runAsUser,
		providerEnv:       providerEnv,
		claudeArgs:        cliArgs,
		cleanups:          cleanups,
		LaunchPolicy:      launchDecision,
		FutureNotes:       standaloneLaunchFutureNotes(opts.Mode, disableCodexShell),
		ProfileResolution: launchProfileResolution(profileName, profile),
	}
	cleanupRendered = false
	return plan, nil
}

func buildRawInteractiveLaunchPlan(opts agentLaunchOptions) (agentLaunchPlan, error) {
	if !opts.Interactive {
		return agentLaunchPlan{}, fmt.Errorf("--raw requires --interactive")
	}
	if strings.TrimSpace(opts.AppPath) != "" || strings.TrimSpace(opts.AgentName) != "" || strings.TrimSpace(opts.AgentFile) != "" || strings.TrimSpace(opts.AgentTemplate) != "" {
		return agentLaunchPlan{}, fmt.Errorf("--raw interactive launch does not use --app, --agent, --agent-file, or --agent-template")
	}
	if strings.TrimSpace(opts.Task) != "" || strings.TrimSpace(opts.TaskFile) != "" {
		return agentLaunchPlan{}, fmt.Errorf("--raw interactive launch does not accept --task or --task-file")
	}
	launchCfg, err := loadLaunchConfig(opts.ConfigPath)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	profileName := firstLaunchNonEmpty(opts.Profile, launchCfg.DefaultProfile)
	profile, hasProfile := launchCfg.Profiles[profileName]
	if profileName != "" && !hasProfile {
		return agentLaunchPlan{}, fmt.Errorf("unknown harness profile %q", profileName)
	}
	workingDir, err := resolveStandaloneLaunchWorkingDir(opts.WorkingDir)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	launchDecision, err := checkLaunchPolicy(launchCfg.AgentLaunchPolicy, "agent.launch.raw_interactive", "raw", workingDir)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	backend := firstLaunchNonEmpty(opts.Backend, profile.Backend, "codex")
	if _, ok := host.ResolveAgentBackendName(backend); !ok && backend != "claude" {
		return agentLaunchPlan{}, fmt.Errorf("unknown backend %q", backend)
	}
	if backend != "codex" && backend != "claude" {
		return agentLaunchPlan{}, fmt.Errorf("--raw interactive launch supports --backend codex or claude, got %q", backend)
	}
	profileName, profile, err = launchProfileForBackend(profileName, profile, backend, opts.Profile, opts.Backend)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	model := launchModelForBackend(backend, firstLaunchNonEmpty(opts.Model, profile.Model))
	effort := firstLaunchNonEmpty(opts.Effort, profile.Effort)
	providerEnv := map[string]string{}
	for k, v := range profile.Env {
		providerEnv[k] = v
	}
	extraEnv, err := parseLaunchEnv(opts.Env)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	for k, v := range extraEnv {
		providerEnv[k] = v
	}
	bin, err := resolveLaunchBin(backend, true)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	bin, runAsUser, err := resolveDelegatedLaunchBin(backend, bin, launchCfg.AgentUserDelegation, true)
	if err != nil {
		return agentLaunchPlan{}, err
	}
	args := buildRawInteractiveArgs(backend, model, effort, workingDir, opts.AddDirs)
	// A launcher shim preserves the caller's backend argv by passing each item
	// through the hidden repeatable --raw-arg flag. Keep those arguments last:
	// explicit CLI input must retain its normal backend precedence over a
	// profile's default model/effort settings.
	args = append(args, opts.RawArgs...)
	return agentLaunchPlan{
		Agent:        "raw",
		Profile:      profileName,
		Backend:      backend,
		Binary:       bin,
		WorkingDir:   workingDir,
		Model:        model,
		Effort:       effort,
		Env:          redactEnv(providerEnv),
		Command:      append([]string{bin}, args...),
		RunAsUser:    runAsUser,
		Interactive:  true,
		providerEnv:  providerEnv,
		LaunchPolicy: launchDecision,
	}, nil
}

func runAgentLaunchPlan(ctx context.Context, cmd *cobra.Command, plan agentLaunchPlan) error {
	if plan.Interactive {
		if len(plan.Command) == 0 {
			return fmt.Errorf("interactive launch has no command")
		}
		run := exec.CommandContext(ctx, plan.Command[0], plan.Command[1:]...)
		run.Dir = plan.WorkingDir
		run.Stdin = os.Stdin
		run.Stdout = cmd.OutOrStdout()
		run.Stderr = cmd.ErrOrStderr()
		run.Env = os.Environ()
		for k, v := range plan.providerEnv {
			run.Env = append(run.Env, k+"="+v)
		}
		return run.Run()
	}
	out, err := host.RunClaudeOneShotForHarness(ctx, plan.Binary, plan.claudeArgs, plan.Stdin, plan.WorkingDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "" {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), out)
	}
	return err
}

func buildInteractiveCodexArgs(model, effort, workingDir string, addDirs []string, mcpServers map[string]any, extraArgs []string, prompt string) []string {
	var args []string
	if m := launchModelForBackend("codex", model); strings.TrimSpace(m) != "" {
		args = append(args, "-m", m)
	}
	if strings.TrimSpace(effort) != "" {
		args = append(args, "-c", "model_reasoning_effort="+launchTOMLString(effort))
	}
	args = append(args, codexBypassApprovalsAndSandboxFlag)
	if strings.TrimSpace(workingDir) != "" {
		args = append(args, "-C", workingDir)
	}
	for _, dir := range addDirs {
		if strings.TrimSpace(dir) != "" {
			args = append(args, "--add-dir", dir)
		}
	}
	args = append(args, codexConfigArgsForMCPServers(mcpServers, workingDir)...)
	args = append(args, extraArgs...)
	if strings.TrimSpace(prompt) != "" {
		args = append(args, prompt)
	}
	return args
}

func buildRawInteractiveArgs(backend, model, effort, workingDir string, addDirs []string) []string {
	switch backend {
	case "claude":
		return buildRawInteractiveClaudeArgs(model, effort, addDirs)
	default:
		return buildRawInteractiveCodexArgs(model, effort, workingDir, addDirs)
	}
}

func buildRawInteractiveCodexArgs(model, effort, workingDir string, addDirs []string) []string {
	var args []string
	if m := launchModelForBackend("codex", model); strings.TrimSpace(m) != "" {
		args = append(args, "-m", m)
	}
	if strings.TrimSpace(effort) != "" {
		args = append(args, "-c", "model_reasoning_effort="+launchTOMLString(effort))
	}
	args = append(args, codexBypassApprovalsAndSandboxFlag)
	if strings.TrimSpace(workingDir) != "" {
		args = append(args, "-C", workingDir)
	}
	for _, dir := range addDirs {
		if strings.TrimSpace(dir) != "" {
			args = append(args, "--add-dir", dir)
		}
	}
	return args
}

func buildRawInteractiveClaudeArgs(model, effort string, addDirs []string) []string {
	var args []string
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	if strings.TrimSpace(effort) != "" {
		args = append(args, "--effort", effort)
	}
	for _, dir := range addDirs {
		if strings.TrimSpace(dir) != "" {
			args = append(args, "--add-dir", dir)
		}
	}
	return args
}

func composeLaunchPrompt(systemPrompt, task string) string {
	systemPrompt = strings.TrimSpace(systemPrompt)
	task = strings.TrimSpace(task)
	switch {
	case systemPrompt == "":
		return task
	case task == "":
		return systemPrompt
	default:
		return systemPrompt + "\n\n---\n\n" + task
	}
}

func codexConfigArgsForMCPServers(mcpServers map[string]any, workingDir string) []string {
	if len(mcpServers) == 0 {
		return nil
	}
	servers := make(map[string]host.CodexMCPServerConfig, len(mcpServers))
	for name := range mcpServers {
		server, ok := mcpServers[name].(map[string]any)
		if !ok {
			continue
		}
		cfg := host.CodexMCPServerConfig{}
		if command, _ := server["command"].(string); strings.TrimSpace(command) != "" {
			cfg.Command = command
		}
		if args, ok := stringSliceFromAny(server["args"]); ok && len(args) > 0 {
			cfg.Args = args
		}
		if env, ok := stringMapFromAny(server["env"]); ok && len(env) > 0 {
			cfg.Env = env
		}
		if cwd, _ := server["cwd"].(string); strings.TrimSpace(cwd) != "" {
			cfg.CWD = cwd
		}
		servers[name] = cfg
	}
	return host.CodexMCPConfigArgsForServers(servers, workingDir)
}

type launchConfig struct {
	Profiles            map[string]orchestrator.HarnessProfile
	DefaultProfile      string
	AgentLaunchPolicy   host.AgentLaunchPolicy
	AgentUserDelegation *webconfig.AgentUserDelegationConfig
}

func loadLaunchConfig(configPath string) (launchConfig, error) {
	cfg, err := webconfig.Load(configPath)
	if err != nil {
		return launchConfig{}, err
	}
	profiles, defaultProfile := harnessProfilesFromConfig(cfg)
	if profiles == nil {
		profiles = map[string]orchestrator.HarnessProfile{}
	}
	return launchConfig{
		Profiles:            profiles,
		DefaultProfile:      defaultProfile,
		AgentLaunchPolicy:   agentLaunchPolicyFromConfig(cfg),
		AgentUserDelegation: cfg.AgentUserDelegation,
	}, nil
}

func checkLaunchPolicy(policy host.AgentLaunchPolicy, verb, agentName, workingDir string) (*host.AgentLaunchDecision, error) {
	policy = policy.Normalized()
	if !policy.Enabled {
		return nil, nil
	}
	decision, err := policy.Check(context.Background(), verb, agentName, workingDir)
	return &decision, err
}

func readLaunchTask(opts agentLaunchOptions) (string, error) {
	if strings.TrimSpace(opts.TaskFile) == "" {
		return opts.Task, nil
	}
	raw, err := os.ReadFile(opts.TaskFile)
	if err != nil {
		return "", fmt.Errorf("read --task-file: %w", err)
	}
	return string(raw), nil
}

func resolveLaunchWorkingDir(dir, appDir string) (string, error) {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	abs, err := filepath.Abs(filepath.Join(appDir, dir))
	if err != nil {
		return "", err
	}
	return abs, nil
}

func resolveStandaloneLaunchWorkingDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	return filepath.Abs(dir)
}

func resolveStandaloneAgentFile(agentName, explicit, template string) (string, func(), error) {
	if strings.TrimSpace(explicit) != "" {
		path, err := filepath.Abs(explicit)
		return path, nil, err
	}
	if strings.TrimSpace(template) != "" {
		path, err := filepath.Abs(template)
		if err != nil {
			return "", nil, err
		}
		if st, statErr := os.Stat(path); statErr != nil || st.IsDir() {
			if statErr != nil {
				return "", nil, fmt.Errorf("agent template %s: %w", path, statErr)
			}
			return "", nil, fmt.Errorf("agent template %s is a directory", path)
		}
		return renderBuiltInStandaloneCodexAgentWithTemplate(agentName, path)
	}
	candidates := []string{
		filepath.Join(".kitsoki", "agents", agentName+".local.toml"),
		filepath.Join(".kitsoki", "agents", agentName+".toml"),
		filepath.Join(".codex", "agents", agentName+".local.toml"),
		filepath.Join(".codex", "agents", agentName+".toml"),
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidates = append(candidates,
			filepath.Join(home, ".codex", "agents", agentName+".local.toml"),
			filepath.Join(home, ".codex", "agents", agentName+".toml"),
		)
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			path, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", nil, absErr
			}
			// Keep complete legacy definitions usable without requiring the embedded
			// library to have been staged into a source-tree test binary. Partial
			// legacy layers fall through to the isolated embedded-base renderer.
			if _, loadErr := loadStandaloneCodexAgent(path); loadErr == nil {
				return path, nil, nil
			}
			return renderBuiltInStandaloneCodexAgentWithTemplate(agentName, path)
		}
	}
	path, cleanup, err := renderBuiltInStandaloneCodexAgent(agentName)
	if err == nil {
		return path, cleanup, nil
	}
	return "", nil, fmt.Errorf("freestanding agent %q not found in project overrides or the embedded Kitsoki agent library; pass --app for a story agents: entry or --agent-file for a .codex/agents/*.toml file: %w", agentName, err)
}

// renderBuiltInStandaloneCodexAgentWithTemplate resolves a TOML overlay against
// the packaged agent, then writes only the fully resolved result to the private
// launch directory. A template need only contain the fields it changes.
func renderBuiltInStandaloneCodexAgentWithTemplate(agentName, templatePath string) (string, func(), error) {
	basePath, cleanup, err := renderBuiltInStandaloneCodexAgent(agentName)
	if err != nil {
		return "", nil, err
	}
	fail := func(err error) (string, func(), error) {
		cleanup()
		return "", nil, err
	}
	base, err := loadStandaloneCodexAgent(basePath)
	if err != nil {
		return fail(fmt.Errorf("load rendered embedded agent: %w", err))
	}
	overlay, err := loadStandaloneCodexAgentOverlay(templatePath)
	if err != nil {
		return fail(fmt.Errorf("load agent template %s: %w", templatePath, err))
	}
	resolved := mergeStandaloneCodexAgents(base, overlay)
	resolved.Extends = ""
	if strings.TrimSpace(resolved.Name) != "" && resolved.Name != agentName {
		return fail(fmt.Errorf("agent template %s declares name %q, not %q", templatePath, resolved.Name, agentName))
	}
	if strings.TrimSpace(resolved.Name) == "" {
		resolved.Name = agentName
	}
	path := filepath.Join(filepath.Dir(basePath), agentName+".templated.toml")
	if err := os.WriteFile(path, []byte(renderStandaloneCodexAgentTOML(resolved)), 0o600); err != nil {
		return fail(fmt.Errorf("write rendered templated agent: %w", err))
	}
	return path, cleanup, nil
}

func renderBuiltInStandaloneCodexAgent(agentName string) (string, func(), error) {
	root, err := materializeBuiltInAgentLibrary(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("materialize embedded agent library: %w", err)
	}
	sourcePath := filepath.Join(root, "agents", agentName+".md")
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("embedded agent %q not found", agentName)
		}
		return "", nil, fmt.Errorf("read embedded agent %q: %w", agentName, err)
	}
	front, instructions, err := parseBuiltInAgentMarkdown(raw)
	if err != nil {
		return "", nil, fmt.Errorf("parse embedded agent %q: %w", agentName, err)
	}
	if front.Name != "" && front.Name != agentName {
		return "", nil, fmt.Errorf("embedded agent %q declares name %q", agentName, front.Name)
	}
	if strings.TrimSpace(instructions) == "" {
		return "", nil, fmt.Errorf("embedded agent %q has no instructions", agentName)
	}

	dir, err := os.MkdirTemp("", "kitsoki-agent-launch-")
	if err != nil {
		return "", nil, fmt.Errorf("create rendered agent directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, agentName+".toml")
	if err := os.WriteFile(path, []byte(renderBuiltInAgentTOML(agentName, front, instructions)), 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write rendered agent: %w", err)
	}
	return path, cleanup, nil
}

func parseBuiltInAgentMarkdown(raw []byte) (builtInAgentFrontmatter, string, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return builtInAgentFrontmatter{}, "", fmt.Errorf("missing YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return builtInAgentFrontmatter{}, "", fmt.Errorf("unterminated YAML frontmatter")
	}
	end += 4
	var front builtInAgentFrontmatter
	if err := yaml.Unmarshal([]byte(text[4:end]), &front); err != nil {
		return builtInAgentFrontmatter{}, "", err
	}
	return front, strings.TrimSpace(text[end+5:]), nil
}

func renderBuiltInAgentTOML(agentName string, front builtInAgentFrontmatter, instructions string) string {
	return renderStandaloneCodexAgentTOML(standaloneCodexAgent{
		Name:                  firstLaunchNonEmpty(front.Name, agentName),
		Description:           front.Description,
		Model:                 front.Model,
		Effort:                front.Effort,
		DeveloperInstructions: instructions,
		// Packaged agents are Studio-MCP contracts. Rendering this server only for
		// the isolated launch keeps normal Codex sessions untouched.
		MCPServers: map[string]any{"kitsoki": map[string]any{"command": "kitsoki", "args": []string{"mcp"}}},
	})
}

func loadStandaloneCodexAgent(path string) (standaloneCodexAgent, error) {
	agent, err := loadStandaloneCodexAgentChain(path, nil)
	if err != nil {
		return standaloneCodexAgent{}, err
	}
	if strings.TrimSpace(agent.DeveloperInstructions) == "" {
		return standaloneCodexAgent{}, fmt.Errorf("agent file %s must set developer_instructions", path)
	}
	if strings.TrimSpace(agent.Name) == "" {
		agent.Name = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), ".local")
	}
	return agent, nil
}

// loadStandaloneCodexAgentOverlay permits a partial TOML layer. Its parent
// chain remains available for compatibility, while the embedded agent supplies
// any fields the layer intentionally leaves blank.
func loadStandaloneCodexAgentOverlay(path string) (standaloneCodexAgent, error) {
	return loadStandaloneCodexAgentChain(path, nil)
}

func loadStandaloneCodexAgentChain(path string, seen map[string]bool) (standaloneCodexAgent, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return standaloneCodexAgent{}, err
	}
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[abs] {
		return standaloneCodexAgent{}, fmt.Errorf("agent extends cycle at %s", abs)
	}
	seen[abs] = true
	defer delete(seen, abs)
	raw, err := os.ReadFile(abs)
	if err != nil {
		return standaloneCodexAgent{}, fmt.Errorf("read agent file: %w", err)
	}
	agent, err := parseStandaloneCodexAgentTOML(string(raw))
	if err != nil {
		return standaloneCodexAgent{}, fmt.Errorf("parse agent file %s: %w", abs, err)
	}
	if strings.TrimSpace(agent.Extends) != "" {
		parentPath := expandStandaloneAgentPath(agent.Extends, filepath.Dir(abs))
		parent, parentErr := loadStandaloneCodexAgentChain(parentPath, seen)
		if parentErr != nil {
			return standaloneCodexAgent{}, fmt.Errorf("load parent agent %s: %w", parentPath, parentErr)
		}
		agent = mergeStandaloneCodexAgents(parent, agent)
	}
	return agent, nil
}

func expandStandaloneAgentPath(path, baseDir string) string {
	path = os.ExpandEnv(strings.TrimSpace(path))
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	return filepath.Clean(path)
}

func mergeStandaloneCodexAgents(parent, child standaloneCodexAgent) standaloneCodexAgent {
	merged := parent
	if child.Name != "" {
		merged.Name = child.Name
	}
	if child.Description != "" {
		merged.Description = child.Description
	}
	if child.DeveloperInstructions != "" {
		merged.DeveloperInstructions = child.DeveloperInstructions
	}
	if child.Model != "" {
		merged.Model = child.Model
	}
	if child.Effort != "" {
		merged.Effort = child.Effort
	}
	if child.SandboxMode != "" {
		merged.SandboxMode = child.SandboxMode
	}
	if child.DeveloperInstructionsAppend != "" {
		merged.DeveloperInstructions = strings.TrimSpace(merged.DeveloperInstructions) + "\n\n" + strings.TrimSpace(child.DeveloperInstructionsAppend)
	}
	merged.Extends = child.Extends
	if parent.MCPServers != nil || child.MCPServers != nil {
		merged.MCPServers = map[string]any{}
		for name, server := range parent.MCPServers {
			merged.MCPServers[name] = cloneStandaloneMCPServer(server)
		}
		for name, server := range child.MCPServers {
			parentServer, hasParent := merged.MCPServers[name]
			if hasParent {
				merged.MCPServers[name] = mergeStandaloneMCPServer(parentServer, server)
			} else {
				merged.MCPServers[name] = cloneStandaloneMCPServer(server)
			}
		}
	}
	return merged
}

func cloneStandaloneMCPServer(server any) any {
	fields, ok := server.(map[string]any)
	if !ok {
		return server
	}
	clone := make(map[string]any, len(fields))
	for key, value := range fields {
		clone[key] = value
	}
	return clone
}

func mergeStandaloneMCPServer(parent, child any) any {
	merged, ok := cloneStandaloneMCPServer(parent).(map[string]any)
	if !ok {
		return cloneStandaloneMCPServer(child)
	}
	childFields, ok := child.(map[string]any)
	if !ok {
		return cloneStandaloneMCPServer(child)
	}
	for key, value := range childFields {
		merged[key] = value
	}
	return merged
}

func renderStandaloneCodexAgentTOML(agent standaloneCodexAgent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\n", launchTOMLString(agent.Name))
	if strings.TrimSpace(agent.Description) != "" {
		fmt.Fprintf(&b, "description = %s\n", launchTOMLString(agent.Description))
	}
	if strings.TrimSpace(agent.Model) != "" {
		fmt.Fprintf(&b, "model = %s\n", launchTOMLString(agent.Model))
	}
	if strings.TrimSpace(agent.Effort) != "" {
		fmt.Fprintf(&b, "model_reasoning_effort = %s\n", launchTOMLString(agent.Effort))
	}
	if strings.TrimSpace(agent.SandboxMode) != "" {
		fmt.Fprintf(&b, "sandbox_mode = %s\n", launchTOMLString(agent.SandboxMode))
	}
	fmt.Fprintf(&b, "developer_instructions = %s\n", launchTOMLString(agent.DeveloperInstructions))
	for _, name := range sortedStandaloneMCPServerNames(agent.MCPServers) {
		server, ok := agent.MCPServers[name].(map[string]any)
		if !ok {
			continue
		}
		b.WriteString("\n[mcp_servers." + name + "]\n")
		if command, ok := server["command"].(string); ok && strings.TrimSpace(command) != "" {
			fmt.Fprintf(&b, "command = %s\n", launchTOMLString(command))
		}
		if args, ok := stringSliceFromAny(server["args"]); ok {
			fmt.Fprintf(&b, "args = %s\n", launchTOMLStringArray(args))
		}
		if cwd, ok := server["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			fmt.Fprintf(&b, "cwd = %s\n", launchTOMLString(cwd))
		}
	}
	return b.String()
}

func sortedStandaloneMCPServerNames(servers map[string]any) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parseStandaloneCodexAgentTOML(src string) (standaloneCodexAgent, error) {
	var agent standaloneCodexAgent
	agent.MCPServers = map[string]any{}
	section := ""
	currentServer := ""
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripLaunchTOMLComment(lines[i]))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			currentServer = ""
			if strings.HasPrefix(section, "mcp_servers.") {
				currentServer = strings.TrimPrefix(section, "mcp_servers.")
				if currentServer == "" || strings.Contains(currentServer, ".") {
					return standaloneCodexAgent{}, fmt.Errorf("unsupported section [%s]", section)
				}
				if _, ok := agent.MCPServers[currentServer]; !ok {
					agent.MCPServers[currentServer] = map[string]any{}
				}
			}
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return standaloneCodexAgent{}, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if strings.HasPrefix(val, `"""`) {
			var text string
			text, i = collectLaunchTOMLMultilineString(lines, i, val)
			val = text
		}
		if currentServer != "" {
			server := agent.MCPServers[currentServer].(map[string]any)
			switch key {
			case "command":
				server["command"] = parseLaunchTOMLString(val)
			case "args":
				server["args"] = parseLaunchTOMLStringArray(val)
			case "cwd":
				server["cwd"] = parseLaunchTOMLString(val)
			default:
				return standaloneCodexAgent{}, fmt.Errorf("line %d: unsupported mcp server key %q", i+1, key)
			}
			continue
		}
		if section != "" {
			return standaloneCodexAgent{}, fmt.Errorf("line %d: unsupported section [%s]", i+1, section)
		}
		switch key {
		case "name":
			agent.Name = parseLaunchTOMLString(val)
		case "description":
			agent.Description = parseLaunchTOMLString(val)
		case "developer_instructions":
			agent.DeveloperInstructions = parseLaunchTOMLString(val)
		case "developer_instructions_append":
			agent.DeveloperInstructionsAppend = parseLaunchTOMLString(val)
		case "extends":
			agent.Extends = parseLaunchTOMLString(val)
		case "model":
			agent.Model = parseLaunchTOMLString(val)
		case "model_reasoning_effort":
			agent.Effort = parseLaunchTOMLString(val)
		case "sandbox_mode":
			agent.SandboxMode = parseLaunchTOMLString(val)
		default:
			// Ignore Codex-agent fields that are useful to Codex itself but not
			// needed for launch planning, such as description variants.
		}
	}
	if len(agent.MCPServers) == 0 {
		agent.MCPServers = nil
	}
	return agent, nil
}

func stripLaunchTOMLComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func collectLaunchTOMLMultilineString(lines []string, start int, firstVal string) (string, int) {
	rest := strings.TrimPrefix(firstVal, `"""`)
	if end := strings.Index(rest, `"""`); end >= 0 {
		return rest[:end], start
	}
	var b strings.Builder
	b.WriteString(rest)
	for i := start + 1; i < len(lines); i++ {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		line := lines[i]
		if end := strings.Index(line, `"""`); end >= 0 {
			b.WriteString(line[:end])
			return b.String(), i
		}
		b.WriteString(line)
	}
	return b.String(), len(lines) - 1
}

func parseLaunchTOMLString(val string) string {
	val = strings.TrimSpace(val)
	if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) && len(val) >= 2 {
		val = strings.TrimSuffix(strings.TrimPrefix(val, `"`), `"`)
		repl := strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\t`, "\t", `\r`, "\r")
		return repl.Replace(val)
	}
	return val
}

func parseLaunchTOMLStringArray(val string) []string {
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(val, "["), "]"))
	if inner == "" {
		return nil
	}
	var out []string
	var b strings.Builder
	inString := false
	escaped := false
	for _, r := range inner {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case r == ',' && !inString:
			out = append(out, parseLaunchTOMLString(strings.TrimSpace(b.String())))
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, parseLaunchTOMLString(strings.TrimSpace(b.String())))
	}
	return out
}

func stringSliceFromAny(v any) ([]string, bool) {
	switch typed := v.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func stringMapFromAny(v any) (map[string]string, bool) {
	switch typed := v.(type) {
	case map[string]string:
		return typed, true
	case map[string]any:
		out := make(map[string]string, len(typed))
		for k, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out[k] = s
		}
		return out, true
	default:
		return nil, false
	}
}

func launchTOMLString(s string) string {
	repl := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	)
	return `"` + repl.Replace(s) + `"`
}

func launchTOMLStringArray(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = launchTOMLString(x)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func launchTOMLStringTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = launchTOMLString(k) + "=" + launchTOMLString(m[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func writeLaunchMCPConfigTempfile(mcpServers map[string]any, prefix string) (string, func(), error) {
	mcpConfig := map[string]any{"mcpServers": mcpServers}
	mcpBytes, err := json.Marshal(mcpConfig)
	if err != nil {
		return "", nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	f, err := os.CreateTemp("", prefix+"-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("create mcp config tempfile: %w", err)
	}
	if _, err := f.Write(mcpBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("write mcp config: %w", err)
	}
	_ = f.Close()
	path := f.Name()
	return path, func() { _ = os.Remove(path) }, nil
}

func normalizeAgentLaunchMode(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", "normal", "default":
		return "", nil
	case launchModeCodeact:
		return launchModeCodeact, nil
	default:
		return "", fmt.Errorf("unknown --mode %q; supported modes are normal and codeact", mode)
	}
}

func validateCodeactLaunchOptions(opts agentLaunchOptions) error {
	switch strings.TrimSpace(opts.PermissionMode) {
	case "", "denyAll":
		// CodeAct mode owns the permission posture so Claude's allowlist binds.
	default:
		return fmt.Errorf("--mode codeact requires --permission-mode denyAll or no --permission-mode; got %q", opts.PermissionMode)
	}
	_, _, err := codeactLaunchCapabilitiesArg(opts)
	return err
}

func shouldDefaultCodeactToCodex(opts agentLaunchOptions, profile orchestrator.HarnessProfile, providerBackend string) bool {
	if strings.TrimSpace(opts.Backend) != "" || strings.TrimSpace(opts.Profile) != "" {
		return false
	}
	if launchBackendName(providerBackend) == "codex" {
		return true
	}
	return launchProfileBackend(profile) != "codex"
}

func codeactBackendSupportsHardNoShell(backend string) bool {
	switch launchBackendName(backend) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func codeactLaunchModel(backend, model, explicitModel string) (string, error) {
	if launchBackendName(backend) != "claude" {
		return model, nil
	}
	model = strings.TrimSpace(model)
	if model == "" || host.IsClaudeModelID(model) {
		return model, nil
	}
	if strings.TrimSpace(explicitModel) != "" {
		return "", fmt.Errorf("--mode codeact uses backend claude; --model %q is not a Claude model id", model)
	}
	return "", nil
}

func appendCodeactBackendArgs(args []string, mode, backend string) []string {
	return appendCodexShellToolDisableArg(args, backend, mode == launchModeCodeact)
}

func appendCodexFeatureDisableArg(args []string, backend, feature string, disable bool) []string {
	if !disable || launchBackendName(backend) != "codex" || strings.TrimSpace(feature) == "" {
		return args
	}
	flag := "--disable=" + feature
	for _, arg := range args {
		if arg == flag {
			return args
		}
	}
	return append(args, flag)
}

func appendCodexShellToolDisableArg(args []string, backend string, disable bool) []string {
	return appendCodexFeatureDisableArg(args, backend, launchCodexShellToolFeature, disable)
}

func appendCodexAppsDisableArg(args []string, backend string, disable bool) []string {
	return appendCodexFeatureDisableArg(args, backend, launchCodexAppsFeature, disable)
}

func shouldDisableStandaloneCodexShell(mode, backend, requestedName string, agent standaloneCodexAgent) bool {
	if launchBackendName(backend) != "codex" {
		return false
	}
	if mode == launchModeCodeact {
		return true
	}
	return requestedName == launchStudioMCPDriverAgentName || agent.Name == launchStudioMCPDriverAgentName
}

func shouldDisableCodexAppsForMCPServers(backend string, mcpServers map[string]any) bool {
	return launchBackendName(backend) == "codex" && len(mcpServers) > 0
}

func launchFutureNotes(mode string) []string {
	if mode == launchModeCodeact {
		return []string{
			"CodeAct mode attaches only the kitsoki-codeact MCP server. Claude permits only mcp__kitsoki-codeact__codeact_eval; Codex runs with --disable shell_tool.",
			"CodeAct capabilities are enforced by mcp-codeact startup args rooted at the launch working directory; launch policy is still a preflight guard, not a kernel/filesystem sandbox.",
		}
	}
	return []string{
		"Launch policy is a preflight guard, not a kernel/filesystem sandbox; backend sandboxing remains provider-specific.",
		"Extension overlays and HTTP rules should be modeled as future agent/profile fields and resolved into this launch plan.",
	}
}

func standaloneLaunchFutureNotes(mode string, codexShellDisabled bool) []string {
	if mode == launchModeCodeact {
		return launchFutureNotes(mode)
	}
	if codexShellDisabled {
		return []string{
			"Codex runs with --disable shell_tool for this freestanding agent; the shell is absent while MCP servers remain attached.",
			"Codex exec currently requires --dangerously-bypass-approvals-and-sandbox for MCP calls to run non-interactively.",
		}
	}
	return []string{
		"Codex has no hard --agent-style per-tool allowlist in this launch path; the MCP-only posture comes from the freestanding agent instructions plus the attached MCP server set.",
		"Codex exec currently requires --dangerously-bypass-approvals-and-sandbox for MCP calls to run non-interactively.",
	}
}

func buildLaunchCodeactMCPServer(opts agentLaunchOptions, workingDir string) (map[string]any, error) {
	capFlag, capValue, err := codeactLaunchCapabilitiesArg(opts)
	if err != nil {
		return nil, err
	}
	args := []string{"mcp-codeact", "--working-dir", workingDir, capFlag, capValue}
	return map[string]any{
		"command": "kitsoki",
		"args":    args,
	}, nil
}

func codeactLaunchCapabilitiesArg(opts agentLaunchOptions) (flag, value string, err error) {
	inline := strings.TrimSpace(opts.CodeactCapabilitiesJSON)
	path := strings.TrimSpace(opts.CodeactCapabilitiesFile)
	if inline != "" && path != "" {
		return "", "", fmt.Errorf("use only one of --codeact-capabilities-json or --codeact-capabilities-file")
	}
	if path != "" {
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			return "", "", fmt.Errorf("resolve --codeact-capabilities-file: %w", absErr)
		}
		if _, parseErr := parseCodeactCapabilities("", abs); parseErr != nil {
			return "", "", parseErr
		}
		return "--capabilities-file", abs, nil
	}
	if inline == "" {
		inline = launchCodeactDefaultCapabilitiesJSON
	}
	if _, parseErr := parseCodeactCapabilities(inline, ""); parseErr != nil {
		return "", "", parseErr
	}
	return "--capabilities-json", inline, nil
}

func appendCodeactLaunchSystemPrompt(systemPrompt string) string {
	const codeactPrompt = `Kitsoki CodeAct mode:
- You have no Bash, Python, Node, shell, or direct editor tools.
- Inspect and edit files only through the kitsoki-codeact MCP server's codeact_eval tool. In Claude this tool is named mcp__kitsoki-codeact__codeact_eval; in Codex, use tool_search if the MCP tool is not visible yet.
- Every codeact_eval call takes {"snippet": "...", "inputs": {...}, "world": {...}}. The snippet is Starlark, must define def main(ctx):, and must return a dict. The returned dict appears as outputs.
- Start with small inspection snippets. Prefer ctx.probe("git.status") for current git state and ctx.probe("git.ls_files", ["<pathspec>"]) to list tracked files. Use ctx.fs.read(path), ctx.fs.exists(path), and ctx.fs.glob(pattern) for targeted filesystem inspection.
- Make edits with read-modify-write snippets. Check that the expected text exists before writing; call fail("reason") instead of silently writing a guessed change.
- Typical edit snippet:
  def main(ctx):
      path = ctx.inputs["path"]
      old = ctx.fs.read(path)
      new = old.replace(ctx.inputs["old"], ctx.inputs["new"])
      if new == old:
          fail("expected text not found: " + path)
      written = ctx.fs.write(path, new)
      return {"written": written}
- Typical status snippet:
  def main(ctx):
      status = ctx.probe("git.status")
      return {"status": status["out"], "exit": status["exit"]}
- Starlark is not Python: no imports, no classes, no exceptions, no while loops, no recursion, and strings are not iterable. Use split("\n") for lines and plain dict/list values.
- Do not try to run tests, package managers, git commands, or shell commands. With the default launch capabilities, ctx.probe is read-only git inspection and ctx.fs is the only write surface.`
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		return codeactPrompt
	}
	return systemPrompt + "\n\n" + codeactPrompt
}

func backendForAgentProvider(def *app.AppDef, provider string) string {
	if strings.TrimSpace(provider) == "" || def == nil || def.Providers == nil {
		return ""
	}
	p := def.Providers[provider]
	if p == nil {
		return ""
	}
	for k, v := range p.Env {
		if strings.EqualFold(k, "OPENAI_BASE_URL") || strings.EqualFold(k, "OPENAI_API_KEY") {
			_ = v
			return "codex"
		}
	}
	return ""
}

func providerEnvForAgent(def *app.AppDef, provider string) map[string]string {
	if strings.TrimSpace(provider) == "" || def == nil || def.Providers == nil {
		return nil
	}
	p := def.Providers[provider]
	if p == nil {
		return nil
	}
	return p.Env
}

func providerDefaultsForAgent(def *app.AppDef, provider string) (model, effort string) {
	if strings.TrimSpace(provider) == "" || def == nil || def.Providers == nil {
		return "", ""
	}
	p := def.Providers[provider]
	if p == nil {
		return "", ""
	}
	return p.Model, p.Effort
}

func buildLaunchClaudeArgs(decl *app.AgentDecl, model, effort, permissionMode string, addDirs []string) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	args = append(args, "--setting-sources", "project,local")
	args = append(args, "--disable-slash-commands")
	args = append(args, "--strict-mcp-config")
	requestedPermissionMode := firstLaunchNonEmpty(permissionMode, launchDefaultPermissionMode(decl))
	permissionMode = normalizeLaunchPermissionMode(requestedPermissionMode)
	args = append(args, "--permission-mode", permissionMode)
	if strings.TrimSpace(decl.SystemPrompt) != "" {
		if decl.InheritClaudeDefault {
			args = append(args, "--append-system-prompt", decl.SystemPrompt)
		} else {
			args = append(args, "--system-prompt", decl.SystemPrompt)
		}
	}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	if strings.TrimSpace(effort) != "" {
		args = append(args, "--effort", effort)
	}
	if len(decl.Tools) > 0 {
		args = append(args, "--allowedTools", strings.Join(decl.Tools, ","))
	}
	denied := launchDeniedTools(decl, requestedPermissionMode)
	if len(denied) > 0 {
		args = append(args, "--disallowedTools", strings.Join(denied, ","))
	}
	for _, dir := range addDirs {
		if strings.TrimSpace(dir) != "" {
			args = append(args, "--add-dir", dir)
		}
	}
	return args
}

func launchDefaultPermissionMode(decl *app.AgentDecl) string {
	if decl != nil && decl.ExternalSideEffect != nil && !*decl.ExternalSideEffect {
		return "default"
	}
	return "bypassPermissions"
}

func launchDeniedTools(decl *app.AgentDecl, permissionMode string) []string {
	denied := []string{"AskUserQuestion", "Agent", "Task"}
	readOnly := permissionMode == "denyAll" || (decl != nil && decl.ExternalSideEffect != nil && !*decl.ExternalSideEffect)
	if readOnly {
		denied = append(denied, "Write", "Edit", "MultiEdit", "NotebookEdit", "Bash")
	}
	sort.Strings(denied)
	out := denied[:0]
	for _, tool := range denied {
		if len(out) == 0 || out[len(out)-1] != tool {
			out = append(out, tool)
		}
	}
	return out
}

func normalizeLaunchPermissionMode(mode string) string {
	switch mode {
	case "ask", "denyAll":
		return "default"
	case "":
		return "bypassPermissions"
	default:
		return mode
	}
}

func resolveLaunchBin(backend string, requireInstalled bool) (string, error) {
	binName := "claude"
	envName := host.AgentBinEnv
	switch backend {
	case "codex":
		if env := os.Getenv(host.CodexBinEnv); env != "" {
			return env, nil
		}
		binName = "codex"
		envName = host.CodexBinEnv
	case "copilot":
		if env := os.Getenv(host.CopilotBinEnv); env != "" {
			return env, nil
		}
		binName = "copilot"
		envName = host.CopilotBinEnv
	default:
		if env := os.Getenv(host.AgentBinEnv); env != "" {
			return env, nil
		}
	}
	path, err := exec.LookPath(binName)
	if err != nil {
		if requireInstalled {
			return "", fmt.Errorf("%s not found (set %s to override): %w", binName, envName, err)
		}
		return binName, nil
	}
	return path, nil
}

func resolveDelegatedLaunchBin(backend, fallback string, delegation *webconfig.AgentUserDelegationConfig, requireInstalled bool) (bin, runAsUser string, err error) {
	if !webconfig.AgentUserDelegationRuntimeEnabled || delegation == nil || !delegation.Enabled || strings.TrimSpace(delegation.RunAsUser) == "" {
		return fallback, "", nil
	}
	runAsUser = strings.TrimSpace(delegation.RunAsUser)
	wrapperBin := strings.TrimSpace(delegation.WrapperBin)
	if wrapperBin == "" {
		return "", "", fmt.Errorf("agent_user_delegation.wrapper_bin is required for kitsoki agent launch run_as_user execution")
	}
	wrapper := filepath.Join(wrapperBin, launchBackendExecutableName(backend))
	if requireInstalled {
		st, statErr := os.Stat(wrapper)
		if statErr != nil {
			return "", "", fmt.Errorf("agent_user_delegation wrapper for backend %q not found at %s: %w", backend, wrapper, statErr)
		}
		if st.IsDir() || st.Mode().Perm()&0111 == 0 {
			return "", "", fmt.Errorf("agent_user_delegation wrapper for backend %q is not executable: %s", backend, wrapper)
		}
	}
	return wrapper, runAsUser, nil
}

func launchBackendExecutableName(backend string) string {
	switch backend {
	case "codex", "copilot", "agy":
		return backend
	default:
		return "claude"
	}
}

func parseLaunchEnv(entries []string) (map[string]string, error) {
	out := map[string]string{}
	for _, entry := range entries {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("--env must be KEY=VALUE, got %q", entry)
		}
		out[k] = v
	}
	return out, nil
}

func redactEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if isSecretEnvKey(k) {
			out[k] = "<redacted>"
		} else {
			out[k] = env[k]
		}
	}
	return out
}

func isSecretEnvKey(k string) bool {
	upper := strings.ToUpper(k)
	return strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET")
}

func launchProfileForBackend(profileName string, profile orchestrator.HarnessProfile, backend, explicitProfile, explicitBackend string) (string, orchestrator.HarnessProfile, error) {
	if strings.TrimSpace(profileName) == "" || strings.TrimSpace(explicitBackend) == "" {
		return profileName, profile, nil
	}
	profileBackend := launchProfileBackend(profile)
	effectiveBackend := launchBackendName(backend)
	if profileBackend == effectiveBackend {
		return profileName, profile, nil
	}
	if strings.TrimSpace(explicitProfile) != "" {
		return "", orchestrator.HarnessProfile{}, fmt.Errorf("--profile %q selects backend %q, which conflicts with --backend %q", profileName, profileBackend, effectiveBackend)
	}
	return "", orchestrator.HarnessProfile{}, nil
}

func launchProfileBackend(profile orchestrator.HarnessProfile) string {
	if strings.TrimSpace(profile.Plugin) != "" {
		return "plugin:" + strings.TrimSpace(profile.Plugin)
	}
	return launchBackendName(profile.Backend)
}

func launchBackendName(backend string) string {
	backend = strings.TrimSpace(backend)
	if backend == "" {
		return "claude"
	}
	return backend
}

func launchModelForBackend(backend, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	switch launchBackendName(backend) {
	case "codex", "copilot", "agy":
		if host.IsClaudeModelID(model) {
			return ""
		}
	}
	return model
}

func writeAgentLaunchPlan(w interface{ Write([]byte) (int, error) }, plan agentLaunchPlan) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

func firstLaunchNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
