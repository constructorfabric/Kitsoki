package orchestrator

import (
	"fmt"
	"sort"

	"kitsoki/internal/host"
)

// HarnessProfile is the orchestrator-side runtime form of an operator-declared
// harness profile (webconfig.HarnessProfile, ${VAR} already expanded). It is a
// named bundle of the oracle-selection axes a live session can switch between:
// which backend CLI is forked, which model it defaults to, and the env retarget
// (e.g. synthetic.new). See docs/architecture/harness-profiles.md.
type HarnessProfile struct {
	// Name is the profile key from .kitsoki.yaml (the headline operators pick by).
	Name string
	// Backend is claude|copilot|codex (empty ⇒ claude). Ignored when Plugin set.
	Backend string
	// Model is the default --model for the profile; an explicit per-effect/agent
	// model still wins over it.
	Model string
	// Models is the catalog /model and the web dropdown list. Optional.
	Models []string
	// Env is the ${VAR}-expanded env retarget merged onto the forked CLI. Carried
	// here for resolution; never exposed through Profiles() / traces.
	Env map[string]string
	// Plugin routes through an oracle plugin instead of a backend CLI. Optional.
	Plugin string
}

// ProfileSelection is a session's live choice: a profile name plus an optional
// operator model override (a pick from the profile's catalog). The zero value
// means "no selection" — resolution falls through to the flag-derived default.
type ProfileSelection struct {
	Profile string `json:"profile"`
	// Model, when set, overrides the profile's default model for this session.
	Model string `json:"model,omitempty"`
}

// ProfileInfo is the secret-free view of a profile for surfaces (TUI /provider,
// web picker). It deliberately omits Env so a selection list can never leak a
// token. Active marks the currently selected profile.
type ProfileInfo struct {
	Name    string   `json:"name"`
	Backend string   `json:"backend,omitempty"`
	Model   string   `json:"model,omitempty"`
	Models  []string `json:"models,omitempty"`
	Active  bool     `json:"active"`
}

// WithHarnessProfiles seeds the orchestrator with the declared profiles and the
// default-profile name new sessions start on. A nil/empty map leaves the session
// on the legacy flag-derived path (Selection/Profiles report empty, every
// dispatch resolves to the static --oracle backend). The default profile, when
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
			Models:  p.Models,
			Active:  name == o.selection.Profile,
		})
	}
	return out
}

// Selection returns the session's current profile selection (a copy).
func (o *Orchestrator) Selection() ProfileSelection {
	o.selMu.RLock()
	defer o.selMu.RUnlock()
	return o.selection
}

// SetSelection switches the active profile (and optional model override) for
// every subsequent dispatch. The in-flight call, if any, finishes on the prior
// selection (next-turn semantics — resolution snapshots the selection once per
// dispatch). It rejects an unknown profile, or a model absent from a non-empty
// catalog, rather than silently falling back, so the surface can show an error.
func (o *Orchestrator) SetSelection(profile, model string) error {
	o.selMu.Lock()
	defer o.selMu.Unlock()
	if len(o.harnessProfiles) == 0 {
		return fmt.Errorf("no harness profiles are declared")
	}
	p, ok := o.harnessProfiles[profile]
	if !ok {
		return fmt.Errorf("unknown harness profile %q", profile)
	}
	if model != "" && len(p.Models) > 0 {
		found := false
		for _, m := range p.Models {
			if m == model {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("model %q is not in profile %q's catalog", model, profile)
		}
	}
	o.selection = ProfileSelection{Profile: profile, Model: model}
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
	active = host.ActiveProfile{
		Name: p.Name,
		Provider: host.Provider{
			Model: model,
			Env:   p.Env,
		},
	}
	// A plugin profile keeps the fallback backend (plugins route through the
	// oracle registry, not a backend fork); a CLI profile selects its backend.
	backend = fallbackBackend
	if p.Plugin == "" {
		backend = p.Backend
	}
	return backend, active
}
