package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

func TestAgentLaunchPlan_UsesStoryAgentAndHarnessProfile(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	t.Setenv("SYNTHETIC_API_KEY", "secret-token")
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(`
app: { id: launch-test, version: 0.1.0, title: Launch Test }
hosts: [host.agent.task]
agents:
  maker:
    system_prompt: "You are the maker."
    model: claude-sonnet-4-6
    tools: [Read, Write]
    cwd: work
world: {}
intents: {}
root: idle
states:
  idle: { view: "idle" }
`), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "work"), 0755))
	cfgPath := filepath.Join(dir, ".kitsoki.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
default_profile: synthetic-codex
harness_profiles:
  synthetic-codex:
    backend: codex
    model: hf:test/model
    env:
      OPENAI_BASE_URL: https://api.synthetic.new/openai/v1
      OPENAI_API_KEY: ${SYNTHETIC_API_KEY}
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AppPath:    appPath,
		ConfigPath: cfgPath,
		AgentName:  "maker",
		Task:       "implement it",
	})
	require.NoError(t, err)
	for _, cleanup := range plan.cleanups {
		t.Cleanup(cleanup)
	}
	require.Equal(t, "synthetic-codex", plan.Profile)
	require.Equal(t, "codex", plan.Backend)
	require.Equal(t, "/bin/codex-test", plan.Binary)
	require.Equal(t, "hf:test/model", plan.Model, "profile model supersedes story-local Claude model")
	require.Equal(t, filepath.Join(dir, "work"), plan.WorkingDir)
	require.NotContains(t, plan.Stdin, "You are the maker.")
	require.Contains(t, plan.Stdin, "implement it")
	require.Contains(t, readCodexModelInstructionsFile(t, plan.Command), "You are the maker.")
	require.Equal(t, "<redacted>", plan.Env["OPENAI_API_KEY"])
	require.Equal(t, "https://api.synthetic.new/openai/v1", plan.Env["OPENAI_BASE_URL"])
	require.Contains(t, plan.Command, "exec")
	require.Contains(t, plan.Command, "-m")
	require.Contains(t, plan.Command, "hf:test/model")
	require.Contains(t, plan.Command, "-C")
	require.Contains(t, plan.Command, filepath.Join(dir, "work"))
}

func TestAgentLaunchPlan_ReadOnlyAgentDeniesMutationTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(host.AgentBinEnv, "/bin/claude-test")
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(`
app: { id: launch-test, version: 0.1.0, title: Launch Test }
hosts: [host.agent.ask]
agents:
  reviewer:
    system_prompt: "Review only."
    tools: [Read, Grep, Glob]
    external_side_effect: false
world: {}
intents: {}
root: idle
states:
  idle: { view: "idle" }
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AppPath:    appPath,
		ConfigPath: filepath.Join(dir, ".kitsoki.yaml"),
		AgentName:  "reviewer",
		Task:       "look around",
	})
	require.NoError(t, err)
	require.Equal(t, "claude", plan.Backend)
	require.Equal(t, "/bin/claude-test", plan.Binary)
	require.Contains(t, plan.Command, "--permission-mode")
	require.Contains(t, plan.Command, "default")
	denied := flagValue(t, plan.Command, "--disallowedTools")
	require.Contains(t, denied, "Agent")
	require.Contains(t, denied, "AskUserQuestion")
	require.Contains(t, denied, "Bash")
	require.Contains(t, denied, "Write")
}

func TestAgentLaunchPlan_CodeactModeStoryAgentOnlyAllowsCodeactTool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(host.AgentBinEnv, "/bin/claude-test")
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(`
app: { id: launch-test, version: 0.1.0, title: Launch Test }
hosts: [host.agent.task]
agents:
  maker:
    system_prompt: "You are the maker."
    model: claude-sonnet-4-6
    tools: [Read, Write]
    cwd: work
world: {}
intents: {}
root: idle
states:
  idle: { view: "idle" }
`), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "work"), 0755))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AppPath:    appPath,
		ConfigPath: filepath.Join(dir, ".kitsoki.yaml"),
		AgentName:  "maker",
		Backend:    "claude",
		Mode:       "codeact",
		Task:       "implement it without shell",
	})
	require.NoError(t, err)
	for _, cleanup := range plan.cleanups {
		t.Cleanup(cleanup)
	}
	require.Equal(t, "claude", plan.Backend)
	require.Equal(t, "codeact", plan.Mode)
	require.Equal(t, []string{launchCodeactMCPToolName}, plan.Tools)
	require.Contains(t, flagValue(t, plan.Command, "--allowedTools"), launchCodeactMCPToolName)
	require.NotContains(t, flagValue(t, plan.Command, "--allowedTools"), "Bash")
	require.NotContains(t, flagValue(t, plan.Command, "--allowedTools"), "Write")
	denied := flagValue(t, plan.Command, "--disallowedTools")
	require.Contains(t, denied, "Bash")
	require.Contains(t, denied, "Write")
	require.Contains(t, denied, "Edit")
	require.Contains(t, plan.Command, "--permission-mode")
	require.Contains(t, plan.Command, "default")
	systemPrompt := flagValue(t, plan.Command, "--system-prompt")
	require.Contains(t, systemPrompt, "You have no Bash")
	require.Contains(t, systemPrompt, `Every codeact_eval call takes {"snippet": "..."`)
	require.Contains(t, systemPrompt, `ctx.probe("git.status")`)
	require.Contains(t, systemPrompt, "Typical edit snippet")
	require.Contains(t, systemPrompt, "Starlark is not Python")

	servers := readLaunchMCPServers(t, plan.Command)
	require.ElementsMatch(t, []string{launchCodeactMCPServerName}, mapKeys(servers))
	server := servers[launchCodeactMCPServerName]
	require.Equal(t, "kitsoki", server.Command)
	require.Equal(t, []string{
		"mcp-codeact",
		"--working-dir", filepath.Join(dir, "work"),
		"--capabilities-json", launchCodeactDefaultCapabilitiesJSON,
	}, server.Args)
}

func TestAgentLaunchPlan_CodeactModeUsesCodexWithShellToolDisabled(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.AgentBinEnv, "/bin/claude-test")
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kitsoki.yaml"), []byte(`
default_profile: codex-default
harness_profiles:
  codex-default:
    backend: codex
    model: gpt-5.5
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "codeact-worker.toml"), []byte(`
name = "codeact-worker"
developer_instructions = "Use the available code action surface."
model = "gpt-5.5"

[mcp_servers.existing]
command = "node"
args = ["server.js"]
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName:  "codeact-worker",
		ConfigPath: filepath.Join(dir, ".kitsoki.yaml"),
		Mode:       "codeact",
		Task:       "edit files",
	})
	require.NoError(t, err)
	for _, cleanup := range plan.cleanups {
		t.Cleanup(cleanup)
	}
	require.Equal(t, "codex", plan.Backend)
	require.Equal(t, "/bin/codex-test", plan.Binary)
	require.Equal(t, "codex-default", plan.Profile)
	require.Equal(t, "gpt-5.5", plan.Model)
	require.Equal(t, []string{launchCodeactMCPToolName}, plan.Tools)
	joined := strings.Join(plan.Command, " ")
	require.NotContains(t, joined, "mcp_servers.existing")
	require.Contains(t, plan.Command, codexBypassApprovalsAndSandboxFlag)
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.Contains(t, plan.Command, "--disable="+launchCodexAppsFeature)
	require.Contains(t, joined, "mcp_servers.kitsoki-codeact.command=\"kitsoki\"")
	require.Contains(t, joined, "mcp-codeact")
	require.Contains(t, joined, "gpt-5.5")
	require.Contains(t, strings.Join(plan.FutureNotes, " "), "--disable shell_tool")
}

func TestAgentLaunchPlan_CodeactModeDefaultsToCodexOverImplicitClaudeProfile(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.AgentBinEnv, "/bin/claude-test")
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kitsoki.yaml"), []byte(`
default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: opus
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "codeact-worker.toml"), []byte(`
name = "codeact-worker"
developer_instructions = "Use the available code action surface."
model = "gpt-5.5"
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName:  "codeact-worker",
		ConfigPath: filepath.Join(dir, ".kitsoki.yaml"),
		Mode:       "codeact",
		Task:       "edit files",
	})
	require.NoError(t, err)
	for _, cleanup := range plan.cleanups {
		t.Cleanup(cleanup)
	}
	require.Equal(t, "codex", plan.Backend)
	require.Equal(t, "/bin/codex-test", plan.Binary)
	require.Empty(t, plan.Profile)
	require.Equal(t, "gpt-5.5", plan.Model)
	joined := strings.Join(plan.Command, " ")
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.Contains(t, plan.Command, "--disable="+launchCodexAppsFeature)
	require.NotContains(t, joined, "opus")
	require.NotContains(t, joined, "claude-native")
}

func TestAgentLaunchPlan_CodeactModeRejectsBackendWithoutHardToolRemoval(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "codeact-worker.toml"), []byte(`
name = "codeact-worker"
developer_instructions = "Use the available code action surface."
`), 0644))

	_, err = buildAgentLaunchPlan(agentLaunchOptions{
		AgentName: "codeact-worker",
		Backend:   "copilot",
		Mode:      "codeact",
		Task:      "edit files",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot hard-remove shell access")
}

func TestAgentLaunchPlan_CodeactModeFreestandingInteractiveUsesCodexWithoutShellTool(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "codeact-worker.toml"), []byte(`
name = "codeact-worker"
developer_instructions = "Use the available code action surface."
model = "gpt-5.5"

[mcp_servers.existing]
command = "node"
args = ["server.js"]
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName: "codeact-worker",
		Mode:      "codeact",
	})
	require.NoError(t, err)
	for _, cleanup := range plan.cleanups {
		t.Cleanup(cleanup)
	}
	require.True(t, plan.Interactive)
	require.Equal(t, "codex", plan.Backend)
	require.Equal(t, "codeact", plan.Mode)
	require.Equal(t, []string{launchCodeactMCPToolName}, plan.Tools)
	require.NotContains(t, plan.Command, "exec", "interactive CodeAct must use top-level codex, not codex exec")
	require.Contains(t, plan.Command, codexBypassApprovalsAndSandboxFlag)
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.Contains(t, plan.Command, "--disable="+launchCodexAppsFeature)
	joined := strings.Join(plan.Command, " ")
	require.NotContains(t, joined, "mcp_servers.existing")
	require.Contains(t, joined, "mcp_servers.kitsoki-codeact.command=\"kitsoki\"")
	require.Contains(t, joined, "mcp-codeact")
	require.Contains(t, plan.Command[len(plan.Command)-1], "Use the available code action surface.")
	require.Contains(t, plan.Command[len(plan.Command)-1], "You have no Bash")
	require.Contains(t, plan.Command[len(plan.Command)-1], `Every codeact_eval call takes {"snippet": "..."`)
	require.Contains(t, plan.Command[len(plan.Command)-1], `ctx.fs.write(path, new)`)
	require.Contains(t, plan.Command[len(plan.Command)-1], "Do not try to run tests")
	require.Contains(t, strings.Join(plan.FutureNotes, " "), "--disable shell_tool")
}

func TestAgentLaunchPlan_CodeactModeFreestandingInteractiveRejectsClaude(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(host.AgentBinEnv, "/bin/claude-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "codeact-worker.toml"), []byte(`
name = "codeact-worker"
developer_instructions = "Use the available code action surface."
`), 0644))

	_, err = buildAgentLaunchPlan(agentLaunchOptions{
		AgentName: "codeact-worker",
		Backend:   "claude",
		Mode:      "codeact",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "interactive freestanding launch currently supports --backend codex")
}

func TestAgentLaunchPlan_FreestandingCodexAgentAttachesMCP(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	codexHome := filepath.Join(dir, "codex-home")
	require.NoError(t, os.MkdirAll(codexHome, 0755))
	t.Setenv("CODEX_HOME", codexHome)
	require.NoError(t, os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(`
[mcp_servers.kitsoki]
command = "/old/kitsoki"

[mcp_servers.slidey]
command = "/bin/slidey"

[mcp_servers.codex_app]
command = "/bin/codex-app"
`), 0644))
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
description = "Drive kitsoki through MCP."
developer_instructions = """
Use ONLY the kitsoki studio MCP.
Start with studio.ping.
"""
model = "gpt-5.5"
model_reasoning_effort = "medium"
sandbox_mode = "read-only"

[mcp_servers.kitsoki]
command = "kitsoki"
args = ["mcp", "--stories-dir", "stories"]
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName: "kitsoki-mcp-driver",
		Task:      "Call studio.ping and report the version.",
	})
	require.NoError(t, err)
	for _, cleanup := range plan.cleanups {
		t.Cleanup(cleanup)
	}
	require.False(t, plan.Interactive)
	require.Empty(t, plan.App)
	wantAgentFile, err := filepath.Abs(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"))
	require.NoError(t, err)
	wantAgentFile, err = filepath.EvalSymlinks(wantAgentFile)
	require.NoError(t, err)
	require.Equal(t, wantAgentFile, plan.AgentFile)
	require.Equal(t, "codex", plan.Backend)
	require.Equal(t, "/bin/codex-test", plan.Binary)
	require.Equal(t, "gpt-5.5", plan.Model)
	require.Equal(t, "medium", plan.Effort)
	wantWorkingDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.Equal(t, wantWorkingDir, plan.WorkingDir)
	require.Contains(t, plan.Stdin, "tool_search")
	require.Contains(t, plan.Stdin, "Call studio.ping")
	require.Contains(t, readCodexModelInstructionsFile(t, plan.Command), "Use ONLY the kitsoki studio MCP.")
	joined := strings.Join(plan.Command, " ")
	require.Contains(t, joined, "mcp_servers.kitsoki.command=\"kitsoki\"")
	require.Contains(t, joined, "mcp_servers.kitsoki.args=[\"mcp\",\"--stories-dir\",\"stories\"]")
	require.Contains(t, joined, "mcp_servers.kitsoki.enabled=true")
	require.Contains(t, joined, "mcp_servers.slidey={")
	require.Contains(t, joined, "mcp_servers.codex_app={")
	require.Contains(t, joined, "enabled=false")
	require.Contains(t, plan.Command, "--dangerously-bypass-approvals-and-sandbox")
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.Contains(t, plan.Command, "--disable="+launchCodexAppsFeature)
	require.Contains(t, strings.Join(plan.FutureNotes, " "), "--disable shell_tool")
}

func TestAgentLaunchPlan_FreestandingCodexAgentDisablesInheritedMCPServers(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".codex"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(`
[mcp_servers.extra-alpha]
command = "/tmp/extra-alpha-mcp"
args = ["--root", "/tmp/old-root"]
cwd = "/tmp/old-root"
startup_timeout_sec = 120
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte(`
[mcp_servers.extra-beta]
command = "node"
args = ["server.mjs"]

[mcp_servers.extra-beta.env]
KITSOKI_REPO = "/tmp/old-root"
KITSOKI_AGENT_CMD = "kitsoki"
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
developer_instructions = "Use ONLY the kitsoki studio MCP."
model = "gpt-5.5"

[mcp_servers.kitsoki]
command = "kitsoki"
args = ["mcp", "--stories-dir", "stories"]
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName: "kitsoki-mcp-driver",
		Task:      "Call studio.ping.",
	})
	require.NoError(t, err)
	for _, cleanup := range plan.cleanups {
		t.Cleanup(cleanup)
	}
	joined := strings.Join(plan.Command, " ")
	require.Contains(t, joined, "mcp_servers.extra-alpha={")
	require.Contains(t, joined, `command="/tmp/extra-alpha-mcp"`)
	require.Contains(t, joined, `enabled=false`)
	require.Contains(t, joined, "mcp_servers.extra-beta={")
	require.Contains(t, joined, `env={"KITSOKI_AGENT_CMD"="kitsoki","KITSOKI_REPO"="/tmp/old-root"}`)
	require.Contains(t, joined, "mcp_servers.kitsoki.enabled=true")
	require.Contains(t, joined, "mcp_servers.kitsoki.command=\"kitsoki\"")
	require.Contains(t, joined, "mcp_servers.kitsoki.cwd="+launchTOMLString(plan.WorkingDir))
	require.NotContains(t, joined, "mcp_servers.extra-alpha.enabled=true")
}

func TestAgentLaunchPlan_FreestandingCodexAgentInteractive(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	codexHome := filepath.Join(dir, "codex-home")
	require.NoError(t, os.MkdirAll(codexHome, 0755))
	t.Setenv("CODEX_HOME", codexHome)
	require.NoError(t, os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(`
[mcp_servers.slidey]
command = "/bin/slidey"
`), 0644))
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
developer_instructions = "Use ONLY the kitsoki studio MCP."
model = "gpt-5.5"
model_reasoning_effort = "medium"
sandbox_mode = "read-only"

[mcp_servers.kitsoki]
command = "kitsoki"
args = ["mcp", "--stories-dir", "stories"]
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName: "kitsoki-mcp-driver",
	})
	require.NoError(t, err)
	require.True(t, plan.Interactive)
	require.Equal(t, "codex", plan.Backend)
	require.Equal(t, "/bin/codex-test", plan.Binary)
	require.Empty(t, plan.RunAsUser)
	require.Empty(t, plan.Stdin)
	require.NotContains(t, plan.Command, "exec", "interactive launch must use top-level codex, not codex exec")
	require.Contains(t, plan.Command, codexBypassApprovalsAndSandboxFlag)
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.Contains(t, plan.Command, "--disable="+launchCodexAppsFeature)
	require.Contains(t, plan.Command, "-m")
	require.Contains(t, plan.Command, "gpt-5.5")
	require.NotContains(t, plan.Command, "--sandbox")
	require.NotContains(t, plan.Command, "read-only")
	require.Contains(t, plan.Command, "model_reasoning_effort=\"medium\"")
	joined := strings.Join(plan.Command, " ")
	require.Contains(t, joined, "mcp_servers.kitsoki.command=\"kitsoki\"")
	require.Contains(t, joined, "mcp_servers.kitsoki.args=[\"mcp\",\"--stories-dir\",\"stories\"]")
	require.Contains(t, joined, "mcp_servers.kitsoki.enabled=true")
	require.Contains(t, joined, "mcp_servers.slidey={")
	require.Contains(t, joined, "enabled=false")
	require.Contains(t, plan.Command[len(plan.Command)-1], "Use ONLY the kitsoki studio MCP.")
	require.Contains(t, strings.Join(plan.FutureNotes, " "), "--disable shell_tool")
}

func TestAgentLaunchPlan_FreestandingCodexAgentInteractiveDisablesInheritedMCPServers(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".codex"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(`
[mcp_servers.extra-alpha]
command = "/tmp/extra-alpha-mcp"
args = ["--root", "/tmp/old-root"]
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
developer_instructions = "Use ONLY the kitsoki studio MCP."
model = "gpt-5.5"

[mcp_servers.kitsoki]
command = "kitsoki"
args = ["mcp", "--stories-dir", "stories"]
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName: "kitsoki-mcp-driver",
	})
	require.NoError(t, err)
	require.True(t, plan.Interactive)
	joined := strings.Join(plan.Command, " ")
	require.Contains(t, joined, "mcp_servers.extra-alpha={")
	require.Contains(t, joined, `enabled=false`)
	require.Contains(t, joined, "mcp_servers.kitsoki.enabled=true")
	require.Contains(t, joined, "mcp_servers.kitsoki.cwd="+launchTOMLString(plan.WorkingDir))
	require.Contains(t, plan.Command, "--disable="+launchCodexAppsFeature)
}

func TestAgentLaunchPlan_FreestandingCodexAgentInteractiveIgnoresRunAsUserWhileDisabled(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "agent-bin"), 0755))
	wrapper := filepath.Join(dir, "agent-bin", "codex")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\nexec /usr/bin/sudo -n -H -u kitsoki-agent /bin/codex \"$@\"\n"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kitsoki.yaml"), []byte("story_dirs: []\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kitsoki.local.yaml"), []byte(`
agent_user_delegation:
  enabled: true
  run_as_user: kitsoki-agent
  wrapper_bin: ./agent-bin
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
developer_instructions = "Use ONLY the kitsoki studio MCP."
model = "gpt-5.5"
sandbox_mode = "read-only"

[mcp_servers.kitsoki]
command = "kitsoki"
args = ["mcp", "--stories-dir", "stories"]
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		ConfigPath: filepath.Join(dir, ".kitsoki.yaml"),
		AgentName:  "kitsoki-mcp-driver",
	})
	require.NoError(t, err)
	require.True(t, plan.Interactive)
	require.Empty(t, plan.RunAsUser)
	require.Equal(t, "/bin/codex-test", plan.Binary)
	require.Equal(t, "/bin/codex-test", plan.Command[0])
	require.NotEqual(t, wrapper, plan.Binary)
	require.Contains(t, plan.Command, codexBypassApprovalsAndSandboxFlag)
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.Contains(t, plan.Command, "--disable="+launchCodexAppsFeature)
	require.NotContains(t, plan.Command, "--sandbox")
	require.NotContains(t, plan.Command, "read-only")
	require.Contains(t, plan.Command[len(plan.Command)-1], "Use ONLY the kitsoki studio MCP.")
}

func TestAgentLaunchPlan_FreestandingCodexBackendIgnoresMismatchedDefaultProfile(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kitsoki.yaml"), []byte(`
default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: opus
    effort: high
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
developer_instructions = "Use ONLY the kitsoki studio MCP."
model = "gpt-5.5"
model_reasoning_effort = "medium"
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName:   "kitsoki-mcp-driver",
		ConfigPath:  filepath.Join(dir, ".kitsoki.yaml"),
		Backend:     "codex",
		Interactive: true,
		WorkingDir:  ".",
	})
	require.NoError(t, err)
	require.True(t, plan.Interactive)
	require.Equal(t, "codex", plan.Backend)
	require.Empty(t, plan.Profile)
	require.Equal(t, "gpt-5.5", plan.Model)
	require.Equal(t, "medium", plan.Effort)
	joined := strings.Join(plan.Command, " ")
	require.NotContains(t, joined, "opus")
	require.NotContains(t, joined, "claude-native")
	require.Contains(t, plan.Command, "-m")
	require.Contains(t, plan.Command, "gpt-5.5")
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.NotContains(t, plan.Command, "--disable="+launchCodexAppsFeature)
}

func TestAgentLaunchPlan_FreestandingCodexInteractiveDropsClaudeAgentModel(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
developer_instructions = "Use ONLY the kitsoki studio MCP."
model = "opus"
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		AgentName:   "kitsoki-mcp-driver",
		Backend:     "codex",
		Interactive: true,
		WorkingDir:  ".",
	})
	require.NoError(t, err)
	require.True(t, plan.Interactive)
	require.Equal(t, "codex", plan.Backend)
	require.Empty(t, plan.Model)
	joined := strings.Join(plan.Command, " ")
	require.NotContains(t, joined, "opus")
	require.NotContains(t, plan.Command, "-m")
	require.Contains(t, plan.Command, "--disable="+launchCodexShellToolFeature)
	require.NotContains(t, plan.Command, "--disable="+launchCodexAppsFeature)
}

func TestAgentLaunchPlan_ExplicitProfileBackendConflict(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kitsoki.yaml"), []byte(`
default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: opus
`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".codex", "agents", "kitsoki-mcp-driver.toml"), []byte(`
name = "kitsoki-mcp-driver"
developer_instructions = "Use ONLY the kitsoki studio MCP."
`), 0644))

	_, err = buildAgentLaunchPlan(agentLaunchOptions{
		AgentName:   "kitsoki-mcp-driver",
		ConfigPath:  filepath.Join(dir, ".kitsoki.yaml"),
		Profile:     "claude-native",
		Backend:     "codex",
		Interactive: true,
		WorkingDir:  ".",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `--profile "claude-native" selects backend "claude", which conflicts with --backend "codex"`)
}

func TestAgentLaunchPlan_RawInteractiveNoAgentPrompt(t *testing.T) {
	dir := t.TempDir()
	isolateLaunchCodexHome(t, dir)
	t.Setenv(host.CodexBinEnv, "/bin/codex-test")
	t.Setenv("RAW_OPENAI_API_KEY", "secret-token")
	cfgPath := filepath.Join(dir, ".kitsoki.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
default_profile: desktop
harness_profiles:
  desktop:
    backend: codex
    model: gpt-5.5
    effort: medium
    env:
      OPENAI_API_KEY: ${RAW_OPENAI_API_KEY}
`), 0644))

	plan, err := buildAgentLaunchPlan(agentLaunchOptions{
		ConfigPath:     cfgPath,
		RawInteractive: true,
		WorkingDir:     dir,
		AddDirs:        []string{filepath.Join(dir, "extra")},
	})
	require.NoError(t, err)
	require.True(t, plan.Interactive)
	require.Equal(t, "raw", plan.Agent)
	require.Empty(t, plan.App)
	require.Empty(t, plan.AgentFile)
	require.Equal(t, "desktop", plan.Profile)
	require.Equal(t, "codex", plan.Backend)
	require.Equal(t, "/bin/codex-test", plan.Binary)
	require.Empty(t, plan.Stdin)
	require.Equal(t, "<redacted>", plan.Env["OPENAI_API_KEY"])
	require.Contains(t, plan.Command, "-m")
	require.Contains(t, plan.Command, "gpt-5.5")
	require.Contains(t, plan.Command, "-C")
	require.Contains(t, plan.Command, dir)
	require.Contains(t, plan.Command, "--add-dir")
	require.Contains(t, plan.Command, filepath.Join(dir, "extra"))
	joined := strings.Join(plan.Command, " ")
	require.Contains(t, joined, codexBypassApprovalsAndSandboxFlag)
	require.NotContains(t, joined, "--disable="+launchCodexShellToolFeature)
	require.NotContains(t, joined, "--disable="+launchCodexAppsFeature)
	require.NotContains(t, joined, "developer_instructions")
	require.NotContains(t, joined, "Use ONLY")
}

func TestAgentLaunchPlan_LaunchPolicyDeniesRawInteractive(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".kitsoki.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
agent_launch_policy:
  enabled: true
  require_capsule: true
  protected_branches: [release]
`), 0644))

	_, err := buildAgentLaunchPlan(agentLaunchOptions{
		ConfigPath:     cfgPath,
		RawInteractive: true,
		WorkingDir:     dir,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent launch policy denied")
	require.Contains(t, err.Error(), "inside protected root")
}

func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("flag %s not found in %v", flag, args)
	return ""
}

func readCodexModelInstructionsFile(t *testing.T, args []string) string {
	t.Helper()
	path := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-c" {
			continue
		}
		if v, ok := strings.CutPrefix(args[i+1], "model_instructions_file="); ok {
			path = strings.Trim(v, `"`)
			break
		}
	}
	require.NotEmpty(t, path, "codex command missing model_instructions_file override: %v", args)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

type launchMCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func readLaunchMCPServers(t *testing.T, args []string) map[string]launchMCPServer {
	t.Helper()
	path := flagValue(t, args, "--mcp-config")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg struct {
		MCPServers map[string]launchMCPServer `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	return cfg.MCPServers
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func isolateLaunchCodexHome(t *testing.T, dir string) {
	t.Helper()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0755))
	t.Setenv("HOME", home)
}
