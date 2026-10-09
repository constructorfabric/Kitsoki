package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrerequisites_Valid(t *testing.T) {
	const yamlSrc = `
app:
  id: prereq-ok
  version: 0.1.0
world:
  configured: { type: bool, default: false }
intents:
  setup: {}
root: idle
states:
  idle:
    prerequisites:
      - id: setup
        title: "Project setup"
        severity: warning
        satisfied_when: "world.configured"
        summary: "Run setup before driving work."
        action:
          label: "setup"
          intent: setup
    on:
      setup:
        - target: .
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)
}

func TestPrerequisites_RejectMalformedContract(t *testing.T) {
	const yamlSrc = `
app:
  id: prereq-bad
  version: 0.1.0
world:
  configured: { type: bool, default: false }
root: idle
states:
  idle:
    prerequisites:
      - id: setup
        title: ""
        severity: urgent
        satisfied_when: "world.configured =="
        action:
          intent: missing
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "title is required")
	require.Contains(t, msg, "severity")
	require.Contains(t, msg, "satisfied_when")
	require.Contains(t, msg, "action.intent")
}

func TestPrerequisites_RewrittenWhenImported(t *testing.T) {
	s := &State{
		Prerequisites: []Prerequisite{{
			ID:            "setup",
			Title:         "{{ world.ready_title }}",
			When:          "world.enabled",
			SatisfiedWhen: "world.ready",
			Summary:       "Use {{ world.setup_hint }}.",
			Action: &PrerequisiteAction{
				Label:  "{{ world.setup_label }}",
				Hint:   "{{ world.setup_hint }}",
				Intent: "setup",
				Slots:  map[string]any{"target": "{{ world.target }}"},
			},
		}},
	}
	rw := &childRewriter{
		alias:         "core",
		childWorldKey: map[string]struct{}{"ready_title": {}, "enabled": {}, "ready": {}, "setup_hint": {}, "setup_label": {}, "target": {}},
		childIntent:   map[string]struct{}{"setup": {}},
	}

	rw.rewriteState("idle", s)

	require.Len(t, s.Prerequisites, 1)
	got := s.Prerequisites[0]
	require.Equal(t, "{{ world.core__ready_title }}", got.Title)
	require.Equal(t, "world.core__enabled", got.When)
	require.Equal(t, "world.core__ready", got.SatisfiedWhen)
	require.Equal(t, "Use {{ world.core__setup_hint }}.", got.Summary)
	require.NotNil(t, got.Action)
	require.Equal(t, "{{ world.core__setup_label }}", got.Action.Label)
	require.Equal(t, "{{ world.core__setup_hint }}", got.Action.Hint)
	require.Equal(t, "core__setup", got.Action.Intent)
	require.Equal(t, "{{ world.core__target }}", got.Action.Slots["target"])
}
