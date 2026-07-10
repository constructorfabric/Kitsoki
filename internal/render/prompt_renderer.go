package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flosch/pongo2/v6"

	"kitsoki/internal/expr"
)

// Prompt extension: rendering a story's prompts through a per-app
// pongo2.TemplateSet so {% extends %} / {% include %} resolve, with an
// ordered overlay → story search path so a project can extend a story's
// base prompts without forking the story. This brings prompts to parity
// with views (which already render through AppRenderer) — see
// docs/stories/prompts.md.

// Reference namespaces a prompt's {% extends %} / {% include %} target may use.
const (
	// promptNSStory forces resolution against the story's own prompt root,
	// bypassing any overlay. An overlay file uses it to extend the very base
	// it shadows: overlay `prompts/x.md` does `{% extends "@story/prompts/x.md" %}`.
	promptNSStory = "@story/"
	// promptNSShared resolves against the declared shared fragment dirs.
	promptNSShared = "@shared/"
	// promptNSImport resolves against an imported child story's prompt root:
	// `@import/<alias>/<path>`. It lets a parent's prompt override file extend
	// the base prompt of the story it imported — the overlay-extend form of
	// imports.overrides.prompts (see docs/stories/imports.md).
	promptNSImport = "@import/"
)

// PromptPath declares the ordered roots a prompt renderer searches.
//
// A bare reference (the common case, e.g. an effect's `prompt: prompts/x.md`)
// resolves overlay-first: Overlay then Story. A story is always valid with no
// overlay — bare names then resolve at Story exactly as the legacy
// resolvePromptPath join did, so a story with no overlay and no blocks renders
// byte-identically to the pre-extension `render.Pongo` path.
//
// Story is the story's base dir (the directory holding app.yaml); prompt
// references keep their existing `prompts/…` form and resolve relative to it.
// Overlay mirrors that layout. Shared dirs hold fragments addressed via
// `@shared/…`.
type PromptPath struct {
	Story   string   // required
	Overlay string   // optional; "" means no overlay
	Shared  []string // optional
	// Imports maps an import alias to that imported child story's base dir,
	// so a parent override prompt can `{% extends "@import/<alias>/…" %}` the
	// imported story's base instead of swapping it wholesale. Optional.
	Imports map[string]string
}

// searchPathLoader is the bespoke pongo2.TemplateLoader that powers prompt
// extension. Stock multi-loader stacking cannot express overlay-first bare
// names for {% extends %} / {% include %} targets: pongo2 resolves a target
// referenced *inside* an already-loaded template via loaders[0] only and
// relative to the parent template's name (resolveFilename in template_sets.go),
// while only top-level resolveTemplate iterates all loaders. So one custom
// loader owns the whole policy: Abs is the identity (no parent-relative
// rewriting) and Get does all path resolution and the ?? rewrite.
type searchPathLoader struct {
	pp PromptPath
}

// cycleSentinel poisons a template name that would extend/include itself so
// Get refuses it instead of recursing. pongo2 resolves an {% extends %} target
// at parse time with no cycle guard, and a self-reference recurses until the Go
// stack overflows — fatal and NOT recoverable — so it must be caught here,
// before parse. pongo2's resolveTemplate collapses a loader Get error into a
// generic "unable to resolve template", so the marker is kept human-readable
// (it surfaces in the failing template name): it both drives detection and
// tells the author what went wrong. No real prompt path begins with it.
const cycleSentinel = "self-reference (use @story/ to extend the shadowed base): "

// Abs is the identity for normal names: every name (bare or @-namespaced) is
// resolved in Get against the search path, never relative to the parent
// template. This is what lets an overlay file resolve overlay-first while its
// own `{% extends "@story/…" %}` still reaches the story base. The returned
// value is also the pongo2 cache key, so identity keeps `@story/x` and bare
// `x` as distinct, stable cache entries.
//
// The one non-identity case: a direct self-reference (a bare
// `{% extends "x.md" %}` in the overlay file `x.md`, which resolves
// overlay-first back to itself — the classic overlay footgun, where the author
// meant `@story/x.md`). pongo2 would recurse to a fatal stack overflow, so Abs
// poisons the name and Get turns it into an ordinary error.
func (l *searchPathLoader) Abs(base, name string) string {
	if base != "" {
		if b, berr := l.resolve(base); berr == nil {
			if n, nerr := l.resolve(name); nerr == nil && b == n {
				return cycleSentinel + name
			}
		}
	}
	return name
}

// Get resolves name to a file on the search path, reads it, and applies the
// same `??` → `|default:` rewrite the inline Pongo path applies so a base /
// overlay / fragment loaded here parses identically to one rendered inline.
func (l *searchPathLoader) Get(name string) (io.Reader, error) {
	if ref, ok := strings.CutPrefix(name, cycleSentinel); ok {
		return nil, fmt.Errorf("prompt loader: %q extends/includes itself; use @story/… to extend the base an overlay shadows", ref)
	}
	path, err := l.resolve(name)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("prompt loader: read %q (for %q): %w", path, name, err)
	}
	return bytes.NewReader([]byte(preprocessCoalesce(string(raw)))), nil
}

// resolve maps a (possibly namespaced) template name to an absolute file path.
func (l *searchPathLoader) resolve(name string) (string, error) {
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("prompt loader: %q escapes the search path (`..` not allowed)", name)
	}
	// An absolute reference (e.g. an imported sub-app prompt rebased to an
	// absolute path at fold time) is used as-is rather than misjoined under a
	// search root — filepath.Join(root, "/abs/x") would graft it under root.
	if filepath.IsAbs(name) {
		if fileExists(name) {
			return name, nil
		}
		return "", fmt.Errorf("prompt loader: absolute reference %q not found", name)
	}
	switch {
	case strings.HasPrefix(name, promptNSStory):
		rel := strings.TrimPrefix(name, promptNSStory)
		p := filepath.Join(l.pp.Story, rel)
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("prompt loader: %s not found at story root %q", name, l.pp.Story)
	case strings.HasPrefix(name, promptNSShared):
		rel := strings.TrimPrefix(name, promptNSShared)
		for _, d := range l.pp.Shared {
			if p := filepath.Join(d, rel); fileExists(p) {
				return p, nil
			}
		}
		return "", fmt.Errorf("prompt loader: %s not found in shared dirs %v", name, l.pp.Shared)
	case strings.HasPrefix(name, promptNSImport):
		rest := strings.TrimPrefix(name, promptNSImport)
		alias, rel, ok := strings.Cut(rest, "/")
		base, known := l.pp.Imports[alias]
		if !ok || !known {
			return "", fmt.Errorf("prompt loader: %s — unknown import alias %q (known: %v)", name, alias, sortedImportAliases(l.pp.Imports))
		}
		if p := filepath.Join(base, rel); fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("prompt loader: %s not found under import %q root %q", name, alias, base)
	default:
		for _, root := range l.bareRoots() {
			if p := filepath.Join(root, name); fileExists(p) {
				return p, nil
			}
		}
		return "", fmt.Errorf("prompt loader: %q not found on search path (overlay=%q story=%q)", name, l.pp.Overlay, l.pp.Story)
	}
}

// bareRoots returns the ordered roots a bare reference searches: overlay
// first (when present), then story.
func (l *searchPathLoader) bareRoots() []string {
	roots := make([]string, 0, 2)
	if l.pp.Overlay != "" {
		roots = append(roots, l.pp.Overlay)
	}
	return append(roots, l.pp.Story)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// sortedImportAliases returns the import aliases sorted, for deterministic
// error messages.
func sortedImportAliases(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NewPromptRenderer builds an AppRenderer whose loader resolves
// {% extends %} / {% include %} across pp's overlay → story search path and
// the @story / @shared namespaces. Use cached=false for interactive/dev mode
// (templates re-read every render) and cached=true for tests and
// non-interactive runs.
func NewPromptRenderer(pp PromptPath, cached bool) (*AppRenderer, error) {
	if pp.Story == "" {
		return nil, fmt.Errorf("render: NewPromptRenderer requires a story root")
	}
	loader := &searchPathLoader{pp: pp}
	set := pongo2.NewSet("prompts:"+filepath.Base(pp.Story), loader)
	set.Debug = !cached
	return &AppRenderer{set: set, rootDir: pp.Story, cached: cached, prompts: loader}, nil
}

// SpecProvenance reports, for a bare prompt reference, which of the story
// base's spec_ blocks an active overlay overrode vs. left at their provisional
// default on this render. It is the moat datapoint behind an extended prompt:
// "this provisional default was never specialized here" becomes queryable.
//
// Returns (nil, nil) when there is no overlay, no overlayed file of this name,
// the reference isn't a bare story-relative path, or the base has no spec_
// blocks — i.e. only meaningful, non-empty results are produced. defaulted and
// overridden are sorted spec_ block names.
func (r *AppRenderer) SpecProvenance(name string) (defaulted, overridden []string) {
	if r.prompts == nil || r.prompts.pp.Overlay == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "@") {
		return nil, nil
	}
	basePath := filepath.Join(r.prompts.pp.Story, name)
	overlayPath := filepath.Join(r.prompts.pp.Overlay, name)
	if !fileExists(basePath) || !fileExists(overlayPath) {
		return nil, nil
	}
	baseRaw, err1 := os.ReadFile(basePath)
	overlayRaw, err2 := os.ReadFile(overlayPath)
	if err1 != nil || err2 != nil {
		return nil, nil
	}
	overlayBlocks := blockNamesInSource(string(overlayRaw))
	for _, b := range EnumerateSpecBlocksInSource(basePath, string(baseRaw)) {
		if overlayBlocks[b.Name] {
			overridden = append(overridden, b.Name)
		} else {
			defaulted = append(defaulted, b.Name)
		}
	}
	sort.Strings(defaulted)
	sort.Strings(overridden)
	return defaulted, overridden
}

// OverlayDir returns the project overlay directory this prompt renderer
// searches first, or "" when none is configured (or this is not a prompt
// renderer). Recorded in the agent trace as the provenance of an extended
// prompt.
func (r *AppRenderer) OverlayDir() string {
	if r.prompts == nil {
		return ""
	}
	return r.prompts.pp.Overlay
}

// ResolvePromptName resolves a prompt reference (e.g. "prompts/diagnose.md" or
// "@story/prompts/diagnose.md") to an absolute file path using this renderer's
// search path, overlay-first. ok is false when this is not a prompt renderer
// or the name does not resolve to a readable file — the caller then treats the
// value as inline content (the stdin / `--prompt -` path).
func (r *AppRenderer) ResolvePromptName(name string) (path string, ok bool) {
	if r.prompts == nil {
		return "", false
	}
	p, err := r.prompts.resolve(name)
	if err != nil {
		return "", false
	}
	return p, true
}

// ValidatePrompt loads and parses the named prompt through the search-path
// TemplateSet WITHOUT executing it, so a missing file, an unresolved
// {% extends %} / {% include %} target, an unknown @import alias, a
// self-reference, or a pongo2 syntax error surfaces as a located error at
// load time rather than at first agent dispatch. Returns nil on a non-prompt
// renderer (the legacy path validates nothing). name is the prompt reference
// as it appears on the effect (bare, @-namespaced, or absolute).
func (r *AppRenderer) ValidatePrompt(name string) error {
	if r == nil || r.prompts == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.set.FromCache(name); err != nil {
		return fmt.Errorf("prompt %q: %w", name, err)
	}
	return nil
}

// OverrideIssues returns the names of {% block %} overrides in the prompt
// `name` (resolved overlay-first) that no template in its {% extends %} chain
// declares — overrides that would silently do nothing (the classic typo'd
// overlay block, or a block renamed in the base). pongo2 ignores such blocks
// without error, so this is the load-time guard against a silent specialization
// no-op. Returns nil when `name` doesn't extend anything or every override
// targets a real ancestor block.
func (r *AppRenderer) OverrideIssues(name string) []string {
	if r.prompts == nil {
		return nil
	}
	path, err := r.prompts.resolve(name)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	src := string(raw)
	if extendsTargetInSource(src) == "" {
		return nil // not an extending file — its blocks are originals, not overrides
	}
	ancestors := map[string]bool{}
	r.collectAncestorBlocks(extendsTargetInSource(src), ancestors, map[string]bool{})
	var dead []string
	for b := range blockNamesInSource(src) {
		if !ancestors[b] {
			dead = append(dead, b)
		}
	}
	sort.Strings(dead)
	return dead
}

// collectAncestorBlocks unions the block names declared by name and every
// template up its {% extends %} chain into out. visited guards cycles.
func (r *AppRenderer) collectAncestorBlocks(name string, out, visited map[string]bool) {
	if name == "" || visited[name] {
		return
	}
	visited[name] = true
	path, err := r.prompts.resolve(name)
	if err != nil {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	src := string(raw)
	for b := range blockNamesInSource(src) {
		out[b] = true
	}
	r.collectAncestorBlocks(extendsTargetInSource(src), out, visited)
}

// RenderPrompt resolves name overlay-first, loads it through the search-path
// loader (so its {% extends %} / {% include %} targets resolve), and renders
// it against env. Unlike RenderFile it carries the prompt-rendering semantics:
// the loaded bytes pass through the same `??` rewrite as inline Pongo. Returns
// a clear error if name does not resolve or fails to render.
func (r *AppRenderer) RenderPrompt(name string, env expr.Env) (out string, err error) {
	if r.prompts == nil {
		return "", fmt.Errorf("render: RenderPrompt called on a non-prompt renderer")
	}
	tpl, err := r.set.FromCache(name)
	if err != nil {
		return "", fmt.Errorf("render: load prompt %q: %w", name, err)
	}
	// Recover from filter panics — see AppRenderer.Render for the rationale.
	defer func() {
		if rec := recover(); rec != nil {
			out = ""
			err = fmt.Errorf("render: panic executing prompt %q: %v", name, rec)
		}
	}()
	out, err = tpl.Execute(ToContext(env))
	if err != nil {
		return "", fmt.Errorf("render: execute prompt %q: %w", name, err)
	}
	return out, nil
}
