package graph

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// OpKind is the kind of a single changeset operation.
type OpKind string

const (
	OpAdded    OpKind = "added"
	OpModified OpKind = "modified"
	OpRemoved  OpKind = "removed"
	OpRenamed  OpKind = "renamed"
	OpRetyped  OpKind = "retyped"
	OpRegistryTypeAdded    OpKind = "registry_type_added"
	OpRegistryTypeModified OpKind = "registry_type_modified"
)

// FieldChange is one field-level edit within a "modified" operation. Path
// mirrors Node's own field names: ["title"|"status"|"visibility"], ["sources"],
// ["edges", "<edge-field>"], or ["fields", "<type-specific-key>"]. Before is
// an optimistic stale-guard — apply rejects the whole changeset if the
// catalog's current value doesn't match it.
type FieldChange struct {
	Path   []string
	Before any
	After  any
}

// Operation is one entry in a changeset's ordered operations list.
type Operation struct {
	Kind OpKind

	// Node is the target node id for added/modified/removed/retyped. For renamed,
	// use From/To instead (NodeID is catalog-global, so a rename is a
	// distinct shape, not a field edit). For registry type ops, Node represents the type ID.
	Node NodeID

	After   map[string]any // added/registry_type_added: the full new mapping (must include "id" == Node)
	Before  map[string]any // removed: the full expected current node mapping (audit + stale-guard)
	Changes []FieldChange  // modified/registry_type_modified

	From NodeID // renamed
	To   NodeID // renamed

	FromType string // retyped
	ToType   string // retyped
}

// Changeset is the parsed, typed form of a `graph/changeset/v1` node's
// operations field. The changeset's lifecycle (ECR/ECO/ECN) rides on the
// node's own Status field — proposed / authorized / notified — not a
// second, changeset-specific field (Shared decision + WM.1-style W2.0
// design session: one source of truth for lifecycle state).
type Changeset struct {
	NodeID     NodeID
	Status     string
	Operations []Operation
}

const (
	ChangesetStatusProposed   = "proposed"
	ChangesetStatusAuthorized = "authorized"
	ChangesetStatusNotified   = "notified"
)

// ParseChangeset decodes a changeset node's Fields["operations"] into typed
// Operations. It does not validate the operations against a catalog — see
// ValidateChangeset for that.
func ParseChangeset(node *Node) (*Changeset, error) {
	if node.TypeID != "changeset" {
		return nil, fmt.Errorf("graph: node %q is type %q, not changeset", node.ID, node.TypeID)
	}
	raw, ok := node.Fields["operations"]
	if !ok {
		return nil, fmt.Errorf("graph: changeset %q missing operations", node.ID)
	}
	rawOps, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("graph: changeset %q operations must be a list", node.ID)
	}

	cs := &Changeset{NodeID: node.ID, Status: node.Status}
	for i, rawOp := range rawOps {
		opMap, ok := rawOp.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("graph: changeset %q operation %d must be a mapping", node.ID, i)
		}
		op, err := parseOperation(opMap)
		if err != nil {
			return nil, fmt.Errorf("graph: changeset %q operation %d: %w", node.ID, i, err)
		}
		cs.Operations = append(cs.Operations, op)
	}
	return cs, nil
}

func parseOperation(m map[string]any) (Operation, error) {
	kindRaw, _ := m["kind"].(string)
	kind := OpKind(kindRaw)
	switch kind {
	case OpAdded:
		after, ok := m["after"].(map[string]any)
		if !ok {
			return Operation{}, fmt.Errorf("added op missing \"after\" mapping")
		}
		idVal, _ := after["id"].(string)
		if idVal == "" {
			return Operation{}, fmt.Errorf("added op's \"after\" is missing \"id\"")
		}
		return Operation{Kind: kind, Node: NodeID(idVal), After: after}, nil
	case OpModified:
		nodeID, _ := m["node"].(string)
		if nodeID == "" {
			return Operation{}, fmt.Errorf("modified op missing \"node\"")
		}
		rawChanges, _ := m["changes"].([]any)
		if len(rawChanges) == 0 {
			return Operation{}, fmt.Errorf("modified op %q missing non-empty \"changes\"", nodeID)
		}
		var changes []FieldChange
		for i, rc := range rawChanges {
			cm, ok := rc.(map[string]any)
			if !ok {
				return Operation{}, fmt.Errorf("modified op %q change %d must be a mapping", nodeID, i)
			}
			rawPath, _ := cm["path"].([]any)
			if len(rawPath) == 0 {
				return Operation{}, fmt.Errorf("modified op %q change %d missing \"path\"", nodeID, i)
			}
			path := make([]string, len(rawPath))
			for j, p := range rawPath {
				s, ok := p.(string)
				if !ok {
					return Operation{}, fmt.Errorf("modified op %q change %d path segment %d must be a string", nodeID, i, j)
				}
				path[j] = s
			}
			changes = append(changes, FieldChange{Path: path, Before: cm["before"], After: cm["after"]})
		}
		return Operation{Kind: kind, Node: NodeID(nodeID), Changes: changes}, nil
	case OpRemoved:
		nodeID, _ := m["node"].(string)
		if nodeID == "" {
			return Operation{}, fmt.Errorf("removed op missing \"node\"")
		}
		before, ok := m["before"].(map[string]any)
		if !ok {
			return Operation{}, fmt.Errorf("removed op %q missing \"before\" mapping", nodeID)
		}
		return Operation{Kind: kind, Node: NodeID(nodeID), Before: before}, nil
	case OpRenamed:
		from, _ := m["from"].(string)
		to, _ := m["to"].(string)
		if from == "" || to == "" {
			return Operation{}, fmt.Errorf("renamed op requires both \"from\" and \"to\"")
		}
		return Operation{Kind: kind, From: NodeID(from), To: NodeID(to)}, nil
	case OpRetyped:
		nodeID, _ := m["node"].(string)
		fromType, _ := m["from_type"].(string)
		toType, _ := m["to_type"].(string)
		if nodeID == "" || fromType == "" || toType == "" {
			return Operation{}, fmt.Errorf("retyped op requires \"node\", \"from_type\", and \"to_type\"")
		}
		return Operation{Kind: kind, Node: NodeID(nodeID), FromType: fromType, ToType: toType}, nil
	case OpRegistryTypeAdded:
		after, ok := m["after"].(map[string]any)
		if !ok {
			return Operation{}, fmt.Errorf("registry_type_added op missing \"after\" mapping")
		}
		idVal, _ := after["id"].(string)
		if idVal == "" {
			return Operation{}, fmt.Errorf("registry_type_added op's \"after\" is missing \"id\"")
		}
		return Operation{Kind: kind, Node: NodeID(idVal), After: after}, nil
	case OpRegistryTypeModified:
		nodeID, _ := m["node"].(string)
		if nodeID == "" {
			return Operation{}, fmt.Errorf("registry_type_modified op missing \"node\"")
		}
		rawChanges, _ := m["changes"].([]any)
		if len(rawChanges) == 0 {
			return Operation{}, fmt.Errorf("registry_type_modified op %q missing non-empty \"changes\"", nodeID)
		}
		var changes []FieldChange
		for i, rc := range rawChanges {
			cm, ok := rc.(map[string]any)
			if !ok {
				return Operation{}, fmt.Errorf("registry_type_modified op %q change %d must be a mapping", nodeID, i)
			}
			rawPath, _ := cm["path"].([]any)
			if len(rawPath) == 0 {
				return Operation{}, fmt.Errorf("registry_type_modified op %q change %d missing \"path\"", nodeID, i)
			}
			path := make([]string, len(rawPath))
			for j, p := range rawPath {
				s, ok := p.(string)
				if !ok {
					return Operation{}, fmt.Errorf("registry_type_modified op %q change %d path segment %d must be a string", nodeID, i, j)
				}
				path[j] = s
			}
			changes = append(changes, FieldChange{Path: path, Before: cm["before"], After: cm["after"]})
		}
		return Operation{Kind: kind, Node: NodeID(nodeID), Changes: changes}, nil
	default:
		return Operation{}, fmt.Errorf("unknown op kind %q", kindRaw)
	}
}

// ValidateChangeset runs the pre-apply checks a changeset must pass before
// Apply will even attempt to build a candidate: node existence per op kind,
// and the optimistic stale-guard (before values must match the catalog's
// current state). It does not run graph.Lint — that's a post-apply check
// against the candidate catalog, done by Apply.
func ValidateChangeset(cs *Changeset, cat *Catalog) []string {
	var reasons []string
	reg := cloneRegistry(cat.Registry)

	for i, op := range cs.Operations {
		switch op.Kind {
		case OpAdded:
			if _, exists := cat.Nodes[op.Node]; exists {
				reasons = append(reasons, fmt.Sprintf("op %d (added): node %q already exists", i, op.Node))
			}
		case OpModified:
			node, exists := cat.Nodes[op.Node]
			if !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (modified): node %q does not exist", i, op.Node))
				continue
			}
			for _, ch := range op.Changes {
				current := readNodePath(node, ch.Path)
				if ch.Before != nil && !valuesEqual(current, ch.Before) {
					reasons = append(reasons, fmt.Sprintf("op %d (modified): node %q path %v is stale: catalog has %v, changeset expected %v", i, op.Node, ch.Path, current, ch.Before))
				}
			}
		case OpRemoved:
			if _, exists := cat.Nodes[op.Node]; !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (removed): node %q does not exist", i, op.Node))
			}
		case OpRenamed:
			if _, exists := cat.Nodes[op.From]; !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (renamed): node %q (from) does not exist", i, op.From))
			}
			if _, exists := cat.Nodes[op.To]; exists {
				reasons = append(reasons, fmt.Sprintf("op %d (renamed): node %q (to) already exists", i, op.To))
			}
		case OpRetyped:
			node, exists := cat.Nodes[op.Node]
			if !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q does not exist", i, op.Node))
				continue
			}
			if node.TypeID != op.FromType {
				reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q type is %q, expected %q", i, op.Node, node.TypeID, op.FromType))
			}
			if !reg.HasTypeDef(op.ToType) {
				reasons = append(reasons, fmt.Sprintf("op %d (retyped): target type %q does not exist in registry", i, op.ToType))
				continue
			}
			effTo, _ := reg.Effective(op.ToType)
			for _, reqField := range effTo.RequiredFields {
				if !isFieldSatisfied(node, reqField) {
					reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q missing required field %q under target type %q", i, op.Node, reqField, op.ToType))
				}
			}
			declaredEdges := map[EdgeField]bool{}
			for _, decl := range effTo.EdgeFields {
				declaredEdges[decl.ID] = true
			}
			effFrom, _ := reg.Effective(op.FromType)
			for _, decl := range effFrom.EdgeFields {
				targets := node.EdgeTargets(decl)
				if len(targets) > 0 && !declaredEdges[decl.ID] {
					reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q carries edge field %q which is not declared by target type %q", i, op.Node, decl.ID, op.ToType))
				}
			}
			for edgeField, targets := range node.Edges {
				if len(targets) > 0 && !declaredEdges[edgeField] {
					reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q carries edge field %q which is not declared by target type %q", i, op.Node, edgeField, op.ToType))
				}
			}
			for _, otherNode := range cat.Nodes {
				otherEff, ok := reg.Effective(otherNode.TypeID)
				if !ok {
					continue
				}
				for _, decl := range otherEff.EdgeFields {
					for _, target := range otherNode.EdgeTargets(decl) {
						if target == op.Node {
							if decl.TargetType != "" && !reg.IsA(op.ToType, decl.TargetType) {
								reasons = append(reasons, fmt.Sprintf("op %d (retyped): inbound edge %q on node %q (type %q) targets %q, but %q is not a %q",
									i, decl.ID, otherNode.ID, otherNode.TypeID, op.Node, op.ToType, decl.TargetType))
							}
						}
					}
				}
			}
		case OpRegistryTypeAdded:
			if reg.HasTypeDef(string(op.Node)) {
				reasons = append(reasons, fmt.Sprintf("op %d (registry_type_added): type %q already exists", i, op.Node))
			} else {
				var fileDef fileTypeDef
				raw, err := yaml.Marshal(op.After)
				if err == nil {
					_ = yaml.Unmarshal(raw, &fileDef)
				}
				def := TypeDef{
					ID:             fileDef.ID,
					Schema:         SchemaPin(fileDef.Schema),
					Extends:        fileDef.Extends,
					Summary:        fileDef.Summary,
					RequiredFields: fileDef.RequiredFields,
				}
				for _, e := range fileDef.EdgeFields {
					def.EdgeFields = append(def.EdgeFields, EdgeFieldDecl{
						ID:          EdgeField(e.ID),
						TargetType:  e.TargetType,
						Cardinality: Cardinality(e.Cardinality),
						Storage:     EdgeStorage(e.Storage),
						Acyclic:     e.Acyclic,
						Renders:     e.Renders,
						NestsUnder:  e.NestsUnder,
					})
				}
				_ = reg.Register(def)
				_ = reg.Resolve()
			}
		case OpRegistryTypeModified:
			def, exists := reg.TypeDef(string(op.Node))
			if !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (registry_type_modified): type %q does not exist", i, op.Node))
				continue
			}
			for _, ch := range op.Changes {
				current := readTypeDefPath(def, ch.Path)
				if ch.Before != nil && !valuesEqual(current, ch.Before) {
					reasons = append(reasons, fmt.Sprintf("op %d (registry_type_modified): type %q path %v is stale: catalog has %v, changeset expected %v", i, op.Node, ch.Path, current, ch.Before))
				}
			}
			for _, ch := range op.Changes {
				if len(ch.Path) == 1 {
					switch ch.Path[0] {
					case "extends":
						def.Extends, _ = ch.After.(string)
					case "summary":
						def.Summary, _ = ch.After.(string)
					case "required_fields":
						if rawList, ok := ch.After.([]any); ok {
							var list []string
							for _, item := range rawList {
								if s, ok := item.(string); ok {
									list = append(list, s)
								}
							}
							def.RequiredFields = list
						}
					case "edge_fields":
						if rawList, ok := ch.After.([]any); ok {
							var edges []EdgeFieldDecl
							for _, item := range rawList {
								if m, ok := item.(map[string]any); ok {
									id, _ := m["id"].(string)
									tt, _ := m["target_type"].(string)
									card, _ := m["cardinality"].(string)
									st, _ := m["storage"].(string)
									ac, _ := m["acyclic"].(bool)
									ren, _ := m["renders"].(bool)
									nu, _ := m["nests_under"].(bool)
									edges = append(edges, EdgeFieldDecl{
										ID:          EdgeField(id),
										TargetType:  tt,
										Cardinality: Cardinality(card),
										Storage:     EdgeStorage(st),
										Acyclic:     ac,
										Renders:     ren,
										NestsUnder:  nu,
									})
								}
							}
							def.EdgeFields = edges
						}
					}
				}
			}
			reg.defs[string(op.Node)] = def
			_ = reg.Resolve()
		}
	}
	return reasons
}

func cloneRegistry(r *Registry) *Registry {
	clone := NewRegistry()
	for id, def := range r.defs {
		clone.defs[id] = def
	}
	_ = clone.Resolve()
	return clone
}


// readNodePath reads the current value at a FieldChange-style path off a
// live Node, for the stale-guard comparison in ValidateChangeset.
func readNodePath(node *Node, path []string) any {
	if len(path) == 0 {
		return nil
	}
	switch path[0] {
	case "title":
		return node.Title
	case "status":
		return node.Status
	case "visibility":
		return string(node.Visibility)
	case "sources":
		return node.Sources
	case "edges":
		if len(path) != 2 {
			return nil
		}
		return node.Edges[EdgeField(path[1])]
	case "fields":
		if len(path) != 2 {
			return nil
		}
		return node.Fields[path[1]]
	default:
		return nil
	}
}

// valuesEqual is a pragmatic stale-guard comparison (string rendering, not a
// deep-equal) — good enough to catch "someone else already changed this
// field" without needing a full YAML-value equality library for v1.
func valuesEqual(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func isFieldSatisfied(node *Node, fieldName string) bool {
	switch fieldName {
	case "id":
		return node.ID != ""
	case "schema":
		return node.Schema != ""
	case "title":
		return node.Title != ""
	case "status":
		return node.Status != ""
	case "visibility":
		return node.Visibility != ""
	case "sources":
		return len(node.Sources) > 0
	default:
		val, exists := node.Fields[fieldName]
		if !exists || val == nil {
			return false
		}
		if s, ok := val.(string); ok && s == "" {
			return false
		}
		return true
	}
}

func readTypeDefPath(def TypeDef, path []string) any {
	if len(path) == 0 {
		return nil
	}
	switch path[0] {
	case "schema":
		return string(def.Schema)
	case "extends":
		return def.Extends
	case "summary":
		return def.Summary
	case "required_fields":
		return def.RequiredFields
	case "edge_fields":
		return def.EdgeFields
	default:
		return nil
	}
}

