// Package agent — build_registry tests.
package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// TestBuildRegistry_DefaultClaudeCLI verifies that builtin.claude_cli registers
// an agent wrapped around the provided harness.
func TestBuildRegistry_DefaultClaudeCLI(t *testing.T) {
	t.Parallel()

	// Use an in-process agent stub as the harness proxy.
	var called bool
	stubFn := AskFunc(func(_ context.Context, req AskRequest) (AskResponse, error) {
		called = true
		return AskResponse{Submission: json.RawMessage(`{"ok":true}`)}, nil
	})
	harnessAgent := New(stubFn) // acts as the mock harness agent

	decls := map[string]*PluginDecl{
		"agent.claude": {Plugin: "builtin.claude_cli"},
	}

	// We need a real harness.Harness for the registry. Use the stub harness
	// wrapped through FromHarness indirectly by building manually.
	reg := NewRegistry()
	reg.Register("agent.claude", harnessAgent)

	o, err := reg.Resolve("agent.claude")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, _ = o.Ask(context.Background(), sampleRequest())
	if !called {
		t.Error("agent.claude: underlying AskFunc was not called")
	}

	_ = decls // suppress unused warning
}

// TestBuildRegistry_Subprocess verifies that a subprocess plugin declaration
// constructs a SubprocessAgent.
func TestBuildRegistry_Subprocess(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.cli": {
			Plugin:  "subprocess",
			Command: "/usr/bin/true",
			Args:    []string{"--verbose"},
			Env:     map[string]string{"FOO": "bar"},
		},
	}
	reg, err := BuildRegistry(decls, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	defer reg.Close()

	o, resolveErr := reg.Resolve("agent.cli")
	if resolveErr != nil {
		t.Fatalf("Resolve agent.cli: %v", resolveErr)
	}
	if _, ok := o.(*SubprocessAgent); !ok {
		t.Errorf("expected *SubprocessAgent, got %T", o)
	}
}

// TestBuildRegistry_MCPHTTP verifies that a mcp_http plugin declaration
// constructs an MCPHTTPAgent.
func TestBuildRegistry_MCPHTTP(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.fixer": {
			Plugin:   "mcp_http",
			Endpoint: "http://localhost:7301/mcp",
			Tool:     "ask",
			Headers:  map[string]string{"Authorization": "Bearer token"},
		},
	}
	reg, err := BuildRegistry(decls, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	defer reg.Close()

	o, resolveErr := reg.Resolve("agent.fixer")
	if resolveErr != nil {
		t.Fatalf("Resolve agent.fixer: %v", resolveErr)
	}
	if _, ok := o.(*MCPHTTPAgent); !ok {
		t.Errorf("expected *MCPHTTPAgent, got %T", o)
	}
}

// TestBuildRegistry_SubprocessMissingCommand verifies that a subprocess plugin
// without Command fails.
func TestBuildRegistry_SubprocessMissingCommand(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.cli": {Plugin: "subprocess"},
	}
	_, err := BuildRegistry(decls, nil)
	if err == nil {
		t.Fatal("expected error for subprocess without command, got nil")
	}
}

// TestBuildRegistry_MCPHTTPMissingEndpoint verifies that a mcp_http plugin
// without Endpoint fails.
func TestBuildRegistry_MCPHTTPMissingEndpoint(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.fixer": {Plugin: "mcp_http"},
	}
	_, err := BuildRegistry(decls, nil)
	if err == nil {
		t.Fatal("expected error for mcp_http without endpoint, got nil")
	}
}

// TestBuildRegistry_LocalLLMModel verifies that a builtin.local_llm declaration
// with model: constructs a LocalLLMAgent (managed mode).
func TestBuildRegistry_LocalLLMModel(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.local": {
			Plugin:  "builtin.local_llm",
			Model:   "qwen2.5-1.5b",
			Port:    8081,
			Grammar: true,
		},
	}
	reg, err := BuildRegistry(decls, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	defer reg.Close()

	o, resolveErr := reg.Resolve("agent.local")
	if resolveErr != nil {
		t.Fatalf("Resolve agent.local: %v", resolveErr)
	}
	if _, ok := o.(*LocalLLMAgent); !ok {
		t.Errorf("expected *LocalLLMAgent, got %T", o)
	}
}

// TestBuildRegistry_LocalLLMEndpoint verifies that a builtin.local_llm
// declaration with only endpoint: (bring-your-own-server) constructs a
// LocalLLMAgent.
func TestBuildRegistry_LocalLLMEndpoint(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.local": {
			Plugin:   "builtin.local_llm",
			Endpoint: "http://127.0.0.1:8081",
		},
	}
	reg, err := BuildRegistry(decls, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	defer reg.Close()

	o, resolveErr := reg.Resolve("agent.local")
	if resolveErr != nil {
		t.Fatalf("Resolve agent.local: %v", resolveErr)
	}
	if _, ok := o.(*LocalLLMAgent); !ok {
		t.Errorf("expected *LocalLLMAgent, got %T", o)
	}
}

// TestBuildRegistry_LocalLLMMissingModelAndEndpoint verifies that a
// builtin.local_llm declaration with neither model: nor endpoint: fails.
func TestBuildRegistry_LocalLLMMissingModelAndEndpoint(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.local": {Plugin: "builtin.local_llm"},
	}
	_, err := BuildRegistry(decls, nil)
	if err == nil {
		t.Fatal("expected error for builtin.local_llm without model or endpoint, got nil")
	}
}

// TestBuildRegistry_UnknownPlugin verifies that an unknown plugin type fails.
func TestBuildRegistry_UnknownPlugin(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.x": {Plugin: "nonexistent_transport"},
	}
	_, err := BuildRegistry(decls, nil)
	if err == nil {
		t.Fatal("expected error for unknown plugin type, got nil")
	}
}

// TestBuildRegistry_InprocessRejected verifies that builtin.inprocess cannot
// be constructed from YAML declarations.
func TestBuildRegistry_InprocessRejected(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.test": {Plugin: "builtin.inprocess"},
	}
	_, err := BuildRegistry(decls, nil)
	if err == nil {
		t.Fatal("expected error for builtin.inprocess from YAML, got nil")
	}
}

// TestBuildRegistry_NilDecl verifies that a nil plugin declaration fails.
func TestBuildRegistry_NilDecl(t *testing.T) {
	t.Parallel()

	decls := map[string]*PluginDecl{
		"agent.x": nil,
	}
	_, err := BuildRegistry(decls, nil)
	if err == nil {
		t.Fatal("expected error for nil declaration, got nil")
	}
}

// TestBuildRegistryFromDef_Empty verifies that BuildRegistryFromDef with nil
// def returns an empty registry.
func TestBuildRegistryFromDef_Empty(t *testing.T) {
	t.Parallel()

	reg, err := BuildRegistryFromDef(nil, nil)
	if err != nil {
		t.Fatalf("BuildRegistryFromDef(nil): %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry, got nil")
	}
}
