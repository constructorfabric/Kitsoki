// Package app — story imports (reference: docs/stories/imports.md).
//
// Imports compose one app inside another with an aliased namespace and an
// explicit world projection. Each import binds a child manifest under an
// alias; the child's states, world keys, intents, and host_interfaces are
// folded into the parent at load time, prefixed under the alias.
//
// Resolution order in Load():
//
//	parseAndMerge   →   resolveImports   →   expandPhases   →   validateDef
//
// resolveImports is recursive. Each child manifest is loaded, its imports
// resolved depth-first, then the child is folded into the parent with:
//
//   - States prefixed under <alias>/<state-name>.
//   - World keys prefixed under <alias>__<key>; every world.<key>
//     reference inside the child's expressions is rewritten to match.
//   - Intents lifted into the parent's flat intent table under
//     <alias>__<name>; child state On: maps are rewritten to match.
//   - Hosts unioned per the import's hosts: policy (inherit | declared).
//   - host_interfaces resolved into concrete handler invocations via the
//     interface's default or the importer's host_bindings.
//   - @exit:<name> transition targets rewritten to parent state names,
//     with world_out projection effects (imp.Exits.<name>.Set) attached
//     to the rewritten transition.
//
// world_in projection becomes a synthesised on_enter effect chain on the
// child's entry state. Both world_in and world_out evaluate in the flat
// runtime world; the loader places them where they fire automatically.
package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitver"
)

// importAliasRE constrains an import alias to characters safe to use as a
// state-path segment and world-key prefix.
var importAliasRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ReservedWorldKeys are engine-owned global world variables that the runtime
// writes directly (see internal/orchestrator/host_dispatch.go on an
// `on_error:` redirect). They are NEVER namespaced by import folding: a story
// imported under an alias still reads/writes the bare `last_error` /
// `host_error` keys, matching how the engine writes them. Consequently any
// room may reference `world.last_error` / `world.host_error` and list them in
// `relevant_world` without declaring them in its own world block, at every
// import-nesting depth.
//
//   - last_error: string — the failing host call's error message.
//   - host_error: map — {namespace, message, data?, stderr?, exit_code?}.
//   - write_mode_scope: string ("" | "turn" | "session") — the active
//     write-mode grant breadth in a write_mode: read_only room. Set and cleared
//     ONLY by the engine on a grant / turn / session boundary; a story `set:`-ing
//     it is rejected at load time (a story must not be able to self-grant write
//     mode). See WriteModeScopeWorldKey and docs/proposals/agent-write-mode-opt-in.md.
//   - session_id: string — the active session's identifier (a UUID). Seeded into
//     world.Vars by the engine in loadJourney (and re-seeded on host dispatch)
//     so story templates can derive a SESSION-distinct working folder
//     (workdir/workspace_id/feature_branch) from it. Without it the folder is
//     derived from the ticket alone, so two concurrent sessions on one ticket
//     resolve to the SAME checkout and a destructive git op in one clobbers
//     the other's WIP (bug9glm2). Engine-owned: a story must not `set:` it.
//   - operation_run: map — the active/completed session-level operation handle.
//     Engine-owned: a story must not `set:` it.
//   - operation_drafts: map — explicit handles created by persist_draft.
var ReservedWorldKeys = map[string]struct{}{
	"last_error":           {},
	"host_error":           {},
	WriteModeScopeWorldKey: {},
	"session_id":           {},
	OperationRunWorldKey:   {},
	"operation_drafts":     {},
}

// ImportResolver is an injected hook that resolves an `@kitsoki/<name>`
// import source to an absolute manifest path. It is the seam through which the
// `--kitsoki-repo` override AND the embedded story library reach the loader
// WITHOUT a package global (DI per CLAUDE.md).
//
// name is the bare story name (`dev-story` for `@kitsoki/dev-story`).
// importerDir is the directory of the manifest doing the import, so a resolver
// that points at a live checkout can apply relative paths correctly.
//
// override distinguishes the resolver's two roles, which sit on OPPOSITE sides
// of on-disk discovery (see resolveImportSource's order):
//
//   - override=true  → "the explicit --kitsoki-repo / KITSOKI_REPO override".
//     Consulted BEFORE findRepoRoot so an operator's checkout always wins.
//     A (path,nil) result is used; a ("",nil) result means "no override set,
//     fall through"; a non-nil error means "override set but the story is
//     missing there" and is surfaced (never silently swallowed).
//   - override=false → "the embedded-library fallback". Consulted only AFTER
//     findRepoRoot fails. A non-nil error there is the terminal failure.
//
// A nil resolver means neither override nor embedded fallback is configured:
// the loader keeps today's behaviour and errors on a failed `@kitsoki/<name>`
// lookup. The loader builds one closure that handles both calls.
type ImportResolver func(name, importerDir string, override bool) (string, error)

// WriteModeScopeWorldKey is the engine-reserved world variable holding the
// active write-mode grant breadth ("" | "turn" | "session") in a
// write_mode: read_only room. It is engine-owned: the write-mode gate reads it
// to short-circuit a re-ask while a turn/session grant is active, and the engine
// sets/clears it on grant and at the turn/session boundary. A story may not
// `set:` it (load-time invariant). See docs/proposals/agent-write-mode-opt-in.md.
const WriteModeScopeWorldKey = "write_mode_scope"

// OperationRunWorldKey is the engine-reserved world variable holding the
// current session-level operation run handle. Transitions start it via their
// `operation:` policy reference; the runtime updates it to running/completed so
// replay, UI, and harness assertions can observe progress without special
// side-channels. A story may not `set:` it.
const OperationRunWorldKey = "operation_run"

// WriteMode posture values for State.WriteMode (validated at load time).
const (
	// WriteModeOpen (or absent) = today's static posture: a dispatched agent runs
	// under its declared bypassPermissions / converse tool policy verbatim.
	WriteModeOpen = "open"
	// WriteModeReadOnly = the room boots read-only and every mutating tool call is
	// gated through the operator-ask write-mode opt-in.
	WriteModeReadOnly = "read_only"
)

// resolveImports walks def.Imports and folds each imported child into def.
// Errors are returned aggregated; the caller wraps in errors.Join.
//
// parents is the canonical-path stack of in-progress loads, used to detect
// cycles. A repeated path produces a ValidationError naming the cycle.
//
// resolver is the injected embedded-library / --kitsoki-repo fallback for
// `@kitsoki/<name>` sources; nil keeps the legacy error-on-missing behaviour.
// It is threaded unchanged into every recursive child load so a base story
// imported from the embedded library can itself import siblings via
// `@kitsoki/<name>`.
func resolveImports(def *AppDef, file, baseDir string, parents []string, resolver ImportResolver) []error {
	if def == nil || len(def.Imports) == 0 {
		return nil
	}
	var errs []error

	for _, alias := range sortedKeys(def.Imports) {
		imp := def.Imports[alias]
		if imp == nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: empty definition", alias)})
			continue
		}
		if !importAliasRE.MatchString(alias) {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: alias must match %s", alias, importAliasRE.String())})
			continue
		}
		if imp.Source == "" {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: source is required", alias)})
			continue
		}

		// Resolve any script-form host_bindings entries (S3a/D2.1) BEFORE
		// folding: synthesizes a handler name for each, records it on
		// def.StarlarkHostBindings, and rewrites the entry in place to a
		// plain Handler spec so foldChild's binding-application logic
		// (9/9a/9b below) never needs to know about scripts at all.
		if hbErrs := resolveHostBindingScripts(def, imp, baseDir, file, alias); len(hbErrs) > 0 {
			errs = append(errs, hbErrs...)
			continue
		}

		childPath, resolveErr := resolveImportSource(imp.Source, baseDir, resolver)
		if resolveErr != nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: %v", alias, resolveErr)})
			continue
		}

		// Cycle detection.
		canonical := canonicalPath(childPath)
		cycle := false
		for _, p := range parents {
			if p == canonical {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: cycle detected via %s", alias, childPath)})
				cycle = true
				break
			}
		}
		if cycle {
			continue
		}

		// Load the child manifest. Imports are recursive — the child's own
		// imports are resolved before we fold it into the parent.
		childDef, childErrs := loadImportedChild(childPath, append(parents, canonical), resolver)
		if len(childErrs) > 0 {
			errs = append(errs, childErrs...)
			continue
		}

		// Constraint matching (S2): imp.Version, previously parsed and
		// inert (see ImportDef.Version's doc comment), is checked against
		// the resolved child's own `app.version:`. A declared constraint
		// that the resolved candidate fails to satisfy is a load-time
		// error — the same "fail fast, never silently substitute" posture
		// resolveImportSource's override tier uses. No constraint
		// declared (imp.Version == "") skips the check entirely, matching
		// pre-S2 behaviour for every import that doesn't opt in.
		if imp.Version != "" {
			ok, vErr := kitver.Satisfies(childDef.App.Version, imp.Version)
			if vErr != nil {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf(
					"imports.%s: version constraint %q: %v", alias, imp.Version, vErr)})
				continue
			}
			if !ok {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf(
					"imports.%s: resolved %s app.version %q does not satisfy version constraint %q",
					alias, childPath, childDef.App.Version, imp.Version)})
				continue
			}
		}

		// Apply overrides BEFORE namespace flattening so override.states
		// names reference child-local state names (not alias-prefixed).
		if imp.Overrides != nil {
			overrideErrs := applyOverrides(childDef, imp.Overrides, file, alias, baseDir, filepath.Dir(childPath))
			if len(overrideErrs) > 0 {
				errs = append(errs, overrideErrs...)
				continue
			}
		}

		// Re-base relative `prompt:` and `schema:` args in the child's
		// effects to absolute paths rooted at the child's own directory.
		// The runtime (resolvePromptPath / schema resolution) joins these
		// against $KITSOKI_APP_DIR, which is the PARENT app's directory at
		// runtime. Without re-rooting, an imported sub-story's effect like
		// `with: { prompt: prompts/foo.md }` would resolve to
		// `<parent-dir>/prompts/foo.md` instead of
		// `<child-dir>/prompts/foo.md` — and the prompt file wouldn't be
		// found. This is the same idea applyOverrides uses for prompt
		// overrides, applied to the base case.
		rebaseEffectPaths(childDef.States, filepath.Dir(childPath))

		// Fold the child into the parent under the alias.
		if foldErrs := foldChild(def, alias, imp, childDef, file); len(foldErrs) > 0 {
			errs = append(errs, foldErrs...)
			continue
		}

		// Record post-fold metadata: the alias, its declared entry, and
		// the child manifest's path. Used by the reach-into-child guard and
		// the metamode auto-watch.
		entryName := imp.Entry
		if entryName == "" {
			if s, ok := childDef.Root.(string); ok {
				entryName = s
			}
		}
		if def.ImportWrappers == nil {
			def.ImportWrappers = make(map[string]*ImportWrapperInfo)
		}
		def.ImportWrappers[alias] = &ImportWrapperInfo{
			Alias:      alias,
			Entry:      entryName,
			SourcePath: childPath,
		}

		// Surface every manifest the child saw (root + its own transitive
		// imports) so the top-level LoadedManifests is the full reachable
		// set. Deduped against the existing list.
		def.LoadedManifests = appendUnique(def.LoadedManifests, childPath)
		for _, p := range childDef.LoadedManifests {
			def.LoadedManifests = appendUnique(def.LoadedManifests, p)
		}
	}

	// Clear def.Imports — they're now folded in. Downstream validation and
	// runtime treat the merged def as a single flat app.
	def.Imports = nil
	return errs
}

// starlarkBindingHandlerPrefix names every synthetic host handler
// resolveHostBindingScripts introduces for a script-form host_bindings entry
// (S3a/D2.1). Kept distinct from ordinary host.* names purely for
// readability in traces/errors; dispatch treats it like any other handler
// name.
const starlarkBindingHandlerPrefix = "host.starlark_binding."

// resolveHostBindingScripts resolves every script-form entry in imp.HostBindings
// (a bare `.star`-suffixed string or a `{script: ...}` mapping — see
// HostBindingSpec) against baseDir (the directory of the app.yaml declaring
// this imports.<alias>.host_bindings block, NOT the child being imported),
// verifies the script and its sidecar exist, synthesizes a deterministic
// handler name, records the (handler name -> absolute script path) pair on
// def.StarlarkHostBindings, and rewrites the entry in place to a plain
// Handler spec. This runs BEFORE foldChild so every later consumer of
// imp.HostBindings (foldChild's binding-application logic, synthesis.go) only
// ever sees plain handler names — script resolution is fully contained here.
//
// The handler name is derived from a hash of the absolute script path so:
//   - the same script bound twice (e.g. via both a base-name and a
//     prefixed host_bindings key) yields the same handler name — no
//     duplicate registration.
//   - two different aliases/levels binding two different scripts never
//     collide, without needing the full alias chain threaded through here.
func resolveHostBindingScripts(def *AppDef, imp *ImportDef, baseDir, file, alias string) []error {
	if imp == nil || len(imp.HostBindings) == 0 {
		return nil
	}
	var errs []error
	for _, name := range sortedKeys(imp.HostBindings) {
		spec := imp.HostBindings[name]
		if spec.Script == "" {
			continue // plain handler-name form — nothing to resolve.
		}

		rawScript := strings.TrimSpace(spec.Script)
		resolved := rawScript
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(baseDir, resolved)
		}
		resolved = filepath.Clean(resolved)

		if _, statErr := os.Stat(resolved); statErr != nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf(
				"imports.%s: host_bindings.%s script %q not found (resolved to %q)", alias, name, rawScript, resolved)})
			continue
		}
		sidecarPath := resolved + ".yaml"
		raw, readErr := os.ReadFile(sidecarPath)
		if readErr != nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf(
				"imports.%s: host_bindings.%s script %q has no sidecar (expected %q): %v", alias, name, rawScript, sidecarPath, readErr)})
			continue
		}
		if _, parseErr := starlarkhost.ParseSidecar(raw); parseErr != nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf(
				"imports.%s: host_bindings.%s sidecar %q is malformed: %v", alias, name, sidecarPath, parseErr)})
			continue
		}

		handlerName := starlarkBindingHandlerName(resolved)
		if def.StarlarkHostBindings == nil {
			def.StarlarkHostBindings = make(map[string]string)
		}
		def.StarlarkHostBindings[handlerName] = resolved
		imp.HostBindings[name] = HostBindingSpec{Handler: handlerName}
	}
	return errs
}

// starlarkBindingHandlerName derives a deterministic, collision-safe handler
// name from an absolute script path (see resolveHostBindingScripts).
func starlarkBindingHandlerName(absScriptPath string) string {
	sum := sha1.Sum([]byte(absScriptPath))
	return starlarkBindingHandlerPrefix + hex.EncodeToString(sum[:])[:16]
}

// appendUnique adds v to list iff it's not already present. Order is
// preserved (the order children were folded), which keeps the
// LoadedManifests list stable across loads.
func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

// loadImportedChild reads, parses, and recursively resolves a child
// manifest. The child's own include: and imports: are processed in turn.
func loadImportedChild(path string, parents []string, resolver ImportResolver) (*AppDef, []error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, []error{&ValidationError{File: path, Message: fmt.Sprintf("read: %v", err)}}
	}
	baseDir := filepath.Dir(path)
	def, mergeErrs := parseAndMerge(b, path, baseDir)
	if len(mergeErrs) > 0 {
		return nil, mergeErrs
	}
	// Recursively resolve the child's own imports BEFORE folding into parent.
	if impErrs := resolveImports(def, path, baseDir, parents, resolver); len(impErrs) > 0 {
		return nil, impErrs
	}
	// Expand the child's phase templates so the parent sees concrete states.
	if expandErrs := expandPhases(def, path); len(expandErrs) > 0 {
		return nil, expandErrs
	}
	return def, nil
}

// ResolveSource is the exported form of resolveImportSource — the seam
// `kitsoki kit add|verify` (cmd/kitsoki) use to resolve an arbitrary source
// string to an absolute manifest path the same way the loader would, without
// duplicating the tier logic (S2).
func ResolveSource(src, importerDir string, resolver ImportResolver) (string, error) {
	return resolveImportSource(src, importerDir, resolver)
}

// resolveImportSource maps a `source:` value to an absolute manifest path.
//
// Resolution is tiered by source SHAPE, checked in this order:
//
//  0. `git+<url>@<ref>` — the git source tier (S2). Fetched into a
//     content-addressed cache generalizing the basestories/baseskills
//     embed→cache→materialize pattern (see internal/kitgit). This is
//     independent of the `@kitsoki/<name>` tiers below: a git source names
//     its own remote, not a story inside the kitsoki repo/embedded library.
//
// For an `@kitsoki/<name>` source the resolution order is:
//
//  1. the injected resolver's `--kitsoki-repo` / KITSOKI_REPO override
//     (override=true) — checked FIRST so an explicit operator checkout always
//     wins, and a missing story under that override is a hard error;
//  2. a discovered on-disk kitsoki checkout (findRepoRoot walking up from
//     importerDir) — the dev-checkout / dogfood path;
//  3. the injected resolver's embedded-library fallback (override=false) —
//     reached only when no override is set AND no on-disk root is found.
//
// The loader builds one closure serving both resolver calls; nil keeps the
// legacy error-on-missing behaviour.
func resolveImportSource(src, importerDir string, resolver ImportResolver) (string, error) {
	if url, ref, ok := kitgit.ParseSource(src); ok {
		res, err := kitgit.Materialize(context.Background(), kitgit.DefaultRunner, url, ref)
		if err != nil {
			return "", fmt.Errorf("source %q: %w", src, err)
		}
		candidate := filepath.Join(res.Root, "app.yaml")
		if _, statErr := os.Stat(candidate); statErr != nil {
			return "", fmt.Errorf("source %q: app.yaml not found at repo root (%s): %w", src, candidate, statErr)
		}
		return candidate, nil
	}
	if strings.HasPrefix(src, "@kitsoki/") {
		name := strings.TrimPrefix(src, "@kitsoki/")
		if name == "" || strings.ContainsAny(name, "/\\") {
			return "", fmt.Errorf("source %q: invalid @kitsoki name", src)
		}
		// 1. Explicit override wins over everything, even an on-disk root.
		if resolver != nil {
			resolved, rErr := resolver(name, importerDir, true)
			if rErr != nil {
				return "", fmt.Errorf("source %q: %w", src, rErr)
			}
			if resolved != "" {
				return resolved, nil
			}
		}
		// 2. Discovered on-disk kitsoki checkout.
		root, err := findRepoRoot(importerDir)
		if err != nil {
			// 3. No on-disk checkout: embedded-library fallback.
			if resolver != nil {
				resolved, rErr := resolver(name, importerDir, false)
				if rErr != nil {
					return "", fmt.Errorf("source %q: %w", src, rErr)
				}
				return resolved, nil
			}
			return "", fmt.Errorf("source %q: %w", src, err)
		}
		candidate := filepath.Join(root, "stories", name, "app.yaml")
		if _, statErr := os.Stat(candidate); statErr != nil {
			return "", fmt.Errorf("source %q: %v", src, statErr)
		}
		return candidate, nil
	}
	if filepath.IsAbs(src) {
		if info, statErr := os.Stat(src); statErr == nil && info.IsDir() {
			return filepath.Join(src, "app.yaml"), nil
		}
		return src, nil
	}
	candidate := filepath.Join(importerDir, src)
	if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
		return filepath.Join(candidate, "app.yaml"), nil
	}
	return candidate, nil
}

// findRepoRoot walks upward from dir looking for a `.kitsoki-root`
// marker file or a `go.mod` whose `module` directive is exactly
// `kitsoki` or whose path ends in `/kitsoki`. Returns absolute path on
// success. Strict matching (not substring) so a third-party module
// named e.g. `kitsoki-tools` doesn't accidentally claim to be the
// kitsoki repo root.
func findRepoRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	cur := abs
	for {
		if _, statErr := os.Stat(filepath.Join(cur, ".kitsoki-root")); statErr == nil {
			return cur, nil
		}
		if b, readErr := os.ReadFile(filepath.Join(cur, "go.mod")); readErr == nil {
			for _, ln := range strings.Split(string(b), "\n") {
				ln = strings.TrimSpace(ln)
				if !strings.HasPrefix(ln, "module ") {
					continue
				}
				modPath := strings.TrimSpace(strings.TrimPrefix(ln, "module"))
				// Strip optional surrounding quotes (rare in go.mod but tolerated).
				modPath = strings.Trim(modPath, "\"'")
				if modPath == "kitsoki" || strings.HasSuffix(modPath, "/kitsoki") {
					return cur, nil
				}
				// Only inspect the first module directive; modules are
				// declared at most once per go.mod.
				break
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("cannot resolve @kitsoki/<name>: no .kitsoki-root marker and no go.mod with module kitsoki (or */kitsoki) walking up from %s", dir)
		}
		cur = parent
	}
}

func canonicalPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			return real
		}
		return abs
	}
	return p
}

// foldChild merges a resolved child AppDef into parent under alias.
// This is where the namespace, world, intent, and host fan-out happens.
func foldChild(parent *AppDef, alias string, imp *ImportDef, child *AppDef, file string) []error {
	var errs []error

	// 1. Build name-set lookups for the rewriter.
	childWorld := make(map[string]struct{}, len(child.World))
	for k := range child.World {
		// Reserved engine globals stay BARE — never prefixed by the rewriter —
		// so child refs to world.last_error / world.host_error and
		// relevant_world: [last_error] resolve to the same flat keys the
		// runtime writes. See ReservedWorldKeys.
		if _, reserved := ReservedWorldKeys[k]; reserved {
			continue
		}
		childWorld[k] = struct{}{}
	}
	childIntent := make(map[string]struct{}, len(child.Intents))
	for k := range child.Intents {
		childIntent[k] = struct{}{}
	}
	childAgent := make(map[string]struct{}, len(child.Agents))
	for k := range child.Agents {
		childAgent[k] = struct{}{}
	}
	childOperation := make(map[string]struct{}, len(child.Operations))
	for k := range child.Operations {
		childOperation[k] = struct{}{}
	}
	childIface := make(map[string]struct{}, len(child.HostInterfaces))
	for k := range child.HostInterfaces {
		childIface[k] = struct{}{}
	}
	rw := &childRewriter{
		alias:                 alias,
		childWorldKey:         childWorld,
		childIntent:           childIntent,
		childAgent:            childAgent,
		childOperation:        childOperation,
		childIface:            childIface,
		parentExportedIntents: importParentExports(imp),
	}

	// 2. Resolve the entry state name (child's own namespace).
	entryName := imp.Entry
	if entryName == "" {
		if s, ok := child.Root.(string); ok {
			entryName = s
		}
	}
	if entryName == "" {
		errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: entry is required (child declares no root either)", alias)})
		return errs
	}

	// 3. Validate parent exit mappings: every imp.Exits.<name> must match a
	// child-declared exit.
	for parentExitName := range imp.Exits {
		if _, ok := child.Exits[parentExitName]; !ok {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: exits.%s: child does not declare an exit named %q", alias, parentExitName, parentExitName)})
		}
	}

	// 3a. Enforce per-exit `requires:`. For every transition
	// in the child whose target is `@exit:X`, the transition's effects
	// must collectively set every key in child.Exits[X].Requires. This is
	// the best-effort static check; it catches the author-error of
	// declaring `requires: [pr_url]` and then forgetting to set pr_url
	// in the @exit:completed transition. Keys whose world schema declares
	// a non-zero default also count (the key is provably set at runtime).
	defaultedKeys := make(map[string]struct{}, len(child.World))
	for k, v := range child.World {
		if _, reserved := ReservedWorldKeys[k]; reserved {
			continue
		}
		if v.Default != nil && !isZeroDefault(v.Default) {
			defaultedKeys[k] = struct{}{}
		}
	}
	checkExitRequires(child.States, child.Exits, defaultedKeys, file, alias, &errs)

	// 4. Rewrite child states in two passes:
	//    a. Rewrite expression-bearing fields, intent refs, and effect targets.
	//    b. Rewrite transition targets (incl. @exit:<name> with world_out).
	// The rewriter also rewrites bare-name targets to slash form
	// `<alias>/<name>` so a child transition `target: working` resolves
	// to the nested child sibling inside the compound wrapper.
	for name, s := range child.States {
		if s == nil {
			continue
		}
		rw.rewriteState(name, s)
		rewriteChildStateTransitions(s, alias, imp, &errs, file)
	}

	// 5. Install the child as a single compound state under parent.States[alias].
	//    The compound's Initial is the entry state; OnEnter carries the
	//    world_in setters. Child states keep their child-local names as
	//    children of the compound, mirroring the existing nesting model
	//    (cloak's `bar` compound with `dark`/`lit` children).
	if parent.States == nil {
		parent.States = make(map[string]*State)
	}
	if _, exists := parent.States[alias]; exists {
		errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: alias collides with existing parent state of the same name", alias)})
		return errs
	}
	wrapper := &State{
		Type:        "compound",
		Initial:     entryName,
		States:      make(map[string]*State, len(child.States)),
		ImportAlias: alias,
	}
	for _, name := range sortedKeys(child.States) {
		wrapper.States[name] = child.States[name]
	}
	parent.States[alias] = wrapper

	// 6a. Re-entry reset: synthesise on_enter setters that re-seed every
	// child world key to its schema default. The import compound is a
	// flattened path+world overlay with no first-class per-entry "instance":
	// the active state path resets to the entry child statelessly, but the
	// child's prefixed world keys (maker__iteration, maker__goal_achieved,
	// maker__terminal_reason, …) otherwise persist their terminal values from
	// a prior pass. A parent that drives the import to @exit and then RE-ENTERS
	// it for the next item would then run the sub-story's first room against
	// stale "already done" flags and no-op. Re-seeding defaults on every enter
	// makes each entry a fresh instance. On the first entry this is a no-op
	// (the keys were already seeded to these defaults at session boot via
	// WorldFromSchema). Reserved engine globals stay flat and are skipped.
	// These fire BEFORE the world_in projection below so projected inputs win.
	for _, ck := range sortedKeys(child.World) {
		if _, reserved := ReservedWorldKeys[ck]; reserved {
			continue
		}
		def := child.World[ck].Default
		if def == nil {
			continue // no declared default → nothing deterministic to reset to
		}
		// A world_in input is operator/parent-projected on entry; the world_in
		// setter (added next) overwrites it anyway, so skip the redundant reset.
		if _, isInput := imp.WorldIn[ck]; isInput {
			continue
		}
		wrapper.OnEnter = append(wrapper.OnEnter, Effect{Set: map[string]any{alias + "__" + ck: def}})
	}

	// 6. world_in: synthesise on_enter setters on the wrapper. They fire
	// when a transition enters the compound, before the initial child is
	// entered, so the child sees the projected world from the first turn.
	if len(imp.WorldIn) > 0 {
		for _, ck := range sortedKeys(imp.WorldIn) {
			expr := imp.WorldIn[ck]
			if _, ok := childWorld[ck]; !ok {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: world_in.%s: child does not declare world key %q", alias, ck, ck)})
				continue
			}
			wrapper.OnEnter = append(wrapper.OnEnter, Effect{Set: map[string]any{alias + "__" + ck: expr}})
		}
	}

	// 7. Fold the child's world schema into the parent under prefixed keys.
	if parent.World == nil {
		parent.World = make(map[string]VarDef)
	}
	for _, ck := range sortedKeys(child.World) {
		// Reserved engine globals are flat at every depth: don't synthesise an
		// alias__last_error schema entry. See ReservedWorldKeys.
		if _, reserved := ReservedWorldKeys[ck]; reserved {
			continue
		}
		newKey := alias + "__" + ck
		if _, exists := parent.World[newKey]; exists {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: world key %q collides", alias, newKey)})
			continue
		}
		parent.World[newKey] = child.World[ck]
	}

	// 8. Fold child intents into the parent's flat intent table under
	// prefixed names. The rewriter already updated child state On: maps.
	if parent.Intents == nil {
		parent.Intents = make(map[string]Intent)
	}
	for _, ck := range sortedKeys(child.Intents) {
		newKey := alias + "__" + ck
		if _, exists := parent.Intents[newKey]; exists {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: intent %q collides", alias, newKey)})
			continue
		}
		intent := child.Intents[ck]
		rw.rewriteIntent(&intent)
		parent.Intents[newKey] = intent
	}

	// 8a. Fold session-level operation policies under prefixed names. The
	// rewriter updated transition `operation:` refs to the same names, and
	// rewrites policy artifact/world-key references to the folded world.
	if parent.Operations == nil {
		parent.Operations = make(map[string]*OperationPolicy)
	}
	for _, ck := range sortedKeys(child.Operations) {
		newKey := alias + "__" + ck
		if _, exists := parent.Operations[newKey]; exists {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: operation %q collides", alias, newKey)})
			continue
		}
		parent.Operations[newKey] = rw.rewriteOperationPolicy(child.Operations[ck])
	}

	// 9. Host allow-list composition.
	//
	// Host names (parent.Hosts) compose per the `hosts:` mode declared on
	// the import. Concrete handlers introduced by host_interface bindings
	// are NOT folded here — they're resolved at top-level Load() by
	// resolveAllInterfaces, which knows the final iface bindings after
	// all folding completes. Folding the child's plain `hosts:` list and
	// validating `hosts: declared` covers the non-iface surface.
	parentHostSet := make(map[string]struct{}, len(parent.Hosts))
	for _, h := range parent.Hosts {
		parentHostSet[h] = struct{}{}
	}
	hostsMode := imp.Hosts
	if hostsMode == "" {
		hostsMode = "inherit"
	}
	switch hostsMode {
	case "inherit":
		for _, h := range child.Hosts {
			if _, seen := parentHostSet[h]; !seen {
				parent.Hosts = append(parent.Hosts, h)
				parentHostSet[h] = struct{}{}
			}
		}
	case "declared":
		// Strict mode: every child host must already be on the parent's
		// allow-list. Iface-induced handlers aren't required here because
		// they're added implicitly by resolveAllInterfaces at top-level
		// Load and would otherwise be redundant to enumerate in `hosts:`.
		want := make(map[string]string)
		for _, h := range child.Hosts {
			want[h] = fmt.Sprintf("child host %q", h)
		}
		for _, h := range sortedKeys(want) {
			if _, ok := parentHostSet[h]; !ok {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: hosts: declared but parent does not list %s", alias, want[h])})
			}
		}
	default:
		errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: hosts: must be 'inherit' or 'declared' (got %q)", alias, hostsMode)})
	}

	// 9a. Lift child's host_interface declarations into parent.HostInterfaces
	// under <alias>__<name> keys. imp.HostBindings entries override the
	// iface's default at fold time — this is the "single layer" of binding
	// composition. A grandparent rebinds a grandchild iface by spelling
	// the prefixed name in its own host_bindings (e.g.,
	// `host_bindings: { enc__narrator: host.X }`).
	if parent.HostInterfaces == nil {
		parent.HostInterfaces = make(map[string]*HostInterfaceDef)
	}
	for _, ifaceName := range sortedKeys(child.HostInterfaces) {
		newName := alias + "__" + ifaceName
		if _, exists := parent.HostInterfaces[newName]; exists {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: host_interface %q collides", alias, newName)})
			continue
		}
		src := child.HostInterfaces[ifaceName]
		if src == nil {
			continue
		}
		// Shallow copy is fine: Default is the only field we mutate; the
		// rest are read-only metadata.
		clone := *src
		if spec, ok := imp.HostBindings[ifaceName]; ok {
			// Explicit prefixed override wins (e.g. host_bindings:
			// { bf__ticket: host.X }). By fold time every HostBindingSpec's
			// Script form has already been resolved to a synthetic Handler
			// name by resolveHostBindingScripts, so Handler is always the
			// concrete binding here regardless of the author's original form.
			clone.Default = spec.Handler
		} else if terminal := ifaceTerminalName(ifaceName); terminal != ifaceName {
			// Base-name fallback: a binding keyed on the terminal
			// capability name (e.g. `ticket: host.gh.ticket`) cascades
			// to every transitively-lifted `*__ticket` iface that has no
			// explicit prefixed override. This is what lets an instance
			// rebind a capability once and have it apply across
			// all nested folds. See bug 44.
			if spec, ok := imp.HostBindings[terminal]; ok {
				clone.Default = spec.Handler
			}
		}
		parent.HostInterfaces[newName] = &clone
	}
	// Apply any host_bindings whose key targets an *already-prefixed*
	// grandchild iface (e.g., binding `enc__narrator` after the child
	// has folded a grandchild under alias `enc`). The lookup is uniform
	// via `<alias>__<binding-name>` so the grandparent surface is just
	// "spell the prefixed name."
	for bindingName, spec := range imp.HostBindings {
		// Skip ifaces directly owned by the child — already applied above.
		if _, ownedByChild := child.HostInterfaces[bindingName]; ownedByChild {
			continue
		}
		fullName := alias + "__" + bindingName
		if iface, ok := parent.HostInterfaces[fullName]; ok && iface != nil {
			iface.Default = spec.Handler
			continue
		}
		errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: host_bindings.%s: no matching host_interface (looked for %q in lifted child interfaces)", alias, bindingName, fullName)})
	}

	// 9c. Propagate any starlark-script synthetic bindings this level (or
	// any descendant already folded into `child`) introduced, so the
	// top-level Load() sees the full handler-name → script-path map after
	// all folds complete (resolveHostBindingScripts resolves script paths
	// against each level's own baseDir; the mapping itself just needs to
	// bubble up unchanged).
	if len(child.StarlarkHostBindings) > 0 {
		if parent.StarlarkHostBindings == nil {
			parent.StarlarkHostBindings = make(map[string]string, len(child.StarlarkHostBindings))
		}
		for name, script := range child.StarlarkHostBindings {
			parent.StarlarkHostBindings[name] = script
		}
	}

	// 10. Intent re-exports.
	if imp.Intents != nil {
		for _, parentName := range imp.Intents.Export {
			if _, ok := parent.Intents[parentName]; !ok {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: intents.export references undefined parent intent %q", alias, parentName)})
				continue
			}
			parent.Intents[alias+"__"+parentName] = parent.Intents[parentName]
		}
		childExports := make(map[string]struct{})
		if child.Exports != nil {
			for _, n := range child.Exports.Intents {
				childExports[n] = struct{}{}
			}
		}
		for _, childName := range imp.Intents.Import {
			if _, ok := childExports[childName]; !ok {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: intents.import.%s: child does not declare it in exports.intents", alias, childName)})
				continue
			}
			if _, ok := child.Intents[childName]; !ok {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: intents.import.%s: child does not define this intent", alias, childName)})
				continue
			}
			if _, dup := parent.Intents[childName]; dup {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: intents.import.%s: collides with parent intent of same name", alias, childName)})
				continue
			}
			intent := child.Intents[childName]
			rw.rewriteIntent(&intent)
			parent.Intents[childName] = intent
		}
	}

	// 11. Fold MetaModes. The meta_mode's `agent:` field is rewritten to
	// the alias-prefixed agent name when it references a child agent, so
	// the renamed `parent.Agents[<alias>__<agent>]` entry is found at
	// runtime. Without this rewrite the meta_mode would carry a dangling
	// reference to the child's pre-fold agent name.
	for _, k := range sortedKeys(child.MetaModes) {
		newKey := alias + "__" + k
		if parent.MetaModes == nil {
			parent.MetaModes = make(map[string]*MetaModeDef)
		}
		if _, exists := parent.MetaModes[newKey]; exists {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: meta_mode %q collides", alias, newKey)})
			continue
		}
		mm := child.MetaModes[k]
		if mm != nil {
			if _, isChildAgent := childAgent[mm.Agent]; isChildAgent {
				clone := *mm
				clone.Agent = alias + "__" + mm.Agent
				parent.MetaModes[newKey] = &clone
				continue
			}
		}
		parent.MetaModes[newKey] = mm
	}

	// 12. Fold Agents under prefixed names. Child state references to
	// `with: { agent: <name> }` were rewritten during the state pass.
	for _, k := range sortedKeys(child.Agents) {
		newKey := alias + "__" + k
		if parent.Agents == nil {
			parent.Agents = make(map[string]*AgentDecl)
		}
		if _, exists := parent.Agents[newKey]; exists {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: agent %q collides", alias, newKey)})
			continue
		}
		parent.Agents[newKey] = child.Agents[k]
	}

	return errs
}

// importParentExports returns the set of parent-intent names this import
// re-exports into the child's namespace. The rewriter uses this to detect
// bare intent references inside the child that should map to the parent's
// intent (under the alias-prefixed mirror).
func importParentExports(imp *ImportDef) map[string]struct{} {
	if imp == nil || imp.Intents == nil || len(imp.Intents.Export) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(imp.Intents.Export))
	for _, n := range imp.Intents.Export {
		out[n] = struct{}{}
	}
	return out
}

// isZeroDefault reports whether v is the YAML-decoded "zero value" for
// its dynamic type: empty string, 0/0.0, false, empty map/slice. Used to
// distinguish keys whose schema gives them a meaningful default from
// keys that are effectively unset.
func isZeroDefault(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case int:
		return x == 0
	case int64:
		return x == 0
	case float64:
		return x == 0
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// checkExitRequires walks every transition in the child's state tree
// and verifies that transitions targeting `@exit:X` set every key in
// X's Requires list. The set check considers:
//
//   - keys in the transition's own Effects.Set / Bind / Increment maps
//   - keys whose world schema has a non-zero default (already set at
//     world init)
//
// Errors name the offending state path and exit, plus the missing keys
// — making the failure mode crisp for authors who add a requires:
// without updating every exit-firing transition.
func checkExitRequires(states map[string]*State, exits map[string]*ExitDef, defaulted map[string]struct{}, file, alias string, errs *[]error) {
	checkExitRequiresRec("", states, exits, defaulted, file, alias, errs)
}

// checkExitRequiresRec is the recursive walker. statePath tracks the
// dotted state path (used in error messages only).
func checkExitRequiresRec(statePath string, states map[string]*State, exits map[string]*ExitDef, defaulted map[string]struct{}, file, alias string, errs *[]error) {
	for name := range states {
		s := states[name]
		if s == nil {
			continue
		}
		path := name
		if statePath != "" {
			path = statePath + "." + name
		}
		for intent, list := range s.On {
			for _, tr := range list {
				if !strings.HasPrefix(tr.Target, "@exit:") {
					continue
				}
				exitName := strings.TrimPrefix(tr.Target, "@exit:")
				exit, ok := exits[exitName]
				if !ok || exit == nil || len(exit.Requires) == 0 {
					continue
				}
				setKeys := make(map[string]struct{})
				for _, eff := range tr.Effects {
					for k := range eff.Set {
						setKeys[k] = struct{}{}
					}
					for k := range eff.Bind {
						setKeys[k] = struct{}{}
					}
					for k := range eff.Increment {
						setKeys[k] = struct{}{}
					}
				}
				var missing []string
				for _, req := range exit.Requires {
					if _, ok := setKeys[req]; ok {
						continue
					}
					if _, ok := defaulted[req]; ok {
						continue
					}
					missing = append(missing, req)
				}
				if len(missing) > 0 {
					*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: state %q intent %q transitions to @exit:%s but does not set required key(s) %v (declared in child.exits.%s.requires)", alias, path, intent, exitName, missing, exitName)})
				}
			}
		}
		if len(s.States) > 0 {
			checkExitRequiresRec(path, s.States, exits, defaulted, file, alias, errs)
		}
	}
}

// rewriteChildStateTransitions resolves every transition target in s
// so the child composes cleanly under the alias wrapper at any nesting
// depth. Key design decision: bare-name sibling refs are rewritten to
// **relative** form (`../X`) rather than absolute (`<alias>/X`). Relative
// refs compose for free — they walk one level up from the current
// state path at runtime, which after fold is `<alias>.<state>` (or
// `<outer>.<inner>.<state>` at depth 2, etc.), so `../X` always
// resolves to a sibling at the same depth without the loader having to
// know the full alias chain.
//
// Target categories handled:
//
//   - "" or "." — self-references; left alone.
//   - "{{ ... }}" — runtime expressions; left alone (no static rewrite).
//   - "@exit:<name>" — child-declared exit; mapped to imp.Exits[<name>].To
//     and the per-exit `set:` projection appended as additional effects
//     on the transition. Unmapped exits produce a load error; the
//     sentinel stays in place so validation surfaces the failure
//     without cascading null targets.
//   - bare name (no `/`, no `.`) — rewritten to `../<name>` so it
//     resolves to a sibling under the wrapper at runtime.
//   - relative `../...` chain — the consecutive `..` segments are
//     counted; if they would walk above the child's wrapper (i.e.,
//     exceed depthFromChildRoot + 1, where +1 is the wrapper level
//     itself), the load fails: transitions from inside the
//     child to outside are forbidden.
//   - already-qualified targets (slashed or dotted, no leading `..`) —
//     passed through; the validator will reject any that don't resolve.
//
// Walks nested compound/parallel child states recursively, threading
// the current depth-within-child for the `..` overreach check.
func rewriteChildStateTransitions(s *State, alias string, imp *ImportDef, errs *[]error, file string) {
	rewriteChildStateTransitionsAtDepth(s, alias, imp, 0, errs, file)
}

// rewriteChildStateTransitionsAtDepth is rewriteChildStateTransitions
// with explicit depth tracking. depthFromChildRoot is 0 for top-level
// child states and increments by 1 per nested compound/parallel layer.
// It controls the `..`-overreach check: a `..` chain longer than
// depthFromChildRoot+1 escapes the wrapper and must be rejected.
func rewriteChildStateTransitionsAtDepth(s *State, alias string, imp *ImportDef, depth int, errs *[]error, file string) {
	rwTarget := func(t string) string {
		if t == "" || t == "." {
			return t
		}
		if strings.Contains(t, "{{") {
			return t
		}
		if strings.HasPrefix(t, "@exit:") {
			name := strings.TrimPrefix(t, "@exit:")
			if imp.Exits != nil {
				if ex, ok := imp.Exits[name]; ok && ex != nil && ex.To != "" {
					return ex.To
				}
			}
			*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: child uses @exit:%s but parent does not map it", alias, name)})
			return t
		}
		// Already-qualified absolute target. Detect by presence of `/`
		// or `.` (but NOT `..`). These can be deeper paths into compound
		// states; pass through and let the validator catch misses.
		if !strings.HasPrefix(t, "..") && strings.ContainsAny(t, "/.") {
			return t
		}
		// Relative `..` chain: count consecutive `..` segments.
		if strings.HasPrefix(t, "..") {
			segs := strings.Split(t, "/")
			dotdots := 0
			for _, seg := range segs {
				if seg == ".." {
					dotdots++
				} else {
					break
				}
			}
			// The wrapper itself sits at depthFromChildRoot+1 from the leaf
			// state (each `..` pops one segment; the wrapper alias is one
			// segment, so popping past it means leaving the import).
			//
			// Allowed: dotdots <= depth+1 (walks at most up to the wrapper).
			// Forbidden: dotdots > depth+1 (escapes the wrapper).
			if dotdots > depth+1 {
				*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: target %q walks above the child's namespace (depth %d, %d `..` segments); cross-boundary parent targets are forbidden", alias, t, depth, dotdots)})
			}
			return t
		}
		// Bare name: rewrite to relative sibling reference that walks
		// up to the alias wrapper's parent (where the sibling lives).
		//
		// The state being rewritten sits at `depth` levels of compound
		// nesting BELOW the alias wrapper's immediate children (depth=0
		// is a child of the wrapper itself; depth=1 is two levels in,
		// etc.). To reach a sibling of the wrapper from depth N
		// requires `N+1` `..` segments: N to climb out of nested
		// compounds to the wrapper level, plus one more to climb past
		// the wrapper itself.
		//
		// This composes correctly across multi-layer folds (Wave 3 /
		// Phase 3 of the dev-story unify proposal): when the
		// containing app is itself imported under another alias later,
		// the recursive depth tracking on the second pass leaves these
		// `..` chains alone (the `..`-overreach check tolerates them
		// up to depth+1 at the new nesting), and the runtime
		// resolveTarget pops the correct number of segments off the
		// runtime state path to land on the desired sibling. A state
		// at depth=1 inside bf (folded under dev-story) needs
		// `../../pr` to reach the pr sibling at the top of dev-story
		// after kitsoki-dev's second fold under `core`.
		return strings.Repeat("../", depth+1) + t
	}

	// On: transitions.
	for intent, list := range s.On {
		for i, tr := range list {
			origTarget := tr.Target
			// Capture world_out projection BEFORE rewriting the target.
			if strings.HasPrefix(origTarget, "@exit:") {
				name := strings.TrimPrefix(origTarget, "@exit:")
				tr.OperationExit = name
				tr.OperationExitPolicyPrefix = alias + "__"
				if imp.Exits != nil {
					if ex, ok := imp.Exits[name]; ok && ex != nil && len(ex.Set) > 0 {
						tr.Effects = append(tr.Effects, Effect{Set: ex.Set})
					}
				}
			}
			tr.Target = rwTarget(origTarget)
			// Effect-internal targets: on_error redirects AND the
			// `target:` an on_complete: effect dispatches when a
			// background job finishes. Both are state refs in the
			// child's namespace and must be rewritten exactly like a
			// transition target — otherwise a folded phase-template
			// graph (bugfix) lands on bare `phase_N_executing` names
			// that don't exist under the alias wrapper.
			for j, eff := range tr.Effects {
				if eff.OnError != "" {
					eff.OnError = rwTarget(eff.OnError)
				}
				if eff.Target != "" {
					eff.Target = rwTarget(eff.Target)
				}
				for k, sub := range eff.OnComplete {
					if sub.OnError != "" {
						sub.OnError = rwTarget(sub.OnError)
					}
					if sub.Target != "" {
						sub.Target = rwTarget(sub.Target)
					}
					eff.OnComplete[k] = sub
				}
				tr.Effects[j] = eff
			}
			list[i] = tr
		}
		s.On[intent] = list
	}
	// Timeout target.
	if s.Timeout != nil {
		s.Timeout.Target = rwTarget(s.Timeout.Target)
	}
	// OnEnter effect-internal targets: on_error redirects AND the
	// on_complete: `target:` a finishing background job dispatches.
	// (Most folded background jobs live in on_enter:, which is where the
	// bugfix phase template puts its execute → next-phase chains.)
	for i, eff := range s.OnEnter {
		if eff.OnError != "" {
			eff.OnError = rwTarget(eff.OnError)
		}
		if eff.Target != "" {
			eff.Target = rwTarget(eff.Target)
		}
		for j, sub := range eff.OnComplete {
			if sub.OnError != "" {
				sub.OnError = rwTarget(sub.OnError)
			}
			if sub.Target != "" {
				sub.Target = rwTarget(sub.Target)
			}
			eff.OnComplete[j] = sub
		}
		s.OnEnter[i] = eff
	}
	// Compound state initial: bare child name resolves to a nested child
	// of this state. No rewriting needed (the existing compound-state
	// validator follows the dot-notation path).
	//
	// Recurse into nested compound/parallel children at depth+1 so the
	// overreach check accounts for the extra layer.
	for _, c := range s.States {
		if c != nil {
			rewriteChildStateTransitionsAtDepth(c, alias, imp, depth+1, errs, file)
		}
	}
}
