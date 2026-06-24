package main

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/webconfig"
)

// TestCheckAuthorIdentity proves the fail-fast invariant: a story that gates a
// turn on the author ACL key requires a configured server identity, while a
// story that merely declares the key (or none) starts fine.
func TestCheckAuthorIdentity(t *testing.T) {
	guarded := &app.AppDef{
		States: map[string]*app.State{
			"reproducing": {On: map[string][]app.Transition{
				"accept": {{Target: "proposing", When: "slots.author in world.allowed_authors"}},
			}},
		},
	}
	guarded.App.ID = "guarded"

	declaredOnly := &app.AppDef{
		World: map[string]app.VarDef{"allowed_authors": {}},
		States: map[string]*app.State{
			"reproducing": {On: map[string][]app.Transition{
				"accept": {{Target: "proposing", When: "true"}},
			}},
		},
	}
	declaredOnly.App.ID = "declared-only"

	t.Run("guarded story without configured actor fails fast", func(t *testing.T) {
		r := NewRegistry(webconfig.WebConfig{}, nil, runtimeBase{DefaultActor: ""})
		err := r.checkAuthorIdentity(guarded)
		if err == nil {
			t.Fatal("expected fail-fast for a guarded story with no configured actor")
		}
		if !strings.Contains(err.Error(), "--actor") {
			t.Fatalf("error should point the operator at --actor, got %q", err)
		}
	})

	t.Run("guarded story with configured actor is allowed", func(t *testing.T) {
		r := NewRegistry(webconfig.WebConfig{}, nil, runtimeBase{DefaultActor: "alice"})
		if err := r.checkAuthorIdentity(guarded); err != nil {
			t.Fatalf("a configured actor satisfies the invariant, got %v", err)
		}
	})

	t.Run("declared-only story needs no actor", func(t *testing.T) {
		r := NewRegistry(webconfig.WebConfig{}, nil, runtimeBase{DefaultActor: ""})
		if err := r.checkAuthorIdentity(declaredOnly); err != nil {
			t.Fatalf("a story that never reads the ACL key must start fine, got %v", err)
		}
	})
}
