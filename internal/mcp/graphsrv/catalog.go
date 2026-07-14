package graphsrv

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "github.com/goccy/go-yaml"
)

// defaultCatalogProbePath is checked (relative to server cwd) when no
// --catalog flag is given at all, per plan §3.2.
const defaultCatalogProbePath = "pog/catalog.yaml"

// CatalogBinding is one --catalog alias=path pair, resolved to an absolute
// path.
type CatalogBinding struct {
	Alias string
	Path  string
}

// CatalogSet holds the server's bound catalogs (possibly empty — the
// NO_CATALOG case) and resolves a tool call's optional `catalog` argument
// to a bound path.
type CatalogSet struct {
	bindings     map[string]string // alias -> absolute path
	order        []string          // alias order, bindings[0] is default
	defaultAlias string
}

// ParseCatalogFlags resolves the repeatable --catalog flag's raw values
// (each "[alias=]path") into a CatalogSet. An alias defaults to the
// catalog's own `catalog.id` field if present, else the filename stem
// (extension-less base name). The first entry (in flag order) becomes the
// default catalog.
//
// If raw is empty, this probes defaultCatalogProbePath under cwd; if that
// doesn't exist either, an empty (zero-catalog) CatalogSet is returned —
// not an error. The server still starts; every tool call then reports
// NO_CATALOG.
func ParseCatalogFlags(raw []string) (*CatalogSet, error) {
	cs := &CatalogSet{bindings: map[string]string{}}

	if len(raw) == 0 {
		if _, err := os.Stat(defaultCatalogProbePath); err == nil {
			raw = []string{defaultCatalogProbePath}
		} else {
			return cs, nil
		}
	}

	for i, entry := range raw {
		alias, path, hasAlias := strings.Cut(entry, "=")
		if !hasAlias {
			// No "alias=" prefix: the whole entry is the path.
			path = alias
			alias = ""
		}
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, fmt.Errorf("--catalog[%d]: empty path in %q", i, entry)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("--catalog[%d]: resolve %q: %w", i, path, err)
		}
		alias = strings.TrimSpace(alias)
		if alias == "" {
			alias = catalogDefaultAlias(abs)
		}
		if _, exists := cs.bindings[alias]; exists {
			return nil, fmt.Errorf("--catalog[%d]: alias %q is already bound (aliases must be unique)", i, alias)
		}
		cs.bindings[alias] = abs
		cs.order = append(cs.order, alias)
	}
	if len(cs.order) > 0 {
		cs.defaultAlias = cs.order[0]
	}
	return cs, nil
}

// catalogDefaultAlias derives a default alias for a bound catalog path: the
// catalog's own `catalog.id` field if the file/bundle is readable and
// declares one, else the filename stem.
func catalogDefaultAlias(path string) string {
	if id := readCatalogID(path); id != "" {
		return id
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// readCatalogID best-effort peeks a catalog file's top-level `catalog: {id:
// ...}` block without going through the full internal/graph loader (which
// resolves types/edges we don't need here, and directory bundles). path may
// be a single catalog file or a bundle directory (catalog.yaml inside).
func readCatalogID(path string) string {
	filePath := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		filePath = filepath.Join(path, "catalog.yaml")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	var doc struct {
		Catalog struct {
			ID string `yaml:"id"`
		} `yaml:"catalog"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Catalog.ID)
}

// Aliases returns the bound aliases in binding order.
func (cs *CatalogSet) Aliases() []string {
	out := make([]string, len(cs.order))
	copy(out, cs.order)
	return out
}

// Empty reports whether no catalogs are bound (the NO_CATALOG state).
func (cs *CatalogSet) Empty() bool {
	return cs == nil || len(cs.order) == 0
}

// Resolve maps a tool call's optional `catalog` argument (an alias, or
// empty for the default) to a bound absolute path. It never accepts a raw
// filesystem path: a value that isn't a bound alias is rejected as
// VALIDATION (if it looks like a path) or UNKNOWN_CATALOG (if it looks like
// a typo'd alias), naming the bound aliases either way.
func (cs *CatalogSet) Resolve(arg string) (path, alias string, errPayload *ErrorPayload) {
	if cs.Empty() {
		return "", "", NewError(CodeNoCatalog, "no catalog is bound to this server",
			"start the server with --catalog [alias=]<path>, or run it with pog/catalog.yaml under cwd")
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return cs.bindings[cs.defaultAlias], cs.defaultAlias, nil
	}
	if p, ok := cs.bindings[arg]; ok {
		return p, arg, nil
	}
	aliasList := boundAliasesHint(cs.Aliases())
	if looksLikeFilesystemPath(arg) {
		return "", "", NewError(CodeValidation,
			fmt.Sprintf("catalog: %q looks like a filesystem path, not a bound alias", arg),
			"mcp-graph only accepts bound catalog aliases in `catalog`, not raw paths. "+aliasList)
	}
	return "", "", NewError(CodeUnknownCatalog,
		fmt.Sprintf("catalog: %q is not a bound alias", arg),
		aliasList)
}

func boundAliasesHint(aliases []string) string {
	sorted := append([]string(nil), aliases...)
	sort.Strings(sorted)
	return fmt.Sprintf("bound aliases: %s", strings.Join(sorted, ", "))
}

// looksLikeFilesystemPath heuristically distinguishes "someone passed a
// path" from "someone typo'd an alias": a path separator, a leading dot, a
// drive-style prefix, or an existing file on disk.
func looksLikeFilesystemPath(arg string) bool {
	if strings.ContainsAny(arg, "/\\") {
		return true
	}
	if strings.HasPrefix(arg, ".") {
		return true
	}
	if strings.HasSuffix(arg, ".yaml") || strings.HasSuffix(arg, ".yml") {
		return true
	}
	if _, err := os.Stat(arg); err == nil {
		return true
	}
	return false
}
