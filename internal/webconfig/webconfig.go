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
	// flag-derived static default (today's --oracle/--model path). Must name a
	// declared profile when set.
	DefaultProfile string `yaml:"default_profile,omitempty"`
}

// HarnessProfile is one operator-declared harness profile: a named bundle of
// the oracle-selection axes collapsed behind a single name. Every field is
// optional; an all-empty profile means "today's ambient default" (claude
// backend, ambient auth). Env values use ${VAR} interpolation, expanded at
// load time against the process environment (an unset var is a hard error,
// mirroring providers:).
type HarnessProfile struct {
	// Backend selects which coding-agent CLI is forked: claude|copilot|codex.
	// Empty ⇒ claude. Ignored when Plugin is set.
	Backend string `yaml:"backend,omitempty"`
	// Model is the default --model for this profile (an explicit per-effect or
	// agent model still wins). Optional.
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
	// Plugin routes the profile through an oracle plugin (e.g. builtin.local_llm
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

// resolveHarnessProfiles validates every declared profile and expands ${VAR}
// references in env values in place. Fail-fast at load (never at first
// dispatch), mirroring the providers: contract.
func (cfg *WebConfig) resolveHarnessProfiles() error {
	for name, p := range cfg.HarnessProfiles {
		if !validBackends[p.Backend] {
			return fmt.Errorf("harness_profiles.%s: backend %q is invalid (want claude|copilot|codex)", name, p.Backend)
		}
		for k, v := range p.Env {
			expanded, missing := expandEnvVar(v)
			if missing != "" {
				return fmt.Errorf("harness_profiles.%s: env var %s referenced in env.%s not set", name, missing, k)
			}
			p.Env[k] = expanded
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
	if cfg.DefaultProfile != "" {
		if _, ok := cfg.HarnessProfiles[cfg.DefaultProfile]; !ok {
			return fmt.Errorf("default_profile %q names no declared harness profile", cfg.DefaultProfile)
		}
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
// named `app.yaml`, and loads it via app.Load. Each successful load yields one
// StoryMeta whose Path is the absolute app.yaml path. A per-file load error is
// logged via the standard logger and skipped — the walk continues so a single
// malformed manifest never suppresses its valid siblings. The only error
// returned is for a root directory that cannot be walked (e.g. unreadable).
func DiscoverStories(dirs []string) ([]StoryMeta, error) {
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
			def, loadErr := app.Load(abs)
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
