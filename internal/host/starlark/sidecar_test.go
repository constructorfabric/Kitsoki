package starlark_test

import (
	"context"
	"strings"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

// TestSidecar_Validate_GoodAndBadTypes confirms that a sidecar declaring known
// types parses, and an unknown type fails the parse with an actionable message
// naming the bad field and the allowed set.
func TestSidecar_Validate_GoodAndBadTypes(t *testing.T) {
	good := `
inputs:
  name:  { type: string, required: true }
  count: { type: int }
  ratio: { type: number }
  flag:  { type: bool }
  blob:  { type: object }
  items: { type: list }
  free:  {}
outputs:
  result: { type: string }
`
	if _, err := starlarkhost.ParseSidecar([]byte(good)); err != nil {
		t.Fatalf("ParseSidecar(good) = %v, want nil", err)
	}

	bad := "inputs:\n  name: { type: strnig }\n"
	_, err := starlarkhost.ParseSidecar([]byte(bad))
	if err == nil {
		t.Fatal("ParseSidecar(bad type) = nil, want error")
	}
	if !strings.Contains(err.Error(), `"name"`) || !strings.Contains(err.Error(), "strnig") {
		t.Fatalf("error %q should name the field and bad type", err)
	}
	if !strings.Contains(err.Error(), "string|int|number|bool|object|list|any") {
		t.Fatalf("error %q should list the allowed types", err)
	}
}

// runWith is a tiny helper: parse the sidecar, run the script with the given
// inputs, and return the result/error.
func runWith(t *testing.T, sidecarYAML, script string, inputs map[string]any) (*starlarkhost.Result, error) {
	t.Helper()
	sc, err := starlarkhost.ParseSidecar([]byte(sidecarYAML))
	if err != nil {
		t.Fatalf("ParseSidecar: %v", err)
	}
	return starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script:  "test.star",
		Source:  []byte(script),
		Sidecar: sc,
		Inputs:  inputs,
	})
}

// TestSidecar_Inputs_MissingRequired asserts a missing required input fails as a
// DomainError naming the input.
func TestSidecar_Inputs_MissingRequired(t *testing.T) {
	_, err := runWith(t,
		"inputs:\n  n: { type: int, required: true }\n",
		"def main(ctx):\n    return {}\n",
		map[string]any{}, // n absent
	)
	if err == nil {
		t.Fatal("expected error for missing required input")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "missing required input") || !strings.Contains(msg, `"n"`) {
		t.Fatalf("error %q should say missing required input \"n\"", msg)
	}
}

// TestSidecar_Inputs_WrongType asserts a wrong-typed input fails as a DomainError
// naming the input, the expected type, and the got type.
func TestSidecar_Inputs_WrongType(t *testing.T) {
	_, err := runWith(t,
		"inputs:\n  n: { type: int, required: true }\n",
		"def main(ctx):\n    return {}\n",
		map[string]any{"n": "not-a-number"},
	)
	if err == nil {
		t.Fatal("expected error for wrong input type")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, `"n"`) || !strings.Contains(msg, "expected int") {
		t.Fatalf("error %q should name input \"n\" and expected int", msg)
	}
}

// TestSidecar_Outputs_MissingDeclared asserts that omitting a declared output is
// a DomainError naming the missing output.
func TestSidecar_Outputs_MissingDeclared(t *testing.T) {
	_, err := runWith(t,
		"outputs:\n  result: { type: string }\n",
		"def main(ctx):\n    return {}\n",
		nil,
	)
	if err == nil {
		t.Fatal("expected error for missing declared output")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "did not return declared output") || !strings.Contains(msg, `"result"`) {
		t.Fatalf("error %q should say did not return declared output \"result\"", msg)
	}
}

// TestSidecar_Outputs_WrongType asserts a wrong-typed output is a DomainError
// naming the output and the expected type.
func TestSidecar_Outputs_WrongType(t *testing.T) {
	_, err := runWith(t,
		"outputs:\n  result: { type: string }\n",
		"def main(ctx):\n    return {\"result\": 42}\n",
		nil,
	)
	if err == nil {
		t.Fatal("expected error for wrong output type")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, `"result"`) || !strings.Contains(msg, "expected string") {
		t.Fatalf("error %q should name output \"result\" and expected string", msg)
	}
}

// TestSidecar_Outputs_Undeclared asserts returning an undeclared key (when
// outputs is non-empty) is a DomainError naming the offending key.
func TestSidecar_Outputs_Undeclared(t *testing.T) {
	_, err := runWith(t,
		"outputs:\n  result: { type: string }\n",
		"def main(ctx):\n    return {\"result\": \"ok\", \"extra\": \"surprise\"}\n",
		nil,
	)
	if err == nil {
		t.Fatal("expected error for undeclared output")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "undeclared output") || !strings.Contains(msg, `"extra"`) {
		t.Fatalf("error %q should reject undeclared output \"extra\"", msg)
	}
}

// TestSidecar_GoodContract_Passes confirms a script honouring its declared
// contract runs clean and returns the typed outputs.
func TestSidecar_GoodContract_Passes(t *testing.T) {
	res, err := runWith(t,
		"inputs:\n  n: { type: int, required: true }\noutputs:\n  doubled: { type: int }\n",
		"def main(ctx):\n    return {\"doubled\": ctx.inputs[\"n\"] * 2}\n",
		map[string]any{"n": 21},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["doubled"]; got != int64(42) {
		t.Fatalf("doubled = %v (%T), want int64(42)", got, got)
	}
}

// TestSidecar_RejectsReservedOutputKey confirms the engine enforces (not just
// documents) the reservation of the __http_exchanges output name: declaring it
// in a sidecar fails the parse with an actionable message, so an author cannot
// shadow the HTTP-exchange summaries the adapter injects under that key.
func TestSidecar_RejectsReservedOutputKey(t *testing.T) {
	src := "outputs:\n  " + starlarkhost.ExchangesOutputKey + ": { type: list }\n"
	_, err := starlarkhost.ParseSidecar([]byte(src))
	if err == nil {
		t.Fatal("ParseSidecar(reserved output) = nil, want error")
	}
	if !strings.Contains(err.Error(), starlarkhost.ExchangesOutputKey) || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error %q should name the reserved key and say it is reserved", err)
	}
}
