package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxBlockLoadsOnAgentTask(t *testing.T) {
	yaml := `app:
  id: sandbox-ok
  version: 0.1.0
hosts:
  - host.agent.task
intents:
  go: { title: Go }
root: work
states:
  work:
    on:
      go:
        - target: work
          effects:
            - invoke: host.agent.task
              with:
                agent: builder
                working_dir: "."
                sandbox:
                  min_strength: supervised
                  repo: read_only
                  rw: [".artifacts/goal"]
                  hidden: [".env"]
                  network: inherit
                  degrade: warn
                  resources: { timeout: "2s" }
                acceptance: { schema: schemas/out.json }
agents:
  builder:
    system_prompt: "build things"
    effect: write
    tools: [Read, Edit]
`
	_, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
}

func TestSandboxBlockLoadsOnAgentDecide(t *testing.T) {
	yaml := `app:
  id: sandbox-decide-ok
  version: 0.1.0
hosts:
  - host.agent.decide
intents:
  go: { title: Go }
root: work
states:
  work:
    on:
      go:
        - target: work
          effects:
            - invoke: host.agent.decide
              with:
                agent: reviewer
                prompt: prompts/review.md
                schema: schemas/review.json
                sandbox:
                  min_strength: supervised
                  repo: read_only
                  network: model_only
                  resources: { timeout: "2s" }
agents:
  reviewer:
    system_prompt: "review things"
    tools: [Read, Grep, Glob, Bash]
    bash_profile: read-only
`
	_, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
}

func TestSandboxBlockRejectsUnknownStrength(t *testing.T) {
	yaml := strings.Replace(sandboxBaseYAML(), "min_strength: supervised", "min_strength: wishful", 1)
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "sandbox.min_strength")
}

func TestSandboxBlockRejectsEmptyPath(t *testing.T) {
	yaml := strings.Replace(sandboxBaseYAML(), "rw: [\".artifacts/goal\"]", "rw: [\"\"]", 1)
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "sandbox.rw entries must be non-empty")
}

func sandboxBaseYAML() string {
	return `app:
  id: sandbox-bad
  version: 0.1.0
hosts:
  - host.agent.task
intents:
  go: { title: Go }
root: work
states:
  work:
    on:
      go:
        - target: work
          effects:
            - invoke: host.agent.task
              with:
                agent: builder
                sandbox:
                  min_strength: supervised
                  rw: [".artifacts/goal"]
                acceptance: { schema: schemas/out.json }
agents:
  builder:
    system_prompt: "build things"
    effect: write
    tools: [Read, Edit]
`
}
