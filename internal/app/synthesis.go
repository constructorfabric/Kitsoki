package app

import (
	"fmt"
	"path/filepath"
	"sort"
)

// RootStoryName is the only base story `root.import` may name in v1. The whole
// implicit-root framing is "specialize dev-story", so dev-story is the single
// blessed base; any other value is rejected before synthesis. Widening this to
// an arbitrary in-story_dirs base is an open question (see
// docs/stories/imports.md "The blank root that grows").
const RootStoryName = "dev-story"

// RootAlias is the import alias the synthesized root folds dev-story under. It
// mirrors kitsoki-dev's `core` alias role but is named `root` so a materialized
// tree reads as "this is the project root, importing dev-story".
const RootAlias = "root"

// DevStoryIfaces is the set of host_interfaces dev-story declares (and thus the
// only ifaces an `overrides.bindings.<iface>` may rebind). Kept here as the
// fail-fast allow-list so a typo'd binding iface is a clear load error rather
// than a silently-ignored binding. Mirrors stories/dev-story/app.yaml's
// host_interfaces: block; if dev-story grows a sixth iface, add it here.
var DevStoryIfaces = map[string]struct{}{
	"ticket":    {},
	"vcs":       {},
	"ci":        {},
	"workspace": {},
	"transport": {},
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
	// names. Folded into imports.<root>.host_bindings. Keyed by iface name.
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
// in-repo dev-story (the downstream installed-dependency path is deferred to
// kitsoki-as-dependency.md). The synthetic app.id is the repo basename so a
// trace shows where the implicit root came from.
//
// A malformed spec (unknown import / unknown binding iface) is rejected here
// before synthesis with a clear message; everything else (an unknown world key
// projected into a dead world_in: setter, a binding naming a non-existent
// handler) is caught downstream by the same validators that catch a malformed
// imports: block.
func SynthesizeRoot(spec *RootSpec, repoRoot string) (*AppDef, error) {
	def, abs, err := BuildRootImporter(spec, repoRoot)
	if err != nil {
		return nil, err
	}
	// Run the identical fold pipeline a file-backed Load runs. The synthetic
	// path has no file on disk; pass a sentinel so error messages read clearly
	// and LoadedManifests is seeded with a stable canonical key.
	return runLoadPipeline(def, syntheticRootPath(abs), abs, nil, nil)
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
	importName := RootStoryName
	if spec != nil && spec.Import != "" {
		importName = spec.Import
	}
	if importName != RootStoryName {
		return nil, "", fmt.Errorf("root.import %q is not a known base story (v1 supports: %s)", importName, RootStoryName)
	}
	if spec != nil {
		for _, iface := range sortedKeys(spec.Bindings) {
			if _, ok := DevStoryIfaces[iface]; !ok {
				return nil, "", fmt.Errorf("root.overrides.bindings: %q is not a host_interface declared by %s (declared: %s)", iface, RootStoryName, ifaceList())
			}
		}
	}

	abs = repoRoot
	if a, absErr := filepath.Abs(repoRoot); absErr == nil {
		abs = a
	}

	imp := &ImportDef{
		Source: "@kitsoki/" + importName,
		// dev-story's root is the free-form workbench `landing` (freeform-landing.md);
		// the implicit root lands there, mirroring kitsoki-dev's imports.core.entry.
		Entry: "landing",
		// Strict host composition mirrors kitsoki-dev: every host the dev-story
		// subtree may invoke must appear in the synthesized hosts: allow-list
		// below, so a synthesized root has the same fail-fast host surface a
		// hand-written instance does.
		Hosts: "declared",
	}

	def = &AppDef{
		App: AppMeta{
			ID:      filepath.Base(abs),
			Version: "0.0.0",
			Title:   fmt.Sprintf("%s — implicit root (dev-story)", filepath.Base(abs)),
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
		Hosts:   synthesizedRootHosts(),
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
		imp.HostBindings = make(map[string]string, len(spec.Bindings))
		for k, v := range spec.Bindings {
			imp.HostBindings[k] = v
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

// synthesizedRootHosts is the host allow-list the synthesized root declares so
// `hosts: declared` on the dev-story import is satisfied. It mirrors
// kitsoki-dev's hosts: block — every host the dev-story subtree may invoke,
// including the iface-backed handlers (rebound via host_bindings) and the bare
// hosts the chain calls directly. Kept in sync with .kitsoki/stories/kitsoki-dev/app.yaml.
func synthesizedRootHosts() []string {
	return []string{
		// Iface-backed handlers (dev-story defaults; rebindable via bindings).
		"host.local_files.ticket",
		"host.gh.ticket",
		"host.git",
		"host.local",
		"host.git_worktree",
		"host.append_to_file",
		// Bare hosts the chain calls directly.
		"host.inbox.add",
		"host.agent.ask",
		"host.agent.decide",
		"host.agent.task",
		"host.run",
		"host.agent.search",
		"host.artifacts_dir",
		"host.ide.open_file",
		"host.ide.open_diff",
		"host.agent.converse",
		"host.chat.resolve",
		"host.diff.open",
		// Ad-hoc structured plan verify gate (dev-story rooms/verifying.yaml):
		// a sandboxed, recordable Starlark script with read-only inspection.
		"host.starlark.run",
	}
}

// syntheticRootPath is the sentinel manifest path a synthesized root carries.
// It is rooted at the repo so canonicalPath / LoadedManifests produce a stable
// key, but no file is read from it (runLoadPipeline never re-reads the root
// manifest — parseAndMerge already produced the in-memory def).
func syntheticRootPath(repoRoot string) string {
	return filepath.Join(repoRoot, "<synthesized-root>", "app.yaml")
}

// ifaceList renders DevStoryIfaces as a sorted comma-list for error messages.
func ifaceList() string {
	names := make([]string, 0, len(DevStoryIfaces))
	for k := range DevStoryIfaces {
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
	src, err := resolveImportSource("@kitsoki/"+RootStoryName, repoRoot, nil)
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
