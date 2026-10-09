// Package graph implements the project object graph substrate: a single
// generic node envelope, a type registry with extends-based derivation, and
// a loader for both the single-file review fixture
// (docs/proposals/project-object-graph/seed-objects.yaml) and the bundle
// catalog layout (catalog.yaml + type_registry.yaml + sources.yaml +
// nodes/*.yaml) that W1.0's design pass chose for real catalogs.
//
// Cross-node validation (cycles, dangling refs, visibility leaks) is W1.1's
// catalog lint, not this package's loader — see decomposition.yaml.
package graph

import "regexp"

// SchemaPin identifies a node's or type's schema, e.g. "graph/feature/v0" or
// "project-object-graph/seed-catalog/v0". Shared decision 2 (the epic) makes
// "<pack>/<type>/v1" the canonical shape; existing fixtures still pin v0 and
// load unchanged.
type SchemaPin string

// NodeID is a catalog-unique kebab-case identifier.
type NodeID string

// EdgeField is the name of a typed edge slot on a node type, e.g.
// "requirements" or "depends_on".
type EdgeField string

// Visibility controls whether a node may be reachable from public output.
// Default is internal; public site codegen (G3) hard-fails on an internal
// node reachable from a public edge rather than silently leaking it.
type Visibility string

const (
	VisibilityPublic   Visibility = "public"
	VisibilityInternal Visibility = "internal"
)

// Node is the single envelope every project object graph node uses,
// regardless of type (Shared decision 1: one envelope, not N bespoke
// objects). Per-type scalars (a requirement's `statement`, a use-case's
// `trigger`, a change's `goal`/`scope`/`acceptance`/`depends_on`, ...) are
// not promoted into the envelope; they live in Fields.
type Node struct {
	Schema     SchemaPin
	ID         NodeID
	Title      string
	Status     string
	Visibility Visibility
	Sources    []NodeID

	// Edges holds cardinality-many-or-one typed edges keyed by field name.
	// A cardinality-one edge still serializes as a one-element slice —
	// cardinality is a lint/registry concern, not a Go type concern.
	Edges map[EdgeField][]NodeID

	// Fields carries every type-specific value the envelope does not
	// promote to a first-class field, decoded as raw YAML values. This
	// includes top-level scalars used by TopLevel-storage edge decls (e.g.
	// a change node's `depends_on`), which is how the shipped
	// schemas/change-node.schema.json contract stays flag-day-free: those
	// fields are read from here, not from Edges.
	Fields map[string]any

	// TypeID is the resolved type-registry id for this node (parsed from
	// Schema's middle segment), set by the loader.
	TypeID string
}

// EdgeTargets returns the target node ids for decl, reading from Edges or
// from Fields depending on decl.Storage — the one place that knows how to
// read a top_level-storage edge (e.g. change.depends_on) the same way as a
// normal Edges-storage one.
func (n *Node) EdgeTargets(decl EdgeFieldDecl) []NodeID {
	if decl.Storage == StorageTopLevel {
		raw, ok := n.Fields[string(decl.ID)]
		if !ok || raw == nil {
			return nil
		}
		switch v := raw.(type) {
		case []any:
			ids := make([]NodeID, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					ids = append(ids, NodeID(s))
				}
			}
			return ids
		case []string:
			ids := make([]NodeID, 0, len(v))
			for _, s := range v {
				ids = append(ids, NodeID(s))
			}
			return ids
		case string:
			return []NodeID{NodeID(v)}
		default:
			return nil
		}
	}
	return n.Edges[decl.ID]
}

// kebabPattern matches the kebab-case id convention (lifecycle-taxonomy.md
// container conventions): lowercase, starts with a letter, hyphens allowed.
var kebabPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// IsKebabID reports whether s is a valid kebab-case identifier.
func IsKebabID(s string) bool {
	return kebabPattern.MatchString(s)
}
