package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/kit"
)

// DefaultRootKitName is the root kit `root.import` (or kitsoki.story) defaults
// to when unset — dev-story, the hub every kitsoki-dev/gears-rust-style
// instance has always imported as its root. Per D6 (root generalization,
// .context/kits-implementation-plan.md "S6"), dev-story is no longer the ONLY
// value root.import may name — it is merely the DEFAULT one. Any installed
// kit whose kit.yaml declares a `root:` block (kit.RootDecl) naming one of its
// own provides.stories qualifies as a blessed root; see ResolveRootKit. This
// replaces the former hardcoded `RootStoryName` const, which rejected every
// import name except this one.
const DefaultRootKitName = "dev-story"

// RootAlias is the import alias the synthesized root folds the root kit's
// story under. It mirrors kitsoki-dev's `core` alias role but is named `root`
// so a materialized tree reads as "this is the project root, importing
// dev-story" (or whichever kit is installed as root).
const RootAlias = "root"

// ResolveRootKit resolves importName (DefaultRootKitName when empty) to its
// kit manifest and validates it declares a `root:` block naming importName as
// its own blessed-root story (D6). This is the manifest-driven replacement
// for the old hardcoded RootStoryName equality check + DevStoryIfaces
// allow-list: BuildRootImporter now reads the story's entry state, the
// rebindable host_interfaces allow-list, and the fixed `hosts: declared`
// allow-list from the resolved manifest's kit.RootDecl instead of Go-side
// constants.
//
// Resolution reuses the exact @kitsoki/<name> resolution tiers every other
// import already goes through (resolveImportSource: injected override /
// on-disk repo / embedded fallback) — dev-story is resolved as "just another
// kit" (D5 Phase A: "engine resolves dev-story through the kit resolver even
// though it's local"), not a special-cased file lookup. The kit.yaml is
// expected to sit beside the resolved story's app.yaml (see
// stories/dev-story/kit.yaml + Provides.StoryDirs for why that is a valid
// in-repo layout in Phase A).
func ResolveRootKit(importName, repoRoot string, resolver ImportResolver) (*kit.Def, error) {
	if importName == "" {
		importName = DefaultRootKitName
	}
	appPath, err := resolveImportSource("@kitsoki/"+importName, repoRoot, resolver)
	if err != nil {
		return nil, fmt.Errorf("root.import %q is not a known base story (could not resolve @kitsoki/%s: %v)", importName, importName, err)
	}
	manifestPath := filepath.Join(filepath.Dir(appPath), kit.ManifestFileName)
	manifest, err := kit.Load(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("root.import %q is not a known base story (no valid kit.yaml root: declaration at %s: %v)", importName, manifestPath, err)
	}
	if !manifest.HasRoot(importName) {
		return nil, fmt.Errorf("root.import %q is not a known base story (its kit.yaml declares no root: block for it)", importName)
	}
	return manifest, nil
}

// RootSpec is the neutral, package-local projection of the `.kitsoki.yaml`
// `root:` block that SynthesizeRoot consumes. webconfig owns the YAML surface
// (webconfig.RootConfig) and converts to this struct via RootConfig.RootSpec()
// — the indirection keeps internal/app free of an import edge back to
// internal/webconfig (which already imports internal/app for DiscoverStories),
// so there is no package cycle. A nil *RootSpec means rung 0 (synthesize a
// dev-story import with no overrides).
type RootSpec struct {
	// Import is the base story name (v1: only "dev-story"). Empty ⇒ dev-story.
	Import string
	// Bindings rebinds named dev-story host_interfaces onto concrete handler
	// names or .star script paths. Folded into imports.<root>.host_bindings.
	// Keyed by iface name.
	Bindings map[string]string
	// World holds instance-level world defaults projected into the import via
	// world_in: (and set as top-level world: defaults). Keyed by world key.
	World map[string]any
	// Synonyms extends routing synonyms for the synthesized instance. Keyed by
	// intent name → alternate phrasings.
	Synonyms map[string][]string
}

// SynthesizeRoot builds an in-memory AppDef that imports the base story
// (dev-story) under the RootAlias and runs it through the EXACT same load
// pipeline LoadWithOverrides uses (resolveImports → expandPhases →
// resolveAllInterfaces → validateDef). The result is byte-for-byte the same
// shape kitsoki-dev hand-writes — a thin importer of dev-story — minus the file
// on disk. This is the loader half of the "blank root that grows" ladder:
//
//   - rung 0 (spec == nil): a dev-story import with NO overrides; dev-story's
//     host_interfaces: defaults carry every binding.
//   - rung 1 (spec != nil): overrides.bindings fold into the import's
//     host_bindings, overrides.world projects into world_in: and the
//     instance-level world: defaults, overrides.synonyms extend routing.
//
// repoRoot is the directory the synthetic importer resolves @kitsoki/dev-story
// against (it becomes the AppDef.BaseDir); it must contain a .kitsoki-root
// marker or a go.mod declaring module kitsoki so findRepoRoot resolves the
// in-repo dev-story (the downstream installed-dependency path is documented
// in stories/dev-story/README.md). The synthetic app.id is the repo basename so a
// trace shows where the implicit root came from.
//
// A malformed spec (unknown import / unknown binding iface) is rejected here
// before synthesis with a clear message; everything else (an unknown world key
// projected into a dead world_in: setter, a binding naming a non-existent
// handler) is caught downstream by the same validators that catch a malformed
// imports: block.
func SynthesizeRoot(spec *RootSpec, repoRoot string) (*AppDef, error) {
	return SynthesizeRootWithResolver(spec, repoRoot, nil)
}

// SynthesizeRootWithResolver is SynthesizeRoot plus the same injected
// @kitsoki/<name> resolver used by file-backed app loads. CLI entrypoints pass
// the embedded-story resolver here so a binary-only user can run an implicit
// dev-story root from a foreign repo that has no Kitsoki checkout on disk.
func SynthesizeRootWithResolver(spec *RootSpec, repoRoot string, resolver ImportResolver) (*AppDef, error) {
	def, abs, err := buildRootImporter(spec, repoRoot, resolver)
	if err != nil {
		return nil, err
	}
	// Run the identical fold pipeline a file-backed Load runs. The synthetic
	// path has no file on disk; pass a sentinel so error messages read clearly
	// and LoadedManifests is seeded with a stable canonical key.
	return runLoadPipeline(def, syntheticRootPath(abs), abs, nil, resolver)
}

// BuildRootImporter constructs the UN-folded importer AppDef — a thin importer
// of dev-story with host_bindings / world_in projections folded in but the
// imports: block still present (not yet resolved). SynthesizeRoot runs this
// through runLoadPipeline; `kitsoki materialize` serializes it to YAML. The two
// share this builder so the materialized rung-2 file is byte-faithful to what
// the loader synthesizes — emit(BuildRootImporter) then app.Load yields a def
// deep-equal to SynthesizeRoot's. abs is the resolved repo root (the importer's
// BaseDir / the @kitsoki resolution root).
func BuildRootImporter(spec *RootSpec, repoRoot string) (def *AppDef, abs string, err error) {
	return buildRootImporter(spec, repoRoot, nil)
}

// BuildRootImporterWithResolver is BuildRootImporter plus the same injected
// @kitsoki/<name> resolver used by SynthesizeRootWithResolver. Emitters that
// serialize a synthesized root must use this variant whenever validation used
// an embedded or override resolver, otherwise the emitted host allow-list can
// drift from the loaded root.
func BuildRootImporterWithResolver(spec *RootSpec, repoRoot string, resolver ImportResolver) (def *AppDef, abs string, err error) {
	return buildRootImporter(spec, repoRoot, resolver)
}

// buildRootImporter is BuildRootImporter plus an injected ImportResolver,
// mirroring buildKitImporter/BuildKitImporter. SynthesizeRootWithResolver
// passes its resolver through here so root-kit resolution (ResolveRootKit)
// uses the same resolver the later fold pipeline uses — a binary-only
// downstream caller's embedded-fallback resolver is not asked to do
// anything it doesn't already do at fold time.
func buildRootImporter(spec *RootSpec, repoRoot string, resolver ImportResolver) (def *AppDef, abs string, err error) {
	importName := DefaultRootKitName
	if spec != nil && spec.Import != "" {
		importName = spec.Import
	}
	manifest, err := ResolveRootKit(importName, repoRoot, resolver)
	if err != nil {
		return nil, "", err
	}
	rootIfaces := manifest.RootHostInterfaces()
	if spec != nil {
		for _, iface := range sortedKeys(spec.Bindings) {
			if _, ok := rootIfaces[iface]; !ok {
				return nil, "", fmt.Errorf("root.overrides.bindings: %q is not a host_interface declared by %s (declared: %s)", iface, importName, ifaceList(rootIfaces))
			}
		}
	}

	abs = repoRoot
	if a, absErr := filepath.Abs(repoRoot); absErr == nil {
		abs = a
	}

	imp := &ImportDef{
		Source: "@kitsoki/" + importName,
		// The root kit's manifest-declared entry state (kit.RootDecl.Entry) —
		// dev-story's is "landing", the free-form workbench
		// (freeform-landing.md); the implicit root lands there, mirroring
		// kitsoki-dev's imports.core.entry.
		Entry: manifest.Root.Entry,
		// Strict host composition mirrors kitsoki-dev: every host the root
		// story's subtree may invoke must appear in the synthesized hosts:
		// allow-list below, so a synthesized root has the same fail-fast
		// host surface a hand-written instance does.
		Hosts: "declared",
	}

	def = &AppDef{
		App: AppMeta{
			ID:      filepath.Base(abs),
			Version: "0.0.0",
			Title:   fmt.Sprintf("%s — implicit root (%s)", filepath.Base(abs), importName),
		},
		// Instance-level agent plugins + embedding model. These are NOT
		// inherited from the imported child (agent_plugins live at the
		// instance level), so the synthesized root must declare them itself
		// exactly as kitsoki-dev does — otherwise dev-story's
		// `agent: agent.local_llm` references resolve to nothing.
		AgentPlugins: map[string]*AgentPluginDecl{
			"agent.local_llm": {
				Plugin:  "builtin.local_llm",
				Model:   "qwen2.5-1.5b-instruct",
				Grammar: true,
			},
		},
		Routing: synthesizedRouting(),
		Hosts:   append([]string(nil), manifest.Root.Hosts...),
		Imports: map[string]*ImportDef{RootAlias: imp},
		Root:    RootAlias,
	}

	if spec != nil {
		applyRootOverrides(def, imp, spec)
	}
	return def, abs, nil
}

// applyRootOverrides folds a rung-1 spec's overrides into the synthesized
// importer + instance app: bindings → imports.<root>.host_bindings, world →
// world_in: projections + instance world: defaults, synonyms → routing
// synonyms on the matching instance intents.
func applyRootOverrides(def *AppDef, imp *ImportDef, spec *RootSpec) {
	if len(spec.Bindings) > 0 {
		imp.HostBindings = make(map[string]HostBindingSpec, len(spec.Bindings))
		for k, v := range spec.Bindings {
			imp.HostBindings[k] = hostBindingSpecFromString(v)
		}
	}
	if len(spec.World) > 0 {
		if def.World == nil {
			def.World = make(map[string]VarDef, len(spec.World))
		}
		if imp.WorldIn == nil {
			imp.WorldIn = make(map[string]string, len(spec.World))
		}
		for _, k := range sortedKeys(spec.World) {
			v := spec.World[k]
			// Instance-level default so the value is the source of truth, and a
			// world_in: projection so it reaches the child's same-named key —
			// mirroring kitsoki-dev's `world_in: { judge_mode: "{{ world.judge_mode }}" }`.
			def.World[k] = VarDef{Type: inferVarType(v), Default: v}
			imp.WorldIn[k] = fmt.Sprintf("{{ world.%s }}", k)
		}
	}
	if len(spec.Synonyms) > 0 {
		if def.Intents == nil {
			def.Intents = make(map[string]Intent, len(spec.Synonyms))
		}
		for _, name := range sortedKeys(spec.Synonyms) {
			in := def.Intents[name]
			in.Synonyms = append(in.Synonyms, spec.Synonyms[name]...)
			def.Intents[name] = in
		}
	}
}

func hostBindingSpecFromString(raw string) HostBindingSpec {
	v := strings.TrimSpace(raw)
	if strings.HasSuffix(v, ".star") {
		return HostBindingSpec{Script: v}
	}
	return HostBindingSpec{Handler: v}
}

// inferVarType picks a world VarDef type from an override value's Go kind. The
// loader only needs a plausible type for the schema entry; exact precision is
// not required since these are projected through to dev-story's own typed keys.
func inferVarType(v any) string {
	switch v.(type) {
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float32, float64:
		return "float"
	case map[string]any:
		return "object"
	case []any:
		return "list"
	default:
		return "string"
	}
}

// synthesizedRouting builds the instance-level routing block exactly as the
// YAML loader would for `routing: { embedding: { model: nomic-embed-text-v1.5 } }`
// — start from DefaultRoutingConfig (matching RoutingConfig.UnmarshalYAML's
// seed), set the embedding model, then WithDefaults. Building it this way keeps
// a synthesized def byte-identical to the materialized-then-loaded one (the
// round-trip equality anchor). Mirrors kitsoki-dev's routing.embedding block.
func synthesizedRouting() *RoutingConfig {
	r := DefaultRoutingConfig()
	r.Embedding = &EmbedConfig{Model: "nomic-embed-text-v1.5"}
	r = r.WithDefaults()
	return &r
}

// syntheticRootPath is the sentinel manifest path a synthesized root carries.
// It is rooted at the repo so canonicalPath / LoadedManifests produce a stable
// key, but no file is read from it (runLoadPipeline never re-reads the root
// manifest — parseAndMerge already produced the in-memory def).
func syntheticRootPath(repoRoot string) string {
	return filepath.Join(repoRoot, "<synthesized-root>", "app.yaml")
}

// ifaceList renders a root kit's RootHostInterfaces() set as a sorted
// comma-list for error messages (formerly rendered the hardcoded
// DevStoryIfaces map).
func ifaceList(ifaces map[string]struct{}) string {
	names := make([]string, 0, len(ifaces))
	for k := range ifaces {
		names = append(names, k)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// KitImportSpec configures how BuildKitImporter/SynthesizeKit binds one of a
// kit's provided stories into a synthesized importer. It is the kit-manifest
// analogue of RootSpec: the same "spec != nil folds overrides in" shape,
// generalized from the single hardcoded dev-story base to an arbitrary
// kit.yaml-declared story.
type KitImportSpec struct {
	// Entry is the child story's initial state (mirrors ImportDef.Entry).
	// Empty is only valid when the child's own root state name is an
	// acceptable entry (import-fold's own validation catches a bad value).
	Entry string
	// Bindings rebinds the story's host_interfaces onto concrete handler
	// names, keyed by iface name. Unlike RootSpec.Bindings — checked here
	// against dev-story's hardcoded DevStoryIfaces set — a kit story's
	// iface names aren't known statically at this call site; an unknown
	// iface name is still rejected fail-fast, but by resolveAllInterfaces
	// during the fold that BuildKitImporter's caller (SynthesizeKit) runs,
	// not by a pre-check here.
	Bindings map[string]string
	// Parameters binds values for the kit manifest's declared `parameters:`
	// block. Every key MUST be declared in manifest.Parameters — checked
	// here fail-fast, mirroring BuildRootImporter's DevStoryIfaces check —
	// and is folded into the import's world_in: on the same-named child
	// world key (the child story must declare a matching world: key;
	// caught downstream like any other dead world_in: projection).
	Parameters map[string]any
}

// BuildKitImporter constructs the UN-folded importer AppDef for one story a
// kit provides — the kit-manifest analogue of BuildRootImporter. manifest
// must already be schema-validated and story-checked (kit.Load/kit.LoadDir
// does both). storyName must be one of manifest.Provides.Stories; this is
// checked here as the fail-fast allow-list BuildRootImporter's DevStoryIfaces
// check mirrors. alias is the import alias the synthesized root folds the
// story under (becomes the AppDef.States[alias] compound wrapper key);
// callers typically pass storyName itself.
//
// SynthesizeKit runs the result through the identical runLoadPipeline
// SynthesizeRoot uses (resolveImports → expandPhases → resolveAllInterfaces →
// validateDef) — same verified Synthesize ≡ Load(emit) round-trip discipline:
// a def built here folds identically to hand-writing a thin importer of the
// kit's story on disk.
func BuildKitImporter(manifest *kit.Def, storyName, alias string, spec *KitImportSpec) (def *AppDef, abs string, err error) {
	return buildKitImporter(manifest, storyName, alias, spec, nil)
}

// buildKitImporter is BuildKitImporter plus an injected ImportResolver used
// for the exit-discovery load of the child story below, so that a kit story's
// own imports resolve through the same resolver SynthesizeKitWithResolver's
// caller supplied — matching that function's documented contract.
func buildKitImporter(manifest *kit.Def, storyName, alias string, spec *KitImportSpec, resolver ImportResolver) (def *AppDef, abs string, err error) {
	if manifest == nil {
		return nil, "", fmt.Errorf("kit importer: nil manifest")
	}
	if !manifest.HasStory(storyName) {
		return nil, "", fmt.Errorf("kit importer: %q does not provide story %q (provides.stories: %v)", manifest.Identity(), storyName, manifest.Provides.Stories)
	}
	if alias == "" {
		alias = storyName
	}
	if spec != nil {
		for _, name := range sortedKeys(spec.Parameters) {
			if _, ok := manifest.Parameters[name]; !ok {
				return nil, "", fmt.Errorf("kit importer: %q: parameter %q is not declared in %s parameters: (declared: %s)", manifest.Identity(), name, manifest.Identity(), kitParamList(manifest))
			}
		}
	}

	abs = manifest.Dir()

	imp := &ImportDef{
		// Absolute story dir — the documented ImportDef.Source escape hatch
		// ("/absolute/path" — test escape hatch), used here for the general
		// case since a kit story is not a `@kitsoki/<name>` base story and
		// may not live inside the loading repo at all.
		Source: manifest.StoryDir(storyName),
		Entry:  "",
		// "inherit" (default) unions the story's hosts silently. Unlike
		// BuildRootImporter's strict "declared" mode (a fixed, hand-audited
		// dev-story host surface), a kit importer has no such fixed surface
		// to hardcode per-kit, so plain union is the correct default here.
		Hosts: "inherit",
	}
	if spec != nil {
		imp.Entry = spec.Entry
	}

	def = &AppDef{
		App: AppMeta{
			ID:      fmt.Sprintf("%s-%s", manifest.Kit, storyName),
			Version: "0.0.0",
			Title:   fmt.Sprintf("%s — kit importer (%s)", manifest.Identity(), storyName),
		},
		Routing: synthesizedRouting(),
		Imports: map[string]*ImportDef{alias: imp},
		Root:    alias,
	}

	// A kit story declares its own `exits:` contract (docs/stories/imports.md);
	// unlike dev-story (whose fixed exit set BuildRootImporter hand-maps to
	// pr_landed/main), an arbitrary kit story's exits aren't known statically
	// here. Discover them by loading the story standalone (it must be
	// standalone-loadable per the "provides.stories" contract) and generically
	// map every declared exit to a synthesized terminal state, so the fold
	// never fails with "child uses @exit:X but parent does not map it"
	// regardless of which story/kit is imported.
	childPath := filepath.Join(imp.Source, "app.yaml")
	var childDef *AppDef
	var loadErr error
	if resolver != nil {
		childDef, loadErr = LoadWithResolver(childPath, nil, resolver)
	} else {
		childDef, loadErr = Load(childPath)
	}
	if loadErr != nil {
		return nil, "", fmt.Errorf("kit importer: %q story %q must be standalone-loadable: %w", manifest.Identity(), storyName, loadErr)
	}
	if len(childDef.Exits) > 0 {
		imp.Exits = make(map[string]*ImportExit, len(childDef.Exits))
		def.States = make(map[string]*State, len(childDef.Exits))
		for _, name := range sortedKeys(childDef.Exits) {
			stateName := fmt.Sprintf("__kit_exit__%s__%s", alias, name)
			imp.Exits[name] = &ImportExit{To: stateName}
			def.States[stateName] = &State{
				Description: fmt.Sprintf("kit exit: %s.%s", storyName, name),
				Terminal:    true,
			}
		}
	}

	if spec != nil {
		applyKitOverrides(def, imp, spec)
	}
	return def, abs, nil
}

// SynthesizeKit builds the importer for one of a kit's provided stories
// (BuildKitImporter) and runs it through the identical fold pipeline a
// file-backed Load runs, exactly mirroring SynthesizeRoot/SynthesizeRootWithResolver.
func SynthesizeKit(manifest *kit.Def, storyName, alias string, spec *KitImportSpec) (*AppDef, error) {
	return SynthesizeKitWithResolver(manifest, storyName, alias, spec, nil)
}

// SynthesizeKitWithResolver is SynthesizeKit plus an injected ImportResolver,
// mirroring SynthesizeRootWithResolver. A kit story's own imports (if any)
// resolve through this resolver exactly as a file-backed load's would.
func SynthesizeKitWithResolver(manifest *kit.Def, storyName, alias string, spec *KitImportSpec, resolver ImportResolver) (*AppDef, error) {
	def, abs, err := buildKitImporter(manifest, storyName, alias, spec, resolver)
	if err != nil {
		return nil, err
	}
	return runLoadPipeline(def, syntheticKitPath(abs, manifest, storyName), abs, nil, resolver)
}

// applyKitOverrides folds a KitImportSpec's overrides into the synthesized
// importer + instance app: bindings → imports.<alias>.host_bindings,
// parameters → world_in: projections + instance world: defaults. Mirrors
// applyRootOverrides's Bindings/World handling (kit parameters have no
// synonyms analogue in v1).
func applyKitOverrides(def *AppDef, imp *ImportDef, spec *KitImportSpec) {
	if len(spec.Bindings) > 0 {
		imp.HostBindings = make(map[string]HostBindingSpec, len(spec.Bindings))
		for k, v := range spec.Bindings {
			imp.HostBindings[k] = hostBindingSpecFromString(v)
		}
	}
	if len(spec.Parameters) > 0 {
		if def.World == nil {
			def.World = make(map[string]VarDef, len(spec.Parameters))
		}
		if imp.WorldIn == nil {
			imp.WorldIn = make(map[string]string, len(spec.Parameters))
		}
		for _, k := range sortedKeys(spec.Parameters) {
			v := spec.Parameters[k]
			def.World[k] = VarDef{Type: inferVarType(v), Default: v}
			imp.WorldIn[k] = fmt.Sprintf("{{ world.%s }}", k)
		}
	}
}

// syntheticKitPath is the sentinel manifest path a synthesized kit importer
// carries, mirroring syntheticRootPath.
func syntheticKitPath(repoRoot string, manifest *kit.Def, storyName string) string {
	return filepath.Join(repoRoot, fmt.Sprintf("<synthesized-kit-%s-%s>", manifest.Kit, storyName), "app.yaml")
}

// kitParamList renders a kit manifest's declared parameter names as a sorted
// comma-list for error messages, mirroring ifaceList.
func kitParamList(manifest *kit.Def) string {
	names := make([]string, 0, len(manifest.Parameters))
	for k := range manifest.Parameters {
		names = append(names, k)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// DevStoryWorldKeys loads dev-story standalone (resolving @kitsoki/dev-story
// from repoRoot) and returns the set of world keys it declares. webconfig uses
// it to fail-fast on an `overrides.world.<key>` that names no dev-story world
// key — surfacing the typo at load rather than projecting a dead world_in:
// setter. Returns an error when dev-story itself cannot be resolved/loaded.
func DevStoryWorldKeys(repoRoot string) (map[string]struct{}, error) {
	src, err := resolveImportSource("@kitsoki/"+DefaultRootKitName, repoRoot, nil)
	if err != nil {
		return nil, err
	}
	def, err := Load(src)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{}, len(def.World))
	for k := range def.World {
		keys[k] = struct{}{}
	}
	return keys, nil
}

// DevStoryWorldKeysFor is DevStoryWorldKeys generalized to an arbitrary
// resolved root kit manifest (D6) — it loads manifest.Root.Story standalone
// (resolving @kitsoki/<story> from repoRoot) and returns the set of world
// keys it declares. webconfig.resolveRoot uses it, after ResolveRootKit has
// already resolved the manifest, to fail-fast on an `overrides.world.<key>`
// that names no root-story world key.
func DevStoryWorldKeysFor(manifest *kit.Def, repoRoot string) (map[string]struct{}, error) {
	if manifest == nil || manifest.Root == nil {
		return nil, fmt.Errorf("kit manifest declares no root: block")
	}
	src, err := resolveImportSource("@kitsoki/"+manifest.Root.Story, repoRoot, nil)
	if err != nil {
		return nil, err
	}
	def, err := Load(src)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{}, len(def.World))
	for k := range def.World {
		keys[k] = struct{}{}
	}
	return keys, nil
}
