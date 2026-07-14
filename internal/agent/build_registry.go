// build_registry.go assembles a Registry from an app's agent_plugins
// declarations at session construction time.
//
// Usage from an orchestrator setup:
//
//	reg, err := agent.BuildRegistryFromDef(def, harness)
//	orch := orchestrator.New(..., orchestrator.WithAgentRegistry(reg))
//
// BuildRegistry maps each declaration's plugin value to a transport
// constructor:
//
//   - "builtin.claude_cli" — FromHarness(h); requires h to be non-nil.
//   - "builtin.inprocess"  — not constructable from YAML alone; the caller
//     must build the in-process agent (via New) and inject it with
//     reg.Register before dispatch. Encountering it here returns an error.
//   - "cassette"           — also injected programmatically (the
//     implementation lives in internal/testrunner); returns an error here.
//   - "subprocess"         — NewSubprocess(command, args, env).
//   - "mcp_http"           — NewMCPHTTP(endpoint, tool, headers).
//   - "builtin.local_llm"  — NewLocalLLM(model, port, server_bin, grammar,
//     endpoint, env); requires either model: or endpoint:.

package agent

import (
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
)

// PluginDecl mirrors app.AgentPluginDecl to avoid an import cycle
// (agent → app would be circular; the caller passes a pre-converted struct).
type PluginDecl struct {
	Plugin   string
	Command  string
	Args     []string
	Endpoint string
	Tool     string
	Env      map[string]string
	Headers  map[string]string
	// Model/Grammar/Port/ServerBin configure the builtin.local_llm transport.
	Model     string
	Grammar   bool
	Port      int
	ServerBin string
	// APIKeyEnv/JSONSchema extend builtin.local_llm for authenticated
	// OpenAI-compatible endpoints (e.g. GLM-5.2): a bearer-token env-var name
	// and the OpenAI-native json_schema constrained-output toggle. See
	// app.AgentPluginDecl for the semantics.
	APIKeyEnv  string
	JSONSchema bool
}

// BuildRegistryFromDef constructs a Registry from an *app.AppDef's agent_plugins
// declarations. It is the primary entry point for orchestrator setup: the def's
// plugin declarations have already been validated and ${VAR}-substituted by the
// app loader.
//
// h is the harness used for "builtin.claude_cli" entries; it may be nil when
// no builtin.claude_cli entries are expected.
func BuildRegistryFromDef(def *app.AppDef, h harness.Harness) (*Registry, error) {
	if def == nil {
		return NewRegistry(), nil
	}
	// Convert app.AgentPluginDecl to agent.PluginDecl.
	decls := make(map[string]*PluginDecl, len(def.AgentPlugins))
	for name, appDecl := range def.AgentPlugins {
		if appDecl == nil {
			continue
		}
		decls[name] = &PluginDecl{
			Plugin:     appDecl.Plugin,
			Command:    appDecl.Command,
			Args:       appDecl.Args,
			Endpoint:   appDecl.Endpoint,
			Tool:       appDecl.Tool,
			Env:        appDecl.Env,
			Headers:    appDecl.Headers,
			Model:      appDecl.Model,
			Grammar:    appDecl.Grammar,
			Port:       appDecl.Port,
			ServerBin:  appDecl.ServerBin,
			APIKeyEnv:  appDecl.APIKeyEnv,
			JSONSchema: appDecl.JSONSchema,
		}
	}
	return BuildRegistry(decls, h)
}

// BuildRegistry constructs a Registry from the given plugin declarations.
// h is the default claude-CLI harness; it is used for "builtin.claude_cli"
// entries. h may be nil when the caller knows no builtin.claude_cli entries
// will be present.
//
// All env and headers values must already be ${VAR}-substituted; BuildRegistry
// does not perform substitution.
func BuildRegistry(plugins map[string]*PluginDecl, h harness.Harness) (*Registry, error) {
	reg := NewRegistry()

	for name, decl := range plugins {
		if decl == nil {
			return nil, fmt.Errorf("agent: BuildRegistry: nil declaration for %q", name)
		}

		var o Agent

		switch decl.Plugin {
		case "builtin.claude_cli":
			if h == nil {
				return nil, fmt.Errorf("agent: BuildRegistry: plugin %q requires a harness but none was provided", name)
			}
			o = FromHarness(h)

		case "builtin.inprocess":
			return nil, fmt.Errorf("agent: BuildRegistry: plugin %q cannot be constructed from YAML; build with New and inject via reg.Register", name)

		case "cassette":
			// The cassette transport cannot be constructed from a YAML path alone here
			// because the Agent implementation lives in internal/testrunner (which
			// imports this package). Callers should construct testrunner.NewCassetteAgent
			// and register it via reg.Register(name, o) before using Dispatch.
			// This case is rejected here so the caller gets a clear error rather than
			// a silent no-op.
			return nil, fmt.Errorf("agent: BuildRegistry: plugin %q cannot be constructed from YAML; use testrunner.NewCassetteAgent and inject via reg.Register", name)

		case "subprocess":
			if decl.Command == "" {
				return nil, fmt.Errorf("agent: BuildRegistry: subprocess plugin %q missing command", name)
			}
			o = NewSubprocess(decl.Command, decl.Args, decl.Env)

		case "mcp_http":
			if decl.Endpoint == "" {
				return nil, fmt.Errorf("agent: BuildRegistry: mcp_http plugin %q missing endpoint", name)
			}
			o = NewMCPHTTP(decl.Endpoint, decl.Tool, decl.Headers)

		case "builtin.local_llm":
			// Managed mode needs a model to fetch/serve; endpoint mode talks to
			// an already-running server. One of the two must be set.
			if decl.Model == "" && decl.Endpoint == "" {
				return nil, fmt.Errorf("agent: BuildRegistry: builtin.local_llm plugin %q requires either model: or endpoint:", name)
			}
			o = NewLocalLLM(decl.Model, decl.Port, decl.ServerBin, decl.Grammar, decl.Endpoint, decl.Env).
				WithAPIKeyEnv(decl.APIKeyEnv).
				WithJSONSchema(decl.JSONSchema)

		default:
			return nil, fmt.Errorf("agent: BuildRegistry: unknown plugin type %q for %q", decl.Plugin, name)
		}

		reg.Register(name, o)
	}

	return reg, nil
}
