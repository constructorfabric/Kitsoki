package kitendpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/kit"
)

// syntheticKitDir points at the S1 loader fixture kit — internal/app owns it
// (internal/app/testdata/kits/synthetic-kit), but nothing stops another
// package from loading it read-only for its own tests. It declares a
// `greeter` story with host_interface `reporter` (operation `announce`,
// default binding `host.run`) — exactly the shape kit.<kit>.<iface>.<op>
// dispatch needs to resolve against, with no LLM/network/starlark involved.
const syntheticKitDir = "../app/testdata/kits/synthetic-kit"

func mustLoadSyntheticKit(t *testing.T) *kit.Def {
	t.Helper()
	def, err := kit.LoadDir(syntheticKitDir)
	if err != nil {
		t.Fatalf("kit.LoadDir(%s): %v", syntheticKitDir, err)
	}
	return def
}

func TestParseMethod(t *testing.T) {
	cases := []struct {
		method                     string
		wantKit, wantIface, wantOp string
		wantOK                     bool
	}{
		{"kit.synthetic.reporter.announce", "synthetic", "reporter", "announce", true},
		{"kit.object-graph.graph.load", "object-graph", "graph", "load", true},
		{"runstatus.session.turn", "", "", "", false},
		{"kit.synthetic.reporter", "", "", "", false},                // too few segments
		{"kit.synthetic.reporter.announce.extra", "", "", "", false}, // too many
		{"kit..reporter.announce", "", "", "", false},                // empty kit segment
	}
	for _, c := range cases {
		gotKit, gotIface, gotOp, ok := ParseMethod(c.method)
		if ok != c.wantOK {
			t.Errorf("ParseMethod(%q) ok = %v, want %v", c.method, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if gotKit != c.wantKit || gotIface != c.wantIface || gotOp != c.wantOp {
			t.Errorf("ParseMethod(%q) = (%q,%q,%q), want (%q,%q,%q)", c.method, gotKit, gotIface, gotOp, c.wantKit, c.wantIface, c.wantOp)
		}
	}
}

func TestDispatcherCall_ResolvesDeclaredInterfaceOp(t *testing.T) {
	manifest := mustLoadSyntheticKit(t)
	kits := kit.NewRegistry()
	if err := kits.Add(manifest); err != nil {
		t.Fatalf("kits.Add: %v", err)
	}

	var gotArgs map[string]any
	reg := host.NewRegistry()
	reg.Register("host.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
		gotArgs = args
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})

	d := NewDispatcher(kits, reg)
	result, err := d.Call(context.Background(), "synthetic", "reporter", "announce", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Data["ok"] != true {
		t.Errorf("result.Data = %v, want ok:true", result.Data)
	}
	// The prefix-fallback (host.run.announce -> host.run) must inject the
	// dropped suffix as args["op"], mirroring any other host_interface op
	// dispatched through the registry (see host.Registry.Invoke).
	if gotArgs["op"] != "announce" {
		t.Errorf("handler args[\"op\"] = %v, want \"announce\"", gotArgs["op"])
	}
	if gotArgs["message"] != "hi" {
		t.Errorf("handler args[\"message\"] = %v, want \"hi\"", gotArgs["message"])
	}
}

func TestDispatcherCall_UnknownKit(t *testing.T) {
	kits := kit.NewRegistry()
	reg := host.NewRegistry()
	d := NewDispatcher(kits, reg)

	if _, err := d.Call(context.Background(), "nope", "reporter", "announce", nil); err == nil {
		t.Fatal("expected an error for an unknown kit, got nil")
	}
}

func TestDispatcherCall_UnknownInterface(t *testing.T) {
	manifest := mustLoadSyntheticKit(t)
	kits := kit.NewRegistry()
	_ = kits.Add(manifest)
	reg := host.NewRegistry()
	d := NewDispatcher(kits, reg)

	if _, err := d.Call(context.Background(), "synthetic", "no-such-iface", "announce", nil); err == nil {
		t.Fatal("expected an error for an undeclared interface, got nil")
	}
}

func TestDispatcherCall_UndeclaredOp(t *testing.T) {
	manifest := mustLoadSyntheticKit(t)
	kits := kit.NewRegistry()
	_ = kits.Add(manifest)
	reg := host.NewRegistry()
	reg.Register("host.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})
	d := NewDispatcher(kits, reg)

	if _, err := d.Call(context.Background(), "synthetic", "reporter", "no-such-op", nil); err == nil {
		t.Fatal("expected an error for an undeclared operation, got nil")
	}
}

func TestRegisterScriptsLocked_PreservesTicketProviderCapabilityOnReplace(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tickets.star")
	if err := os.WriteFile(script, []byte("def search(ctx):\n    return {\"tickets\": []}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script+".yaml", []byte("kind: ticket_provider/v1\nhttp:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := host.NewRegistry()
	const name = "host.starlark_binding.lazy_ticket_provider"
	// Lazy kit registration intentionally replaces an existing entry. The
	// replacement must add the explicit federation capability marker too.
	reg.Register(name, func(context.Context, map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})
	d := NewDispatcher(kit.NewRegistry(), reg)
	d.registerScriptsLocked(&app.AppDef{StarlarkHostBindings: map[string]string{name: script}})
	if !reg.IsTicketProvider(name) {
		t.Fatalf("lazy ticket_provider/v1 binding %q was replaced without its federation capability marker", name)
	}

	generic := filepath.Join(dir, "generic.star")
	if err := os.WriteFile(generic, []byte("def main(ctx):\n    return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generic+".yaml", []byte("inputs: {}\noutputs: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg2 := host.NewRegistry()
	reg2.RegisterTicketProvider(name, func(context.Context, map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})
	d2 := NewDispatcher(kit.NewRegistry(), reg2)
	d2.registerScriptsLocked(&app.AppDef{StarlarkHostBindings: map[string]string{name: generic}})
	if reg2.IsTicketProvider(name) {
		t.Fatalf("generic Starlark replacement %q inherited ticket-provider capability", name)
	}
}
