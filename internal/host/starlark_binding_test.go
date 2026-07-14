package host

// Tests for S3a (kits-implementation-plan.md D2.1): the runtime half of
// starlark-bindable host_bindings — StarlarkBindingHandler and
// RegisterStarlarkBindings. internal/app/imports_starlark_binding_test.go
// covers the load-time synthesis half (script resolution, dedup, the three
// author forms); these tests cover what actually happens when the registry
// dispatches to the synthesized handler.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeStarlarkScript writes a script + sidecar pair into dir and returns the
// script's absolute path.
func writeStarlarkScript(t *testing.T, dir, name, sidecarYAML, scriptSrc string) string {
	t.Helper()
	script := filepath.Join(dir, name)
	if err := os.WriteFile(script, []byte(scriptSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script+".yaml", []byte(sidecarYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return script
}

// TestStarlarkBindingHandler_InjectsOpAndArgs proves the two contractual
// pieces of D2.1 directly against the Handler closure, without going through
// the registry's dispatch fallback: the effect's `with:` args reach
// ctx.inputs untouched, AND an explicit "op" key (as Registry.Invoke's
// prefix-fallback would inject — see host.go's Invoke/getWithName) reaches
// ctx.inputs.op.
func TestStarlarkBindingHandler_InjectsOpAndArgs(t *testing.T) {
	dir := t.TempDir()
	script := writeStarlarkScript(t, dir, "greet.star",
		"inputs:\n  name: { type: string, required: true }\n  op: { type: string, required: false }\noutputs:\n  message: { type: string }\n",
		"def main(ctx):\n    name = ctx.inputs[\"name\"]\n    op = ctx.inputs.get(\"op\", \"<none>\")\n    return {\"message\": \"Hello, \" + name + \" (op=\" + op + \")\"}\n",
	)

	h := StarlarkBindingHandler(script)
	res, err := h(context.Background(), map[string]any{"name": "Ada", "op": "hello"})
	if err != nil {
		t.Fatalf("StarlarkBindingHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected domain error: %s", res.Error)
	}
	want := "Hello, Ada (op=hello)"
	if got := res.Data["message"]; got != want {
		t.Fatalf("res.Data[message] = %v, want %q", got, want)
	}
}

// TestStarlarkBindingHandler_WorldKeyForwardedSeparately proves a top-level
// "world" arg is forwarded to StarlarkRunHandler's own world-override slot
// (reaching ctx.world) rather than leaking into ctx.inputs as a plain named
// input.
func TestStarlarkBindingHandler_WorldKeyForwardedSeparately(t *testing.T) {
	dir := t.TempDir()
	script := writeStarlarkScript(t, dir, "reads_world.star",
		"outputs:\n  saw_world_input: { type: bool }\n  who: { type: string }\n",
		"def main(ctx):\n    return {\"saw_world_input\": \"world\" in ctx.inputs, \"who\": ctx.world.get(\"user\") or \"<none>\"}\n",
	)

	h := StarlarkBindingHandler(script)
	res, err := h(context.Background(), map[string]any{
		"world": map[string]any{"user": "ada"},
	})
	if err != nil {
		t.Fatalf("StarlarkBindingHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected domain error: %s", res.Error)
	}
	if got := res.Data["saw_world_input"]; got != false {
		t.Fatalf("ctx.inputs must not contain a leaked \"world\" key, got saw_world_input=%v", got)
	}
	if got := res.Data["who"]; got != "ada" {
		t.Fatalf("ctx.world.get(user) = %v, want \"ada\" (world arg not forwarded to StarlarkRunHandler)", got)
	}
}

// TestRegisterStarlarkBindings_DispatchesThroughPrefixFallback exercises the
// full seam a real app run relies on: RegisterStarlarkBindings registers one
// Handler under the bare synthesized name (no op suffix — mirroring how
// resolveAllInterfaces sets a binding's Default and appends ".<op>" to build
// the invoke target), and Registry.Invoke's prefix-fallback (host.go) must
// resolve `<name>.<op>` down to that registration and inject "op" into args
// automatically — exactly what StarlarkBindingHandler expects to find.
func TestRegisterStarlarkBindings_DispatchesThroughPrefixFallback(t *testing.T) {
	dir := t.TempDir()
	script := writeStarlarkScript(t, dir, "op_echo.star",
		"inputs:\n  op: { type: string, required: false }\noutputs:\n  op: { type: string }\n",
		"def main(ctx):\n    return {\"op\": ctx.inputs.get(\"op\", \"<none>\")}\n",
	)

	reg := NewRegistry()
	const handlerName = "host.starlark_binding.testfixture"
	RegisterStarlarkBindings(reg, map[string]string{handlerName: script})

	res, err := reg.Invoke(context.Background(), handlerName+".announce", map[string]any{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected domain error: %s", res.Error)
	}
	if got := res.Data["op"]; got != "announce" {
		t.Fatalf("ctx.inputs.op = %v, want \"announce\" (Registry.Invoke's prefix-fallback should inject op)", got)
	}
}

func TestRegisterStarlarkBindings_TicketProviderFunctions(t *testing.T) {
	dir := t.TempDir()
	script := writeStarlarkScript(t, dir, "ticket_provider.star",
		"kind: ticket_provider/v1\nhttp:\n  enabled: false\n",
		"def search(ctx):\n    return {\"tickets\": [{\"id\": \"T-1\", \"title\": ctx.inputs[\"query\"]}], \"project\": ctx.world.get(\"project\")}\n",
	)

	reg := NewRegistry()
	const handlerName = "host.starlark_binding.ticketprovider"
	RegisterStarlarkBindings(reg, map[string]string{handlerName: script})
	if !reg.IsTicketProvider(handlerName) {
		t.Fatalf("ticket_provider/v1 Starlark binding %q was not marked as a federation-safe ticket provider", handlerName)
	}

	res, err := reg.Invoke(context.Background(), handlerName+".search", map[string]any{
		"query": "provider-backed",
		"world": map[any]any{"project": "inherited"},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected provider error: %s", res.Error)
	}
	tickets, ok := res.Data["tickets"].([]any)
	if !ok || len(tickets) != 1 {
		t.Fatalf("tickets = %#v, want one ticket row", res.Data["tickets"])
	}
	row := tickets[0].(map[string]any)
	if got := row["title"]; got != "provider-backed" {
		t.Fatalf("ticket title = %v, want provider-backed", got)
	}
	if got := res.Data["project"]; got != "inherited" {
		t.Fatalf("provider world project = %v, want inherited", got)
	}
}

func TestRegistryReplaceClearsTicketProviderCapability(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterTicketProvider("host.test.ticket", func(context.Context, map[string]any) (Result, error) {
		return Result{}, nil
	})
	reg.Replace("host.test.ticket", func(context.Context, map[string]any) (Result, error) {
		return Result{}, nil
	})
	if reg.IsTicketProvider("host.test.ticket") {
		t.Fatal("ordinary replacement retained ticket-provider capability")
	}
}

// TestRegisterStarlarkBindings_PanicsOnDuplicateName matches RegisterBuiltins'
// init-time contract (Registry.Register panics on a duplicate) — a caller
// wiring the same bindings map twice is a caller bug, not something to
// silently paper over.
func TestRegisterStarlarkBindings_PanicsOnDuplicateName(t *testing.T) {
	dir := t.TempDir()
	script := writeStarlarkScript(t, dir, "noop.star", "outputs: {}\n", "def main(ctx):\n    return {}\n")

	reg := NewRegistry()
	RegisterStarlarkBindings(reg, map[string]string{"host.starlark_binding.dup": script})

	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic registering a duplicate handler name")
		}
	}()
	RegisterStarlarkBindings(reg, map[string]string{"host.starlark_binding.dup": script})
}
