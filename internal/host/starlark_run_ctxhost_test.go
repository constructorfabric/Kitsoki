package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestNewStarlarkRunHandler_CtxHostInvokesAllowedVerb exercises S3d end to
// end at the production-adapter level: a script bound to a registry via
// NewStarlarkRunHandler calls ctx.host.call("host.workspace_manager.get",
// ...) — one of AllowedStarlarkHostVerbs — and the call reaches a fake
// handler registered on the SAME Registry the handler closes over.
func TestNewStarlarkRunHandler_CtxHostInvokesAllowedVerb(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "glue.star")
	if err := os.WriteFile(script, []byte(
		"def main(ctx):\n"+
			"    out = ctx.host.call(\"host.workspace_manager.get\", {\"id\": \"w1\"})\n"+
			"    return {\"id\": out[\"id\"]}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script+".yaml", []byte(
		"outputs:\n  id: { type: string }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	var gotArgs map[string]any
	reg.Register("host.workspace_manager.get", func(_ context.Context, args map[string]any) (Result, error) {
		gotArgs = args
		return Result{Data: map[string]any{"id": args["id"]}}, nil
	})
	handler := NewStarlarkRunHandler(reg)

	res, err := handler(context.Background(), map[string]any{
		"script":       script,
		"capabilities": starlarkTestHostCapabilities("host.workspace_manager.get"),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("handler result.Error = %q, want empty", res.Error)
	}
	if res.Data["id"] != "w1" {
		t.Errorf("result.Data[id] = %v, want w1", res.Data["id"])
	}
	if gotArgs["id"] != "w1" {
		t.Errorf("workspace_manager.get args[id] = %v, want w1", gotArgs["id"])
	}
}

// TestNewStarlarkRunHandler_CtxHostRejectsUnlistedVerb confirms a verb NOT in
// AllowedStarlarkHostVerbs is rejected even when it IS registered on reg —
// the allow-list, not registration, is the gate.
func TestNewStarlarkRunHandler_CtxHostRejectsUnlistedVerb(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "glue.star")
	if err := os.WriteFile(script, []byte(
		"def main(ctx):\n"+
			"    ctx.host.call(\"host.run\", {\"cmd\": \"echo hi\"})\n"+
			"    return {\"ok\": True}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script+".yaml", []byte(
		"outputs:\n  ok: { type: bool }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	called := false
	reg.Register("host.run", func(_ context.Context, _ map[string]any) (Result, error) {
		called = true
		return Result{}, nil
	})
	handler := NewStarlarkRunHandler(reg)

	res, err := handler(context.Background(), map[string]any{
		"script":       script,
		"capabilities": starlarkTestHostCapabilities("host.workspace_manager.get"),
	})
	if err != nil {
		t.Fatalf("handler returned an infra error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected a domain Result.Error for a non-allow-listed verb, got none")
	}
	if called {
		t.Error("host.run was invoked despite not being allow-listed")
	}
}
