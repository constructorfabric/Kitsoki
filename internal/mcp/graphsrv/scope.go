package graphsrv

// Scope bindings: the deterministic "session sees only this subset of the
// graph" mechanism (docs/architecture/mcp-graph.md, "Scoped sessions").
// A scope is bound to a catalog ALIAS at server construction — the same
// point the catalogs themselves are bound — and threaded into every
// host.graph.* call as args["scope"]. Nothing a client sends can widen or
// drop it: there is no scope tool argument, so the restriction lives below
// the prompt/LLM layer entirely.
//
// This file is server-construction plumbing, so (like writevia.go's
// LoadCatalog use) it may touch internal/graph directly; tool CAPABILITY
// still lands exclusively as host.graph.* engine ops.

import (
	"fmt"
	"strings"

	objectgraph "kitsoki/internal/graph"
)

// ParseScopeFlags resolves the repeatable --scope flag's raw values (each
// "[alias=]path-to-scope-yaml") into alias -> parsed ScopeSpec. The alias
// must name a bound catalog; omitting it binds the scope to the default
// catalog. Scope files are parsed (and shape-validated) here so a broken
// spec fails server construction loudly — a scope must never silently
// degrade to "unscoped".
func ParseScopeFlags(raw []string, catalogs *CatalogSet) (map[string]*objectgraph.ScopeSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if catalogs.Empty() {
		return nil, fmt.Errorf("--scope: no catalog is bound to attach a scope to (pass --catalog)")
	}
	out := make(map[string]*objectgraph.ScopeSpec, len(raw))
	for i, entry := range raw {
		alias, path, hasAlias := strings.Cut(entry, "=")
		if !hasAlias {
			path = alias
			alias = ""
		}
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, fmt.Errorf("--scope[%d]: empty path in %q", i, entry)
		}
		alias = strings.TrimSpace(alias)
		if alias == "" {
			alias = catalogs.defaultAlias
		}
		if _, bound := catalogs.bindings[alias]; !bound {
			return nil, fmt.Errorf("--scope[%d]: %q is not a bound catalog alias (%s)", i, alias, boundAliasesHint(catalogs.Aliases()))
		}
		if _, dup := out[alias]; dup {
			return nil, fmt.Errorf("--scope[%d]: catalog alias %q already has a scope bound", i, alias)
		}
		spec, err := objectgraph.LoadScopeFile(path)
		if err != nil {
			return nil, fmt.Errorf("--scope[%d]: %w", i, err)
		}
		out[alias] = spec
	}
	return out, nil
}

// applyScope injects the resolved alias's baked scope (if any) into a
// host.graph.* call's args. Called by every tool handler right after
// resolveRead/resolveWrite — the scope rides the SAME resolution the
// catalog path does, so a scoped alias can never be read or written
// unscoped through this server.
func (d *Deps) applyScope(alias string, hostArgs map[string]any) {
	if d.Scopes == nil {
		return
	}
	if spec := d.Scopes[alias]; spec != nil {
		hostArgs["scope"] = spec.WireMap()
	}
}
