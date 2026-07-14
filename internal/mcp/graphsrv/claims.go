package graphsrv

import (
	"context"
	"sync"
	"time"

	"kitsoki/internal/clock"
)

// Liveness is deliberately small so launch/workspace ownership and later
// queue heartbeats can provide the same signal without coupling graph MCP to a
// particular process manager. False is a best-effort dead observation.
type Liveness interface {
	Live(context.Context, string) bool
}

type alwaysLive struct{}

func (alwaysLive) Live(context.Context, string) bool { return true }

// ClaimProvenance identifies a claimant and the candidate it is working in.
// Handle is opaque to graph MCP; launch/workspace infrastructure owns it.
type ClaimProvenance struct {
	Actor  string    `json:"actor"`
	Branch string    `json:"branch"`
	Handle string    `json:"liveness_handle"`
	At     time.Time `json:"claimed_at"`
}

type claimKey struct{ catalog, node string }

// ClaimRegistry is a concurrency-safe, process-local claim authority. Its
// narrow interface is intentional groundwork: durable/federated backing can
// replace it without changing MCP request or result semantics.
type ClaimRegistry struct {
	mu       sync.Mutex
	claims   map[claimKey]ClaimProvenance
	liveness Liveness
	clock    clock.Clock
}

func NewClaimRegistry(liveness Liveness, clk clock.Clock) *ClaimRegistry {
	if liveness == nil {
		liveness = alwaysLive{}
	}
	if clk == nil {
		clk = clock.Real()
	}
	return &ClaimRegistry{claims: make(map[claimKey]ClaimProvenance), liveness: liveness, clock: clk}
}

// Claim atomically acquires key. A holder observed dead is transferred; a
// live holder is returned unchanged so callers can point at its provenance.
func (r *ClaimRegistry) Claim(ctx context.Context, catalog, node, actor, branch, handle string) (ClaimProvenance, *ClaimProvenance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := claimKey{catalog: catalog, node: node}
	if holder, ok := r.claims[k]; ok {
		if r.liveness.Live(ctx, holder.Handle) {
			return ClaimProvenance{}, &holder, false
		}
		claim := ClaimProvenance{Actor: actor, Branch: branch, Handle: handle, At: r.clock.Now().UTC()}
		r.claims[k] = claim
		return claim, &holder, true
	}
	claim := ClaimProvenance{Actor: actor, Branch: branch, Handle: handle, At: r.clock.Now().UTC()}
	r.claims[k] = claim
	return claim, nil, false
}

func (r *ClaimRegistry) Release(catalog, node, actor, handle string) (ClaimProvenance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := claimKey{catalog: catalog, node: node}
	holder, ok := r.claims[k]
	if !ok || holder.Actor != actor || holder.Handle != handle {
		return holder, false
	}
	delete(r.claims, k)
	return holder, true
}
