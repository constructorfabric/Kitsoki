package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Catalog is a loaded, type-checked project object graph.
type Catalog struct {
	Schema   SchemaPin
	Registry *Registry
	Nodes    map[NodeID]*Node

	// RootPath is the path LoadCatalog was called with (a bundle dir or a
	// single catalog file).
	RootPath string
	// NodeFile maps each node id to the file it was loaded from — for a
	// single-file catalog every node maps to RootPath; for a bundle, to
	// whichever nodes/*.yaml file declared it. Apply (W2.0) uses this to
	// know which file to rewrite for a given node.
	NodeFile map[NodeID]string

	// Warnings collects non-fatal issues surfaced during load, e.g. a type
	// def using the deprecated `derives_from:` alias instead of `extends:`.
	Warnings []string

	// LintHints optionally names the domain type/field identifiers the two
	// soft-coupled catalog-shape lints (lintOrphanFeature, lintInitiativeScope
	// — S5 de-domaining, .context/kits-implementation-plan.md D4) operate
	// over. A catalog may declare a top-level `lint_hints:` block; when it
	// doesn't (every catalog before S5, including kitsoki's own
	// seed-objects.yaml), DefaultLintHints() supplies the pre-S5 hardcoded
	// names so behavior is unchanged. This is what makes the two checks
	// "registry-data-driven instead of hardcoded": a different catalog can
	// name its own membership/scope type+field vocabulary without touching
	// Go.
	LintHints *LintHints

	// WritePolicy optionally overrides Propose's D9 auto-authorize allowlist
	// (autoAuthorizeFieldRoots in propose.go) via a top-level `write_policy:`
	// catalog block. A catalog that declares none falls back to the
	// hardcoded Go constant — same extensibility shape as LintHints/Hints().
	WritePolicy *WritePolicy

	// FeedbackRouting optionally names the parse-and-expose-only
	// `feedback_routing:` catalog block (graph-mcp plan §3.4) a later stage
	// consumes to route evidence/feedback writes to the right node/edge
	// shape. Unused by any check today; validated for shape only.
	FeedbackRouting *FeedbackRouting

	// Scope, when non-nil, marks this catalog as ApplyScope's pruned READ
	// view of a larger catalog (scope.go): Nodes holds only the scope's
	// member nodes and every edge target pointing outside the member set
	// has been dropped. A scoped catalog must never be written back to
	// disk — write ops load the full catalog and gate via
	// ScopeWriteViolations instead.
	Scope *ScopeInfo

	// ContentDigest is a fingerprint of every real on-disk file backing this
	// catalog, computed once at load time (buildCatalog, via
	// computeContentDigest in guards.go). Propose/Authorize/Withdraw/Apply
	// re-compute this immediately before copying scratch changes back over
	// the real catalog (commitWithCAS) and compare — a mismatch means a
	// concurrent writer landed in the load->scratch->copy-back window
	// (hazard guard #2, plan §3.4 red-team amendment #2), and the whole
	// operation is retried against a fresh load instead of clobbering it.
	ContentDigest string
}

// WritePolicy is the optional `write_policy:` catalog-level block.
type WritePolicy struct {
	// AutoAuthorizeFieldRoots names the Fields[...] roots (plus the bare
	// "status" path) a system-authored (Provenance-carrying) changeset may
	// touch and still self-authorize (D9). Mirrors propose.go's
	// autoAuthorizeFieldRoots constant.
	AutoAuthorizeFieldRoots []string `yaml:"auto_authorize_field_roots"`
}

// DefaultWritePolicy returns the pre-D9-generalization hardcoded allowlist,
// used whenever a catalog declares no `write_policy:` block.
func DefaultWritePolicy() WritePolicy {
	roots := make([]string, 0, len(autoAuthorizeFieldRoots))
	for root := range autoAuthorizeFieldRoots {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return WritePolicy{AutoAuthorizeFieldRoots: roots}
}

// WritePolicyOrDefault returns c.WritePolicy, or DefaultWritePolicy() when
// the catalog declared none.
func (c *Catalog) WritePolicyOrDefault() WritePolicy {
	if c.WritePolicy != nil {
		return *c.WritePolicy
	}
	return DefaultWritePolicy()
}

// FeedbackRouting is the optional `feedback_routing:` catalog-level block:
// parsed and shape-validated, but not yet consulted by any check (plan
// §3.4 item 4 — "parse-and-expose only ... later stages consume it").
type FeedbackRouting struct {
	Type   string   `yaml:"type"`
	Fields []string `yaml:"fields"`
	Edges  []string `yaml:"edges"`
}

// LintHints is the optional `lint_hints:` catalog-level block.
type LintHints struct {
	// OrphanMembership configures lintOrphanFeature: every PUBLIC node whose
	// type IsA(Type) must have >=1 target on its Edge field. Defaults to
	// {Type: "feature", Edge: "in_area"}.
	OrphanMembership MembershipHint `yaml:"orphan_membership"`
	// ScopeDerivation configures lintInitiativeScope: a ScopeType node's
	// TargetsEdge must equal the set derived by walking
	// IncludeEdge -> HopEdge -> MembershipEdge. Defaults to the initiative /
	// includes / implements / in_area / targets chain.
	ScopeDerivation ScopeHint `yaml:"scope_derivation"`
}

// MembershipHint names the (type, edge) pair lintOrphanFeature checks.
type MembershipHint struct {
	Type string `yaml:"type"`
	Edge string `yaml:"edge"`
}

// ScopeHint names the type/edge chain lintInitiativeScope checks.
type ScopeHint struct {
	ScopeType      string `yaml:"scope_type"`
	IncludeEdge    string `yaml:"include_edge"`
	HopEdge        string `yaml:"hop_edge"`
	MembershipEdge string `yaml:"membership_edge"`
	TargetsEdge    string `yaml:"targets_edge"`
}

// DefaultLintHints returns the pre-S5 hardcoded names, used whenever a
// catalog doesn't declare its own `lint_hints:` block.
func DefaultLintHints() LintHints {
	return LintHints{
		OrphanMembership: MembershipHint{Type: "feature", Edge: "in_area"},
		ScopeDerivation: ScopeHint{
			ScopeType:      "initiative",
			IncludeEdge:    "includes",
			HopEdge:        "implements",
			MembershipEdge: "in_area",
			TargetsEdge:    "targets",
		},
	}
}

// Hints returns c.LintHints, or DefaultLintHints() when the catalog declared
// none. Safe to call on a nil *Catalog only via the zero value contract of
// its caller — c is expected non-nil here.
func (c *Catalog) Hints() LintHints {
	if c.LintHints != nil {
		return *c.LintHints
	}
	return DefaultLintHints()
}

// SortedNodeIDs returns the catalog's node ids in deterministic order.
func (c *Catalog) SortedNodeIDs() []NodeID {
	ids := make([]NodeID, 0, len(c.Nodes))
	for id := range c.Nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// ParseSchemaPin splits a schema pin "<pack>/<type>/<version>" into its
// three segments.
func ParseSchemaPin(pin SchemaPin) (pack, typeID, version string, err error) {
	parts := strings.Split(string(pin), "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("schema pin %q must have shape <pack>/<type>/<version>", pin)
	}
	return parts[0], parts[1], parts[2], nil
}

// --- YAML file shapes -------------------------------------------------

type fileEdgeField struct {
	ID          string `yaml:"id"`
	TargetType  string `yaml:"target_type"`
	Cardinality string `yaml:"cardinality"`
	Storage     string `yaml:"storage"`
	Acyclic     bool   `yaml:"acyclic"`
	Renders     bool   `yaml:"renders"`
	NestsUnder  bool   `yaml:"nests_under"`
}

type fileMaterializeParam struct {
	ID          string   `yaml:"id"`
	Type        string   `yaml:"type"`
	Default     any      `yaml:"default"`
	Values      []string `yaml:"values"`
	Required    bool     `yaml:"required"`
	SourceField string   `yaml:"source_field"`
	SourceEdge  string   `yaml:"source_edge"`
}

type fileArtifactDecl struct {
	Schema       string `yaml:"schema"`
	Format       string `yaml:"format"`
	Presentation string `yaml:"presentation"`
}

type fileMaterializeCheck struct {
	ID           string         `yaml:"id"`
	Script       string         `yaml:"script"`
	ScriptField  string         `yaml:"script_field"`
	Inputs       map[string]any `yaml:"inputs"`
	InputsField  string         `yaml:"inputs_field"`
	Capabilities map[string]any `yaml:"capabilities"`
}

type fileMaterializeDecl struct {
	Story        string                 `yaml:"story"`
	ContextEdges []string               `yaml:"context_edges"`
	Params       []fileMaterializeParam `yaml:"params"`
	Gates        []string               `yaml:"gates"`
	Checks       []fileMaterializeCheck `yaml:"checks"`
}

type fileTypeDef struct {
	ID             string               `yaml:"id"`
	Schema         string               `yaml:"schema"`
	Extends        string               `yaml:"extends"`
	DerivesFrom    *string              `yaml:"derives_from"`
	Summary        string               `yaml:"summary"`
	RequiredFields []string             `yaml:"required_fields"`
	EdgeFields     []fileEdgeField      `yaml:"edge_fields"`
	Artifact       *fileArtifactDecl    `yaml:"artifact"`
	Materialize    *fileMaterializeDecl `yaml:"materialize"`
}

func (ft fileTypeDef) toTypeDef() (TypeDef, string, error) {
	def := TypeDef{
		ID:             ft.ID,
		Schema:         SchemaPin(ft.Schema),
		Summary:        ft.Summary,
		RequiredFields: ft.RequiredFields,
	}
	if ft.Artifact != nil {
		def.Artifact = &ArtifactDecl{
			Schema:       SchemaPin(ft.Artifact.Schema),
			Format:       ft.Artifact.Format,
			Presentation: ft.Artifact.Presentation,
		}
	}
	if ft.Materialize != nil {
		md := &MaterializeDecl{
			Story: ft.Materialize.Story,
			Gates: ft.Materialize.Gates,
		}
		for _, e := range ft.Materialize.ContextEdges {
			md.ContextEdges = append(md.ContextEdges, EdgeField(e))
		}
		for _, p := range ft.Materialize.Params {
			if p.SourceField != "" && p.SourceEdge != "" {
				return TypeDef{}, "", fmt.Errorf("type %q materialize param %q: source_field and source_edge are mutually exclusive", ft.ID, p.ID)
			}
			md.Params = append(md.Params, MaterializeParamDecl{
				ID:          p.ID,
				Type:        p.Type,
				Default:     p.Default,
				Values:      p.Values,
				Required:    p.Required,
				SourceField: p.SourceField,
				SourceEdge:  EdgeField(p.SourceEdge),
			})
		}
		for _, c := range ft.Materialize.Checks {
			if c.ID == "" {
				return TypeDef{}, "", fmt.Errorf("type %q materialize check: id is required", ft.ID)
			}
			if (c.Script == "") == (c.ScriptField == "") {
				return TypeDef{}, "", fmt.Errorf("type %q materialize check %q: exactly one of script: or script_field: is required", ft.ID, c.ID)
			}
			md.Checks = append(md.Checks, MaterializeCheckDecl{
				ID:           c.ID,
				Script:       c.Script,
				ScriptField:  c.ScriptField,
				Inputs:       c.Inputs,
				InputsField:  c.InputsField,
				Capabilities: c.Capabilities,
			})
		}
		def.Materialize = md
	}
	var warning string
	switch {
	case ft.Extends != "":
		def.Extends = ft.Extends
	case ft.DerivesFrom != nil && *ft.DerivesFrom != "":
		def.Extends = *ft.DerivesFrom
		def.DeprecatedParentAlias = true
		warning = fmt.Sprintf("type %q uses deprecated `derives_from:` (%q) — use `extends:` (Shared decision 2)", ft.ID, *ft.DerivesFrom)
	}
	for _, fe := range ft.EdgeFields {
		if fe.Cardinality != string(CardinalityOne) && fe.Cardinality != string(CardinalityMany) {
			return TypeDef{}, "", fmt.Errorf("type %q edge field %q: cardinality must be \"one\" or \"many\", got %q", ft.ID, fe.ID, fe.Cardinality)
		}
		def.EdgeFields = append(def.EdgeFields, EdgeFieldDecl{
			ID:          EdgeField(fe.ID),
			TargetType:  fe.TargetType,
			Cardinality: Cardinality(fe.Cardinality),
			Storage:     EdgeStorage(fe.Storage),
			Acyclic:     fe.Acyclic,
			Renders:     fe.Renders,
			NestsUnder:  fe.NestsUnder,
		})
	}
	return def, warning, nil
}

type fileNode struct {
	Schema     string              `yaml:"schema"`
	ID         string              `yaml:"id"`
	Title      string              `yaml:"title"`
	Status     string              `yaml:"status"`
	Visibility string              `yaml:"visibility"`
	Sources    []string            `yaml:"sources"`
	Edges      map[string][]string `yaml:"edges"`
	Extra      map[string]any      `yaml:",inline"`
}

type fileSingleCatalog struct {
	Schema          string           `yaml:"schema"`
	Catalog         map[string]any   `yaml:"catalog"`
	TypeRegistry    []fileTypeDef    `yaml:"type_registry"`
	LintHints       *LintHints       `yaml:"lint_hints"`
	WritePolicy     *WritePolicy     `yaml:"write_policy"`
	FeedbackRouting *FeedbackRouting `yaml:"feedback_routing"`
	Nodes           []fileNode       `yaml:"nodes"`
}

// --- loading ------------------------------------------------------------

// LoadCatalog loads a project object graph catalog from path. If path is a
// directory, it is loaded as a bundle catalog (catalog.yaml +
// type_registry.yaml + sources.yaml + nodes/*.yaml); otherwise it is loaded
// as a single-file catalog (the seed-objects.yaml review fixture shape).
func LoadCatalog(path string) (*Catalog, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("graph: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return loadBundle(path)
	}
	return loadSingleFile(path)
}

func loadSingleFile(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("graph: read %s: %w", path, err)
	}
	var file fileSingleCatalog
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("graph: parse %s: %w", path, err)
	}
	sources := make([]nodeSource, len(file.Nodes))
	for i, n := range file.Nodes {
		sources[i] = nodeSource{node: n, file: path}
	}
	cat, err := buildCatalog(path, SchemaPin(file.Schema), file.TypeRegistry, sources)
	if err != nil {
		return nil, err
	}
	cat.LintHints = file.LintHints
	cat.WritePolicy = file.WritePolicy
	cat.FeedbackRouting = file.FeedbackRouting
	return cat, nil
}

// LoadCatalogWithOverlay loads basePath (single-file or bundle, same as
// LoadCatalog) as the current state, then unions in overlayPath's `nodes:`
// list to produce the desired state a diff-mode caller wants — a small,
// additive delta file instead of a hand-duplicated full catalog copy. This
// is intentionally additive-only: overlayPath declaring an id that already
// exists in basePath is a duplicate-id error (buildCatalog's existing
// invariant), the same way two bundle files declaring the same id today
// error. Representing a *modified* or *removed* node in the desired state
// needs the real changeset apply/merge machinery (epic slice 2, not yet
// built), not this loader.
func LoadCatalogWithOverlay(basePath, overlayPath string) (*Catalog, error) {
	base, err := LoadCatalog(basePath)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(overlayPath)
	if err != nil {
		return nil, fmt.Errorf("graph: read overlay %s: %w", overlayPath, err)
	}
	var overlay struct {
		Nodes []fileNode `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(raw, &overlay); err != nil {
		return nil, fmt.Errorf("graph: parse overlay %s: %w", overlayPath, err)
	}

	merged := &Catalog{
		Schema:   base.Schema,
		Registry: base.Registry,
		Nodes:    make(map[NodeID]*Node, len(base.Nodes)+len(overlay.Nodes)),
		NodeFile: make(map[NodeID]string, len(base.NodeFile)+len(overlay.Nodes)),
		RootPath: basePath,
		Warnings: base.Warnings,
	}
	for id, n := range base.Nodes {
		merged.Nodes[id] = n
		merged.NodeFile[id] = base.NodeFile[id]
	}
	for _, fn := range overlay.Nodes {
		node, err := buildNode(fn, merged.Registry)
		if err != nil {
			return nil, fmt.Errorf("graph: overlay %s: %w", overlayPath, err)
		}
		if _, exists := merged.Nodes[node.ID]; exists {
			return nil, fmt.Errorf("graph: overlay %s: duplicate node id %q already present in %s", overlayPath, node.ID, basePath)
		}
		merged.Nodes[node.ID] = node
		merged.NodeFile[node.ID] = overlayPath
	}
	return merged, nil
}

func loadBundle(dir string) (*Catalog, error) {
	var schema string
	if raw, err := os.ReadFile(filepath.Join(dir, "catalog.yaml")); err == nil {
		var cat struct {
			Schema string `yaml:"schema"`
		}
		if err := yaml.Unmarshal(raw, &cat); err != nil {
			return nil, fmt.Errorf("graph: parse %s/catalog.yaml: %w", dir, err)
		}
		schema = cat.Schema
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("graph: read %s/catalog.yaml: %w", dir, err)
	}

	var typeDefs []fileTypeDef
	if raw, err := os.ReadFile(filepath.Join(dir, "type_registry.yaml")); err == nil {
		if err := yaml.Unmarshal(raw, &typeDefs); err != nil {
			return nil, fmt.Errorf("graph: parse %s/type_registry.yaml: %w", dir, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("graph: read %s/type_registry.yaml: %w", dir, err)
	}

	nodeFiles, err := filepath.Glob(filepath.Join(dir, "nodes", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("graph: glob %s/nodes/*.yaml: %w", dir, err)
	}
	sort.Strings(nodeFiles)

	var sources []nodeSource
	for _, nf := range nodeFiles {
		raw, err := os.ReadFile(nf)
		if err != nil {
			return nil, fmt.Errorf("graph: read %s: %w", nf, err)
		}
		var batch []fileNode
		if err := yaml.Unmarshal(raw, &batch); err != nil {
			return nil, fmt.Errorf("graph: parse %s: %w", nf, err)
		}
		for _, n := range batch {
			sources = append(sources, nodeSource{node: n, file: nf})
		}
	}

	return buildCatalog(dir, SchemaPin(schema), typeDefs, sources)
}

// nodeSource pairs a decoded node with the file it came from, so Apply
// (W2.0) knows which file to rewrite for a given node id.
type nodeSource struct {
	node fileNode
	file string
}

func buildCatalog(rootPath string, schema SchemaPin, typeDefs []fileTypeDef, sources []nodeSource) (*Catalog, error) {
	cat := &Catalog{
		Schema:   schema,
		Registry: NewRegistry(),
		Nodes:    map[NodeID]*Node{},
		NodeFile: map[NodeID]string{},
		RootPath: rootPath,
	}

	for _, ft := range typeDefs {
		def, warning, err := ft.toTypeDef()
		if err != nil {
			return nil, err
		}
		if warning != "" {
			cat.Warnings = append(cat.Warnings, warning)
		}
		if err := cat.Registry.Register(def); err != nil {
			return nil, err
		}
	}
	if err := cat.Registry.Resolve(); err != nil {
		return nil, err
	}

	for _, src := range sources {
		node, err := buildNode(src.node, cat.Registry)
		if err != nil {
			return nil, err
		}
		if _, exists := cat.Nodes[node.ID]; exists {
			return nil, fmt.Errorf("graph: duplicate node id %q", node.ID)
		}
		cat.Nodes[node.ID] = node
		cat.NodeFile[node.ID] = src.file
	}

	digest, err := computeContentDigest(cat)
	if err != nil {
		return nil, fmt.Errorf("graph: content digest %s: %w", rootPath, err)
	}
	cat.ContentDigest = digest

	return cat, nil
}

func buildNode(fn fileNode, reg *Registry) (*Node, error) {
	if fn.ID == "" {
		return nil, fmt.Errorf("graph: node missing id (schema %q)", fn.Schema)
	}
	if !IsKebabID(fn.ID) {
		return nil, fmt.Errorf("graph: node %q is not a kebab-case id", fn.ID)
	}
	if fn.Schema == "" {
		return nil, fmt.Errorf("graph: node %q missing schema", fn.ID)
	}
	_, typeID, _, err := ParseSchemaPin(SchemaPin(fn.Schema))
	if err != nil {
		return nil, fmt.Errorf("graph: node %q: %w", fn.ID, err)
	}
	eff, ok := reg.Effective(typeID)
	if !ok {
		return nil, fmt.Errorf("graph: node %q has unknown type %q (schema %q)", fn.ID, typeID, fn.Schema)
	}
	if fn.Title == "" {
		return nil, fmt.Errorf("graph: node %q missing title", fn.ID)
	}
	if fn.Status == "" {
		return nil, fmt.Errorf("graph: node %q missing status", fn.ID)
	}
	if fn.Visibility == "" {
		return nil, fmt.Errorf("graph: node %q missing visibility", fn.ID)
	}
	vis := Visibility(fn.Visibility)
	if vis != VisibilityPublic && vis != VisibilityInternal {
		return nil, fmt.Errorf("graph: node %q has invalid visibility %q (must be public or internal)", fn.ID, fn.Visibility)
	}

	node := &Node{
		Schema:     SchemaPin(fn.Schema),
		ID:         NodeID(fn.ID),
		Title:      fn.Title,
		Status:     fn.Status,
		Visibility: vis,
		TypeID:     typeID,
		Fields:     fn.Extra,
	}
	for _, s := range fn.Sources {
		node.Sources = append(node.Sources, NodeID(s))
	}
	if len(fn.Edges) > 0 {
		node.Edges = map[EdgeField][]NodeID{}
		for field, targets := range fn.Edges {
			ids := make([]NodeID, 0, len(targets))
			for _, t := range targets {
				ids = append(ids, NodeID(t))
			}
			node.Edges[EdgeField(field)] = ids
		}
	}

	if err := checkEdgeCardinality(node, eff); err != nil {
		return nil, err
	}

	return node, nil
}

// checkEdgeCardinality enforces the LOCAL shape of a declared cardinality
// (a "one" edge may have at most one target). It does not check that edge
// targets exist or are visibility-safe — that is W1.1's catalog lint, which
// needs the whole catalog assembled first.
func checkEdgeCardinality(node *Node, eff EffectiveType) error {
	for _, decl := range eff.EdgeFields {
		targets := node.EdgeTargets(decl)
		if decl.Cardinality == CardinalityOne && len(targets) > 1 {
			return fmt.Errorf("graph: node %q edge %q has cardinality \"one\" but %d targets", node.ID, decl.ID, len(targets))
		}
	}
	return nil
}
