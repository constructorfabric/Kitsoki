package opschema

import (
	"os"
	"testing"

	goyaml "github.com/goccy/go-yaml"
)

func TestRegistry_RegisterLookup(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup("host.git", "commit"); ok {
		t.Fatalf("expected no entry before Register")
	}
	r.Register("host.git", "commit", Op{Input: fields("workdir", "string"), Output: fields("ok", "bool")})
	spec, ok := r.Lookup("host.git", "commit")
	if !ok {
		t.Fatalf("expected entry after Register")
	}
	if spec.Input["workdir"].Type != "string" {
		t.Fatalf("unexpected input spec: %+v", spec.Input)
	}
}

func TestRegistry_Merge(t *testing.T) {
	a := NewRegistry()
	a.Register("host.a", "op1", Op{})
	b := NewRegistry()
	b.Register("host.b", "op2", Op{})
	a.Merge(b)
	if _, ok := a.Lookup("host.a", "op1"); !ok {
		t.Fatalf("expected host.a.op1 to survive merge")
	}
	if _, ok := a.Lookup("host.b", "op2"); !ok {
		t.Fatalf("expected host.b.op2 folded in by merge")
	}
}

// ─── Self-consistency: Builtins() must mirror dev-story's app.yaml ──────────

// rawHostInterfacesDoc is the minimal shape needed to read
// stories/dev-story/app.yaml's host_interfaces: block directly, without
// going through the full app.Load pipeline (which would pull in dev-story's
// sub-imports and their own resolution — unnecessary weight for a table
// drift check).
type rawHostInterfacesDoc struct {
	HostInterfaces map[string]struct {
		Operations map[string]struct {
			Input  map[string]string `yaml:"input"`
			Output map[string]string `yaml:"output"`
		} `yaml:"operations"`
		Default string `yaml:"default"`
	} `yaml:"host_interfaces"`
}

// TestBuiltins_MatchesDevStoryManifest guards against Builtins() drifting
// from stories/dev-story/app.yaml's host_interfaces block, the same way
// internal/app/synthesis.go's DevStoryIfaces is guarded by a fail-fast check
// against an unknown iface name. If dev-story's app.yaml changes an
// operation's input/output shape (or a default binding), this test fails
// until Builtins() is updated to match.
func TestBuiltins_MatchesDevStoryManifest(t *testing.T) {
	raw, err := os.ReadFile("../../../stories/dev-story/app.yaml")
	if err != nil {
		t.Skipf("stories/dev-story/app.yaml not found from this test's cwd (%v) — skipping drift check", err)
	}
	var doc rawHostInterfacesDoc
	if err := goyaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse dev-story app.yaml: %v", err)
	}

	builtins := Builtins()

	// devStoryDefaults mirrors internal/app.DevStoryIfaces -> Go handler,
	// i.e. the handler name each interface's `default:` should resolve to.
	// Kept local (rather than importing internal/app, which would create an
	// import edge from a leaf host subpackage back up to app) — a mismatch
	// here against the live YAML is exactly what this test is for.
	wantDefaults := map[string]string{
		"ticket":    "host.local_files.ticket",
		"vcs":       "host.git",
		"ci":        "host.local",
		"workspace": "host.git_worktree",
		"transport": "host.append_to_file",
	}

	for ifaceName, wantHandler := range wantDefaults {
		iface, ok := doc.HostInterfaces[ifaceName]
		if !ok {
			t.Errorf("dev-story app.yaml no longer declares host_interfaces.%s", ifaceName)
			continue
		}
		if iface.Default != wantHandler {
			t.Errorf("dev-story app.yaml host_interfaces.%s.default = %q, opschema.Builtins() assumes %q", ifaceName, iface.Default, wantHandler)
		}
		for opName, opDef := range iface.Operations {
			got, ok := builtins.Lookup(wantHandler, opName)
			if !ok {
				t.Errorf("opschema.Builtins() has no entry for %s.%s (declared by dev-story host_interfaces.%s)", wantHandler, opName, ifaceName)
				continue
			}
			for field, typ := range opDef.Input {
				spec, ok := got.Input[field]
				if !ok {
					t.Errorf("%s.%s: Builtins() input missing field %q (dev-story declares type %q)", wantHandler, opName, field, typ)
					continue
				}
				if spec.Type != typ {
					t.Errorf("%s.%s: Builtins() input %q type = %q, dev-story declares %q", wantHandler, opName, field, spec.Type, typ)
				}
			}
			for field, typ := range opDef.Output {
				spec, ok := got.Output[field]
				if !ok {
					t.Errorf("%s.%s: Builtins() output missing field %q (dev-story declares type %q)", wantHandler, opName, field, typ)
					continue
				}
				if spec.Type != typ {
					t.Errorf("%s.%s: Builtins() output %q type = %q, dev-story declares %q", wantHandler, opName, field, spec.Type, typ)
				}
			}
		}
	}
}
