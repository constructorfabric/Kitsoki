package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Cardinality is the arity of an edge field.
type Cardinality string

const (
	CardinalityOne  Cardinality = "one"
	CardinalityMany Cardinality = "many"
)

// EdgeStorage says where an edge field's data actually lives on a Node.
type EdgeStorage string

const (
	// StorageEdges is the default: the edge lives at Node.Edges[field].
	StorageEdges EdgeStorage = ""
	// StorageTopLevel means the edge is read from Node.Fields[field]
	// instead — how `change` keeps `depends_on` as a top-level field
	// matching the shipped change-node schema, with no edges.depends_on
	// migration required.
	StorageTopLevel EdgeStorage = "top_level"
)

// EdgeFieldDecl declares one typed edge slot a type contributes.
type EdgeFieldDecl struct {
	ID          EdgeField
	TargetType  string
	Cardinality Cardinality
	Storage     EdgeStorage
	// Acyclic marks an edge field whose targets must not form a cycle
	// (e.g. change.depends_on) — checked by W1.1's catalog lint.
	Acyclic bool
	// Renders marks an edge field that public site codegen (G3) actually
	// walks to build rendered output — e.g. a site-page's `presents`. Only
	// Renders edges are checked for the internal-node-reachable-from-a-
	// public-edge leak; most edges (implemented_by, verified_by,
	// assigned_to, proposed_by, ...) are traceability cross-references a
	// generator never recurses into, so an internal target there is not a
	// leak. W3 (the site conversion) is what decides which edges are
	// Renders when it wires the real generator.
	Renders bool
	// NestsUnder marks an edge field whose source node is a sub-item of its
	// target, not a peer — e.g. persona.persona_of (a persona profiles an
	// actor) or proposal.child_of (a proposal slice decomposes an epic). A
	// generic UI list projection (unlike a graph canvas, where the edge
	// itself already draws the relationship) can use this to nest a node
	// under its target instead of listing it as a flat peer, without a
	// hand-maintained kind->edge table that silently drifts from the type
	// registry (see runstatus's CatalogPanel.vue, W6.2 follow-up).
	NestsUnder bool
}

// TypeDef is one type-registry entry: a node type's schema pin, optional
// parent (Extends), required envelope fields, and edge field declarations.
type TypeDef struct {
	ID             string
	Schema         SchemaPin
	Extends        string
	Summary        string
	RequiredFields []string
	EdgeFields     []EdgeFieldDecl

	// DeprecatedParentAlias is set when the type def was authored with the
	// pre-decision `derives_from:` field instead of the canonical
	// `extends:` (Shared decision 2). The loader accepts it as an alias for
	// Extends and records a warning; new type defs should use `extends:`.
	DeprecatedParentAlias bool
}

// EffectiveType is a TypeDef with its whole extends ancestry resolved: the
// union of required fields, and edge fields inherited by name. A subtype may
// add edges; redeclaring an inherited edge field incompatibly is a
// registration error (v1 does not support covariant edge overrides).
type EffectiveType struct {
	TypeDef
	// Ancestry is ordered root-to-self, inclusive of the type's own id.
	Ancestry []string
}

// Registry resolves extends chains into EffectiveTypes — GTS's
// schema-per-type-with-validation-at-registration discipline (Shared
// decision 2), without GTS's dotted-id grammar or Rust crates.
type Registry struct {
	defs      map[string]TypeDef
	effective map[string]EffectiveType
}

// NewRegistry returns an empty type registry.
func NewRegistry() *Registry {
	return &Registry{defs: map[string]TypeDef{}, effective: map[string]EffectiveType{}}
}

// Register adds a type definition. Types may be registered in any order —
// call Resolve once every type in the catalog has been registered.
func (r *Registry) Register(def TypeDef) error {
	if def.ID == "" {
		return fmt.Errorf("type registry: type with empty id")
	}
	if !IsKebabID(def.ID) {
		return fmt.Errorf("type registry: type id %q is not kebab-case", def.ID)
	}
	if _, exists := r.defs[def.ID]; exists {
		return fmt.Errorf("type registry: duplicate type id %q", def.ID)
	}
	r.defs[def.ID] = def
	return nil
}

// Resolve validates the whole registry (missing/cyclic parents, incompatible
// edge-field redeclaration) and computes every type's EffectiveType.
func (r *Registry) Resolve() error {
	r.effective = map[string]EffectiveType{}
	ids := make([]string, 0, len(r.defs))
	for id := range r.defs {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic error ordering
	for _, id := range ids {
		if _, err := r.resolve(id, nil); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) resolve(id string, chain []string) (EffectiveType, error) {
	if eff, ok := r.effective[id]; ok {
		return eff, nil
	}
	for _, seen := range chain {
		if seen == id {
			return EffectiveType{}, fmt.Errorf("type registry: cyclic extends chain: %s -> %s", strings.Join(chain, " -> "), id)
		}
	}
	def, ok := r.defs[id]
	if !ok {
		return EffectiveType{}, fmt.Errorf("type registry: unknown type %q referenced by extends chain %s", id, strings.Join(append(append([]string{}, chain...), id), " -> "))
	}
	chain = append(append([]string{}, chain...), id)

	if def.Extends == "" {
		eff := EffectiveType{TypeDef: def, Ancestry: []string{id}}
		r.effective[id] = eff
		return eff, nil
	}

	parent, err := r.resolve(def.Extends, chain)
	if err != nil {
		return EffectiveType{}, err
	}

	required := append([]string{}, parent.RequiredFields...)
	required = appendUnique(required, def.RequiredFields...)

	edgeByID := map[EdgeField]EdgeFieldDecl{}
	for _, e := range parent.EdgeFields {
		edgeByID[e.ID] = e
	}
	for _, e := range def.EdgeFields {
		if existing, ok := edgeByID[e.ID]; ok && existing != e {
			return EffectiveType{}, fmt.Errorf("type registry: %q redeclares inherited edge field %q incompatibly (inherited from ancestor of %q)", id, e.ID, def.Extends)
		}
		edgeByID[e.ID] = e
	}
	edgeIDs := make([]string, 0, len(edgeByID))
	for fieldID := range edgeByID {
		edgeIDs = append(edgeIDs, string(fieldID))
	}
	sort.Strings(edgeIDs)
	edges := make([]EdgeFieldDecl, 0, len(edgeIDs))
	for _, fieldID := range edgeIDs {
		edges = append(edges, edgeByID[EdgeField(fieldID)])
	}

	eff := EffectiveType{
		TypeDef: TypeDef{
			ID:             id,
			Schema:         def.Schema,
			Extends:        def.Extends,
			Summary:        def.Summary,
			RequiredFields: required,
			EdgeFields:     edges,
		},
		Ancestry: append(append([]string{}, parent.Ancestry...), id),
	}
	r.effective[id] = eff
	return eff, nil
}

// Effective returns the fully-resolved type, or false if unknown / Resolve
// has not been called successfully.
func (r *Registry) Effective(id string) (EffectiveType, bool) {
	eff, ok := r.effective[id]
	return eff, ok
}

// HasTypeDef reports whether a type ID exists in the registry.
func (r *Registry) HasTypeDef(id string) bool {
	_, exists := r.defs[id]
	return exists
}

// TypeDef returns the TypeDef for id, or false if not registered.
func (r *Registry) TypeDef(id string) (TypeDef, bool) {
	def, ok := r.defs[id]
	return def, ok
}


// IsA reports whether typeID equals ancestorID or extends it (directly or
// transitively) — the assignability check an edge target uses (an edge
// declared with target_type "requirement" accepts any type extending
// "requirement", e.g. an ISO pack's "iso9001-clause").
func (r *Registry) IsA(typeID, ancestorID string) bool {
	eff, ok := r.effective[typeID]
	if !ok {
		return false
	}
	for _, a := range eff.Ancestry {
		if a == ancestorID {
			return true
		}
	}
	return false
}

func appendUnique(base []string, add ...string) []string {
	seen := make(map[string]bool, len(base))
	for _, b := range base {
		seen[b] = true
	}
	for _, a := range add {
		if !seen[a] {
			base = append(base, a)
			seen[a] = true
		}
	}
	return base
}
