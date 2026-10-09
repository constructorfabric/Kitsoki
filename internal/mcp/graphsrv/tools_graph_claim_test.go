package graphsrv_test

import (
	"context"
	"sync"
	"testing"

	"kitsoki/internal/mcp/graphsrv"
)

type livenessFixture struct {
	mu   sync.Mutex
	live map[string]bool
}

func (l *livenessFixture) Live(_ context.Context, handle string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.live[handle]
}

func (l *livenessFixture) set(handle string, live bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.live[handle] = live
}

// This is the K3 deterministic acceptance seam: two actors cannot work a
// live node concurrently, but a detector-confirmed dead holder transfers the
// claim atomically and preserves the old holder's provenance for queue policy.
func TestGraphClaim_RefusesLiveHolderThenTransfersDeadHolder(t *testing.T) {
	path := mutableFixture(t)
	live := &livenessFixture{live: map[string]bool{"alice-run": true, "bob-run": true}}
	claims := graphsrv.NewClaimRegistry(live, nil)

	alice, closeAlice := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "alice", Claims: claims})
	defer closeAlice()
	bob, closeBob := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "bob", Claims: claims})
	defer closeBob()

	first, isErr := callTool(t, alice, "graph.claim", map[string]any{"id": "req-alpha", "branch": "agent/alice", "liveness_handle": "alice-run"})
	if isErr {
		t.Fatalf("alice graph.claim: %+v", first)
	}
	claim, _ := first["claim"].(map[string]any)
	if claim["actor"] != "alice" || claim["branch"] != "agent/alice" || claim["liveness_handle"] != "alice-run" {
		t.Fatalf("claim provenance = %+v", claim)
	}

	blocked, isErr := callTool(t, bob, "graph.claim", map[string]any{"id": "req-alpha", "branch": "agent/bob", "liveness_handle": "bob-run"})
	if !isErr || blocked["code"] != graphsrv.CodeClaimHeld {
		t.Fatalf("live second claim = %+v, isErr=%v; want CLAIM_HELD", blocked, isErr)
	}

	live.set("alice-run", false)
	stolen, isErr := callTool(t, bob, "graph.claim", map[string]any{"id": "req-alpha", "branch": "agent/bob", "liveness_handle": "bob-run"})
	if isErr {
		t.Fatalf("dead-holder transfer: %+v", stolen)
	}
	transferred, _ := stolen["transferred_from"].(map[string]any)
	if transferred["actor"] != "alice" || stolen["claim"].(map[string]any)["actor"] != "bob" {
		t.Fatalf("transfer provenance = %+v", stolen)
	}

	wrong, isErr := callTool(t, alice, "graph.release", map[string]any{"id": "req-alpha", "liveness_handle": "alice-run"})
	if !isErr || wrong["code"] != graphsrv.CodeNotClaimHolder {
		t.Fatalf("zombie release = %+v, isErr=%v; want NOT_CLAIM_HOLDER", wrong, isErr)
	}
	released, isErr := callTool(t, bob, "graph.release", map[string]any{"id": "req-alpha", "liveness_handle": "bob-run"})
	if isErr || released["released"].(map[string]any)["actor"] != "bob" {
		t.Fatalf("bob release = %+v, isErr=%v", released, isErr)
	}
}

func TestGraphClaim_RequiresLauncherActor(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}, Mode: graphsrv.ModePropose})
	defer done()
	got, isErr := callTool(t, cs, "graph.claim", map[string]any{"id": "req-alpha", "branch": "agent/no-actor", "liveness_handle": "run"})
	if !isErr || got["code"] != graphsrv.CodeValidation {
		t.Fatalf("actorless claim = %+v, isErr=%v", got, isErr)
	}
}
