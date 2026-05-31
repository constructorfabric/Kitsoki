package agents

import _ "embed"

//go:embed default_oracle.md
var defaultOraclePrompt string

// defaultOracle returns the builtin default-oracle Agent: a vanilla
// helpful-assistant with no tools and no privileged surface. Used as
// the fallback when a caller doesn't name an agent (off-path runtime
// when it ships LLM dispatch; future general-purpose chat). Apps
// override under the same name in their agents: block when they want
// a different default.
func defaultOracle() Agent {
	return Agent{
		Name:         NameDefaultOracle,
		SystemPrompt: defaultOraclePrompt,
		Model:        "",
		Tools:        nil,
		DefaultCwd:   "",
	}
}
