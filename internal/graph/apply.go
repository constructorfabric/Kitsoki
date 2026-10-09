package graph

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ApplyResult reports what happened attempting to apply a changeset.
type ApplyResult struct {
	// Applied is true only when the changeset's operations were
	// successfully written to the real catalog at RootPath.
	Applied bool
	// ChangedFiles are the files (relative to the catalog root) that were
	// rewritten. Populated on a successful apply or a successful dry-run.
	ChangedFiles []string
	// RejectReasons is non-empty when the changeset failed a pre-apply
	// validation check (ValidateChangeset, or changeset shape/status).
	// The real catalog was never touched.
	RejectReasons []string
	// LintIssues is non-empty when the changeset's operations applied
	// cleanly to a candidate catalog, but that candidate failed
	// graph.Lint. The real catalog was never touched.
	LintIssues []LintIssue
}

// Rejected reports whether Apply refused to apply the changeset, for any
// reason (pre-apply validation or post-apply lint).
func (r *ApplyResult) Rejected() bool {
	return len(r.RejectReasons) > 0 || len(r.LintIssues) > 0
}

// Apply loads the catalog at rootPath, finds the changeset node
// changesetID, and — dry-run-first — builds a candidate catalog on a
// scratch copy of the tree, re-loads and re-lints that candidate, and only
// if it comes back clean copies the changed files over rootPath. A rejected
// changeset (failing either pre-apply validation or post-apply lint) never
// touches rootPath (Shared decision 3 / W2.0 design session Q4).
//
// dryRun previews the result (ChangedFiles reflects what WOULD change)
// without committing, and does not require the changeset to be in
// "authorized" status.
func Apply(rootPath string, changesetID NodeID, dryRun bool) (*ApplyResult, error) {
	cat, err := LoadCatalog(rootPath)
	if err != nil {
		return nil, fmt.Errorf("graph apply: load %s: %w", rootPath, err)
	}
	csNode, ok := cat.Nodes[changesetID]
	if !ok {
		return nil, fmt.Errorf("graph apply: changeset %q not found in catalog", changesetID)
	}
	cs, err := ParseChangeset(csNode)
	if err != nil {
		return nil, fmt.Errorf("graph apply: %w", err)
	}
	if !dryRun && cs.Status != ChangesetStatusAuthorized {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("changeset %q has status %q, must be %q to apply (pass dryRun to preview an unauthorized changeset)", cs.NodeID, cs.Status, ChangesetStatusAuthorized),
		}}, nil
	}
	if reasons := ValidateChangeset(cs, cat); len(reasons) > 0 {
		return &ApplyResult{RejectReasons: reasons}, nil
	}

	tmpDir, err := os.MkdirTemp("", "kitsoki-graph-apply-*")
	if err != nil {
		return nil, fmt.Errorf("graph apply: mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	scratchRoot, err := copyCatalogTree(rootPath, tmpDir, cat)
	if err != nil {
		return nil, fmt.Errorf("graph apply: copy scratch tree: %w", err)
	}

	changedFiles, err := applyOperations(cat, cs.Operations, scratchRoot)
	if err != nil {
		return &ApplyResult{RejectReasons: []string{err.Error()}}, nil
	}

	candidate, err := LoadCatalog(scratchRoot)
	if err != nil {
		return &ApplyResult{RejectReasons: []string{fmt.Sprintf("candidate catalog failed to load: %v", err)}}, nil
	}
	if issues := Lint(candidate); len(issues) > 0 {
		return &ApplyResult{LintIssues: issues}, nil
	}

	if dryRun {
		return &ApplyResult{Applied: false, ChangedFiles: changedFiles}, nil
	}

	realRoot := rootDir(rootPath)
	for _, rel := range changedFiles {
		src := filepath.Join(scratchRoot, rel)
		dst := filepath.Join(realRoot, rel)
		if err := copyFile(src, dst); err != nil {
			return nil, fmt.Errorf("graph apply: commit %s: %w", rel, err)
		}
	}
	return &ApplyResult{Applied: true, ChangedFiles: changedFiles}, nil
}

// rootDir returns the directory a relative changed-file path should be
// resolved against: the bundle dir itself, or the single file's parent dir
// (since a single-file catalog's "relative path" is just its own basename).
func rootDir(rootPath string) string {
	info, err := os.Stat(rootPath)
	if err == nil && !info.IsDir() {
		return filepath.Dir(rootPath)
	}
	return rootPath
}

// copyCatalogTree copies every file the loaded catalog is made of (the
// single file, or catalog.yaml/type_registry.yaml/nodes/*.yaml) into tmpDir,
// preserving relative layout, and returns the scratch root to LoadCatalog
// against (a file path or a dir, mirroring rootPath's own shape).
func copyCatalogTree(rootPath, tmpDir string, cat *Catalog) (string, error) {
	info, err := os.Stat(rootPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		dst := filepath.Join(tmpDir, filepath.Base(rootPath))
		if err := copyFile(rootPath, dst); err != nil {
			return "", err
		}
		return dst, nil
	}

	files := map[string]bool{}
	for _, f := range cat.NodeFile {
		files[f] = true
	}
	for _, extra := range []string{"catalog.yaml", "type_registry.yaml", "sources.yaml"} {
		if _, err := os.Stat(filepath.Join(rootPath, extra)); err == nil {
			files[filepath.Join(rootPath, extra)] = true
		}
	}
	for f := range files {
		rel, err := filepath.Rel(rootPath, f)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := copyFile(f, dst); err != nil {
			return "", err
		}
	}
	return tmpDir, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// applyOperations rewrites the YAML files under scratchRoot in place (via
// yaml.v3 Node trees, so comments/formatting survive) and returns the paths
// (relative to scratchRoot) of every file it touched.
func applyOperations(cat *Catalog, ops []Operation, scratchRoot string) ([]string, error) {
	// Load every catalog file's document once, keyed by its scratch path.
	docs := map[string]*yaml.Node{}
	touched := map[string]bool{}
	loadDoc := func(scratchFile string) (*yaml.Node, error) {
		if doc, ok := docs[scratchFile]; ok {
			return doc, nil
		}
		raw, err := os.ReadFile(scratchFile)
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", scratchFile, err)
		}
		docs[scratchFile] = &doc
		return &doc, nil
	}
	scratchFileFor := func(realFile string) (string, error) {
		rel, err := filepath.Rel(rootDir(cat.RootPath), realFile)
		if err != nil {
			return "", err
		}
		info, statErr := os.Stat(cat.RootPath)
		if statErr == nil && !info.IsDir() {
			return scratchRoot, nil // single-file catalog: scratchRoot IS the one file
		}
		return filepath.Join(scratchRoot, rel), nil
	}

	for _, op := range ops {
		switch op.Kind {
		case OpAdded:
			realFile := destinationFileForAdd(cat, op)
			scratchFile, err := scratchFileFor(realFile)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(scratchFile); os.IsNotExist(err) {
				empty := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}
				docs[scratchFile] = empty
			} else if err := ensureDocLoaded(loadDoc, scratchFile); err != nil {
				return nil, err
			}
			doc := docs[scratchFile]
			seq, err := topLevelSequence(doc)
			if err != nil {
				return nil, fmt.Errorf("added op for %q: %w", op.Node, err)
			}
			mapping, err := valueToNode(op.After)
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, mapping)
			touched[scratchFile] = true

		case OpModified:
			realFile := cat.NodeFile[op.Node]
			scratchFile, err := scratchFileFor(realFile)
			if err != nil {
				return nil, err
			}
			doc, err := loadDoc(scratchFile)
			if err != nil {
				return nil, err
			}
			seq, err := topLevelSequence(doc)
			if err != nil {
				return nil, err
			}
			mapping, _, found := findNodeMapping(seq, string(op.Node))
			if !found {
				return nil, fmt.Errorf("modified op: node %q not found in %s", op.Node, scratchFile)
			}
			for _, ch := range op.Changes {
				if err := setNodeField(mapping, ch.Path, ch.After); err != nil {
					return nil, fmt.Errorf("modified op for %q: %w", op.Node, err)
				}
			}
			touched[scratchFile] = true

		case OpRemoved:
			realFile := cat.NodeFile[op.Node]
			scratchFile, err := scratchFileFor(realFile)
			if err != nil {
				return nil, err
			}
			doc, err := loadDoc(scratchFile)
			if err != nil {
				return nil, err
			}
			seq, err := topLevelSequence(doc)
			if err != nil {
				return nil, err
			}
			_, idx, found := findNodeMapping(seq, string(op.Node))
			if !found {
				return nil, fmt.Errorf("removed op: node %q not found in %s", op.Node, scratchFile)
			}
			seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
			touched[scratchFile] = true

		case OpRenamed:
			realFile := cat.NodeFile[op.From]
			scratchFile, err := scratchFileFor(realFile)
			if err != nil {
				return nil, err
			}
			doc, err := loadDoc(scratchFile)
			if err != nil {
				return nil, err
			}
			seq, err := topLevelSequence(doc)
			if err != nil {
				return nil, err
			}
			mapping, _, found := findNodeMapping(seq, string(op.From))
			if !found {
				return nil, fmt.Errorf("renamed op: node %q not found in %s", op.From, scratchFile)
			}
			if err := setMappingKey(mapping, "id", string(op.To)); err != nil {
				return nil, err
			}
			touched[scratchFile] = true

			// Coordinate-rewrite every reference to the renamed id across
			// every catalog file (edges, and top_level-storage fields like
			// change.depends_on both live as plain string scalars).
			for id, f := range cat.NodeFile {
				if id == op.From {
					continue
				}
				sf, err := scratchFileFor(f)
				if err != nil {
					return nil, err
				}
				d, err := loadDoc(sf)
				if err != nil {
					return nil, err
				}
				if renameScalarRefs(d, string(op.From), string(op.To)) {
					touched[sf] = true
				}
			}

		case OpRetyped:
			realFile := cat.NodeFile[op.Node]
			scratchFile, err := scratchFileFor(realFile)
			if err != nil {
				return nil, err
			}
			doc, err := loadDoc(scratchFile)
			if err != nil {
				return nil, err
			}
			seq, err := topLevelSequence(doc)
			if err != nil {
				return nil, err
			}
			mapping, _, found := findNodeMapping(seq, string(op.Node))
			if !found {
				return nil, fmt.Errorf("retyped op: node %q not found in %s", op.Node, scratchFile)
			}
			node := cat.Nodes[op.Node]
			pack, _, version, err := ParseSchemaPin(node.Schema)
			if err != nil {
				return nil, err
			}
			newSchema := fmt.Sprintf("%s/%s/%s", pack, op.ToType, version)
			if err := setMappingKey(mapping, "schema", newSchema); err != nil {
				return nil, err
			}
			touched[scratchFile] = true

		case OpRegistryTypeAdded:
			regFile, err := registryFile(cat)
			if err != nil {
				return nil, err
			}
			scratchFile, err := scratchFileFor(regFile)
			if err != nil {
				return nil, err
			}
			doc, err := loadDoc(scratchFile)
			if err != nil {
				return nil, err
			}
			seq, err := typeRegistrySequence(doc)
			if err != nil {
				return nil, err
			}
			mapping, err := valueToNode(op.After)
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, mapping)
			touched[scratchFile] = true

		case OpRegistryTypeModified:
			regFile, err := registryFile(cat)
			if err != nil {
				return nil, err
			}
			scratchFile, err := scratchFileFor(regFile)
			if err != nil {
				return nil, err
			}
			doc, err := loadDoc(scratchFile)
			if err != nil {
				return nil, err
			}
			seq, err := typeRegistrySequence(doc)
			if err != nil {
				return nil, err
			}
			mapping, _, found := findTypeMapping(seq, string(op.Node))
			if !found {
				return nil, fmt.Errorf("registry_type_modified op: type %q not found in %s", op.Node, scratchFile)
			}
			for _, ch := range op.Changes {
				if err := setNodeField(mapping, ch.Path, ch.After); err != nil {
					return nil, fmt.Errorf("registry_type_modified op for %q: %w", op.Node, err)
				}
			}
			touched[scratchFile] = true
		}
	}

	// Write back every touched document.
	var changed []string
	for scratchFile := range touched {
		doc := docs[scratchFile]
		out, err := yaml.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", scratchFile, err)
		}
		if err := os.MkdirAll(filepath.Dir(scratchFile), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(scratchFile, out, 0o644); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(scratchRoot, scratchFile)
		if err != nil {
			return nil, err
		}
		if info, statErr := os.Stat(scratchRoot); statErr == nil && !info.IsDir() {
			rel = filepath.Base(scratchFile) // single-file catalog: scratchRoot IS the file
		}
		changed = append(changed, rel)
	}
	return changed, nil
}

func ensureDocLoaded(loadDoc func(string) (*yaml.Node, error), file string) error {
	_, err := loadDoc(file)
	return err
}

// destinationFileForAdd picks a deterministic file for a new node: the
// bundle's nodes/<type-id>.yaml, or the single catalog file if RootPath
// isn't a bundle dir. The changeset never carries a physical path (W2.0
// design session Q1) — the loader/apply layer owns file placement.
func destinationFileForAdd(cat *Catalog, op Operation) string {
	info, err := os.Stat(cat.RootPath)
	if err != nil || !info.IsDir() {
		return cat.RootPath
	}
	typeID, _ := op.After["schema"].(string)
	_, tid, _, parseErr := ParseSchemaPin(SchemaPin(typeID))
	if parseErr != nil || tid == "" {
		tid = "misc"
	}
	return filepath.Join(cat.RootPath, "nodes", tid+".yaml")
}

// topLevelSequence returns the yaml sequence node holding node entries: a
// bundle nodes/*.yaml file's own top-level sequence, or a single-file
// catalog's `nodes:` key.
func topLevelSequence(doc *yaml.Node) (*yaml.Node, error) {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, fmt.Errorf("empty document")
		}
		root = root.Content[0]
	}
	if root.Kind == yaml.SequenceNode {
		return root, nil
	}
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "nodes" {
				return root.Content[i+1], nil
			}
		}
		return nil, fmt.Errorf("no top-level \"nodes\" sequence")
	}
	return nil, fmt.Errorf("unexpected document shape %v", root.Kind)
}

// findNodeMapping locates the mapping node whose "id" scalar equals id
// within seq, returning it and its index.
func findNodeMapping(seq *yaml.Node, id string) (*yaml.Node, int, bool) {
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			if item.Content[j].Value == "id" && item.Content[j+1].Value == id {
				return item, i, true
			}
		}
	}
	return nil, -1, false
}

// setNodeField applies one FieldChange's After value to mapping, per the
// path convention in changeset.go's FieldChange doc comment.
func setNodeField(mapping *yaml.Node, path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field change path")
	}
	switch path[0] {
	case "edges":
		if len(path) != 2 {
			return fmt.Errorf("edges path must be [\"edges\", \"<field>\"], got %v", path)
		}
		edges, err := ensureMappingChild(mapping, "edges")
		if err != nil {
			return err
		}
		return setMappingKey(edges, path[1], value)
	case "fields":
		if len(path) != 2 {
			return fmt.Errorf("fields path must be [\"fields\", \"<key>\"], got %v", path)
		}
		return setMappingKey(mapping, path[1], value)
	default:
		if len(path) != 1 {
			return fmt.Errorf("unsupported nested path %v", path)
		}
		return setMappingKey(mapping, path[0], value)
	}
}

// ensureMappingChild returns the mapping node value of key, creating an
// empty mapping under key if it doesn't already exist.
func ensureMappingChild(mapping *yaml.Node, key string) (*yaml.Node, error) {
	if mapping.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("not a mapping node")
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], nil
		}
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		child,
	)
	return child, nil
}

// setMappingKey sets (or inserts) key's value in mapping to value.
func setMappingKey(mapping *yaml.Node, key string, value any) error {
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("not a mapping node")
	}
	valueNode, err := valueToNode(value)
	if err != nil {
		return err
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = valueNode
			return nil
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		valueNode,
	)
	return nil
}

// valueToNode round-trips a Go value (string, []any, map[string]any, ...)
// through yaml.Marshal/Unmarshal into a *yaml.Node — the simplest correct
// way to build a node fragment generically without a type switch per shape.
func valueToNode(value any) (*yaml.Node, error) {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		return doc.Content[0], nil
	}
	return &doc, nil
}

// renameScalarRefs walks every scalar string value in doc and replaces
// exact matches of from with to — the blunt, catalog-wide side of a rename:
// NodeID is global, so any edge (Edges-storage or top_level-storage) may
// reference it. Returns true if it changed anything.
func renameScalarRefs(node *yaml.Node, from, to string) bool {
	changed := false
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Value == from {
		node.Value = to
		changed = true
	}
	for _, child := range node.Content {
		if renameScalarRefs(child, from, to) {
			changed = true
		}
	}
	return changed
}

func registryFile(cat *Catalog) (string, error) {
	info, err := os.Stat(cat.RootPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return cat.RootPath, nil
	}
	return filepath.Join(cat.RootPath, "type_registry.yaml"), nil
}

func typeRegistrySequence(doc *yaml.Node) (*yaml.Node, error) {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, fmt.Errorf("empty document")
		}
		root = root.Content[0]
	}
	if root.Kind == yaml.SequenceNode {
		return root, nil
	}
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "type_registry" {
				return root.Content[i+1], nil
			}
		}
		return nil, fmt.Errorf("no top-level \"type_registry\" sequence")
	}
	return nil, fmt.Errorf("unexpected document shape %v", root.Kind)
}

func findTypeMapping(seq *yaml.Node, id string) (*yaml.Node, int, bool) {
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			if item.Content[j].Value == "id" && item.Content[j+1].Value == id {
				return item, i, true
			}
		}
	}
	return nil, -1, false
}

