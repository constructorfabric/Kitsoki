// Package kit validates and loads kit.yaml manifests: the kit/v1 pinned
// schema that names, versions, and describes a distributable bundle of
// stories (docs/proposals/kits.md, .context/kits-implementation-plan.md "S1").
//
// The pattern mirrors internal/projectprofile: a pinned JSON Schema embedded
// via go:embed, validated with santhosh-tekuri/jsonschema/v6, plus a typed Go
// projection (KitDef) for callers that want structured access rather than a
// raw map. internal/app.BuildKitImporter consumes KitDef the way
// SynthesizeRoot/BuildRootImporter consume the dev-story base story.
package kit

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	goyaml "github.com/goccy/go-yaml"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.json
var schemaBytes []byte

// SchemaJSON returns the embedded kit/v1 JSON Schema.
func SchemaJSON() []byte {
	out := make([]byte, len(schemaBytes))
	copy(out, schemaBytes)
	return out
}

// Result is the machine-readable schema-validation report, mirroring
// projectprofile.Result.
type Result struct {
	OK     bool     `json:"ok"`
	Schema []string `json:"schema,omitempty"`
}

// ManifestFileName is the conventional manifest filename inside a kit root.
const ManifestFileName = "kit.yaml"

// Dependency is one `extends:`/`composes:` entry.
type Dependency struct {
	Kit        string `yaml:"kit"`
	Constraint string `yaml:"constraint,omitempty"`
}

// Parameter is one entry in the `parameters:` specialization surface.
type Parameter struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required,omitempty"`
	Default     any    `yaml:"default,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Provides declares what a kit contributes.
type Provides struct {
	Stories    []string  `yaml:"stories"`
	Schemas    []string  `yaml:"schemas,omitempty"`
	Interfaces []string  `yaml:"interfaces,omitempty"`
	UI         []UIEntry `yaml:"ui,omitempty"`
	Onboarding string    `yaml:"onboarding,omitempty"`

	// StoryDirs overrides the default `stories/<name>` resolution for one or
	// more provides.stories entries, keyed by story name, value a kit-root-
	// relative directory (e.g. "." when the kit.yaml lives directly beside
	// the story's own app.yaml instead of the usual nested stories/<name>/
	// layout). Introduced by S6 (root generalization) for a "self-contained"
	// root kit whose kit.yaml sits at the story's existing on-disk location
	// (stories/dev-story/kit.yaml) rather than requiring a directory
	// restructure — the repo-split-worthy move is deferred to Phase B. A
	// story name absent from this map falls back to the original
	// convention (see StoryDir).
	StoryDirs map[string]string `yaml:"story_dirs,omitempty"`
}

// RootDecl declares that one of this kit's provided stories may serve as the
// "blessed root" a synthesized instance imports (D6, S6). It replaces the
// formerly hardcoded RootStoryName const + the duplicated five-interface
// allow-list (internal/app/synthesis.go, internal/webconfig/webconfig.go,
// internal/projectprofile) with manifest-driven data: "any installed kit
// with a declared root: block can be the blessed root," fed by these fields
// instead of a Go-side constant.
type RootDecl struct {
	// Story names which of this kit's provides.stories is enterable as root.
	// Must be one of Provides.Stories.
	Story string `yaml:"story"`
	// Entry is the root story's initial state when synthesized as the
	// implicit root (mirrors the old hardcoded "landing").
	Entry string `yaml:"entry"`
	// HostInterfaces is the allow-list of host_interfaces names an
	// `overrides.bindings.<iface>` may rebind — replaces the hardcoded
	// DevStoryIfaces map.
	HostInterfaces []string `yaml:"host_interfaces"`
	// Hosts is the fixed host allow-list the synthesized root declares
	// (`hosts: declared`) so the root story's subtree has the same
	// fail-fast host surface a hand-written instance does — replaces the
	// hardcoded synthesizedRootHosts list.
	Hosts []string `yaml:"hosts,omitempty"`
}

// UIEntry is one `provides.ui` module entry point (S3c's minimal vertical
// slice shipped this as a bare entry-path string; S5 widens it to D3's full
// `provides.ui: [{id, title, entry, nav}]` shape while keeping the bare-
// string form valid — every S1/S3c fixture and any kit.yaml already
// authored against the narrower shape keeps parsing unchanged).
type UIEntry struct {
	// ID identifies this UI module within the kit (used to build the
	// "@<namespace>/<kit>/ui/<id>" import-map module id). Defaults to Entry
	// when authored as a bare string.
	ID string `yaml:"-"`
	// Title is a human-readable label for a nav link. Empty for a bare
	// string entry (no nav metadata was available in that form).
	Title string `yaml:"-"`
	// Entry is the module's file basename (without extension) under the
	// kit's ui/ directory, e.g. "graph" for ui/graph.mjs.
	Entry string `yaml:"-"`
	// Nav, when true, tells the SPA's runtime loader to add a nav link for
	// this page (D3's "provides.ui: [{..., nav}]"). Always false for a bare
	// string entry.
	Nav bool `yaml:"-"`
}

// UnmarshalYAML accepts either a bare string (id=entry=that string, no
// title, nav=false — the S3c-era shape) or a `{id, title, entry, nav}`
// mapping (D3's full shape).
func (u *UIEntry) UnmarshalYAML(data []byte) error {
	var s string
	if err := goyaml.Unmarshal(data, &s); err == nil {
		*u = UIEntry{ID: s, Entry: s}
		return nil
	}
	var raw struct {
		ID    string `yaml:"id"`
		Title string `yaml:"title"`
		Entry string `yaml:"entry"`
		Nav   bool   `yaml:"nav"`
	}
	if err := goyaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("provides.ui entry: must be a string or {id, title, entry, nav}: %w", err)
	}
	if raw.Entry == "" {
		return fmt.Errorf("provides.ui entry: {entry: ...} is required in object form")
	}
	if raw.ID == "" {
		raw.ID = raw.Entry
	}
	*u = UIEntry{ID: raw.ID, Title: raw.Title, Entry: raw.Entry, Nav: raw.Nav}
	return nil
}

// Conformance declares the kit's no-LLM contract suite.
type Conformance struct {
	Flows []string `yaml:"flows,omitempty"`
}

// Requires declares engine version constraints.
type Requires struct {
	Kitsoki string `yaml:"kitsoki,omitempty"`
}

// Compat declares mechanical migration hints for downstream absorbers.
type Compat struct {
	// Renamed is keyed by category (exits, intents, world, ...) then
	// old-name -> new-name.
	Renamed map[string]map[string]string `yaml:"renamed,omitempty"`
}

// Def is the typed projection of a kit.yaml document.
type Def struct {
	Schema    string `yaml:"schema"`
	Kit       string `yaml:"kit"`
	Namespace string `yaml:"namespace"`
	Version   string `yaml:"version"`
	Title     string `yaml:"title,omitempty"`
	Summary   string `yaml:"summary,omitempty"`

	Requires Requires `yaml:"requires,omitempty"`

	Extends  []Dependency `yaml:"extends,omitempty"`
	Composes []Dependency `yaml:"composes,omitempty"`

	Parameters map[string]Parameter `yaml:"parameters,omitempty"`

	Provides    Provides    `yaml:"provides"`
	Root        *RootDecl   `yaml:"root,omitempty"`
	Conformance Conformance `yaml:"conformance,omitempty"`
	Compat      Compat      `yaml:"compat,omitempty"`

	// dir is the absolute kit root directory this manifest was loaded from
	// (not part of the YAML surface). Empty when built in-memory.
	dir string
}

// Identity returns the fully-qualified "@namespace/kit" name.
func (d *Def) Identity() string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("@%s/%s", d.Namespace, d.Kit)
}

// Dir returns the kit root directory this manifest was loaded from.
func (d *Def) Dir() string {
	if d == nil {
		return ""
	}
	return d.dir
}

// HasStory reports whether name is among provides.stories.
func (d *Def) HasStory(name string) bool {
	if d == nil {
		return false
	}
	for _, s := range d.Provides.Stories {
		if s == name {
			return true
		}
	}
	return false
}

// StoryDir returns the absolute directory a provided story resolves to:
// <kit-root>/stories/<name> by default, or <kit-root>/<Provides.StoryDirs[name]>
// when that story has a StoryDirs override (see Provides.StoryDirs). Callers
// should check HasStory first; StoryDir does not itself validate that name
// is declared.
func (d *Def) StoryDir(name string) string {
	if d == nil {
		return ""
	}
	if rel, ok := d.Provides.StoryDirs[name]; ok {
		return filepath.Join(d.dir, rel)
	}
	return filepath.Join(d.dir, "stories", name)
}

// HasRoot reports whether this manifest declares a root: block naming story
// as its blessed-root story (D6). Used by internal/app.ResolveRootKit to
// validate a resolved manifest actually opts in as a root kit.
func (d *Def) HasRoot(story string) bool {
	if d == nil || d.Root == nil {
		return false
	}
	return d.Root.Story == story
}

// RootHostInterfaces returns Def.Root.HostInterfaces as a set, or nil when
// this manifest declares no root: block.
func (d *Def) RootHostInterfaces() map[string]struct{} {
	if d == nil || d.Root == nil {
		return nil
	}
	out := make(map[string]struct{}, len(d.Root.HostInterfaces))
	for _, iface := range d.Root.HostInterfaces {
		out[iface] = struct{}{}
	}
	return out
}

// ValidateFile validates a kit.yaml file on disk against the kit/v1 schema
// (schema-only; no semantic/filesystem cross-checks — those live in Load,
// which also checks provided story dirs exist).
func ValidateFile(path string) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read kit manifest: %w", err)
	}
	return Validate(raw)
}

// Validate validates kit/v1 YAML bytes against the schema.
func Validate(raw []byte) (Result, error) {
	doc, err := decodeYAML(raw)
	if err != nil {
		return Result{}, err
	}
	res := Result{Schema: validateSchema(doc)}
	res.OK = len(res.Schema) == 0
	return res, nil
}

// Load reads, schema-validates, and decodes a kit.yaml at manifestPath into a
// Def. manifestPath's parent directory becomes Def.Dir(). Load additionally
// checks that every provides.stories entry resolves to an existing
// stories/<name>/app.yaml on disk (a "does this kit even contain what it
// claims" fail-fast, distinct from the pure-schema Validate/ValidateFile
// entry points).
func Load(manifestPath string) (*Def, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read kit manifest: %w", err)
	}
	res, err := Validate(raw)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, fmt.Errorf("kit manifest %s failed kit/v1 schema validation: %v", manifestPath, res.Schema)
	}

	var def Def
	if err := goyaml.Unmarshal(raw, &def); err != nil {
		return nil, fmt.Errorf("decode kit manifest %s: %w", manifestPath, err)
	}

	abs, err := filepath.Abs(filepath.Dir(manifestPath))
	if err != nil {
		abs = filepath.Dir(manifestPath)
	}
	def.dir = abs

	var missing []string
	for _, story := range def.Provides.Stories {
		storyManifest := filepath.Join(def.StoryDir(story), "app.yaml")
		if _, statErr := os.Stat(storyManifest); statErr != nil {
			missing = append(missing, story)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("kit manifest %s: provides.stories names story(ies) with no stories/<name>/app.yaml on disk: %v", manifestPath, missing)
	}

	return &def, nil
}

// LoadDir is Load(filepath.Join(kitDir, ManifestFileName)).
func LoadDir(kitDir string) (*Def, error) {
	return Load(filepath.Join(kitDir, ManifestFileName))
}

func decodeYAML(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := goyaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse kit manifest yaml: %w", err)
	}
	normalized := normalize(doc)
	root, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("kit manifest must be an object")
	}
	return root, nil
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalize(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprint(k)] = normalize(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalize(v)
		}
		return out
	default:
		return v
	}
}

func validateSchema(doc map[string]any) []string {
	var schemaVal any
	if err := json.Unmarshal(schemaBytes, &schemaVal); err != nil {
		return []string{"embedded kit/v1 schema is invalid JSON: " + err.Error()}
	}
	compiler := jsonschema.NewCompiler()
	const uri = "kitsoki://kit/v1"
	if err := compiler.AddResource(uri, schemaVal); err != nil {
		return []string{"embedded kit/v1 schema is invalid: " + err.Error()}
	}
	schema, err := compiler.Compile(uri)
	if err != nil {
		return []string{"embedded kit/v1 schema does not compile: " + err.Error()}
	}
	if err := schema.Validate(doc); err != nil {
		return formatValidationErrors(err)
	}
	return nil
}

func formatValidationErrors(err error) []string {
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, e.Error())
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(verr)
	sort.Strings(out)
	return out
}
