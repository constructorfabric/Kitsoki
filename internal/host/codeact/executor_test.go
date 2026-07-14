package codeact

import (
	"context"
	"strings"
	"testing"
)

// scriptedAgent is the "LLM" stand-in for these RED-gate tests: a deterministic
// function of the step index + last observation/error, so the whole executor
// is provable with zero real LLM per repo AGENTS.md.
type scriptedAgent struct {
	steps []func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission
}

func (s *scriptedAgent) Next(_ context.Context, step int, obs map[string]any, errEnv *ErrorEnvelope) (Emission, error) {
	if step >= len(s.steps) {
		// Ran out of scripted steps without done() — keep emitting a no-op so
		// budget exhaustion is exercised deterministically.
		return Emission{Snippet: "def main(ctx):\n    return {}\n"}, nil
	}
	return s.steps[step](step, obs, errEnv), nil
}

func TestExecutor_HappyPath_DoneSchemaGated(t *testing.T) {
	schema := func(payload map[string]any) error {
		if _, ok := payload["sha"].(string); !ok {
			return errNotAString("sha")
		}
		return nil
	}

	agent := &scriptedAgent{steps: []func(int, map[string]any, *ErrorEnvelope) Emission{
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			return Emission{Snippet: "def main(ctx):\n    return {\"seen\": ctx.world.get(\"target\")}\n"}
		},
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			if obs["seen"] != "regression-commit" {
				t.Fatalf("expected step 0's observation to feed back into step 1, got %v", obs)
			}
			return Emission{Done: true, Payload: map[string]any{"sha": "abc123"}}
		},
	}}

	res, err := Run(context.Background(), Params{
		Budget: 5,
		World:  map[string]any{"target": "regression-commit"},
		Agent:  agent,
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Terminated != TerminatedDone {
		t.Fatalf("expected TerminatedDone, got %s (steps=%d)", res.Terminated, len(res.Steps))
	}
	if res.Payload["sha"] != "abc123" {
		t.Fatalf("expected payload sha=abc123, got %v", res.Payload)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("expected 2 journaled steps, got %d", len(res.Steps))
	}
}

func TestExecutor_ErrorEnvelope_SelfDebug(t *testing.T) {
	agent := &scriptedAgent{steps: []func(int, map[string]any, *ErrorEnvelope) Emission{
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			// Deliberately broken: calls an undeclared ctx attribute.
			return Emission{Snippet: "def main(ctx):\n    return ctx.nope.get(\"x\")\n"}
		},
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			if errEnv == nil {
				t.Fatalf("expected step 1 to receive the structured error envelope from step 0's failure")
			}
			if !strings.Contains(errEnv.Message, "nope") {
				t.Fatalf("expected structured error to mention the failing attribute, got %q", errEnv.Message)
			}
			return Emission{Done: true, Payload: map[string]any{"recovered": true}}
		},
	}}

	res, err := Run(context.Background(), Params{
		Budget: 5,
		Agent:  agent,
		Schema: func(map[string]any) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Terminated != TerminatedDone {
		t.Fatalf("expected recovery to reach TerminatedDone, got %s", res.Terminated)
	}
	if res.Steps[0].Err == nil {
		t.Fatalf("expected step 0 to record a structured error, got none")
	}
}

func TestExecutor_BudgetExhaustion_TerminatesDeterministically(t *testing.T) {
	agent := &scriptedAgent{steps: nil} // never emits done()

	res, err := Run(context.Background(), Params{
		Budget: 3,
		Agent:  agent,
		Schema: func(map[string]any) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Terminated != TerminatedBudgetExhausted {
		t.Fatalf("expected TerminatedBudgetExhausted, got %s", res.Terminated)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("expected exactly budget=3 steps, got %d", len(res.Steps))
	}
}

func TestExecutor_SchemaReject_OnDone(t *testing.T) {
	schema := func(payload map[string]any) error {
		if _, ok := payload["sha"].(string); !ok {
			return errNotAString("sha")
		}
		return nil
	}

	agent := &scriptedAgent{steps: []func(int, map[string]any, *ErrorEnvelope) Emission{
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			// Missing the required "sha" key — schema must reject this.
			return Emission{Done: true, Payload: map[string]any{"wrong_key": "x"}}
		},
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			if errEnv == nil || !strings.Contains(errEnv.Message, "sha") {
				t.Fatalf("expected step 1 to see a schema-rejection error envelope naming the missing field, got %v", errEnv)
			}
			return Emission{Done: true, Payload: map[string]any{"sha": "fixed-after-reject"}}
		},
	}}

	res, err := Run(context.Background(), Params{
		Budget: 5,
		Agent:  agent,
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Terminated != TerminatedDone {
		t.Fatalf("expected eventual TerminatedDone after a schema-rejected attempt, got %s", res.Terminated)
	}
	if res.Payload["sha"] != "fixed-after-reject" {
		t.Fatalf("expected the corrected payload, got %v", res.Payload)
	}
}

// errNotAString is a tiny helper so the schema funcs above read as one-liners.
func errNotAString(field string) error {
	return &schemaFieldError{field: field}
}

type schemaFieldError struct{ field string }

func (e *schemaFieldError) Error() string {
	return "missing or wrong-typed required field: " + e.field
}
