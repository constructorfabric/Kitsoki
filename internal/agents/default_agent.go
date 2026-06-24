package agents

import _ "embed"

//go:embed default_agent.md
var defaultAgentPrompt string

// defaultAgent returns the builtin default-agent Agent: a vanilla
// helpful-assistant with no tools and no privileged surface. Used as
// the fallback when a caller doesn't name an agent (off-path runtime
// when it ships LLM dispatch; future general-purpose chat). Apps
// override under the same name in their agents: block when they want
// a different default.
func defaultAgent() Agent {
	return Agent{
		Name:         NameDefaultAgent,
		SystemPrompt: defaultAgentPrompt,
		Model:        "",
		Tools:        nil,
		DefaultCwd:   "",
	}
}
