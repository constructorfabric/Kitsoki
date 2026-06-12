// Package app holds the loaded, validated application definition.
// Types here map directly onto the YAML authoring format (see
// docs/stories/authoring.md) and carry
// yaml struct tags for deserialization via goccy/go-yaml.
package app

import (
	"fmt"
	"strings"
	"time"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/bashprofile"
)

// BashProfileDecl is the YAML representation of a bash_profile: field on an
// agent declaration. Three forms are supported:
//
//	bash_profile: read-only               # built-in read-only allowlist
//	bash_profile:
//	  commands: [git, jq, grep]           # explicit argv0 allowlist
//	bash_profile:
//	  sandboxed_write: /optional/dir      # write to scratch dir; network denied
//
// The string "read-only" parses to Kind==bashprofile.ReadOnly. A map with
// "commands" parses to Kind==bashprofile.Commands. A map with
// "sandboxed_write" parses to Kind==bashprofile.SandboxWrite.
type BashProfileDecl struct {
	Kind       bashprofile.Kind // resolved form
	Commands   []string         // set when Kind == bashprofile.Commands
	ScratchDir string           // set when Kind == bashprofile.SandboxWrite
}

// BashProfileKind is an alias for bashprofile.Kind kept for source compatibility.
// New callers should prefer bashprofile.Kind directly.
type BashProfileKind = bashprofile.Kind

// BashProfileReadOnly, BashProfileCommands, and BashProfileSandboxWrite are
// aliases for the canonical constants in package bashprofile.
const (
	BashProfileReadOnly     = bashprofile.ReadOnly
	BashProfileCommands     = bashprofile.Commands
	BashProfileSandboxWrite = bashprofile.SandboxWrite
)

// UnmarshalYAML implements goccy/go-yaml's BytesUnmarshaler. Accepts the three
// author forms described on BashProfileDecl.
func (bp *BashProfileDecl) UnmarshalYAML(b []byte) error {
	s := strings.TrimSpace(string(b))

	// Strip surrounding quotes if present (goccy/go-yaml hands raw scalar bytes).
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		s = s[1 : len(s)-1]
	}

	// String scalar forms.
	if s == "read-only" {
		bp.Kind = BashProfileReadOnly
		return nil
	}

	// Map form — try to decode as a raw map.
	var raw map[string]any
	if err := goyaml.Unmarshal(b, &raw); err == nil {
		if cmds, ok := raw["commands"]; ok {
			bp.Kind = BashProfileCommands
			switch v := cmds.(type) {
			case []any:
				for _, item := range v {
					if str, ok2 := item.(string); ok2 {
						bp.Commands = append(bp.Commands, str)
					}
				}
			case []string:
				bp.Commands = append(bp.Commands, v...)
			}
			return nil
		}
		if dir, ok := raw["sandboxed_write"]; ok {
			bp.Kind = BashProfileSandboxWrite
			if dirStr, ok2 := dir.(string); ok2 {
				bp.ScratchDir = dirStr
			}
			return nil
		}
	}

	return fmt.Errorf("app.BashProfileDecl: unrecognised form %q; expected \"read-only\", {commands:[...]}, or {sandboxed_write:<dir>}", s)
}

// StatePath is a slash-separated path identifying a state in the graph,
// e.g. "bar/dark" for a nested compound state.
type StatePath string

// TopLevel returns the first segment of this path — i.e. the room
// identifier the single-pane-tui proposal calls a "room". Path
// separators are dots in the internal representation (see
// loader.go::joinPath); a path like "bar.dark" reports "bar". An
// empty path reports "". Used by the TUI to detect navigation
// between rooms (changes to the top-level segment) versus moves
// within a room (changes that leave the top-level segment intact).
func (p StatePath) TopLevel() StatePath {
	s := string(p)
	if i := indexByte(s, '.'); i >= 0 {
		return StatePath(s[:i])
	}
	return p
}

// indexByte is a tiny local helper to avoid importing "strings" in
// this types-only file.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// SessionID uniquely identifies a runtime session.
type SessionID string

// TurnNumber is the monotonic turn counter within a session.
type TurnNumber int64

// ---- App-definition types (loaded from YAML) --------------------------------

// OraclePluginDecl declares one oracle plugin entry under the top-level
// `oracle_plugins:` YAML key (a map of oracle alias → declaration). This is
// distinct from the `hosts:` field, which is the flat string host allow-list.
//
// Supported plugin values (B-3):
//   - "builtin.claude_cli" — the default; wraps the existing claude-CLI harness.
//   - "builtin.inprocess"  — opt-in in-process oracle (used by tests / stubs).
//   - "subprocess"         — JSON-RPC 2.0 over stdio; requires command:.
//   - "mcp_http"           — MCP-over-HTTP; requires endpoint:.
//   - "builtin.local_llm"  — local llama.cpp sidecar (OpenAI HTTP); requires
//     either model: (managed sidecar) or endpoint: (bring-your-own-server).
type OraclePluginDecl struct {
	// Plugin is the transport identifier (e.g. "builtin.claude_cli", "mcp_http").
	Plugin string `yaml:"plugin"`
	// Command is the subprocess binary path. Required for subprocess transport.
	Command string `yaml:"command,omitempty"`
	// Args is the subprocess argument list. Optional for subprocess transport.
	Args []string `yaml:"args,omitempty"`
	// Endpoint is used by mcp_http plugins; ignored by builtin transports.
	Endpoint string `yaml:"endpoint,omitempty"`
	// Tool is the MCP tool name on mcp_http plugins. Defaults to "ask".
	Tool string `yaml:"tool,omitempty"`
	// Env is a map of environment variable names → values (supports ${VAR}
	// interpolation at plugin-init time). Used by subprocess transport.
	Env map[string]string `yaml:"env,omitempty"`
	// Headers is a map of HTTP header names → values (supports ${VAR}
	// interpolation at plugin-init time). Used by mcp_http transport.
	Headers map[string]string `yaml:"headers,omitempty"`
	// Model is the GGUF model id the local-model sidecar should serve. Used by
	// the builtin.local_llm transport in managed mode (when endpoint: is empty).
	Model string `yaml:"model,omitempty"`
	// Grammar enables best-effort grammar/json_schema constraint on the local
	// model's first decode (builtin.local_llm only). Schemas outside the
	// translatable subset are silently left unconstrained; ValidateSubmission
	// remains the sole authority on shape.
	Grammar bool `yaml:"grammar,omitempty"`
	// Port is the TCP port the managed local-model sidecar binds on 127.0.0.1.
	// Used by builtin.local_llm in managed mode; ignored in endpoint mode.
	Port int `yaml:"port,omitempty"`
	// ServerBin overrides the llama-server binary path for the managed
	// local-model sidecar (builtin.local_llm only). Empty fetches/uses the
	// cached default.
	ServerBin string `yaml:"server_bin,omitempty"`
}

// ProviderDecl declares one named LLM backend profile (see AppDef.Providers).
//
// A provider is a thin, transport-agnostic override applied to the `claude`
// subprocess: Env entries are merged onto the process environment for the
// invocation (overriding any ambient value of the same key), and Model, when
// set, supplies the --model default for an invocation whose agent declares no
// explicit model. Both fields are optional — a provider with only Env keeps
// each call's own model; a provider with only Model just retargets the model
// against the ambient backend.
type ProviderDecl struct {
	// Model is the --model value used for invocations that select this provider
	// and whose agent (and effect) declare no explicit model. Optional.
	Model string `yaml:"model,omitempty"`
	// Effort is the --effort value (low|medium|high|xhigh|max) used for
	// invocations that select this provider and whose agent (and effect) declare
	// no explicit effort. Optional.
	Effort string `yaml:"effort,omitempty"`
	// Env maps environment-variable names → values merged onto the claude
	// subprocess environment (supports ${VAR} interpolation at load time).
	// Typical keys: ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN,
	// NODE_EXTRA_CA_CERTS. An entry overrides any ambient value of the same key.
	Env map[string]string `yaml:"env,omitempty"`
}

// AppDef is the top-level deserialized application definition.
type AppDef struct {
	App     AppMeta           `yaml:"app"`
	World   map[string]VarDef `yaml:"world,omitempty"`
	Intents map[string]Intent `yaml:"intents,omitempty"`
	Root    any               `yaml:"root"` // string state name or inline compound/parallel root
	States  map[string]*State `yaml:"states,omitempty"`
	OffPath *OffPathDef       `yaml:"off_path,omitempty"`
	// Hosts is the allow-list of host handler names this app may invoke.
	Hosts []string `yaml:"hosts,omitempty"`
	// OraclePlugins declares oracle plugin configurations under the top-level
	// `oracle_plugins:` YAML key (B-2 additive syntax). Keys are oracle alias
	// names (e.g. "oracle.claude", "oracle.autofix_fixer"); values are plugin
	// declarations. When absent or nil, the loader injects a default
	// "oracle.claude" entry with plugin "builtin.claude_cli".
	//
	// Note: an earlier design named this block `hosts:` but that key conflicts
	// with the existing AppDef.Hosts []string allow-list field. The YAML key
	// `oracle_plugins:` is the stable authoring surface.
	OraclePlugins map[string]*OraclePluginDecl `yaml:"oracle_plugins,omitempty"`
	// Providers declares named LLM backend profiles under the top-level
	// `providers:` YAML key. A provider bundles a default model and a set of
	// environment-variable overrides applied to the `claude` subprocess for any
	// oracle invocation that selects it (via an agent's `provider:` field or an
	// effect's `with: { provider: <name> }` arg). This lets some oracle calls run
	// against the ambient Anthropic auth while others point claude at an
	// alternate backend (e.g. an internal LiteLLM proxy) by overriding
	// ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN / NODE_EXTRA_CA_CERTS. Selecting
	// no provider preserves today's behavior (ambient environment). Env values
	// support ${VAR} interpolation, resolved at load time.
	Providers map[string]*ProviderDecl `yaml:"providers,omitempty"`
	// Proposals declares named proposal kinds.
	Proposals map[string]*ProposalKind `yaml:"proposals,omitempty"`
	// Include lists glob patterns for additional YAML files to merge.
	Include []string `yaml:"include,omitempty"`

	// PhaseTemplates declares reusable phase shapes (see
	// docs/stories/state-machine.md "Phase templates").
	// Authors instantiate templates by listing phases under `phases:`.
	PhaseTemplates map[string]*PhaseTemplate `yaml:"phase_templates,omitempty"`

	// Agents declares named Claude agents — first-class personas / system
	// prompts (and optionally model overrides) reusable across any
	// host.oracle.{ask,talk,ask_with_mcp} call via the `agent: <name>` arg
	// in the effect's `with:` block. Generalises OffPathDef.Persona into a
	// per-call primitive so different rooms / intents can speak in
	// different voices through the same engine path.
	// Phases declares the phase graph that instantiates a phase template
	// (see docs/stories/state-machine.md "Phase templates"). Expanded into
	// States at load time.
	Phases *PhasesBlock `yaml:"phases,omitempty"`
	// CheckpointIntents is a per-room intent menu merged into every
	// {id}_awaiting_reply state during phase-template expansion.
	CheckpointIntents map[string]Intent `yaml:"checkpoint_intents,omitempty"`
	// MetaModes declares named off-path concerns (see docs/stories/meta-mode.md).
	MetaModes map[string]*MetaModeDef `yaml:"meta_modes,omitempty"`

	// Decider configures the engine-driven LLM decider used to resolve
	// one-shot (or decider:llm) decision gates without per-room judge
	// wiring. Optional; nil disables it.
	Decider *DeciderSpec `yaml:"decider,omitempty"`
	// Agents declares named per-context agents (see docs/stories/meta-mode.md).
	// Generalises OffPathDef.Persona / OffPathDef.Agent into a top-level
	// primitive any host.oracle.* call site can reference by name. Bound
	// at startup via agents.BuildRegistry(def.AgentSpecs()) + host.SetAgentRegistry.
	Agents map[string]*AgentDecl `yaml:"agents,omitempty"`

	// Imports declares aliased composition with private worlds
	// (see docs/stories/imports.md). Each import binds a child app
	// under a string alias; child states/world keys are namespaced
	// under that alias at load time.
	Imports map[string]*ImportDef `yaml:"imports,omitempty"`

	// Exits declares the child-side exit contract — named return
	// points the parent maps to its own states via imports.<alias>.exits.
	// Standalone apps may declare exits for documentation but they have
	// no runtime effect outside an import context.
	Exits map[string]*ExitDef `yaml:"exits,omitempty"`

	// Exports declares what the child app surfaces to importers.
	// Currently only intents (see docs/stories/imports.md).
	Exports *ExportsBlock `yaml:"exports,omitempty"`

	// HostInterfaces declares named capabilities the app depends on
	// (see docs/stories/imports.md). Importers rebind via
	// imports.<alias>.host_bindings.
	HostInterfaces map[string]*HostInterfaceDef `yaml:"host_interfaces,omitempty"`

	// ImportWrappers records one entry per immediate import the loader
	// folded into this AppDef. Populated by resolveImports; never set
	// by YAML authors. Used by:
	//   - validateDef to reject parent transitions targeting a deep
	//     state inside an imported child (see docs/stories/imports.md);
	//   - the metamode controller's file-watch tree to include every
	//     imported manifest directory;
	//   - the trace logger / future tooling to label states by the
	//     import alias chain they live under.
	ImportWrappers map[string]*ImportWrapperInfo `yaml:"-"`

	// LoadedManifests is the set of absolute paths the loader read
	// during this AppDef's recursive load: the root manifest plus
	// every imported child's manifest at every depth. Used by the
	// metamode controller's auto-watch so edits to a sibling
	// imported story trigger reload.
	LoadedManifests []string `yaml:"-"`

	// Prompts declares the prompt search roots for prompt extension
	// (see docs/stories/prompts.md): the optional shared-fragment dirs a
	// prompt addresses via `@shared/…`, and an optional project overlay dir
	// whose files {% extends %} the story's base prompts. A nil Prompts (or an
	// empty block) means "story-only" — prompts resolve relative to BaseDir
	// exactly as before, and a story with no overlay and no blocks renders
	// byte-identically to the pre-extension path.
	Prompts *PromptsConfig `yaml:"prompts,omitempty"`

	// Routing holds the per-app semantic-routing configuration
	// (see docs/architecture/semantic-routing.md). Lives at the root app level and
	// is NOT merged across imports — when an importer folds a child,
	// the importer's Routing block wins and the child's is dropped.
	// A nil Routing means "use defaults" (see RoutingConfig.WithDefaults).
	Routing *RoutingConfig `yaml:"routing,omitempty"`

	// BaseDir is the absolute path to the directory containing the
	// root manifest. Populated by the loader (not by YAML authors).
	// Downstream consumers — notably internal/render.AppRenderer —
	// resolve <appDir>/views/ against this path so {% extends %} /
	// {% include %} references in pongo2 templates locate per-app
	// .pongo files (see docs/stories/story-style.md).
	BaseDir string `yaml:"-"`
}

// PromptsConfig declares a story's prompt search roots for prompt extension.
// All paths are relative to the app's BaseDir unless absolute. See
// docs/stories/prompts.md.
type PromptsConfig struct {
	// Shared lists directories holding reusable prompt fragments addressed
	// via `@shared/<path>` from a story's prompts. Defaults to none.
	Shared []string `yaml:"shared,omitempty"`

	// Overlay is a project overlay directory whose prompt files shadow the
	// story's (resolved overlay-first) and may `{% extends "@story/…" %}` the
	// base they shadow. Usually supplied at run time (kitsoki run
	// --prompt-overlay) rather than committed into the shared story, but a
	// story may declare a default here. Empty means no overlay.
	Overlay string `yaml:"overlay,omitempty"`
}

// RoutingConfig is the per-app semantic-routing block declared under
// `app.routing:` in YAML (see docs/architecture/semantic-routing.md). All fields are
// optional; zero values are replaced by WithDefaults so a partially-
// specified routing: block still loads with sane defaults. The loader
// calls WithDefaults once after unmarshalling.
type RoutingConfig struct {
	// Enabled toggles the semantic-routing tier on this app. Defaults
	// to true; set false to keep today's deterministic-or-LLM behaviour.
	Enabled bool `yaml:"enabled,omitempty"`
	// SemanticHighBar is the confidence floor above which a semantic
	// verdict is submitted directly. Defaults to 0.80.
	SemanticHighBar float64 `yaml:"semantic_high_bar,omitempty"`
	// SemanticMidBar is the confidence floor above which a verdict
	// triggers a clarification card. Defaults to 0.65. Must be
	// strictly less than SemanticHighBar.
	SemanticMidBar float64 `yaml:"semantic_mid_bar,omitempty"`
	// CacheEnabled toggles the turn-result cache. Defaults to true.
	CacheEnabled bool `yaml:"cache_enabled,omitempty"`
	// CacheMaxAge is the duration after which a cold cache row is
	// evicted. Defaults to 30 days. Set "0" to disable.
	CacheMaxAge Duration `yaml:"cache_max_age,omitempty"`
	// StopwordsExtra extends the built-in stopword list with
	// app-specific filler ("yall", "wagon", …).
	StopwordsExtra []string `yaml:"stopwords_extra,omitempty"`
	// CacheCap is the row-count ceiling per app before LRU trim fires
	// Defaults to 10000.
	CacheCap int `yaml:"cache_cap,omitempty"`
	// CacheTrimFraction is the fraction of the cap evicted on overflow
	// Defaults to 0.10.
	CacheTrimFraction float64 `yaml:"cache_trim_fraction,omitempty"`
	// RevalidateStrikes is the number of consecutive revalidate
	// failures before a cache row is evicted. Defaults to 3.
	RevalidateStrikes int `yaml:"revalidate_strikes,omitempty"`
	// ConfidenceDecay halves the effective CacheMaxAge for rows whose
	// originating LLM verdict had confidence < 0.7. Default off.
	ConfidenceDecay bool `yaml:"confidence_decay,omitempty"`
	// ExtractLLMOnNoMatch, when true, lets the deterministic semantic
	// router (TrySemantic) invoke the host.oracle.extract LLM tier on a
	// no_match before falling through to the main-turn LLM. The point is
	// to back that LLM tier with a cheap local model (oracle: oracle.local)
	// so routing stays offline and schema-bounded. Default off — the
	// deterministic tiers always run first, and this is strictly opt-in
	// because the extract LLM call adds a model round-trip on every
	// otherwise-unrouted turn. See docs/architecture/semantic-routing.md
	// and docs/architecture/oracle-plugin.md "Local model backend".
	ExtractLLMOnNoMatch bool `yaml:"extract_llm_on_no_match,omitempty"`
	// ExtractLLMOracle is the oracle_plugins alias the no_match LLM routing
	// tier dispatches to (see ExtractLLMOnNoMatch). Empty defaults to
	// "oracle.local" — the convention for the local-model backend.
	ExtractLLMOracle string `yaml:"extract_llm_oracle,omitempty"`
	// Embedding configures the shared embedding sidecar used by both
	// host.oracle.search and the embedding routing tier. When nil or when
	// both Endpoint and Model are empty, host.oracle.search remains the
	// no-op sentinel and the embedding routing tier is disabled.
	Embedding *EmbedConfig `yaml:"embedding,omitempty"`
}

// EmbedConfig is the app.routing.embedding config block. It is shared by the
// host.oracle.search handler and the embedding routing tier.
type EmbedConfig struct {
	// Endpoint is the base URL of a running llama-server started with
	// --embeddings --pooling mean (e.g. "http://localhost:8082"). When set
	// the sidecar attaches without fetching or spawning. Required for now;
	// managed mode requires a verified model pin in fetch.go.
	Endpoint string `yaml:"endpoint,omitempty"`
	// Model is the embedding model id (default "nomic-embed-text-v1.5").
	// Used as the model field in /v1/embeddings requests and in the Store
	// cache key.
	Model string `yaml:"model,omitempty"`
	// CacheDir is the directory for the gob corpus cache produced by
	// host.oracle.search. Defaults to ".kitsoki-embed-cache" relative to
	// the working directory.
	CacheDir string `yaml:"cache_dir,omitempty"`
	// ConfidentBar is the top-1 cosine threshold for the routing tier
	// (default 0.82). Only relevant when the routing tier is active.
	ConfidentBar float64 `yaml:"confident_bar,omitempty"`
	// Margin is the top1-top2 delta required to avoid a tie verdict
	// (default 0.08). Only relevant when the routing tier is active.
	Margin float64 `yaml:"margin,omitempty"`
}

// DefaultRoutingConfig returns the all-defaults RoutingConfig used when
// an app declares no `routing:` block. Callers that find AppDef.Routing
// == nil should treat the app as if it carried this value.
func DefaultRoutingConfig() RoutingConfig {
	return RoutingConfig{
		Enabled:           true,
		SemanticHighBar:   0.80,
		SemanticMidBar:    0.65,
		CacheEnabled:      true,
		CacheMaxAge:       Duration(30 * 24 * time.Hour),
		CacheCap:          10000,
		CacheTrimFraction: 0.10,
		RevalidateStrikes: 3,
		ConfidenceDecay:   false,
	}
}

// WithDefaults returns a RoutingConfig where every zero-valued numeric /
// duration field is replaced by the corresponding default from
// DefaultRoutingConfig. Note: bool fields (Enabled, CacheEnabled,
// ConfidenceDecay) pass through unchanged — Go's zero value for bool is
// false, but two of the three default to true. The loader is responsible
// for unmarshalling routing: through a code path that seeds defaults
// before YAML decode, so an absent key keeps its default rather than
// being overwritten by the zero value. UnmarshalYAML on this type does
// exactly that (see below).
//
// String/slice fields (StopwordsExtra) pass through untouched: a nil
// slice is the natural "no extras."
func (r RoutingConfig) WithDefaults() RoutingConfig {
	d := DefaultRoutingConfig()
	out := r
	if out.SemanticHighBar == 0 {
		out.SemanticHighBar = d.SemanticHighBar
	}
	if out.SemanticMidBar == 0 {
		out.SemanticMidBar = d.SemanticMidBar
	}
	if out.CacheMaxAge == 0 {
		out.CacheMaxAge = d.CacheMaxAge
	}
	if out.CacheCap == 0 {
		out.CacheCap = d.CacheCap
	}
	if out.CacheTrimFraction == 0 {
		out.CacheTrimFraction = d.CacheTrimFraction
	}
	if out.RevalidateStrikes == 0 {
		out.RevalidateStrikes = d.RevalidateStrikes
	}
	return out
}

// UnmarshalYAML implements goccy/go-yaml's BytesUnmarshaler. It seeds
// the receiver with DefaultRoutingConfig before decoding the YAML body
// so author-omitted bool fields (`enabled:`, `cache_enabled:`) keep
// their default of true rather than landing on Go's zero value. After
// decode the function calls WithDefaults to backfill numeric/duration
// fields the author left out. The combined effect: a partial
// `routing:` block like
//
//	routing: { semantic_high_bar: 0.85 }
//
// loads as { Enabled:true, CacheEnabled:true, SemanticHighBar:0.85,
// SemanticMidBar:0.65, CacheMaxAge:30d, CacheCap:10000, … }.
func (r *RoutingConfig) UnmarshalYAML(b []byte) error {
	*r = DefaultRoutingConfig()
	// Decode into a temporary type alias so we don't recurse into this
	// UnmarshalYAML. The defaults seeded above survive any field the
	// YAML body omits.
	type raw RoutingConfig
	tmp := raw(*r)
	if err := unmarshalRoutingRaw(b, &tmp); err != nil {
		return err
	}
	*r = RoutingConfig(tmp).WithDefaults()
	return nil
}

// ImportWrapperInfo carries the post-fold metadata for one import
// alias. Populated by resolveImports.
type ImportWrapperInfo struct {
	// Alias is the prefix the importer chose (`bf`, `frontier`, …).
	// Mirrors the map key but kept here so callers iterating values
	// don't lose the key.
	Alias string
	// Entry is the child state the import was declared to start in
	// (the import's `entry:` field). Used by the reach-into-child guard to
	// allow `<alias>.<entry>` from the parent while rejecting deeper paths.
	Entry string
	// SourcePath is the absolute path to the child manifest.
	// Used by metamode auto-watch and for diagnostic messages.
	SourcePath string
}

// ImportDef declares one entry in an AppDef's `imports:` block
// (see docs/stories/imports.md).
type ImportDef struct {
	// Source resolves to a child app.yaml. Forms:
	//   - "./relative/path"          — relative to importer's dir
	//   - "/absolute/path"           — absolute path (test escape hatch)
	//   - "@kitsoki/<name>"          — under <repo-root>/stories/<name>
	Source string `yaml:"source"`
	// Version is optional metadata; v1 parses and stores it for
	// traceability only — no semver resolution or compatibility check
	// happens at load time. Reserved for a future registry / lockfile
	// surface.
	Version string `yaml:"version,omitempty"`
	// Entry is the child's initial state when this import is invoked.
	// Path is in the *child's* namespace (not alias-prefixed). Required
	// unless the child declares a Root the parent accepts as entry.
	Entry string `yaml:"entry,omitempty"`
	// Exits maps the child's declared exit names to parent-side targets
	// with optional projection effects (world_out per-exit).
	Exits map[string]*ImportExit `yaml:"exits,omitempty"`
	// WorldIn maps child world keys → parent expressions evaluated at
	// entry. The LHS names child world keys; the RHS is parent-scope
	// expr that resolves to the value pushed into the child's world.
	WorldIn map[string]string `yaml:"world_in,omitempty"`
	// Hosts controls how the child's host allow-list composes with the
	// parent's. "inherit" (default) unions silently; "declared" requires
	// the parent to list every child host explicitly.
	Hosts string `yaml:"hosts,omitempty"`
	// Intents declares parent↔child intent re-exports.
	Intents *ImportIntents `yaml:"intents,omitempty"`
	// Overrides patches child states/intents/prompts at import time.
	Overrides *ImportOverrides `yaml:"overrides,omitempty"`
	// HostBindings rebinds named child host_interfaces onto concrete
	// handler names.
	HostBindings map[string]string `yaml:"host_bindings,omitempty"`
}

// ImportExit declares how a child exit maps to a parent state.
type ImportExit struct {
	// To is the parent-side state to transition into when the child
	// exits via this name.
	To string `yaml:"to"`
	// Set is an optional projection — child-scope expressions evaluated
	// at exit and written to parent world keys.
	Set map[string]any `yaml:"set,omitempty"`
}

// ImportIntents declares per-import intent re-exports.
type ImportIntents struct {
	// Export lists parent intent names made visible inside the child
	// under the same name.
	Export []string `yaml:"export,omitempty"`
	// Import lists child intent names lifted into the parent (rare).
	// The child must list these in its own exports.intents.
	Import []string `yaml:"import,omitempty"`
}

// ImportOverrides patches a child app's states / intents / prompts.
type ImportOverrides struct {
	// States replaces named child states whole-cloth. Each key must
	// match an existing child state name; load fails otherwise.
	States map[string]*State `yaml:"states,omitempty"`
	// Intents replaces named child intent definitions (slots, examples).
	Intents map[string]Intent `yaml:"intents,omitempty"`
	// Prompts maps child-relative prompt paths → parent-relative paths.
	// At load time the parent's file replaces the child's bytes.
	Prompts map[string]string `yaml:"prompts,omitempty"`
}

// ExitDef declares one named exit the child app surfaces.
type ExitDef struct {
	Description string `yaml:"description,omitempty"`
	// Requires lists child world keys that must be set when this exit
	// fires. Best-effort static check; runtime guard backs it up.
	Requires []string `yaml:"requires,omitempty"`
}

// ExportsBlock declares what an app surfaces to importers.
type ExportsBlock struct {
	Intents []string `yaml:"intents,omitempty"`
}

// HostInterfaceDef declares one named capability surface.
type HostInterfaceDef struct {
	Description string                      `yaml:"description,omitempty"`
	Operations  map[string]*HostInterfaceOp `yaml:"operations,omitempty"`
	// Default is the handler name bound when no importer overrides.
	Default string `yaml:"default,omitempty"`
}

// HostInterfaceOp declares one operation in a host interface.
type HostInterfaceOp struct {
	Input  map[string]any `yaml:"input,omitempty"`
	Output map[string]any `yaml:"output,omitempty"`
}

// PhaseTemplate is a reusable phase shape. It declares a parameter schema and
// a set of states that get instantiated once per phase. State-key keys may
// contain `{paramname}` placeholders; state body strings may contain
// `{{ tpl.paramname }}` and `{{ phase.next.<arc> }}` expressions which the
// loader substitutes at expansion time.
type PhaseTemplate struct {
	Parameters map[string]PhaseTemplateParam `yaml:"parameters,omitempty"`
	States     map[string]*State             `yaml:"states,omitempty"`
}

// PhaseTemplateParam declares one parameter on a phase template.
type PhaseTemplateParam struct {
	Type     string `yaml:"type"`
	Required bool   `yaml:"required,omitempty"`
	Default  any    `yaml:"default,omitempty"`
}

// PhasesBlock is the top-level `phases:` declaration that picks a template
// and supplies a graph of phase instances. Graph is parsed as a raw map so
// authors can put arbitrary template-parameter keys alongside the
// structured `next:`, `cycle_budgets:`, and `checkpoint:` fields.
type PhasesBlock struct {
	Template string                    `yaml:"template"`
	Graph    map[string]map[string]any `yaml:"graph,omitempty"`
}

// AppMeta holds the app-level metadata block.
type AppMeta struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Title   string `yaml:"title,omitempty"`
	Author  string `yaml:"author,omitempty"`
	License string `yaml:"license,omitempty"`

	// Context / ContextPath author the project's Layer-2 system-prompt
	// grounding: the app's domain, purpose, and voice, shared by every oracle
	// call and the router. At most one may be set (loader-enforced). Context is
	// an inline template string; ContextPath points at a prompt file resolved
	// through the same overlay/@shared search path as any other prompt, so it
	// can {% include %} shared fragments. When neither is set, the optional
	// `prompts/_project.md` convention supplies Layer 2 if that file exists.
	// See docs/architecture/system-prompt.md (Layer 2).
	Context     string `yaml:"context,omitempty"`
	ContextPath string `yaml:"context_path,omitempty"`
}

// VarDef describes one world variable in the schema.
type VarDef struct {
	Type    string   `yaml:"type"`
	Default any      `yaml:"default,omitempty"`
	Values  []string `yaml:"values,omitempty"` // for enum types
}

// WorldSchema is the compiled schema of all world variables.
type WorldSchema map[string]VarDef

// State is a node in the directed graph. It may be atomic, compound, or parallel.
// DeciderSpec is the app-level configuration for the engine-driven LLM
// decider. The engine invokes `agent` via
// host.oracle.decide at any decision gate that owes an autonomous decision,
// passing the gate's candidate intents; the agent submits {intent, confidence,
// reason} validated against `schema`.
type DeciderSpec struct {
	// Agent is the judge agent name (must be declared in agents:). Required.
	Agent string `yaml:"agent"`
	// Schema is the path to the decision schema (intent/confidence/reason).
	// Required — host.oracle.decide rejects an empty schema.
	Schema string `yaml:"schema"`
	// Prompt is an optional decision-prompt template path; when empty the
	// engine synthesises a prompt from the gate's candidate intents.
	Prompt string `yaml:"prompt,omitempty"`
	// Threshold is the confidence floor for auto-firing (default 0.8).
	Threshold float64 `yaml:"threshold,omitempty"`
}

type State struct {
	// Type is "atomic" (default), "compound", or "parallel".
	Type string `yaml:"type,omitempty"`
	// Mode declares a special harness mode for this state.
	// "conversational" enables the Oracle Room free-form harness.
	Mode string `yaml:"mode,omitempty"`
	// Description is shown in the location indicator.
	Description string `yaml:"description,omitempty"`
	// View is the render template shown to the user on arrival.
	//
	// The View type custom-unmarshals YAML and accepts either the legacy
	// scalar string form (a Markdown / pongo2 template body) or the typed
	// element-array form (see docs/stories/story-style.md and
	// docs/embedded/app-schema.md). See view_element.go for
	// the schema; the array form is opt-in per-state.
	View View `yaml:"view,omitempty"`
	// Terminal marks end states.
	Terminal bool `yaml:"terminal,omitempty"`
	// Decider pins how this room's intent gate is resolved, overriding the
	// run's execution mode (the "mix"). "" = follow the
	// run mode; "human" = always stop at a multi-way gate for an operator,
	// even in one-shot; "llm" = always auto-advance (let the emit fire),
	// even in staged. Validated at load time.
	Decider string `yaml:"decider,omitempty"`
	// Initial is the initial child state for compound states; supports expr interpolation.
	Initial string `yaml:"initial,omitempty"`
	// States holds nested child states (compound/parallel).
	States map[string]*State `yaml:"states,omitempty"`
	// On maps intent names to ordered transition lists.
	On map[string][]Transition `yaml:"on,omitempty"`
	// OnEnter holds effects/invocations fired on state entry.
	OnEnter []Effect `yaml:"on_enter,omitempty"`
	// Intents holds locally-scoped intent definitions.
	Intents map[string]Intent `yaml:"intents,omitempty"`
	// Menu is an explicit list of allowed intent names overriding the default.
	Menu []string `yaml:"menu,omitempty"`
	// DefaultIntent names the free-text sink for this state: when an utterance
	// matches no intent deterministically or semantically, the engine routes it
	// straight to this intent with the whole input filling its single required
	// string slot — no main-turn LLM classification. This is how a
	// conversational/discovery room (e.g. one whose `discuss` arc converses)
	// guarantees plain prose reaches the conversation instead of being
	// mis-classified into a command intent. The named intent must be reachable
	// from this state (have an `on:` arc) and declare exactly one required
	// string slot. Authored bare; resolved through IntentAliases at runtime so
	// it survives import-folding.
	DefaultIntent string `yaml:"default_intent,omitempty"`
	// RelevantWorld pins world keys shown in the location indicator.
	RelevantWorld []string `yaml:"relevant_world,omitempty"`
	// Footer is an optional pongo2 template body rendered as the
	// per-room status line beneath the prompt. Same expression env as Views
	// (world, slots, menu). Empty falls back to the framework
	// default — room · state · mode · queue · unread. Only honoured
	// on top-level states (one room = one footer).
	Footer string `yaml:"footer,omitempty"`
	// RelevantSlots pins slot names shown in the location indicator.
	RelevantSlots []string `yaml:"relevant_slots,omitempty"`
	// Timeout declares an automatic transition after a duration.
	Timeout *TimeoutDef `yaml:"timeout,omitempty"`

	// IntentAliases records the bare → renamed mapping produced by the
	// imports rewriter. When this state lives inside one or more import
	// alias wrappers, every intent that the rewriter renamed (e.g.
	// `accept` → `bf__accept`, then `bf__accept` → `core__bf__accept`
	// on a second fold) gains an entry here. Both the original bare
	// name and any intermediate prefixed names point to the final,
	// fully-prefixed key actually present in `On`. Used at runtime by
	// the emit_intent dispatcher to resolve a bare intent name emitted
	// by an LLM-judge inside the imported child against the renamed
	// arc on the current state. Never set by YAML authors; populated
	// by `internal/app/imports_rewriter.go::rewriteState` during fold.
	IntentAliases map[string]string `yaml:"-"`

	// Transcript declares how this room's transcript buffer is treated
	// on entry. Only meaningful on top-level (room) states; nested
	// states must leave it empty. Allowed values:
	//   "persistent" — keep prior content visible on re-entry (default
	//                  for on-path rooms).
	//   "transient"  — scroll past prior content on entry so the new
	//                  session lands at the top of the visible window
	//                  (default for conversational / meta-mode rooms).
	// Empty (the default) is resolved per-state in the TUI: rooms
	// whose Mode == "conversational" default to "transient"; everything
	// else defaults to "persistent". See the single-pane-tui proposal
	// §"Per-room transcript buffers".
	Transcript string `yaml:"transcript,omitempty"`

	// Theme names a blocks.Renderer theme applied on entry into this
	// room. Only meaningful on top-level (room) states; nested states
	// must leave it empty. Valid names match the themes shipped under
	// internal/tui/blocks/themes.go: "default", "meta-blue",
	// "meta-amber", "off-path". Unknown names fall back to "default"
	// at render time.
	Theme string `yaml:"theme,omitempty"`
}

// Transition is one entry in a state's on[intent] list.
type Transition struct {
	// Target is the destination state path. "." means self.
	Target string `yaml:"target"`
	// When is the guard expression (expr-lang). Empty = always true.
	When string `yaml:"when,omitempty"`
	// Default marks this as the catch-all branch when no prior guard matched.
	Default bool `yaml:"default,omitempty"`
	// Effects is the ordered list of world mutations applied when this transition fires.
	Effects []Effect `yaml:"effects,omitempty"`
	// GuardHint is a human-readable hint shown when the guard fails.
	GuardHint string `yaml:"guard_hint,omitempty"`
	// View is the transition-scoped narrative shown on this transition.
	// When present it wins over the target state's view for this turn. Same
	// schema as State.View — see view_element.go.
	View View `yaml:"view,omitempty"`
	// Emit lists events emitted to parallel regions after this transition.
	Emit []string `yaml:"emit,omitempty"`
	// PushHistory controls whether the outgoing state is pushed onto the history stack.
	// Default true (push on every normal transition). Set false for utility transitions
	// like entering the Oracle Room or Inbox (stackless transitions).
	PushHistory *bool `yaml:"push_history,omitempty"`
}

// Effect is one atomic world mutation or side-effect invocation.
type Effect struct {
	// When is an optional guard expression (expr-lang). When non-empty
	// and the expression evaluates false against the current world, the
	// effect is skipped silently (no set/invoke/say/etc. runs). Empty =
	// always-on. Used to branch on_enter / transition effects on world
	// flags (e.g. `when: world.narration` vs `when: not world.narration`).
	// Eval errors are non-fatal: a bad
	// expression aborts the surrounding effects chain with an error so
	// authors get a loud failure rather than a silently-skipped effect.
	When string `yaml:"when,omitempty"`
	// Set maps world-variable names to new values (expr or literal).
	Set map[string]any `yaml:"set,omitempty"`
	// Increment maps world-variable names to integer delta values.
	Increment map[string]int `yaml:"increment,omitempty"`
	// Say appends a narrative message (expr interpolation supported).
	Say string `yaml:"say,omitempty"`
	// Invoke calls a host-namespace function.
	Invoke string `yaml:"invoke,omitempty"`
	// Id is an optional, author-assigned address for this invoke's call site.
	// It is threaded into the dispatched args under the reserved `call` key so
	// a single fixture can stub or match two calls that share a handler name —
	// e.g. two `host.oracle.decide` call sites in one room. Flow stubs select on
	// it via `by_call:`; cassettes via `match: { call: <id> }`. Distinct from the
	// deterministic 16-hex `call_id` correlation hash (see host.DeriveCallID):
	// this is a stable human label the author controls, independent of verb,
	// agent, and schema. Empty = no address (the call falls back to handler-name
	// keying). Only meaningful on Invoke effects.
	Id string `yaml:"id,omitempty"`
	// With holds arguments for an Invoke call.
	With map[string]any `yaml:"with,omitempty"`
	// Bind extracts keys from the host result into world variables: bind: {world_key: result_key}.
	Bind map[string]string `yaml:"bind,omitempty"`
	// OnError is a state transition target fired when a host invoke returns an error.
	// Before the redirect, the engine sets two reserved global world vars the
	// target room may read (and list in relevant_world) without declaring:
	//   - last_error (string): the failing call's error message.
	//   - host_error (map): {namespace, message, data?, stderr?, exit_code?}.
	// Both are exempt from import folding — they stay bare at every nesting
	// depth. See app.ReservedWorldKeys.
	OnError string `yaml:"on_error,omitempty"`
	// Emit sends a named event to parallel regions.
	Emit string `yaml:"emit,omitempty"`
	// Background, when true, dispatches Invoke as a job and binds job_id
	// (or default last_job_id) instead of running synchronously. Requires
	// Invoke to be non-empty; validated at load time.
	Background bool `yaml:"background,omitempty"`
	// Once, when true, makes the Invoke idempotent on re-entry: the engine
	// SKIPS the call when every one of its Bind target world keys is already
	// "set" (non-empty), and runs it normally otherwise — binding as usual.
	// The bind target IS the cache, so clearing it (e.g. a re-run intent's
	// `set: {key: ""}` / `{}`) re-arms the call. This generalizes the
	// hand-rolled `when: "<result_key> == ''"` reload guard so an on_enter
	// host call (oracle.decide/task/converse, artifacts_dir write) does not
	// re-fire on /reload, self-transitions, or on_error re-entry.
	//
	// "Set" means: nil, empty string "", empty map {}, and empty slice []
	// all count as UNSET; anything else is SET (see machine.allBindTargetsSet).
	// Scalar int/bool binds are AMBIGUOUS (a real 0 / false reads as unset),
	// so Once is intended for object / string / path binds — guard scalars by
	// hand with When instead. Requires a non-empty Bind (validated at load
	// time: Once with nothing to cache is meaningless). Only meaningful on
	// Invoke effects. The skip is recorded on the existing EffectApplied
	// event with `skipped: "cached"` so a trace shows the elision and why.
	Once bool `yaml:"once,omitempty"`
	// OnComplete fires after the job terminates. Effects in this list run in
	// the originating state's context. Cannot itself contain background: true
	// (validated at load time).
	OnComplete []Effect `yaml:"on_complete,omitempty"`
	// Target, when non-empty, is the state path the session transitions to
	// after this effect's mutations land. Only meaningful inside an
	// `on_complete:` chain — the orchestrator scans the chain for the
	// first effect with Target set and dispatches a synthetic transition
	// (TransitionApplied + StateExited + StateEntered + target on_enter)
	// once all preceding effects have applied without error. Mixing Target
	// with Set / Increment / Say / Invoke on the same effect is rejected
	// at load time (transition and mutation should live on separate
	// effects). Target is also rejected outside on_complete: blocks (use
	// a normal transition's target: instead). The standard Effect.When
	// guard still applies — a false guard skips the entire effect,
	// Target included.
	Target string `yaml:"target,omitempty"`

	// EmitIntent dispatches a synthetic intent against the current state
	// after the surrounding effects chain completes. Used to auto-advance
	// from on_enter (e.g. LLM judge → "accept") and from transition effect
	// chains where a follow-up intent should fire without an external
	// driver. Together with EmitSlots it forms a self-loop within the
	// single-turn budget.
	//
	// Loader enforces:
	//   - non-empty EmitIntent must resolve to an intent declared on the
	//     current state's `on:` arcs (after compile-time intent prefix
	//     expansion through imports);
	//   - the synthetic dispatch is bounded by the depth cap (see
	//     EmitIntentMaxDepth in the machine package);
	//   - mixing EmitIntent with Target on the same effect is rejected.
	//
	// Template values are supported — the literal string may itself be a
	// `{{ ... }}` expression resolved at fire time against the current
	// world (e.g. `emit_intent: "{{ world.llm_verdict.intent }}"`).
	EmitIntent string `yaml:"emit_intent,omitempty"`

	// EmitSlots holds slot values passed to the synthesised intent call.
	// Expression interpolation supported, evaluated against the world AT
	// THE TIME the emit_intent effect fires (post all preceding effects
	// in the same chain).
	EmitSlots map[string]any `yaml:"slots,omitempty"`

	// Effects holds inline sub-effects that fire as part of this effect's
	// execution. Used inside on_complete: target: blocks to attach world
	// mutations (set:, increment:) that should apply as the synthetic
	// transition fires. Processed sequentially before the Target transition
	// is dispatched. Validated like on_complete: children (no background:,
	// no nested on_complete:).
	Effects []Effect `yaml:"effects,omitempty"`

	// OraclePlugin names the oracle alias declared in `oracle_plugins:` that
	// should handle this effect's oracle call (e.g. "oracle.autofix_fixer").
	// Empty resolves to "oracle.claude" (the default). This field is populated
	// when a room declares `oracle: oracle.<name>` on an effect (see
	// docs/architecture/oracle-plugin.md). When absent, the dispatcher
	// resolves the default plugin.
	OraclePlugin string `yaml:"oracle,omitempty"`
}

// ProposalKind declares a named proposal kind.
type ProposalKind struct {
	// Schema declares the typed fields of the proposal draft.
	Schema map[string]string `yaml:"schema,omitempty"`
	// Draft configures the initial drafting step.
	Draft *ProposalStep `yaml:"draft,omitempty"`
	// Refine configures the refinement step.
	Refine *ProposalStep `yaml:"refine,omitempty"`
	// Execute declares the host invocation that runs the proposal.
	Execute *ProposalExecute `yaml:"execute,omitempty"`
	// Views holds optional view overrides per lifecycle phase.
	Views map[string]string `yaml:"views,omitempty"`
	// Policy declares acceptance and confirmation policies.
	Policy *ProposalPolicy `yaml:"policy,omitempty"`
}

// ProposalStep configures a draft or refine step.
type ProposalStep struct {
	// Prompt is the path to the prompt template used for this step.
	Prompt string `yaml:"prompt,omitempty"`
}

// ProposalExecute declares what happens when the proposal is executed.
type ProposalExecute struct {
	// Invoke is the host handler name to call.
	Invoke string `yaml:"invoke,omitempty"`
	// With are the templated args passed to the handler.
	With map[string]any `yaml:"with,omitempty"`
	// Repeatable controls whether rerun/modify_and_rerun are available after success.
	Repeatable bool `yaml:"repeatable,omitempty"`
	// OnSuccess declares the transition after successful execution.
	// Valid values: "stay", "back", or a named state.
	OnSuccess string `yaml:"on_success,omitempty"`
	// Background, when true, runs the execute as a background job (see
	// docs/stories/background-jobs).
	Background bool `yaml:"background,omitempty"`
	// OnComplete declares effects to run when a background job completes.
	OnComplete []Effect `yaml:"on_complete,omitempty"`
}

// ProposalPolicy configures automatic acceptance and confirmation.
type ProposalPolicy struct {
	// AutoAcceptIf is an expr evaluated against {$proposal, $world, $slots}.
	// When true on drafting→reviewing, skip straight to executing.
	AutoAcceptIf string `yaml:"auto_accept_if,omitempty"`
	// RequireConfirm, when true, always requires explicit user confirmation before execute.
	RequireConfirm bool `yaml:"require_confirm,omitempty"`
}

// Intent is a named, typed action available in a state.
type Intent struct {
	Title       string          `yaml:"title,omitempty"`
	Description string          `yaml:"description,omitempty"`
	Examples    []string        `yaml:"examples,omitempty"`
	Priority    int             `yaml:"priority,omitempty"`
	Hidden      bool            `yaml:"hidden,omitempty"`
	Slots       map[string]Slot `yaml:"slots,omitempty"`
	// Synonyms is the author-declared list of alternate phrasings that
	// resolve to this intent (see docs/architecture/semantic-routing.md).
	// Each entry is either a plain phrase ("wade", "walk it") or a
	// template-shaped phrase ("buy {items} for {total_cost}"). At
	// Phase 0 the loader stores the raw strings and validates that
	// they're non-empty; template compilation lands in Phase 4
	// (internal/semroute).
	Synonyms []string `yaml:"synonyms,omitempty"`
}

// Slot is a typed parameter on an intent.
type Slot struct {
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required,omitempty"`
	Default     any      `yaml:"default,omitempty"`
	Values      []string `yaml:"values,omitempty"` // enum values
	Description string   `yaml:"description,omitempty"`
	Examples    []string `yaml:"examples,omitempty"`
	FormatHint  string   `yaml:"format_hint,omitempty"`
	Prompt      string   `yaml:"prompt,omitempty"`
	Validator   string   `yaml:"validator,omitempty"` // expr guard expression
	// Format names a custom semantic format (e.g. "jql"). Validated by
	// the MCP validator's RegisterFormat hooks. Distinct from FormatHint,
	// which is documentation-only.
	Format string `yaml:"format,omitempty"`
	// Synonyms maps each enum value to a list of alternate phrasings
	// (see docs/architecture/semantic-routing.md). Only meaningful when
	// Type == "enum"; the loader rejects the field on non-enum slots
	// and rejects keys that are not in Values.
	Synonyms map[string][]string `yaml:"synonyms,omitempty"`
}

// GuardExpr is a compiled guard expression (produced by internal/expr).
type GuardExpr struct {
	Source string // original expr-lang source
}

// OffPathDef configures the off-path escape hatch.
type OffPathDef struct {
	Trigger string `yaml:"trigger"`
	Banner  string `yaml:"banner,omitempty"`
	Return  string `yaml:"return,omitempty"`
	// Persona is an optional inline system-prompt-style instruction
	// prepended to every off-path oracle call. Lets apps style the
	// off-path "oracle" voice without declaring a top-level agent
	// (e.g., a frontier wise-man for Oregon Trail). Empty falls back
	// to the engine default. When both Persona and Agent are set,
	// Persona wins — apps can override a named agent inline for
	// off-path only.
	Persona string `yaml:"persona,omitempty"`
	// Agent, when non-empty, names an entry in AppDef.Agents whose
	// SystemPrompt is applied to every off-path oracle call. Resolved
	// at runtime via the process-wide agent registry installed at
	// startup by host.SetAgentRegistry. Mutually composable with
	// Persona (above) — Persona wins when both are set.
	Agent string `yaml:"agent,omitempty"`
}

// TimeoutDef configures an automatic state transition after a duration.
type TimeoutDef struct {
	After  string `yaml:"after"`
	Target string `yaml:"target"`
}

// AgentDecl is one entry in the top-level agents: map.
// Exactly one of SystemPrompt or SystemPromptPath must be set; the
// loader resolves SystemPromptPath against the app YAML directory and
// rewrites SystemPrompt with the file contents (clearing SystemPromptPath).
type AgentDecl struct {
	// Exactly one of these is required.
	SystemPrompt     string `yaml:"system_prompt,omitempty"`
	SystemPromptPath string `yaml:"system_prompt_path,omitempty"`

	Model string   `yaml:"model,omitempty"`
	Tools []string `yaml:"tools,omitempty"`
	Cwd   string   `yaml:"cwd,omitempty"`

	// Effort, when non-empty, is forwarded to `claude --effort` for every oracle
	// invocation that resolves to this agent (ask, decide, task, ask_structured,
	// converse). Valid values: low, medium, high, xhigh, max. An effect's
	// `with: { effort: <level> }` arg overrides this per call. Empty leaves the
	// claude CLI default.
	Effort string `yaml:"effort,omitempty"`

	// Provider names an entry in AppDef.Providers whose env overrides (and, when
	// this agent sets no model:, default model) apply to every oracle invocation
	// that resolves to this agent. An effect's `with: { provider: <name> }` arg
	// overrides this per call. Empty means the ambient environment (today's
	// behavior).
	Provider string `yaml:"provider,omitempty"`

	// InheritClaudeDefault opts this agent OUT of the layered system prompt and
	// back to the legacy posture: the persona is passed via
	// --append-system-prompt, stacking on top of Claude Code's full default
	// coding-agent prompt (kitsoki + project grounding are NOT prepended). It
	// is a migration escape hatch for an agent that genuinely needs Claude
	// Code's default behavior; default false. See
	// docs/architecture/system-prompt.md (Replace vs append).
	InheritClaudeDefault bool `yaml:"inherit_claude_default,omitempty"`

	// BashProfile restricts Bash tool usage when the agent's tool surface
	// includes "Bash". Required when Bash is in Tools and the agent is
	// referenced by a host.oracle.ask or host.oracle.decide effect (enforced
	// by the loader). Ignored for host.oracle.task and host.oracle.converse.
	BashProfile *BashProfileDecl `yaml:"bash_profile,omitempty"`

	// ExternalSideEffect, when non-nil, declares whether the agent may
	// mutate external state (Mode C — read-write external side effects).
	// When nil, the loader infers the value from the tool surface:
	// WebFetch/WebSearch or any non-read_only MCP server → true; otherwise
	// false. A disagreement between inferred and declared values produces a
	// loader warn-line.
	ExternalSideEffect *bool `yaml:"external_side_effect,omitempty"`
}

// MetaModeDef declares one meta mode.
//
// Group + Trigger form the `group + verb` namespacing scheme. Map keys
// are `<group>.<verb>` (e.g. "story.bug"); the trigger parser splits
// `/meta <group> <verb>` on whitespace and resolves via that key.
// Exactly one mode per group may set Default:true — it is the verb
// bare `/meta <group>` resolves to.
//
// Backward compat: an un-namespaced YAML mode (key has no `.`) is
// treated by the loader as having Group == its key and no default-verb
// rule (a single-mode group is implicitly default-able). See
// docs/stories/meta-mode.md for the user-facing reference.
type MetaModeDef struct {
	Trigger string `yaml:"trigger"`
	// Group is the namespace token (`story`, `kitsoki`, or an
	// app-defined name). When set, the map key MUST be `Group.Trigger`.
	// Optional for back-compat with un-namespaced YAML.
	Group string `yaml:"group,omitempty"`
	// Default flags the verb that bare `/meta <Group>` resolves to.
	// Exactly one mode per group may set this; the validator enforces
	// the rule for groups with ≥2 modes.
	Default bool           `yaml:"default,omitempty"`
	Label   string         `yaml:"label,omitempty"`
	Banner  string         `yaml:"banner,omitempty"`
	Agent   string         `yaml:"agent"`
	Persist *bool          `yaml:"persist,omitempty"`
	Cwd     string         `yaml:"cwd,omitempty"`
	Tools   []string       `yaml:"tools,omitempty"`
	Return  *MetaReturnDef `yaml:"return,omitempty"`
}

// MetaReturnDef configures the exit message and intent for a meta mode.
type MetaReturnDef struct {
	Message string `yaml:"message,omitempty"`
	Intent  string `yaml:"intent,omitempty"`
}

// PersistOrDefault returns true unless the author explicitly set persist: false.
func (m *MetaModeDef) PersistOrDefault() bool {
	if m == nil || m.Persist == nil {
		return true
	}
	return *m.Persist
}

// ExitIntentOrDefault returns the configured return.intent or "onpath" if unset.
func (m *MetaModeDef) ExitIntentOrDefault() string {
	if m == nil || m.Return == nil || m.Return.Intent == "" {
		return "onpath"
	}
	return m.Return.Intent
}
