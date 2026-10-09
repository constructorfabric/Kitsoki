package app_test

// loader_agent_plugins_test.go — unit tests for agent_plugins: parsing,
// ${VAR} substitution, default injection, and fast-fail on unset env vars.
//
// All tests use LoadBytes (in-memory parse) so no disk I/O is needed.
// LoadBytes calls resolveAgentPlugins as part of its pipeline.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/app"
)

const minimalApp = `
app:
  id: test-agent-plugins
  version: 0.1.0
root: idle
states:
  idle:
    terminal: true
`

// TestAgentPlugins_DefaultInjected verifies that when agent_plugins: is
// absent from the YAML, the loader injects agent.claude with
// plugin: builtin.claude_cli.
func TestAgentPlugins_DefaultInjected(t *testing.T) {
	t.Parallel()
	def, err := app.LoadBytes([]byte(minimalApp))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if def.AgentPlugins == nil {
		t.Fatal("AgentPlugins: expected non-nil map after default injection")
	}
	plug, ok := def.AgentPlugins["agent.claude"]
	if !ok {
		t.Fatal("AgentPlugins: missing default 'agent.claude' entry")
	}
	if plug.Plugin != "builtin.claude_cli" {
		t.Errorf("agent.claude plugin: got %q, want %q", plug.Plugin, "builtin.claude_cli")
	}
}

// TestAgentPlugins_ExplicitClaudeCLI verifies that an explicit
// agent.claude declaration with builtin.claude_cli loads correctly.
func TestAgentPlugins_ExplicitClaudeCLI(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.claude:
    plugin: builtin.claude_cli
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.AgentPlugins["agent.claude"]
	if !ok {
		t.Fatal("missing agent.claude")
	}
	if plug.Plugin != "builtin.claude_cli" {
		t.Errorf("plugin: got %q, want builtin.claude_cli", plug.Plugin)
	}
}

// TestAgentPlugins_InProcess verifies that builtin.inprocess loads correctly.
func TestAgentPlugins_InProcess(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.test_inproc:
    plugin: builtin.inprocess
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.AgentPlugins["agent.test_inproc"]
	if !ok {
		t.Fatal("missing agent.test_inproc")
	}
	if plug.Plugin != "builtin.inprocess" {
		t.Errorf("plugin: got %q, want builtin.inprocess", plug.Plugin)
	}
	// Default agent.claude should also be injected.
	if _, hasDefault := def.AgentPlugins["agent.claude"]; !hasDefault {
		t.Error("default agent.claude not injected alongside agent.test_inproc")
	}
}

// TestAgentPlugins_EnvVarSubstitution verifies that ${VAR} tokens in env:
// are expanded when the env var is set.
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestAgentPlugins_EnvVarSubstitution(t *testing.T) {
	t.Setenv("TEST_AGENT_TOKEN_B2", "secret-value")
	yaml := minimalApp + `
agent_plugins:
  agent.claude:
    plugin: builtin.claude_cli
    env:
      API_TOKEN: "${TEST_AGENT_TOKEN_B2}"
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug := def.AgentPlugins["agent.claude"]
	if plug.Env["API_TOKEN"] != "secret-value" {
		t.Errorf("env.API_TOKEN: got %q, want %q", plug.Env["API_TOKEN"], "secret-value")
	}
}

// TestAgentPlugins_UnsetEnvVar verifies that an unset ${VAR} token causes
// story load to fail fast with a clear error message.
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestAgentPlugins_UnsetEnvVar(t *testing.T) {
	// Ensure the var is not set by using a unique enough name.
	yaml := minimalApp + `
agent_plugins:
  agent.claude:
    plugin: builtin.claude_cli
    env:
      SECRET: "${UNSET_VAR_THAT_DOES_NOT_EXIST_B2_AGENT}"
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unset env var, got nil")
	}
	if !strings.Contains(err.Error(), "UNSET_VAR_THAT_DOES_NOT_EXIST_B2_AGENT") {
		t.Errorf("error should mention the missing var name; got: %v", err)
	}
}

// TestAgentPlugins_InvalidPrefix verifies that plugin keys without "agent."
// prefix are rejected.
func TestAgentPlugins_InvalidPrefix(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  myagent:
    plugin: builtin.claude_cli
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid prefix, got nil")
	}
	if !strings.Contains(err.Error(), "agent.") {
		t.Errorf("error should mention 'agent.' prefix requirement; got: %v", err)
	}
}

// TestAgentPlugins_UnknownPlugin verifies that an unrecognised plugin value
// fails story load.
func TestAgentPlugins_UnknownPlugin(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.test:
    plugin: unknown_plugin_value
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown plugin, got nil")
	}
	if !strings.Contains(err.Error(), "unknown_plugin_value") {
		t.Errorf("error should mention the unknown plugin name; got: %v", err)
	}
}

// TestAgentPlugins_SubprocessMissingCommand verifies that a subprocess plugin
// without a command: field is rejected.
func TestAgentPlugins_SubprocessMissingCommand(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.external:
    plugin: subprocess
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for subprocess without command, got nil")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Errorf("error should mention 'command'; got: %v", err)
	}
}

// TestAgentPlugins_MCPHTTPMissingEndpoint verifies that a mcp_http plugin
// without an endpoint: field is rejected.
func TestAgentPlugins_MCPHTTPMissingEndpoint(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.external:
    plugin: mcp_http
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for mcp_http without endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention 'endpoint'; got: %v", err)
	}
}

// TestAgentPlugins_SubprocessAccepted verifies that a subprocess plugin with
// command: is accepted by the loader.
func TestAgentPlugins_SubprocessAccepted(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.external:
    plugin: subprocess
    command: /usr/bin/my-agent
    args: ["--mode", "fast"]
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.AgentPlugins["agent.external"]
	if !ok {
		t.Fatal("agent.external not found in AgentPlugins")
	}
	if plug.Command != "/usr/bin/my-agent" {
		t.Errorf("Command: got %q, want /usr/bin/my-agent", plug.Command)
	}
}

// TestAgentPlugins_MCPHTTPAccepted verifies that a mcp_http plugin with
// endpoint: is accepted by the loader.
func TestAgentPlugins_MCPHTTPAccepted(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.external:
    plugin: mcp_http
    endpoint: "http://localhost:7301/mcp"
    tool: ask
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.AgentPlugins["agent.external"]
	if !ok {
		t.Fatal("agent.external not found in AgentPlugins")
	}
	if plug.Endpoint != "http://localhost:7301/mcp" {
		t.Errorf("Endpoint: got %q, want http://localhost:7301/mcp", plug.Endpoint)
	}
	if plug.Tool != "ask" {
		t.Errorf("Tool: got %q, want ask", plug.Tool)
	}
}

// TestAgentPlugins_LocalLLMModelAccepted verifies that a builtin.local_llm
// plugin with model: (and the grammar/port/server_bin fields) is accepted and
// the fields are threaded onto the decl.
func TestAgentPlugins_LocalLLMModelAccepted(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.local:
    plugin: builtin.local_llm
    model: qwen2.5-1.5b
    grammar: true
    port: 8081
    server_bin: /opt/llama-server
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.AgentPlugins["agent.local"]
	if !ok {
		t.Fatal("agent.local not found in AgentPlugins")
	}
	if plug.Model != "qwen2.5-1.5b" {
		t.Errorf("Model: got %q, want qwen2.5-1.5b", plug.Model)
	}
	if !plug.Grammar {
		t.Error("Grammar: got false, want true")
	}
	if plug.Port != 8081 {
		t.Errorf("Port: got %d, want 8081", plug.Port)
	}
	if plug.ServerBin != "/opt/llama-server" {
		t.Errorf("ServerBin: got %q, want /opt/llama-server", plug.ServerBin)
	}
}

// TestAgentPlugins_LocalLLMEndpointAccepted verifies that a builtin.local_llm
// plugin with only endpoint: (bring-your-own-server) is accepted.
func TestAgentPlugins_LocalLLMEndpointAccepted(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.local:
    plugin: builtin.local_llm
    endpoint: "http://127.0.0.1:8081"
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if _, ok := def.AgentPlugins["agent.local"]; !ok {
		t.Fatal("agent.local not found in AgentPlugins")
	}
}

// TestAgentPlugins_LocalLLMMissingModelAndEndpoint verifies that a
// builtin.local_llm plugin with neither model: nor endpoint: is rejected.
func TestAgentPlugins_LocalLLMMissingModelAndEndpoint(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.local:
    plugin: builtin.local_llm
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for builtin.local_llm without model or endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "model") || !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error should mention 'model' or 'endpoint'; got: %v", err)
	}
}

// TestAgentPlugins_LocalLLMGrammarInSubsetSchema verifies that a grammar:true
// local_llm decide effect pointed at an in-subset schema loads cleanly.
func TestAgentPlugins_LocalLLMGrammarInSubsetSchema(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "verdict.json")
	const inSubset = `{
  "type": "object",
  "properties": {
    "intent": {"type": "string"},
    "confidence": {"type": "number"},
    "reason": {"type": "string"}
  },
  "required": ["intent"]
}`
	if err := os.WriteFile(schemaPath, []byte(inSubset), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	yaml := localLLMGrammarApp(schemaPath)
	if _, err := app.LoadBytes([]byte(yaml)); err != nil {
		t.Fatalf("LoadBytes: in-subset schema should load cleanly, got: %v", err)
	}
}

// TestAgentPlugins_LocalLLMGrammarOutOfSubsetSchema verifies that a
// grammar:true local_llm decide effect pointed at an OUT-of-subset schema
// (uses $ref) fails load with a message naming the plugin, schema, and the
// offending construct.
func TestAgentPlugins_LocalLLMGrammarOutOfSubsetSchema(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "verdict.json")
	const outOfSubset = `{
  "type": "object",
  "properties": {
    "intent": {"$ref": "#/$defs/intent"}
  }
}`
	if err := os.WriteFile(schemaPath, []byte(outOfSubset), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	yaml := localLLMGrammarApp(schemaPath)
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected load error for out-of-subset grammar schema, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "agent.local") {
		t.Errorf("error should name the plugin alias; got: %v", err)
	}
	if !strings.Contains(msg, "grammar subset") {
		t.Errorf("error should mention the grammar subset; got: %v", err)
	}
	if !strings.Contains(msg, "$ref") {
		t.Errorf("error should name the offending construct $ref; got: %v", err)
	}
}

// localLLMGrammarApp builds a minimal app with a grammar:true local_llm plugin
// and a single decide effect referencing the given absolute schema path.
func localLLMGrammarApp(schemaPath string) string {
	return `
app:
  id: test-local-llm-grammar
  version: 0.1.0
hosts: [host.agent.decide]
agents:
  judge:
    system_prompt: "You are a judge."
root: idle
agent_plugins:
  agent.local:
    plugin: builtin.local_llm
    model: qwen2.5-1.5b
    grammar: true
states:
  idle:
    on_enter:
      - invoke: host.agent.decide
        agent: agent.local
        with:
          agent: judge
          schema: ` + schemaPath + `
          prompt_text: "decide"
    terminal: true
`
}

// TestAgentPlugins_HeadersSubstitution verifies ${VAR} in headers.
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestAgentPlugins_HeadersSubstitution(t *testing.T) {
	t.Setenv("TEST_AGENT_HEADER_B2", "bearer-xyz")
	yaml := minimalApp + `
agent_plugins:
  agent.claude:
    plugin: builtin.claude_cli
    headers:
      Authorization: "Bearer ${TEST_AGENT_HEADER_B2}"
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug := def.AgentPlugins["agent.claude"]
	if plug.Headers["Authorization"] != "Bearer bearer-xyz" {
		t.Errorf("headers.Authorization: got %q, want %q",
			plug.Headers["Authorization"], "Bearer bearer-xyz")
	}
}

// TestAgentPlugins_EnvVarWithLiteralDollarInValue pins the single-pass
// substitution rule:
// When a resolved env var VALUE itself contains "${", that literal "${" is
// NOT re-expanded.  The test sets OUTER_VAR to a value containing "${inner}"
// and verifies the expanded result still contains the literal "${inner}".
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestAgentPlugins_EnvVarWithLiteralDollarInValue(t *testing.T) {
	// The env var value itself contains a literal "${" — single-pass means
	// this should pass through verbatim after the outer substitution.
	t.Setenv("OUTER_AGENT_VAR_B5", "prefix_${inner_not_a_var}_suffix")
	yaml := minimalApp + `
agent_plugins:
  agent.claude:
    plugin: builtin.claude_cli
    env:
      API_KEY: "${OUTER_AGENT_VAR_B5}"
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug := def.AgentPlugins["agent.claude"]
	want := "prefix_${inner_not_a_var}_suffix"
	if plug.Env["API_KEY"] != want {
		t.Errorf("env.API_KEY: got %q, want %q", plug.Env["API_KEY"], want)
	}
	// The literal "${inner_not_a_var}" must be preserved verbatim (single-pass).
	if !strings.Contains(plug.Env["API_KEY"], "${inner_not_a_var}") {
		t.Errorf("literal ${ was re-expanded or stripped; got: %q", plug.Env["API_KEY"])
	}
}

// TestAgentPlugins_LocalLLMAPIKeyAndJSONSchema verifies that the
// api_key_env / json_schema knobs parse on a builtin.local_llm plugin and are
// threaded onto the decl. Neither is required (the endpoint-only plugin in
// TestAgentPlugins_LocalLLMEndpointAccepted omits both without error).
func TestAgentPlugins_LocalLLMAPIKeyAndJSONSchema(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
agent_plugins:
  agent.glm:
    plugin: builtin.local_llm
    endpoint: "https://open.bigmodel.cn/api/paas/v4"
    model: glm-4.6
    api_key_env: GLM_API_KEY
    json_schema: true
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.AgentPlugins["agent.glm"]
	if !ok {
		t.Fatal("agent.glm not found in AgentPlugins")
	}
	if plug.APIKeyEnv != "GLM_API_KEY" {
		t.Errorf("APIKeyEnv: got %q, want GLM_API_KEY", plug.APIKeyEnv)
	}
	if !plug.JSONSchema {
		t.Error("JSONSchema: got false, want true")
	}
}
