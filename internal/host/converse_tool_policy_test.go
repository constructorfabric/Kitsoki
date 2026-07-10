package host

import (
	"testing"

	"github.com/stretchr/testify/require"
	"kitsoki/internal/effect"
	"kitsoki/internal/sysprompt"
)

func boolPtr(b bool) *bool { return &b }

// TestConverseToolPolicy guards the read-only enforcement fix: a converse agent
// that declares external_side_effect:false must not run under bypassPermissions
// (which makes --allowedTools advisory and lets the agent Write/Edit the repo),
// and must carry a hard --disallowedTools backstop. A write-capable agent is
// left untouched.
//
// Regression of record: the dev-story proposal_interviewer was declared
// tools:[Read,Grep,Glob] + external_side_effect:false, yet authored
// docs/proposals/starlark-host.md (and edited the index) during a discovery
// conversation, because converse defaulted to bypassPermissions.
func TestConverseToolPolicy(t *testing.T) {
	readOnly := Agent{ExternalSideEffect: boolPtr(false)}
	writeCapable := Agent{ExternalSideEffect: boolPtr(true)}
	unset := Agent{} // ExternalSideEffect nil

	t.Run("read-only downgrades bypassPermissions and denies mutators", func(t *testing.T) {
		mode, disallowed := converseToolPolicy("bypassPermissions", readOnly)
		require.Equal(t, "default", mode, "bypassPermissions must be downgraded so the allowlist binds")
		require.Equal(t, withAlwaysDenied(readOnlyDeniedTools), disallowed)
		require.Contains(t, disallowed, "Write")
		require.Contains(t, disallowed, "Edit")
		require.Contains(t, disallowed, "Bash", "Bash is arbitrary exec — must be denied for read-only")
		require.Contains(t, disallowed, "AskUserQuestion", "AskUserQuestion is always denied — headless auto-resolves it empty")
	})

	t.Run("read-only enforcing mode still denies mutators", func(t *testing.T) {
		mode, disallowed := converseToolPolicy("ask", readOnly)
		require.Equal(t, "default", mode, "ask translates to the enforcing default CLI mode")
		require.Equal(t, withAlwaysDenied(readOnlyDeniedTools), disallowed)
	})

	t.Run("write-capable agent still denies unsafe headless tools", func(t *testing.T) {
		mode, disallowed := converseToolPolicy("bypassPermissions", writeCapable)
		require.Equal(t, "bypassPermissions", mode)
		require.Equal(t, alwaysDeniedTools, disallowed, "even a write-capable agent must not run tools outside Kitsoki's headless contract")
	})

	t.Run("unset external_side_effect is treated as write-capable but still denies unsafe headless tools", func(t *testing.T) {
		mode, disallowed := converseToolPolicy("bypassPermissions", unset)
		require.Equal(t, "bypassPermissions", mode)
		require.Equal(t, alwaysDeniedTools, disallowed)
	})

	// The kitsoki vocabulary "ask"/"denyAll" must never reach the claude CLI —
	// its --permission-mode only accepts acceptEdits|auto|bypassPermissions|
	// default|dontAsk|plan, so a verbatim forward exits with "invalid choice".
	t.Run("ask translates to a CLI-valid mode", func(t *testing.T) {
		mode, _ := converseToolPolicy("ask", writeCapable)
		require.Equal(t, "default", mode)
	})

	t.Run("denyAll translates to default plus the mutator deny-set", func(t *testing.T) {
		mode, disallowed := converseToolPolicy("denyAll", writeCapable)
		require.Equal(t, "default", mode)
		require.Equal(t, withAlwaysDenied(readOnlyDeniedTools), disallowed)
	})
}

func TestEnforceToolboxMatrix(t *testing.T) {
	tests := []struct {
		name       string
		class      effect.Effect
		fallback   string
		wantMode   string
		wantDenied []string
	}{
		{"ask pure", effect.Pure, "default", "default", withAlwaysDenied(readOnlyDeniedTools)},
		{"decide read", effect.Read, "default", "default", withAlwaysDenied(readOnlyDeniedTools)},
		{"converse write", effect.Write, "bypassPermissions", "bypassPermissions", alwaysDeniedTools},
		{"task external", effect.External, "bypassPermissions", "bypassPermissions", alwaysDeniedTools},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent := Agent{
				Toolbox: "box",
				Effect:  tc.class,
				Tools:   []string{"Read"},
			}
			if tc.class == effect.Pure {
				agent.Tools = nil
			}
			if tc.class == effect.Write {
				agent.Tools = []string{"Read", "Write"}
			}
			if tc.class == effect.External {
				agent.Tools = []string{"Read", "WebFetch"}
			}
			got := enforceToolbox(t.Context(), nil, agent, tc.fallback)
			require.Equal(t, tc.wantMode, got.CLIMode)
			require.Equal(t, tc.wantDenied, got.DeniedTools)
			require.Equal(t, "box", got.Toolbox)
			require.Equal(t, tc.class, got.Effect)
			if tc.class != effect.Pure {
				require.NotEmpty(t, got.AllowedTools)
			}

			payload := got.AgentCalledFields(AgentCalledPayload{Verb: "task", Agent: "agent"})
			require.Equal(t, "box", payload.Toolbox)
			require.Equal(t, string(tc.class), payload.Effect)
			require.Equal(t, got.AllowedTools, payload.AllowedTools)
			require.Equal(t, got.DeniedTools, payload.DeniedTools)
		})
	}
}

func TestEnforceToolboxReadOnlyAgentVerbCeiling(t *testing.T) {
	agent := Agent{
		Effect: effect.Read,
		Tools:  []string{"Read", "Bash", "Write"},
	}
	got := enforceToolbox(t.Context(), nil, agent, "default", ToolboxEnforcementOptions{
		EffectCeiling:       effect.Read,
		ReadOnlyDeniedTools: readOnlyAgentVerbDeniedTools,
	})
	require.Equal(t, "default", got.CLIMode)
	require.Equal(t, effect.Read, got.Effect)
	require.Equal(t, []string{"Read", "Bash", "Write"}, got.AllowedTools)
	require.Contains(t, got.DeniedTools, "Write")
	require.NotContains(t, got.DeniedTools, "Bash", "ask/decide may use Bash through a declared BashProfile")
	require.Contains(t, got.DeniedTools, "AskUserQuestion")
}

// TestAlwaysDeniedTools_HeadlessAgents locks in the headless fix: tools that
// escape Kitsoki's story/tool/profile contract must be denied on every agent
// subprocess. See alwaysDeniedTools.
func TestAlwaysDeniedTools_HeadlessAgents(t *testing.T) {
	require.Contains(t, alwaysDeniedTools, "AskUserQuestion")
	require.Contains(t, alwaysDeniedTools, "Agent")
	require.Contains(t, alwaysDeniedTools, "Task")

	t.Run("buildBaseCLIArgs denies it for ask/decide/task", func(t *testing.T) {
		args := buildBaseCLIArgs(t.Context(), sysprompt.Task, map[string]any{}, Agent{})
		require.Contains(t, args, "--disallowedTools")
		idx := indexOf(args, "--disallowedTools")
		require.Greater(t, len(args), idx+1)
		for _, tool := range alwaysDeniedTools {
			require.Contains(t, args[idx+1], tool)
		}
	})

	t.Run("withAlwaysDenied merges without duplicating", func(t *testing.T) {
		got := withAlwaysDenied([]string{"Bash", "AskUserQuestion"})
		require.Equal(t, []string{"Bash", "AskUserQuestion", "Agent", "Task"}, got, "already-present entry must not be duplicated")
		require.Equal(t, alwaysDeniedTools, withAlwaysDenied(nil))
	})
}

// TestAgentCLI_DisablesSkills locks in that story-dispatched Claude Code agents
// do not receive the skill/slash-command surface. Skills are useful for Codex
// itself, but a story agent should be driven by the story's prompt and declared
// tools, with any relevant skill content copied deterministically into context.
func TestAgentCLI_DisablesSkills(t *testing.T) {
	args := buildBaseCLIArgs(t.Context(), sysprompt.Task, map[string]any{}, Agent{})
	require.Contains(t, args, "--disable-slash-commands")
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
