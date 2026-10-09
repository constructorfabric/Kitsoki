package starlark_test

import (
	"context"
	"strings"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

// fakeHostCaller is a minimal starlarkhost.HostCaller stub for exercising
// ctx.host.call without any dependency on package host / a real Registry.
type fakeHostCaller struct {
	calls []string
}

func (f *fakeHostCaller) Invoke(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, name)
	return map[string]any{"name": name, "echo": args}, nil
}

const hostScript = `
def main(ctx):
    out = ctx.host.call("host.graph.load", {"path": "seed.yaml"})
    return {"kind": out["echo"]["path"]}
`

func mustSidecar(t *testing.T) *starlarkhost.Sidecar {
	t.Helper()
	sc, err := starlarkhost.ParseSidecar([]byte(`
outputs:
  kind: { type: string }
`))
	if err != nil {
		t.Fatalf("ParseSidecar: %v", err)
	}
	return sc
}

func hostCaps(verbs ...string) starlarkhost.CapabilitySpec {
	return starlarkhost.CapabilitySpec{
		World: true,
		Host:  starlarkhost.HostCapability{Verbs: verbs},
	}
}

func TestCtxHost_AllowedVerbInvokes(t *testing.T) {
	fake := &fakeHostCaller{}
	ctx := starlarkhost.WithHost(context.Background(), fake, []string{"host.graph.load"})

	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "graph_glue.star",
		Source:       []byte(hostScript),
		Sidecar:      mustSidecar(t),
		Capabilities: hostCaps("host.graph.load"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outputs["kind"] != "seed.yaml" {
		t.Errorf("outputs[kind] = %v, want seed.yaml", res.Outputs["kind"])
	}
	if len(fake.calls) != 1 || fake.calls[0] != "host.graph.load" {
		t.Errorf("fake.calls = %v, want [host.graph.load]", fake.calls)
	}
}

func TestCtxHost_DisallowedVerbErrors(t *testing.T) {
	fake := &fakeHostCaller{}
	// host.graph.load is NOT in the allow-list this run injects.
	ctx := starlarkhost.WithHost(context.Background(), fake, []string{"host.workspace_manager.get"})

	_, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "graph_glue.star",
		Source:       []byte(hostScript),
		Sidecar:      mustSidecar(t),
		Capabilities: hostCaps("host.graph.load"),
	})
	if err == nil {
		t.Fatal("expected an error calling a non-allow-listed verb, got nil")
	}
	msg, isDomain := starlarkhost.AsDomainError(err)
	if !isDomain {
		t.Fatalf("expected a DomainError, got %v (%T)", err, err)
	}
	if !strings.Contains(msg, "not allow-listed") {
		t.Errorf("error message = %q, want it to mention not allow-listed", msg)
	}
	if len(fake.calls) != 0 {
		t.Errorf("fake.calls = %v, want none (rejected before Invoke)", fake.calls)
	}
}

func TestCtxHost_NoCallerConfiguredErrors(t *testing.T) {
	// No WithHost injection at all — the sandbox's deny-by-default posture.
	_, err := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script:       "graph_glue.star",
		Source:       []byte(hostScript),
		Sidecar:      mustSidecar(t),
		Capabilities: hostCaps("host.graph.load"),
	})
	if err == nil {
		t.Fatal("expected an error with no host caller configured, got nil")
	}
	msg, isDomain := starlarkhost.AsDomainError(err)
	if !isDomain {
		t.Fatalf("expected a DomainError, got %v (%T)", err, err)
	}
	if !strings.Contains(msg, "no host caller configured") {
		t.Errorf("error message = %q, want it to mention no host caller configured", msg)
	}
}

func TestHasHost(t *testing.T) {
	ctx := context.Background()
	if starlarkhost.HasHost(ctx) {
		t.Error("HasHost on a bare context = true, want false")
	}
	ctx = starlarkhost.WithHost(ctx, &fakeHostCaller{}, nil)
	if !starlarkhost.HasHost(ctx) {
		t.Error("HasHost after WithHost = false, want true")
	}
}
