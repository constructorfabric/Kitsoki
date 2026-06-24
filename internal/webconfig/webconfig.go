// Package webconfig resolves where the multi-story web UI looks for stories
// and walks those directories to discover one StoryMeta per app.yaml found.
//
// Two concerns live here, deliberately small and dependency-free beyond the
// app loader:
//
//  1. Configuration — WebConfig carries the operator's story_dirs and harness
//     profiles. It loads from a checked-in `.kitsoki.yaml` (gopkg.in/yaml.v3),
//     then deep-merges an optional, gitignored `.kitsoki.local.yaml` sitting
//     beside it (the same dichotomy as Claude Code's settings.json +
//     settings.local.json): the shared file holds the team baseline; the local
//     file holds personal, secret-bearing, or machine-specific overrides. Local
//     wins. Resolve applies flags > merged config > ./stories default.
//
//  2. Discovery — DiscoverStories walks the resolved directories, matches files
//     literally named `app.yaml`, and loads each via app.Load. A malformed
//     manifest is logged and skipped so one broken story never hides its valid
//     siblings; only an unreadable root directory aborts the walk.
//
// Non-goals (decided leans for the PoC, see docs/proposals/web-multi-story.md):
//   - No fsnotify watch — rescanning is explicit (call DiscoverStories again).
//   - No mode/addr/db config keys — the config file carries only story_dirs.
//     It is intentionally extensible later; for now anything else is ignored.
package webconfig

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kitsoki/internal/app"
)

// DefaultConfigFile is the checked-in, shared config file Load looks for in the
// working directory.
const DefaultConfigFile = ".kitsoki.yaml"

// DefaultLocalConfigFile is the gitignored, per-developer override file Load
// deep-merges on top of DefaultConfigFile. For any base path Load derives the
// sibling local path by inserting ".local" before the extension (so a --config
// of foo/bar.yaml pairs with foo/bar.local.yaml); this constant is the name for
// the conventional .kitsoki.yaml base.
const DefaultLocalConfigFile = ".kitsoki.local.yaml"

// defaultStoryDir is the resolution fallback when neither flags nor the config
// file supply a story directory.
var defaultStoryDirs = []string{"./stories"}

// WebConfig is the on-disk configuration for the web UI and (since harness
// profiles) the TUI. It is the stable extension point for machine-global keys.
type WebConfig struct {
	// StoryDirs lists the directories DiscoverStories walks for app.yaml files.
	StoryDirs []string `yaml:"story_dirs"`

	// HarnessProfiles declares named harness profiles — operator-selectable
	// bundles of {backend, env, model} that a live session can switch between
	// via the TUI's /provider /model commands or the web header picker. Keyed
	// by profile name. See docs/architecture/harness-profiles.md.
	HarnessProfiles map[string]HarnessProfile `yaml:"harness_profiles,omitempty"`
	// DefaultProfile names the profile new sessions start on. Empty ⇒ the
	// flag-derived static default (today's --agent/--model path). Must name a
	// declared profile when set.
	DefaultProfile string `yaml:"default_profile,omitempty"`

	// Intercept binds the `kitsoki intercept` pre-LLM gate (the Stage-3
	// UserPromptSubmit hook) to a story room: app.yaml + starting state +
	// confidence bar. Nil ⇒ no binding (the hook command must then receive
	// --app/--room on the command line). See docs/architecture/prompt-intercept.md.
	Intercept *InterceptConfig `yaml:"intercept,omitempty"`
	// Mining configures the always-on ambient session miner
	// (docs/proposals/ambient-session-miner.md). Absent or Enabled=false ⇒ the
	// miner never starts and nothing in any flow/test path spends LLM. Validated
	// in Load via resolveMining (cadence parses, first_pass_sample non-negative).
	Mining MiningConfig `yaml:"mining,omitempty"`

	// Root declares the implicit project root — the dev-story instance the
	// loader synthesizes when `kitsoki run` is given no app.yaml path. Absent
	// (rung 0) ⇒ synthesize a bare dev-story import with no overrides; present
	// (rung 1) ⇒ fold its overrides into the synthesized importer. Validated
	// fail-fast in Load via resolveRoot, mirroring harness profiles. See
	// docs/stories/imports.md "The blank root that grows".
	Root *RootConfig `yaml:"root,omitempty"`
}

// InterceptConfig is the operator's binding for the pre-LLM intercept gate. It
// names the story (App) and the room (Room) whose no-LLM routing tiers classify
// a piped prompt, plus the confidence ConfidenceBar a deterministic/semantic
// match must clear before the gate executes instead of passing through to the
// LLM. EscapePrefix is reserved for the Stage-3 hook (a leading token that opts
// a prompt out of interception); it carries no behavior here.
type InterceptConfig struct {
	// Enabled gates the whole binding. When false the resolveIntercept
	// validation is skipped and the hook command falls back to its flags.
	Enabled bool `yaml:"enabled"`
	// App is the path to the story's app.yaml the gate classifies against.
	App string `yaml:"app"`
	// Room is the starting state path whose allowed intents are the gate's
	// alphabet.
	Room string `yaml:"room"`
	// ConfidenceBar is the minimum verdict confidence a match must clear to
	// be executed rather than passed through. Zero defaults to 0.90 at load.
	ConfidenceBar float64 `yaml:"confidence_bar"`
	// EscapePrefix is an optional leading token that opts a prompt out of
	// interception (consumed by the Stage-3 hook, not the gate itself).
	EscapePrefix string `yaml:"escape_prefix,omitempty"`
}

// MiningConfig is the `.kitsoki.yaml` `mining:` block — the machine-global
// configuration for the ambient session miner. It sits beside harness_profiles:
// / story_dirs and is the kind of extensible key webconfig anticipated (see the
// package non-goal note). Default off: a zero MiningConfig (no block, or
// enabled: false) starts no miner. See docs/architecture/ambient-mining.md.
type MiningConfig struct {
	// Enabled gates the whole service. Default off until both mining.enabled is
	// set AND first-run consent is recorded (the banner, mine-command-ux). Flow
	// fixtures never set it, so ambient mining contributes nothing to any flow
	// path and never spends LLM in CI.
	Enabled bool `yaml:"enabled,omitempty"`
	// Cadence is the debounce window for live-session passes (Go duration string,
	// e.g. "30s"). Empty ⇒ DefaultMiningCadence. Must parse via time.ParseDuration.
	Cadence string `yaml:"cadence,omitempty"`
	// FirstPassSample is the bounded N of recent sessions the history seed mines
	// (prep.py --sample recency --max N). Zero ⇒ DefaultFirstPassSample. Negative
	// is a load error.
	FirstPassSample int `yaml:"first_pass_sample,omitempty"`
	// PriorityThreshold is passed downstream to the proposer; recipes below it
	// never surface. Mirrors mining.Proposer.PriorityThreshold.
	PriorityThreshold float64 `yaml:"priority_threshold,omitempty"`
	// TranscriptDirs lists extra transcript directories beyond the resolved
	// ~/.claude/projects/<slug> (the `/mine scope` control surface adds here).
	TranscriptDirs []string `yaml:"transcript_dirs,omitempty"`
	// MinedThrough is the per-slug dedup ledger: slug → newest-mined transcript
	// mtime (unix seconds). A pass advances its slug's entry only on completion;
	// the seed fires iff the slug's entry is absent. The watermark invariant.
	MinedThrough map[string]int64 `yaml:"mined_through,omitempty"`
}

// Mining defaults applied by resolveMining when the block leaves them empty.
const (
	// DefaultMiningCadence is the live-pass debounce window when cadence is empty
	// (tens of seconds so a finished turn's transcript is mined within a turn or
	// two — the proposal's lean).
	DefaultMiningCadence = "30s"
	// DefaultFirstPassSample is the history-seed sample size when first_pass_sample
	// is zero (the kit's reference run used 12).
	DefaultFirstPassSample = 12
)

// CadenceOrDefault parses Cadence, falling back to DefaultMiningCadence when
// empty. Load has already validated it parses, so the error is unexpected here.
func (m MiningConfig) CadenceOrDefault() (time.Duration, error) {
	c := m.Cadence
	if c == "" {
		c = DefaultMiningCadence
	}
	return time.ParseDuration(c)
}

// FirstPassSampleOrDefault returns FirstPassSample or DefaultFirstPassSample
// when unset.
func (m MiningConfig) FirstPassSampleOrDefault() int {
	if m.FirstPassSample == 0 {
		return DefaultFirstPassSample
	}
	return m.FirstPassSample
}

// resolveMining validates the `mining:` block fail-fast at load (never at first
// pass), mirroring resolveHarnessProfiles / resolveRoot. A disabled or absent
// block is a no-op. When enabled: cadence (if set) must parse as a Go duration,
// and first_pass_sample must not be negative. An enabled block with no
// resolvable transcript dir is NOT a hard error here (the resolver may pick up
// ~/.claude/projects/<slug> at runtime, and transcript_dirs may name a dir that
// appears later) — it is surfaced as a runtime no-op pass, per the proposal.
func (cfg *WebConfig) resolveMining() error {
	m := cfg.Mining
	if !m.Enabled {
		return nil
	}
	if m.Cadence != "" {
		if _, err := time.ParseDuration(m.Cadence); err != nil {
			return fmt.Errorf("mining.cadence %q is not a valid duration: %w", m.Cadence, err)
		}
	}
	if m.FirstPassSample < 0 {
		return fmt.Errorf("mining.first_pass_sample %d must not be negative", m.FirstPassSample)
	}
	if m.PriorityThreshold < 0 {
		return fmt.Errorf("mining.priority_threshold %.3f must not be negative", m.PriorityThreshold)
	}
	return nil
}

// RootConfig is the `.kitsoki.yaml` `root:` block — the rung-1 surface for the
// implicit project root. import names the base story (v1: only "dev-story");
// overrides folds bindings / world / synonyms into the synthesized importer.
// A nil *RootConfig (no `root:` block) is rung 0.
type RootConfig struct {
	// Import is the base story to specialize. v1 blesses only "dev-story";
	// empty defaults to dev-story. Any other value is a Load error.
	Import string `yaml:"import,omitempty"`
	// Overrides folds project-specific bindings / world / synonyms into the
	// synthesized dev-story importer. Optional; nil ⇒ a bare dev-story import.
	Overrides *RootOverrides `yaml:"overrides,omitempty"`
}

// RootOverrides are the rung-1 fold inputs. Each is optional.
type RootOverrides struct {
	// Bindings rebinds dev-story host_interfaces (ticket/vcs/ci/workspace/
	// transport) onto concrete handlers. Folded into the import's
	// host_bindings. An unknown iface is a Load error.
	Bindings map[string]string `yaml:"bindings,omitempty"`
	// World sets instance-level world defaults projected into dev-story via
	// world_in:. An unknown dev-story world key is a Load error.
	World map[string]any `yaml:"world,omitempty"`
	// Synonyms extends routing synonyms for the synthesized instance, keyed by
	// intent name → alternate phrasings.
	Synonyms map[string][]string `yaml:"synonyms,omitempty"`
}

// RootSpec projects this RootConfig into the neutral app.RootSpec that
// app.SynthesizeRoot consumes. Returns nil for a nil *RootConfig (rung 0). The
// conversion is the seam that keeps internal/app free of an import edge back
// to internal/webconfig (which already imports internal/app).
func (rc *RootConfig) RootSpec() *app.RootSpec {
	if rc == nil {
		return nil
	}
	spec := &app.RootSpec{Import: rc.Import}
	if rc.Overrides != nil {
		spec.Bindings = rc.Overrides.Bindings
		spec.World = rc.Overrides.World
		spec.Synonyms = rc.Overrides.Synonyms
	}
	return spec
}

// HarnessProfile is one operator-declared harness profile: a named bundle of
// the agent-selection axes collapsed behind a single name. Every field is
// optional; an all-empty profile means "today's ambient default" (claude
// backend, ambient auth). Env values use ${VAR} interpolation, expanded at
// load time against the process environment (an unset var is a hard error,
// mirroring providers:).
type HarnessProfile struct {
	// Backend selects which coding-agent CLI is forked: claude|copilot|codex.
	// Empty ⇒ claude. Ignored when Plugin is set.
	Backend string `yaml:"backend,omitempty"`
	// Model is the default --model for this profile. For the active session
	// profile, it supersedes story-local agent model defaults so the selected
	// provider receives a compatible model id. Optional.
	Model string `yaml:"model,omitempty"`
	// Models is the catalog the /model command and web dropdown list. Optional;
	// when set, Model (and any operator model selection) must be a member.
	Models []string `yaml:"models,omitempty"`
	// ModelsEndpoint, when set, is an OpenAI/Anthropic-compatible /models URL
	// (e.g. https://api.synthetic.new/openai/v1/models) whose always-on model ids
	// are fetched and merged into the catalog at selection time — so a provider's
	// full live model list is offered, not a hand-maintained subset. Auth comes
	// from this profile's env (Bearer OPENAI_API_KEY / ANTHROPIC_AUTH_TOKEN).
	ModelsEndpoint string `yaml:"models_endpoint,omitempty"`
	// Effort is the default reasoning effort for the profile (low|medium|high|
	// xhigh|max). Applied where the backend/model supports it (claude --effort).
	Effort string `yaml:"effort,omitempty"`
	// Efforts is the effort catalog the /effort command and web dropdown list —
	// declare it only on profiles whose backend/model supports effort. Empty ⇒
	// no effort control is offered. Each must be a valid level.
	Efforts []string `yaml:"efforts,omitempty"`
	// Env overrides merged onto the forked CLI subprocess (e.g. ANTHROPIC_BASE_URL
	// + ANTHROPIC_AUTH_TOKEN to retarget claude at synthetic.new). ${VAR}-expanded
	// at load time. Never recorded in traces.
	Env map[string]string `yaml:"env,omitempty"`
	// Plugin routes the profile through an agent plugin (e.g. builtin.local_llm
	// for llama.cpp) instead of forking a backend CLI. Optional.
	Plugin string `yaml:"plugin,omitempty"`
}

var validBackends = map[string]bool{"": true, "claude": true, "copilot": true, "codex": true}

// validEfforts mirrors the engine's --effort levels (internal/app loader).
var validEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// Load reads WebConfig from the given base path, then deep-merges the sibling
// local override (see LocalConfigPath) on top of it. A missing base or local
// file is not an error — each absent file contributes nothing, so an empty repo
// returns a zero WebConfig and the caller falls back to the default via Resolve.
//
// Merge happens before validation, so ${VAR} expansion and the backend / model
// / effort / default_profile checks all run once against the effective config:
// a profile the local file overrides is validated in its overridden form, and a
// default_profile the local file adds may legally name a profile only the local
// file declares. Any read, parse, or validation failure is returned.
func Load(path string) (WebConfig, error) {
	base, _, err := parseConfig(path)
	if err != nil {
		return WebConfig{}, err
	}
	local, hadLocal, err := parseConfig(LocalConfigPath(path))
	if err != nil {
		return WebConfig{}, err
	}
	cfg := base
	if hadLocal {
		cfg = mergeConfig(base, local)
	}
	if err := cfg.resolveHarnessProfiles(); err != nil {
		return WebConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.resolveIntercept(); err != nil {
		return WebConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.resolveRoot(path); err != nil {
		return WebConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.resolveMining(); err != nil {
		return WebConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// LocalConfigPath derives the gitignored override path that pairs with a base
// config path by inserting ".local" before the extension: .kitsoki.yaml →
// .kitsoki.local.yaml, foo/bar.yaml → foo/bar.local.yaml. An extensionless path
// gets a trailing ".local".
func LocalConfigPath(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".local" + ext
}

// parseConfig reads and YAML-unmarshals one config file WITHOUT validating or
// expanding it — validation is deferred to Load so it runs once on the merged
// result. A missing file yields a zero WebConfig and exists=false; any other
// read or parse failure is returned.
func parseConfig(path string) (cfg WebConfig, exists bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return WebConfig{}, false, nil
		}
		return WebConfig{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return WebConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

// mergeConfig deep-merges a local override onto a base config, local-wins:
//   - story_dirs and default_profile are scalars/lists — a non-empty local value
//     replaces the base value; an absent local value leaves the base untouched.
//   - harness_profiles merge BY PROFILE NAME: profiles only in base survive,
//     profiles in local are added, and a profile present in both is replaced
//     WHOLE by the local one. (Field-level merging within a profile is
//     deliberately not done — to retune one field of a shared profile, restate
//     that profile in the local file; you never have to restate the others.)
func mergeConfig(base, local WebConfig) WebConfig {
	out := base
	if len(local.StoryDirs) > 0 {
		out.StoryDirs = local.StoryDirs
	}
	if local.DefaultProfile != "" {
		out.DefaultProfile = local.DefaultProfile
	}
	// The intercept binding is a single coherent block, so the local file
	// replaces it whole (matching the per-profile "restate, don't field-merge"
	// rule above) rather than field-merging into the base binding.
	if local.Intercept != nil {
		out.Intercept = local.Intercept
	}
	if len(local.HarnessProfiles) > 0 {
		merged := make(map[string]HarnessProfile, len(base.HarnessProfiles)+len(local.HarnessProfiles))
		for k, v := range base.HarnessProfiles {
			merged[k] = v
		}
		for k, v := range local.HarnessProfiles {
			merged[k] = v
		}
		out.HarnessProfiles = merged
	}
	return out
}

// resolveRoot validates the `root:` block fail-fast at load (never at first
// turn), mirroring resolveHarnessProfiles. Three checks:
//
//   - root.import must be the blessed base story (v1: dev-story);
//   - every overrides.bindings.<iface> must name a dev-story host_interface
//     (ticket/vcs/ci/workspace/transport);
//   - every overrides.world.<key> must name a dev-story world key — resolved by
//     loading dev-story standalone from the repo root (the directory the config
//     file lives in is the resolution start). When dev-story cannot be resolved
//     (a downstream checkout without the in-repo story — the deferred
//     kitsoki-as-dependency case), world-key validation is skipped rather than
//     failing the whole load; the import + binding checks still apply.
//
// A nil Root (rung 0) is a no-op.
func (cfg *WebConfig) resolveRoot(configPath string) error {
	rc := cfg.Root
	if rc == nil {
		return nil
	}
	importName := rc.Import
	if importName == "" {
		importName = app.RootStoryName
	}
	if importName != app.RootStoryName {
		return fmt.Errorf("root.import %q is not a known base story (v1 supports: %s)", importName, app.RootStoryName)
	}
	if rc.Overrides == nil {
		return nil
	}
	for iface := range rc.Overrides.Bindings {
		if _, ok := app.DevStoryIfaces[iface]; !ok {
			return fmt.Errorf("root.overrides.bindings: %q is not a host_interface declared by %s", iface, app.RootStoryName)
		}
	}
	if len(rc.Overrides.World) > 0 {
		repoRoot := filepath.Dir(configPath)
		if abs, err := filepath.Abs(repoRoot); err == nil {
			repoRoot = abs
		}
		keys, err := app.DevStoryWorldKeys(repoRoot)
		if err != nil {
			// dev-story is not resolvable here (downstream dependency case);
			// skip the world-key check rather than failing — the deferred
			// kitsoki-as-dependency slice owns installed-story resolution.
			return nil
		}
		for key := range rc.Overrides.World {
			if _, ok := keys[key]; !ok {
				return fmt.Errorf("root.overrides.world: unknown key %q for base %s", key, app.RootStoryName)
			}
		}
	}
	return nil
}

// resolveHarnessProfiles validates every declared profile and expands ${VAR}
// references in env values in place. Structural errors (bad backend / model /
// effort) fail-fast at load. A profile that references an UNSET env var is a
// special case: it is fatal ONLY when that profile is the selected
// default_profile (the boot profile must work). A non-selected secret-bearing
// profile whose env var is absent is dropped from the usable set with a warning
// instead of killing the whole config — so e.g. a `synthetic-*` profile in a
// gitignored override never blocks startup in an environment that lacks the key
// (a GUI-launched VS Code extension host inherits no shell vars; the operator
// hasn't selected synthetic anyway). Selecting such a profile later surfaces a
// clean error at switch time. This validates the env lazily-by-selection, never
// at first dispatch.
func (cfg *WebConfig) resolveHarnessProfiles() error {
	var dropped []string
	for name, p := range cfg.HarnessProfiles {
		if !validBackends[p.Backend] {
			return fmt.Errorf("harness_profiles.%s: backend %q is invalid (want claude|copilot|codex)", name, p.Backend)
		}
		missingEnv := ""
		for k, v := range p.Env {
			expanded, missing := expandEnvVar(v)
			if missing != "" {
				// The boot profile MUST resolve — a missing secret there is a
				// genuine misconfiguration the operator needs to fix now.
				if name == cfg.DefaultProfile {
					return fmt.Errorf("harness_profiles.%s: env var %s referenced in env.%s not set", name, missing, k)
				}
				missingEnv = missing
				break
			}
			p.Env[k] = expanded
		}
		if missingEnv != "" {
			// Unusable in this environment, but not selected — drop it rather
			// than fail the load. Logged (never silent) so the absence is
			// explainable.
			delete(cfg.HarnessProfiles, name)
			dropped = append(dropped, fmt.Sprintf("%s (env %s unset)", name, missingEnv))
			continue
		}
		if len(p.Models) > 0 && p.Model != "" && !contains(p.Models, p.Model) {
			return fmt.Errorf("harness_profiles.%s: model %q is not in its models catalog", name, p.Model)
		}
		for _, e := range p.Efforts {
			if !validEfforts[e] {
				return fmt.Errorf("harness_profiles.%s: effort %q is invalid (valid: low, medium, high, xhigh, max)", name, e)
			}
		}
		if p.Effort != "" {
			if !validEfforts[p.Effort] {
				return fmt.Errorf("harness_profiles.%s: effort %q is invalid (valid: low, medium, high, xhigh, max)", name, p.Effort)
			}
			if len(p.Efforts) > 0 && !contains(p.Efforts, p.Effort) {
				return fmt.Errorf("harness_profiles.%s: effort %q is not in its efforts catalog", name, p.Effort)
			}
		}
		cfg.HarnessProfiles[name] = p
	}
	if len(dropped) > 0 {
		slog.Warn("webconfig: dropped harness profiles with unset env vars (not selected as default_profile; set the var to enable)",
			"dropped", dropped)
	}
	if cfg.DefaultProfile != "" {
		if _, ok := cfg.HarnessProfiles[cfg.DefaultProfile]; !ok {
			return fmt.Errorf("default_profile %q names no declared harness profile", cfg.DefaultProfile)
		}
	}
	return nil
}

// resolveIntercept validates the intercept binding and applies the default
// confidence bar in place. A nil or disabled block is a no-op (the hook
// command then relies on its --app/--room/--bar flags). An enabled block must
// name a non-empty App and Room; a zero ConfidenceBar defaults to 0.90, and a
// bar outside (0, 1] is rejected. Fail-fast at load, mirroring providers:.
func (cfg *WebConfig) resolveIntercept() error {
	ic := cfg.Intercept
	if ic == nil || !ic.Enabled {
		return nil
	}
	if ic.App == "" {
		return fmt.Errorf("intercept.app is required when intercept.enabled is true")
	}
	if ic.Room == "" {
		return fmt.Errorf("intercept.room is required when intercept.enabled is true")
	}
	if ic.ConfidenceBar == 0 {
		ic.ConfidenceBar = 0.90
	}
	if ic.ConfidenceBar <= 0 || ic.ConfidenceBar > 1 {
		return fmt.Errorf("intercept.confidence_bar %g is invalid (want a value in (0, 1])", ic.ConfidenceBar)
	}
	return nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// expandEnvVar replaces ${VAR} tokens against the process environment, returning
// the expanded string, or ("", VAR) for the first unset variable. A replacement
// value is never re-scanned. Semantics mirror the app loader's expandEnvVar so
// harness_profiles and providers behave identically.
func expandEnvVar(s string) (expanded, missing string) {
	var buf strings.Builder
	for i := 0; i < len(s); {
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			buf.WriteString(s[i:])
			break
		}
		buf.WriteString(s[i : i+idx])
		i += idx + 2
		end := strings.Index(s[i:], "}")
		if end < 0 {
			buf.WriteString("${")
			continue
		}
		name := s[i : i+end]
		i += end + 1
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", name
		}
		buf.WriteString(val)
	}
	return buf.String(), ""
}

// Resolve picks the effective story directories with first-non-empty-wins
// precedence: explicit flags (typically from repeatable --stories-dir), then
// the config's StoryDirs, then the ./stories default. The returned slice is a
// fresh copy the caller may retain and mutate.
func Resolve(flagDirs []string, cfg WebConfig) []string {
	switch {
	case len(flagDirs) > 0:
		return append([]string(nil), flagDirs...)
	case len(cfg.StoryDirs) > 0:
		return append([]string(nil), cfg.StoryDirs...)
	default:
		return append([]string(nil), defaultStoryDirs...)
	}
}

// StoryMeta describes one discovered story. Path is the ABSOLUTE path to its
// app.yaml — the canonical session key per the epic's Shared decision #1; the
// app.id (Def.App.ID) is display-only and may collide across stories.
type StoryMeta struct {
	// Path is the absolute path to the story's app.yaml.
	Path string
	// Def is the loaded, validated app definition.
	Def *app.AppDef
}

// DiscoverStories walks each directory recursively, finds every file literally
// named `app.yaml`, and loads it via app.LoadWithResolver. Each successful load
// yields one StoryMeta whose Path is the absolute app.yaml path. A per-file load
// error is logged via the standard logger and skipped — the walk continues so a
// single malformed manifest never suppresses its valid siblings. The only error
// returned is for a root directory that cannot be walked (e.g. unreadable).
//
// resolver is the injected ImportResolver (DI, no package global) through which
// an `@kitsoki/<name>` import in a discovered manifest resolves against the
// `--kitsoki-repo` override or the embedded story library — this is what lets
// `kitsoki web` discover a vendored instance in a FOREIGN repo with no on-disk
// kitsoki checkout. nil keeps the legacy error-on-missing behaviour.
func DiscoverStories(dirs []string, resolver app.ImportResolver) ([]StoryMeta, error) {
	var metas []StoryMeta
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				// Surface the failure to open the root dir; for entries below it,
				// WalkDir would have already descended, so this is effectively the
				// root-unreadable case the contract aborts on.
				return err
			}
			if d.IsDir() || d.Name() != "app.yaml" {
				return nil
			}
			abs, absErr := filepath.Abs(path)
			if absErr != nil {
				abs = path
			}
			def, loadErr := app.LoadWithResolver(abs, nil, resolver)
			if loadErr != nil {
				slog.Warn("webconfig: skipping malformed story", "path", abs, "err", loadErr)
				return nil
			}
			metas = append(metas, StoryMeta{Path: abs, Def: def})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("discover stories under %s: %w", dir, err)
		}
	}
	return metas, nil
}
