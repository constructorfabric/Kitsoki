// Package opschema is the registered-schema table for Go host handlers that
// back a story's declared host_interfaces operations (S4 conformance,
// .context/kits-implementation-plan.md "Interface op shape checking").
//
// A starlark-bound host_bindings script gets its op contract nearly for free
// from its .star.yaml sidecar (internal/host/starlark's Sidecar type) — the
// sidecar IS the authoritative shape for that handler. A plain Go handler has
// no such sidecar, so this package is the analogous authoritative table for
// Go handlers: a hand-maintained mirror that internal/kitverify compares a
// host_interface's declared operations.<op>.input/output against.
//
// An unregistered handler is NOT an error — most of the engine's host.*
// verbs have no entry here yet (see Builtins' doc comment for the
// deliberately-scoped seed set this slice ships). kitverify treats "no
// registered schema" as "cannot check", not "fails", so this table grows
// incrementally without turning every kit that reuses an unregistered
// builtin handler red.
package opschema

import "sort"

// FieldSpec is a single input/output field's declared shape. Uses the same
// bare-type vocabulary internal/app's HostInterfaceOp fields already do
// (string|int|float|bool|list|object|any) so the two are directly
// comparable — see internal/kitverify's typesCompatible.
type FieldSpec struct {
	Type string
}

// Op is one operation's registered shape.
type Op struct {
	Input  map[string]FieldSpec
	Output map[string]FieldSpec
}

// Registry maps a registered Go handler name -> op name -> Op.
type Registry struct {
	ops map[string]map[string]Op
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{ops: make(map[string]map[string]Op)}
}

// Register records handler.op's shape, overwriting any existing entry. This
// is a data table assembled at init/test time, not a runtime allow-list —
// unlike host.Registry.Register it deliberately does not panic on a
// duplicate.
func (r *Registry) Register(handler, op string, spec Op) {
	if r.ops == nil {
		r.ops = make(map[string]map[string]Op)
	}
	m, ok := r.ops[handler]
	if !ok {
		m = make(map[string]Op)
		r.ops[handler] = m
	}
	m[op] = spec
}

// Lookup returns the registered Op for handler.op, if any.
func (r *Registry) Lookup(handler, op string) (Op, bool) {
	if r == nil {
		return Op{}, false
	}
	m, ok := r.ops[handler]
	if !ok {
		return Op{}, false
	}
	spec, ok := m[op]
	return spec, ok
}

// Handlers returns the sorted set of handler names with at least one
// registered op. Test/diagnostic helper.
func (r *Registry) Handlers() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.ops))
	for h := range r.ops {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Merge folds other's entries into r (other wins on conflict) and returns r
// for chaining.
func (r *Registry) Merge(other *Registry) *Registry {
	if other == nil {
		return r
	}
	for h, ops := range other.ops {
		for op, spec := range ops {
			r.Register(h, op, spec)
		}
	}
	return r
}
