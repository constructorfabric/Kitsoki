package app_test

// loader_oracle_plugins_test.go — unit tests for oracle_plugins: parsing,
// ${VAR} substitution, default injection, and fast-fail on unset env vars.
//
// All tests use LoadBytes (in-memory parse) so no disk I/O is needed.
// LoadBytes calls resolveOraclePlugins as part of its pipeline.

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
)

const minimalApp = `
app:
  id: test-oracle-plugins
  version: 0.1.0
root: idle
states:
  idle:
    terminal: true
`

// TestOraclePlugins_DefaultInjected verifies that when oracle_plugins: is
// absent from the YAML, the loader injects oracle.claude with
// plugin: builtin.claude_cli.
func TestOraclePlugins_DefaultInjected(t *testing.T) {
	t.Parallel()
	def, err := app.LoadBytes([]byte(minimalApp))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if def.OraclePlugins == nil {
		t.Fatal("OraclePlugins: expected non-nil map after default injection")
	}
	plug, ok := def.OraclePlugins["oracle.claude"]
	if !ok {
		t.Fatal("OraclePlugins: missing default 'oracle.claude' entry")
	}
	if plug.Plugin != "builtin.claude_cli" {
		t.Errorf("oracle.claude plugin: got %q, want %q", plug.Plugin, "builtin.claude_cli")
	}
}

// TestOraclePlugins_ExplicitClaudeCLI verifies that an explicit
// oracle.claude declaration with builtin.claude_cli loads correctly.
func TestOraclePlugins_ExplicitClaudeCLI(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  oracle.claude:
    plugin: builtin.claude_cli
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.OraclePlugins["oracle.claude"]
	if !ok {
		t.Fatal("missing oracle.claude")
	}
	if plug.Plugin != "builtin.claude_cli" {
		t.Errorf("plugin: got %q, want builtin.claude_cli", plug.Plugin)
	}
}

// TestOraclePlugins_InProcess verifies that builtin.inprocess loads correctly.
func TestOraclePlugins_InProcess(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  oracle.test_inproc:
    plugin: builtin.inprocess
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.OraclePlugins["oracle.test_inproc"]
	if !ok {
		t.Fatal("missing oracle.test_inproc")
	}
	if plug.Plugin != "builtin.inprocess" {
		t.Errorf("plugin: got %q, want builtin.inprocess", plug.Plugin)
	}
	// Default oracle.claude should also be injected.
	if _, hasDefault := def.OraclePlugins["oracle.claude"]; !hasDefault {
		t.Error("default oracle.claude not injected alongside oracle.test_inproc")
	}
}

// TestOraclePlugins_EnvVarSubstitution verifies that ${VAR} tokens in env:
// are expanded when the env var is set.
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestOraclePlugins_EnvVarSubstitution(t *testing.T) {
	t.Setenv("TEST_ORACLE_TOKEN_B2", "secret-value")
	yaml := minimalApp + `
oracle_plugins:
  oracle.claude:
    plugin: builtin.claude_cli
    env:
      API_TOKEN: "${TEST_ORACLE_TOKEN_B2}"
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug := def.OraclePlugins["oracle.claude"]
	if plug.Env["API_TOKEN"] != "secret-value" {
		t.Errorf("env.API_TOKEN: got %q, want %q", plug.Env["API_TOKEN"], "secret-value")
	}
}

// TestOraclePlugins_UnsetEnvVar verifies that an unset ${VAR} token causes
// story load to fail fast with a clear error message.
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestOraclePlugins_UnsetEnvVar(t *testing.T) {
	// Ensure the var is not set by using a unique enough name.
	yaml := minimalApp + `
oracle_plugins:
  oracle.claude:
    plugin: builtin.claude_cli
    env:
      SECRET: "${UNSET_VAR_THAT_DOES_NOT_EXIST_B2_ORACLE}"
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unset env var, got nil")
	}
	if !strings.Contains(err.Error(), "UNSET_VAR_THAT_DOES_NOT_EXIST_B2_ORACLE") {
		t.Errorf("error should mention the missing var name; got: %v", err)
	}
}

// TestOraclePlugins_InvalidPrefix verifies that plugin keys without "oracle."
// prefix are rejected.
func TestOraclePlugins_InvalidPrefix(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  myoracle:
    plugin: builtin.claude_cli
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid prefix, got nil")
	}
	if !strings.Contains(err.Error(), "oracle.") {
		t.Errorf("error should mention 'oracle.' prefix requirement; got: %v", err)
	}
}

// TestOraclePlugins_UnknownPlugin verifies that an unrecognised plugin value
// fails story load.
func TestOraclePlugins_UnknownPlugin(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  oracle.test:
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

// TestOraclePlugins_SubprocessMissingCommand verifies that a subprocess plugin
// without a command: field is rejected.
func TestOraclePlugins_SubprocessMissingCommand(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  oracle.external:
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

// TestOraclePlugins_MCPHTTPMissingEndpoint verifies that a mcp_http plugin
// without an endpoint: field is rejected.
func TestOraclePlugins_MCPHTTPMissingEndpoint(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  oracle.external:
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

// TestOraclePlugins_SubprocessAccepted verifies that a subprocess plugin with
// command: is accepted by the loader.
func TestOraclePlugins_SubprocessAccepted(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  oracle.external:
    plugin: subprocess
    command: /usr/bin/my-oracle
    args: ["--mode", "fast"]
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.OraclePlugins["oracle.external"]
	if !ok {
		t.Fatal("oracle.external not found in OraclePlugins")
	}
	if plug.Command != "/usr/bin/my-oracle" {
		t.Errorf("Command: got %q, want /usr/bin/my-oracle", plug.Command)
	}
}

// TestOraclePlugins_MCPHTTPAccepted verifies that a mcp_http plugin with
// endpoint: is accepted by the loader.
func TestOraclePlugins_MCPHTTPAccepted(t *testing.T) {
	t.Parallel()
	yaml := minimalApp + `
oracle_plugins:
  oracle.external:
    plugin: mcp_http
    endpoint: "http://localhost:7301/mcp"
    tool: ask
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug, ok := def.OraclePlugins["oracle.external"]
	if !ok {
		t.Fatal("oracle.external not found in OraclePlugins")
	}
	if plug.Endpoint != "http://localhost:7301/mcp" {
		t.Errorf("Endpoint: got %q, want http://localhost:7301/mcp", plug.Endpoint)
	}
	if plug.Tool != "ask" {
		t.Errorf("Tool: got %q, want ask", plug.Tool)
	}
}

// TestOraclePlugins_HeadersSubstitution verifies ${VAR} in headers.
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestOraclePlugins_HeadersSubstitution(t *testing.T) {
	t.Setenv("TEST_ORACLE_HEADER_B2", "bearer-xyz")
	yaml := minimalApp + `
oracle_plugins:
  oracle.claude:
    plugin: builtin.claude_cli
    headers:
      Authorization: "Bearer ${TEST_ORACLE_HEADER_B2}"
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug := def.OraclePlugins["oracle.claude"]
	if plug.Headers["Authorization"] != "Bearer bearer-xyz" {
		t.Errorf("headers.Authorization: got %q, want %q",
			plug.Headers["Authorization"], "Bearer bearer-xyz")
	}
}

// TestOraclePlugins_EnvVarWithLiteralDollarInValue pins the single-pass
// substitution rule:
// When a resolved env var VALUE itself contains "${", that literal "${" is
// NOT re-expanded.  The test sets OUTER_VAR to a value containing "${inner}"
// and verifies the expanded result still contains the literal "${inner}".
// Not parallel because t.Setenv and t.Parallel cannot coexist.
func TestOraclePlugins_EnvVarWithLiteralDollarInValue(t *testing.T) {
	// The env var value itself contains a literal "${" — single-pass means
	// this should pass through verbatim after the outer substitution.
	t.Setenv("OUTER_ORACLE_VAR_B5", "prefix_${inner_not_a_var}_suffix")
	yaml := minimalApp + `
oracle_plugins:
  oracle.claude:
    plugin: builtin.claude_cli
    env:
      API_KEY: "${OUTER_ORACLE_VAR_B5}"
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	plug := def.OraclePlugins["oracle.claude"]
	want := "prefix_${inner_not_a_var}_suffix"
	if plug.Env["API_KEY"] != want {
		t.Errorf("env.API_KEY: got %q, want %q", plug.Env["API_KEY"], want)
	}
	// The literal "${inner_not_a_var}" must be preserved verbatim (single-pass).
	if !strings.Contains(plug.Env["API_KEY"], "${inner_not_a_var}") {
		t.Errorf("literal ${ was re-expanded or stripped; got: %q", plug.Env["API_KEY"])
	}
}
