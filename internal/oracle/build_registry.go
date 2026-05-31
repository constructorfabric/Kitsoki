// build_registry.go assembles a Registry from an app's oracle_plugins
// declarations at session construction time.
//
// Usage from an orchestrator setup:
//
//	reg, err := oracle.BuildRegistryFromDef(def, harness)
//	orch := orchestrator.New(..., orchestrator.WithOracleRegistry(reg))
//
// BuildRegistry maps each declaration's plugin value to a transport
// constructor:
//
//   - "builtin.claude_cli" — FromHarness(h); requires h to be non-nil.
//   - "builtin.inprocess"  — not constructable from YAML alone; the caller
//     must build the in-process oracle (via New) and inject it with
//     reg.Register before dispatch. Encountering it here returns an error.
//   - "cassette"           — also injected programmatically (the
//     implementation lives in internal/testrunner); returns an error here.
//   - "subprocess"         — NewSubprocess(command, args, env).
//   - "mcp_http"           — NewMCPHTTP(endpoint, tool, headers).

package oracle

import (
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
)

// PluginDecl mirrors app.OraclePluginDecl to avoid an import cycle
// (oracle → app would be circular; the caller passes a pre-converted struct).
type PluginDecl struct {
	Plugin   string
	Command  string
	Args     []string
	Endpoint string
	Tool     string
	Env      map[string]string
	Headers  map[string]string
}

// BuildRegistryFromDef constructs a Registry from an *app.AppDef's oracle_plugins
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
	// Convert app.OraclePluginDecl to oracle.PluginDecl.
	decls := make(map[string]*PluginDecl, len(def.OraclePlugins))
	for name, appDecl := range def.OraclePlugins {
		if appDecl == nil {
			continue
		}
		decls[name] = &PluginDecl{
			Plugin:   appDecl.Plugin,
			Command:  appDecl.Command,
			Args:     appDecl.Args,
			Endpoint: appDecl.Endpoint,
			Tool:     appDecl.Tool,
			Env:      appDecl.Env,
			Headers:  appDecl.Headers,
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
			return nil, fmt.Errorf("oracle: BuildRegistry: nil declaration for %q", name)
		}

		var o Oracle

		switch decl.Plugin {
		case "builtin.claude_cli":
			if h == nil {
				return nil, fmt.Errorf("oracle: BuildRegistry: plugin %q requires a harness but none was provided", name)
			}
			o = FromHarness(h)

		case "builtin.inprocess":
			return nil, fmt.Errorf("oracle: BuildRegistry: plugin %q cannot be constructed from YAML; build with New and inject via reg.Register", name)

		case "cassette":
			// The cassette transport cannot be constructed from a YAML path alone here
			// because the Oracle implementation lives in internal/testrunner (which
			// imports this package). Callers should construct testrunner.NewCassetteOracle
			// and register it via reg.Register(name, o) before using Dispatch.
			// This case is rejected here so the caller gets a clear error rather than
			// a silent no-op.
			return nil, fmt.Errorf("oracle: BuildRegistry: plugin %q cannot be constructed from YAML; use testrunner.NewCassetteOracle and inject via reg.Register", name)

		case "subprocess":
			if decl.Command == "" {
				return nil, fmt.Errorf("oracle: BuildRegistry: subprocess plugin %q missing command", name)
			}
			o = NewSubprocess(decl.Command, decl.Args, decl.Env)

		case "mcp_http":
			if decl.Endpoint == "" {
				return nil, fmt.Errorf("oracle: BuildRegistry: mcp_http plugin %q missing endpoint", name)
			}
			o = NewMCPHTTP(decl.Endpoint, decl.Tool, decl.Headers)

		default:
			return nil, fmt.Errorf("oracle: BuildRegistry: unknown plugin type %q for %q", decl.Plugin, name)
		}

		reg.Register(name, o)
	}

	return reg, nil
}
