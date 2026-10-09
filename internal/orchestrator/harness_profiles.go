package orchestrator

import (
	"fmt"
	"sort"

	"kitsoki/internal/host"
)

// WithHarnessLadderConfig installs cfg as the session's harness ladder: the
// automatic multi-provider fallback + effort/model escalation applied to
// every host.agent.decide / host.agent.task dispatch. A disabled cfg
// (cfg.Enabled() == false — the zero value) is the default and leaves every
// dispatch on today's single-attempt behavior, so passing a zero value is
// always safe. See internal/webconfig.HarnessLadder for the `.kitsoki.yaml`
// surface that produces cfg, and internal/host/ladder.go for the design.
func WithHarnessLadderConfig(cfg host.LadderConfig) Option {
	return func(o *Orchestrator) {
		o.harnessLadder = cfg
	}
}

// HarnessProfile is the orchestrator-side runtime form of an operator-declared
// harness profile (webconfig.HarnessProfile, ${VAR} already expanded). It is a
// named bundle of the agent-selection axes a live session can switch between:
// which backend CLI is forked, which model it defaults to, and the env retarget
// (e.g. synthetic.new). See docs/guide/agents/harness-profiles.md.
type HarnessProfile struct {
	// Name is the profile key from .kitsoki.yaml (the headline operators pick by).
	Name string
	// Backend is claude|copilot|codex (empty ⇒ claude). Ignored when Plugin set.
	Backend string
	// Model is the default --model for the profile. For the active session
	// profile, it supersedes story-local agent model defaults so the selected
	// provider receives a compatible model id.
	Model string
	// Models is the static catalog /model and the web dropdown list. Optional.
	Models []string
	// ModelsEndpoint is an OpenAI/Anthropic-compatible /models URL whose always-on
	// model ids are fetched (with this profile's env auth) and merged into the
	// catalog. Optional.
	ModelsEndpoint string
	// Effort is the default reasoning effort; Efforts is the catalog the /effort
	// command + web dropdown list (empty ⇒ no effort control). Optional.
	Effort  string
	Efforts []string
	// Env is the ${VAR}-expanded env retarget merged onto the forked CLI. Carried
	// here for resolution; never exposed through Profiles() / traces.
	Env map[string]string
	// Quota enables local throttling for this profile before calls reach the
	// provider. It is secret-free but operational, so omit it from ProfileInfo
	// until there is a dedicated status surface.
	Quota host.QuotaControl
	// Plugin routes through an agent plugin instead of a backend CLI. Optional.
	Plugin string
}

// ProfileSelection is a session's live choice: a profile name plus an optional
// operator model override (a pick from the profile's catalog). The zero value
// means "no selection" — resolution falls through to the flag-derived default.
type ProfileSelection struct {
	Profile string `json:"profile"`
	// Model, when set, overrides the profile's default model for this session.
	Model string `json:"model,omitempty"`
	// Effort, when set, overrides the profile's default reasoning effort.
	Effort string `json:"effort,omitempty"`
}

// ProfileInfo is the secret-free view of a profile for surfaces (TUI /provider,
// web picker). It deliberately omits Env so a selection list can never leak a
// token. Active marks the currently selected profile.
type ProfileInfo struct {
	Name    string   `json:"name"`
	Backend string   `json:"backend,omitempty"`
	Model   string   `json:"model,omitempty"`
	Models  []string `json:"models,omitempty"`
	Effort  string   `json:"effort,omitempty"`
	Efforts []string `json:"efforts,omitempty"`
	Active  bool     `json:"active"`
}

// WithHarnessProfiles seeds the orchestrator with the declared profiles and the
// default-profile name new sessions start on. A nil/empty map leaves the session
// on the legacy flag-derived path (Selection/Profiles report empty, every
// dispatch resolves to the static --agent backend). The default profile, when
// named and present, becomes the initial selection.
func WithHarnessProfiles(profiles map[string]HarnessProfile, defaultProfile string) Option {
	return func(o *Orchestrator) {
		if len(profiles) == 0 {
			return
		}
		o.harnessProfiles = profiles
		o.defaultProfile = defaultProfile
		if _, ok := profiles[defaultProfile]; ok {
			o.selection = ProfileSelection{Profile: defaultProfile}
		}
	}
}

// providersForDispatch merges story-declared providers with harness profiles
// so a harness-ladder rung can name the same profile key the operator sees in
// /provider. Story providers win on name collisions because explicit story
// config is the older, narrower contract for `with: { provider: ... }`.
func (o *Orchestrator) providersForDispatch() map[string]host.Provider {
	out := providersForContext(o.def)
	if len(o.harnessProfiles) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]host.Provider, len(o.harnessProfiles))
	}
	for name, p := range o.harnessProfiles {
		if _, exists := out[name]; exists {
			continue
		}
		prov := host.Provider{Model: p.Model, Effort: p.Effort}
		if len(p.Env) > 0 {
			prov.Env = make(map[string]string, len(p.Env))
			for k, v := range p.Env {
				prov.Env[k] = v
			}
		}
		out[name] = prov
	}
	return out
}

// Profiles returns the declared profiles as a stable, name-sorted, secret-free
// list with the active one flagged. Empty when no profiles are declared.
func (o *Orchestrator) Profiles() []ProfileInfo {
	o.selMu.RLock()
	defer o.selMu.RUnlock()
	if len(o.harnessProfiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(o.harnessProfiles))
	for name := range o.harnessProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ProfileInfo, 0, len(names))
	for _, name := range names {
		p := o.harnessProfiles[name]
		out = append(out, ProfileInfo{
			Name:    name,
			Backend: p.Backend,
			Model:   p.Model,
			Models:  o.catalogFor(p),
			Effort:  p.Effort,
			Efforts: p.Efforts,
			Active:  name == o.selection.Profile,
		})
	}
	return out
}

// catalogFor returns the profile's model catalog: its static Models plus any
// always-on models fetched from ModelsEndpoint (cached, deduped). A fetch
// failure (no network, bad key in a test) silently yields just the static list,
// so the picker never breaks on a transient endpoint error.
func (o *Orchestrator) catalogFor(p HarnessProfile) []string {
	if p.ModelsEndpoint == "" {
		return p.Models
	}
	fetched := o.fetchModelCatalog(p)
	if len(fetched) == 0 {
		return p.Models
	}
	seen := make(map[string]bool, len(p.Models)+len(fetched))
	out := make([]string, 0, len(p.Models)+len(fetched))
	for _, m := range append(append([]string{}, p.Models...), fetched...) {
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// Selection returns the session's current profile selection (a copy).
func (o *Orchestrator) Selection() ProfileSelection {
	o.selMu.RLock()
	defer o.selMu.RUnlock()
	return o.selection
}

// SetSelection switches the active profile (and optional model / effort
// override) for every subsequent dispatch. The in-flight call, if any, finishes
// on the prior selection (next-turn semantics — resolution snapshots the
// selection once per dispatch). It rejects an unknown profile, a model absent
// from a non-empty catalog, or an effort absent from a non-empty effort catalog,
// rather than silently falling back, so the surface can show an error.
func (o *Orchestrator) SetSelection(profile, model, effort string) error {
	o.selMu.Lock()
	defer o.selMu.Unlock()
	if len(o.harnessProfiles) == 0 {
		return fmt.Errorf("no harness profiles are declared")
	}
	p, ok := o.harnessProfiles[profile]
	if !ok {
		return fmt.Errorf("unknown harness profile %q", profile)
	}
	if model != "" {
		// Validate against the full catalog (static + fetched), so a model from
		// the dynamic /models list is accepted. fetchModelCatalog uses its own
		// cache/mutex, so this is safe under selMu.
		catalog := o.catalogFor(p)
		if len(catalog) > 0 && !containsStr(catalog, model) {
			return fmt.Errorf("model %q is not in profile %q's catalog", model, profile)
		}
	}
	if effort != "" && len(p.Efforts) > 0 && !containsStr(p.Efforts, effort) {
		return fmt.Errorf("effort %q is not in profile %q's effort catalog", effort, profile)
	}
	o.selection = ProfileSelection{Profile: profile, Model: model, Effort: effort}
	if o.ladderSession != nil {
		o.ladderSession.Reset()
	}
	return nil
}

// resolveSelection snapshots the live selection once and returns the backend
// name to fork and the active profile to install on the dispatch context. When
// no profile is selected (none declared, or the legacy path) it returns the
// static fallback backend and a zero ActiveProfile, preserving today's behavior
// byte-for-byte. A single snapshot under RLock means a concurrent SetSelection
// can't tear one dispatch.
func (o *Orchestrator) resolveSelection(fallbackBackend string) (backend string, active host.ActiveProfile) {
	o.selMu.RLock()
	defer o.selMu.RUnlock()
	sel := o.selection
	if sel.Profile == "" {
		return fallbackBackend, host.ActiveProfile{}
	}
	p, ok := o.harnessProfiles[sel.Profile]
	if !ok {
		return fallbackBackend, host.ActiveProfile{}
	}
	model := p.Model
	if sel.Model != "" {
		model = sel.Model
	}
	effort := p.Effort
	if sel.Effort != "" {
		effort = sel.Effort
	}
	active = host.ActiveProfile{
		Name: p.Name,
		Provider: host.Provider{
			Model:  model,
			Effort: effort,
			Env:    p.Env,
		},
		Quota: p.Quota,
	}
	// A plugin profile keeps the fallback backend (plugins route through the
	// agent registry, not a backend fork); a CLI profile selects its backend.
	backend = fallbackBackend
	if p.Plugin == "" {
		backend = p.Backend
	}
	return backend, active
}
