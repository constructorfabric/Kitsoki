// Package app — YAML loader for Kitsoki app definitions (Stage 2).
//
// Load reads an AppDef from disk; LoadBytes reads from an in-memory byte slice.
// After parsing, both perform full referential-integrity validation and return
// all errors together via errors.Join so the caller sees the complete problem
// set on the first broken load.
package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"kitsoki/internal/agents"
	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/oracle/grammar"

	goyaml "github.com/goccy/go-yaml"
)

// ValidationError is one referential-integrity problem found during loading.
// It satisfies the error interface and carries a human-readable, actionable
// message that includes the file path (when available) and the problem.
type ValidationError struct {
	// File is the source path, or "" when loading from bytes.
	File string
	// Line and Column come from goccy/go-yaml token positions (1-based).
	Line   int
	Column int
	// Message is the one-line problem description.
	Message string
}

func (v *ValidationError) Error() string {
	loc := ""
	if v.File != "" {
		loc = v.File
	} else {
		loc = "<bytes>"
	}
	if v.Line > 0 {
		loc = fmt.Sprintf("%s:%d:%d", loc, v.Line, v.Column)
	}
	return fmt.Sprintf("%s: %s", loc, v.Message)
}

// Load reads and validates an AppDef from the given file path.
//
// Pipeline (in order):
//
//  1. parseAndMerge — parse the root manifest; merge any `include:` glob
//     matches into a single AppDef (cloak-style same-app file splitting).
//  2. resolveImports — recursive depth-first: for each entry in
//     def.Imports, load the child manifest (recursively resolving ITS
//     imports first), then fold it under the alias. Folds states under
//     a compound wrapper, prefixes world keys / intents / agents /
//     interfaces, applies overrides, rewrites @exit: targets, and lifts
//     iface declarations into parent.HostInterfaces. Cycles detected via
//     a canonical-path stack and rejected with a clear error.
//  3. expandPhases — instantiate phase templates into concrete states.
//  4. resolveAllInterfaces — final pass: walk every remaining
//     `iface.<name>.<op>` reference and rewrite to the concrete
//     `<binding>.<op>` host invocation, looking up the binding from the
//     merged def.HostInterfaces table. Concrete handler names are
//     unioned into def.Hosts so the allow-list check passes.
//  5. materialiseStandaloneExits — for the root manifest, any remaining
//     `@exit:<name>` targets become synthesised `__exit__<name>` terminal
//     states (the standalone exit sentinel).
//  6. validateDef — referential-integrity pass over the merged tree:
//     intent / state path / host / agent / requires references.
//
// All steps run their own error aggregation; the first stage that
// finds errors short-circuits the rest and returns them via
// errors.Join.
func Load(path string) (*AppDef, error) {
	return LoadWithOverrides(path, nil)
}

// LoadWithOverrides is Load with a per-iface binding-override map
// applied between the import-fold pass and resolveAllInterfaces. Keys
// are top-level host_interface names (e.g. "transport"); values are
// the concrete host handler to bind in place of the iface's declared
// `default:`. Unknown iface names are silently ignored — a caller that
// over-specifies (e.g. covers every story they might run) doesn't have
// to know which ifaces a given app exposes.
//
// Intended caller: testrunner fixtures that need to rebind an iface
// for one flow run without forking the production app.yaml. Production
// code should use `imports.<alias>.host_bindings:` at the parent app
// level — that's the multi-layer compose path. This
// entrypoint is the test seam below it.
func LoadWithOverrides(path string, ifaceOverrides map[string]string) (*AppDef, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	// Parse just enough to find the include: list before full validation.
	baseDir := filepath.Dir(path)
	if abs, absErr := filepath.Abs(baseDir); absErr == nil {
		baseDir = abs
	}
	merged, mergeErrs := parseAndMerge(b, path, baseDir)
	if len(mergeErrs) > 0 {
		return nil, errors.Join(mergeErrs...)
	}
	// Stash the loader's base directory so downstream consumers
	// (notably internal/render.AppRenderer, which roots its template
	// loader at <BaseDir>/views/) don't have to recompute it from
	// the manifest path. See docs/stories/story-style.md.
	merged.BaseDir = baseDir

	// Resolve imports recursively, folding each child into merged.
	canonical := canonicalPath(path)
	// Seed LoadedManifests with the root manifest's canonical path; each
	// folded import appends itself + its own transitive manifests. The
	// metamode controller reads this list to auto-watch every file the
	// loader actually touched.
	merged.LoadedManifests = appendUnique(merged.LoadedManifests, canonical)
	if importErrs := resolveImports(merged, path, baseDir, []string{canonical}); len(importErrs) > 0 {
		return nil, errors.Join(importErrs...)
	}

	// Expand phase templates into concrete states before validation so the
	// referential-integrity pass sees the synthesised states.
	if expandErrs := expandPhases(merged, path); len(expandErrs) > 0 {
		return nil, errors.Join(expandErrs...)
	}

	// Inject builtin meta_modes (`self`, `bug`) that the app didn't
	// declare itself. Done before validation so trigger collisions and
	// missing-env-var diagnostics fire the same way as for app-declared
	// modes.
	injectBuiltinMetaModes(merged)

	// Apply per-iface binding overrides (testrunner seam). Runs AFTER
	// import-fold so every iface declared by a child has been lifted
	// into merged.HostInterfaces under its post-fold key — the caller
	// can override either the top-level name ("transport") or a
	// prefixed import name ("bf__transport") at the same depth a
	// production parent's host_bindings: block would. Runs BEFORE
	// resolveAllInterfaces so the new binding propagates the same way
	// as the original `default:`.
	for name, binding := range ifaceOverrides {
		if iface, ok := merged.HostInterfaces[name]; ok && iface != nil {
			iface.Default = binding
		}
	}

	// Final host_interface resolution: rewrites every remaining
	// iface.<name>.<op> reference to a concrete <binding>.<op> host
	// invocation, after all imports have folded their iface declarations
	// into def.HostInterfaces (under alias-prefixed names) and after
	// each layer's host_bindings have had a chance to override defaults.
	// This is the surface that makes multi-layer rebinding compose.
	if ifaceErrs := resolveAllInterfaces(merged, path); len(ifaceErrs) > 0 {
		return nil, errors.Join(ifaceErrs...)
	}

	// Rewrite any remaining @exit:<name> targets in the top-level app. For
	// the root manifest these are terminal sentinels — the app is loaded
	// standalone, so an exit means "stop here." We synthesise a terminal
	// state per exit and rewrite refs accordingly.
	if exitErrs := materialiseStandaloneExits(merged, path); len(exitErrs) > 0 {
		return nil, errors.Join(exitErrs...)
	}

	// Resolve oracle plugin declarations from oracle_plugins: block. This
	// validates plugin names, performs ${VAR} substitution in env/headers, and
	// injects the default oracle.claude entry when absent.
	if pluginErrs := resolveOraclePlugins(merged, path); len(pluginErrs) > 0 {
		return nil, errors.Join(pluginErrs...)
	}

	// Resolve provider declarations from the providers: block (validate +
	// ${VAR} substitution in env). Reference validation runs inside validateDef.
	if provErrs := resolveProviders(merged, path); len(provErrs) > 0 {
		return nil, errors.Join(provErrs...)
	}

	// Now fully validate the merged definition.
	_, validErrs := validateDef(merged, path)
	if len(validErrs) > 0 {
		return nil, errors.Join(validErrs...)
	}
	return merged, nil
}

// materialiseStandaloneExits walks the top-level app and replaces every
// `@exit:<name>` transition target with a synthesised terminal state
// `__exit__<name>`. This only fires for the root manifest — imported
// children have their @exit: rewritten by foldChild.
func materialiseStandaloneExits(def *AppDef, file string) []error {
	if def == nil || len(def.Exits) == 0 {
		return nil
	}
	// Collect references first so we know which exits are actually used.
	used := make(map[string]bool)
	walkStatesForExits(def.States, used)
	for name := range used {
		if _, ok := def.Exits[name]; !ok {
			return []error{&ValidationError{File: file, Message: fmt.Sprintf("@exit:%s referenced but not declared in exits:", name)}}
		}
	}
	if def.States == nil {
		def.States = make(map[string]*State)
	}
	for name := range used {
		terminalName := "__exit__" + name
		if _, exists := def.States[terminalName]; !exists {
			def.States[terminalName] = &State{Terminal: true, Description: fmt.Sprintf("Exit: %s", name)}
		}
	}
	// Rewrite all targets.
	rewriteExitsInStates(def.States)
	return nil
}

func walkStatesForExits(states map[string]*State, used map[string]bool) {
	for _, s := range states {
		if s == nil {
			continue
		}
		for _, list := range s.On {
			for _, tr := range list {
				if strings.HasPrefix(tr.Target, "@exit:") {
					used[strings.TrimPrefix(tr.Target, "@exit:")] = true
				}
			}
		}
		if s.Timeout != nil && strings.HasPrefix(s.Timeout.Target, "@exit:") {
			used[strings.TrimPrefix(s.Timeout.Target, "@exit:")] = true
		}
		if len(s.States) > 0 {
			walkStatesForExits(s.States, used)
		}
	}
}

func rewriteExitsInStates(states map[string]*State) {
	for _, s := range states {
		if s == nil {
			continue
		}
		for intent, list := range s.On {
			for i, tr := range list {
				if strings.HasPrefix(tr.Target, "@exit:") {
					tr.Target = "__exit__" + strings.TrimPrefix(tr.Target, "@exit:")
					list[i] = tr
				}
			}
			s.On[intent] = list
		}
		if s.Timeout != nil && strings.HasPrefix(s.Timeout.Target, "@exit:") {
			s.Timeout.Target = "__exit__" + strings.TrimPrefix(s.Timeout.Target, "@exit:")
		}
		if len(s.States) > 0 {
			rewriteExitsInStates(s.States)
		}
	}
}

// parseAndMerge parses the main YAML file, resolves include: patterns, and
// merges all included files into a single AppDef.
func parseAndMerge(b []byte, file, baseDir string) (*AppDef, []error) {
	var def AppDef
	if err := goyaml.UnmarshalWithOptions(b, &def, goyaml.Strict()); err != nil {
		var yamlErr *goyaml.SyntaxError
		ve := &ValidationError{File: file, Message: err.Error()}
		if errors.As(err, &yamlErr) {
			_ = yamlErr
		}
		return nil, []error{ve}
	}

	// Resolve any agents: declared in the main file against the main file's dir.
	var errs []error
	if agentErrs := resolveAgentDecls(&def, file, baseDir); len(agentErrs) > 0 {
		errs = append(errs, agentErrs...)
	}

	if len(def.Include) == 0 {
		if len(errs) > 0 {
			return nil, errs
		}
		return &def, nil
	}

	// Resolve each glob pattern and merge included files.
	for _, pattern := range def.Include {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("include glob %q: %v", pattern, err)})
			continue
		}
		for _, matchPath := range matches {
			inclBytes, err := os.ReadFile(matchPath)
			if err != nil {
				errs = append(errs, &ValidationError{File: matchPath, Message: fmt.Sprintf("include read: %v", err)})
				continue
			}
			var inclDef AppDef
			if err := goyaml.UnmarshalWithOptions(inclBytes, &inclDef, goyaml.Strict()); err != nil {
				errs = append(errs, &ValidationError{File: matchPath, Message: err.Error()})
				continue
			}
			// Resolve agents in the included file against its own directory
			// before merging, so system_prompt_path is interpreted from the
			// file that authored the agent.
			if agentErrs := resolveAgentDecls(&inclDef, matchPath, filepath.Dir(matchPath)); len(agentErrs) > 0 {
				errs = append(errs, agentErrs...)
				continue
			}
			if mergeErr := mergeInto(&def, &inclDef, matchPath); mergeErr != nil {
				errs = append(errs, mergeErr...)
			}
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return &def, nil
}

// mergeInto merges src into dst. States, proposals, hosts, intents, world keys,
// phase_templates, phases, and checkpoint_intents from src are merged into dst.
// Collisions are errors.
func mergeInto(dst, src *AppDef, srcFile string) []error {
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: srcFile, Message: msg})
	}

	// Merge states.
	for k, v := range src.States {
		if _, exists := dst.States[k]; exists {
			addErr(fmt.Sprintf("include: state %q is already declared", k))
			continue
		}
		if dst.States == nil {
			dst.States = make(map[string]*State)
		}
		dst.States[k] = v
	}

	// Merge phase_templates.
	for k, v := range src.PhaseTemplates {
		if _, exists := dst.PhaseTemplates[k]; exists {
			addErr(fmt.Sprintf("include: phase_template %q is already declared", k))
			continue
		}
		if dst.PhaseTemplates == nil {
			dst.PhaseTemplates = make(map[string]*PhaseTemplate)
		}
		dst.PhaseTemplates[k] = v
	}

	// Merge phases (singleton — at most one source may declare it).
	if src.Phases != nil {
		if dst.Phases != nil {
			addErr("include: phases: block already declared")
		} else {
			dst.Phases = src.Phases
		}
	}

	// Merge checkpoint_intents.
	for k, v := range src.CheckpointIntents {
		if _, exists := dst.CheckpointIntents[k]; exists {
			addErr(fmt.Sprintf("include: checkpoint_intent %q is already declared", k))
			continue
		}
		if dst.CheckpointIntents == nil {
			dst.CheckpointIntents = make(map[string]Intent)
		}
		dst.CheckpointIntents[k] = v
	}

	// Merge proposals.
	for k, v := range src.Proposals {
		if _, exists := dst.Proposals[k]; exists {
			addErr(fmt.Sprintf("include: proposal %q is already declared", k))
			continue
		}
		if dst.Proposals == nil {
			dst.Proposals = make(map[string]*ProposalKind)
		}
		dst.Proposals[k] = v
	}

	// Merge hosts allow-list (union, no duplicates).
	hostSet := make(map[string]struct{}, len(dst.Hosts))
	for _, h := range dst.Hosts {
		hostSet[h] = struct{}{}
	}
	for _, h := range src.Hosts {
		if _, exists := hostSet[h]; !exists {
			dst.Hosts = append(dst.Hosts, h)
			hostSet[h] = struct{}{}
		}
	}

	// Merge intents.
	for k, v := range src.Intents {
		if _, exists := dst.Intents[k]; exists {
			addErr(fmt.Sprintf("include: intent %q is already declared", k))
			continue
		}
		if dst.Intents == nil {
			dst.Intents = make(map[string]Intent)
		}
		dst.Intents[k] = v
	}

	// Merge world schema.
	for k, v := range src.World {
		if _, exists := dst.World[k]; exists {
			addErr(fmt.Sprintf("include: world variable %q is already declared", k))
			continue
		}
		if dst.World == nil {
			dst.World = make(map[string]VarDef)
		}
		dst.World[k] = v
	}

	// Merge meta modes.
	for k, v := range src.MetaModes {
		if _, exists := dst.MetaModes[k]; exists {
			addErr(fmt.Sprintf("include: meta_mode %q is already declared", k))
			continue
		}
		if dst.MetaModes == nil {
			dst.MetaModes = make(map[string]*MetaModeDef)
		}
		dst.MetaModes[k] = v
	}

	// Merge agent declarations. Collision on key is an error so app authors
	// can't accidentally end up with two definitions of the same agent
	// across an include boundary.
	for k, v := range src.Agents {
		if _, exists := dst.Agents[k]; exists {
			addErr(fmt.Sprintf("include: agent %q is already declared", k))
			continue
		}
		if dst.Agents == nil {
			dst.Agents = make(map[string]*AgentDecl)
		}
		dst.Agents[k] = v
	}

	// Merge provider declarations. Collision on key is an error, mirroring the
	// agents merge above.
	for k, v := range src.Providers {
		if _, exists := dst.Providers[k]; exists {
			addErr(fmt.Sprintf("include: provider %q is already declared", k))
			continue
		}
		if dst.Providers == nil {
			dst.Providers = make(map[string]*ProviderDecl)
		}
		dst.Providers[k] = v
	}

	return errs
}

// resolveAgentDecls walks def.Agents and, for each entry:
//
//   - enforces the system_prompt xor system_prompt_path one-of rule,
//   - reads system_prompt_path (relative to baseDir) into SystemPrompt and
//     clears SystemPromptPath so downstream code only sees resolved prompts,
//   - env-expands Cwd, erroring on any unset ${VAR} reference,
//   - normalises Tools entries to fully-qualified host.x.y form.
//
// A nil or empty Agents map is a no-op. The function reports all problems
// it finds rather than stopping at the first.
func resolveAgentDecls(def *AppDef, file, baseDir string) []error {
	if def == nil || len(def.Agents) == 0 {
		return nil
	}
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: file, Message: msg})
	}

	// Set of agents referenced by a read-only oracle verb (ask/decide), where
	// Bash must run under a bash_profile. Drives the bash_profile cross-check.
	askDecideAgents := collectAskDecideAgents(def)

	for _, name := range sortedKeys(def.Agents) {
		decl := def.Agents[name]
		if decl == nil {
			addErr(fmt.Sprintf("agent %q: empty definition", name))
			continue
		}

		// One-of: system_prompt xor system_prompt_path.
		hasInline := decl.SystemPrompt != ""
		hasPath := decl.SystemPromptPath != ""
		switch {
		case hasInline && hasPath:
			addErr(fmt.Sprintf("agent %q: system_prompt and system_prompt_path are mutually exclusive", name))
			continue
		case !hasInline && !hasPath:
			addErr(fmt.Sprintf("agent %q: one of system_prompt or system_prompt_path is required", name))
			continue
		}

		// Resolve the prompt path against baseDir; promote to an absolute
		// path so error messages and downstream code see a stable location
		// independent of the loader's cwd.
		if hasPath {
			promptPath := decl.SystemPromptPath
			if !filepath.IsAbs(promptPath) {
				promptPath = filepath.Join(baseDir, promptPath)
			}
			if abs, absErr := filepath.Abs(promptPath); absErr == nil {
				promptPath = abs
			}
			contents, err := os.ReadFile(promptPath)
			if err != nil {
				addErr(fmt.Sprintf("agent %q: system_prompt_path %q: %v", name, promptPath, err))
				continue
			}
			decl.SystemPrompt = string(contents)
			decl.SystemPromptPath = ""
		}

		// Env-expand cwd.
		if decl.Cwd != "" {
			expanded, missing := expandMetaCwd(decl.Cwd)
			if missing != "" {
				addErr(fmt.Sprintf("agent %q: cwd %q references unset env var %s", name, decl.Cwd, missing))
				continue
			}
			decl.Cwd = expanded
		}

		// Validate the effort enum (empty is fine — leaves the CLI default).
		if msg := validateEffort(fmt.Sprintf("agent %q", name), decl.Effort); msg != "" {
			addErr(msg)
			continue
		}

		// Normalise tools to fully-qualified form. Logic duplicates
		// metamode.NormaliseToolName here because internal/metamode imports
		// internal/app already; importing back would create a cycle.
		if len(decl.Tools) > 0 {
			out := make([]string, len(decl.Tools))
			for i, t := range decl.Tools {
				out[i] = normaliseAgentTool(t)
			}
			decl.Tools = out
		}

		// bash_profile validation: Bash in the tool surface requires a
		// bash_profile *only* when the agent is actually referenced by a
		// read-only oracle verb (host.oracle.ask / host.oracle.decide), where
		// every Bash invocation must pass through ApplyBashProfile. Agents used
		// solely with host.oracle.task get full, unprofiled Bash by design, so
		// the bare presence of Bash is not a problem there. Cross-check the
		// effect graph rather than warning unconditionally.
		if hasTool(decl.Tools, "host.Bash") && decl.BashProfile == nil && askDecideAgents[name] {
			addErr(fmt.Sprintf("agent %q declares Bash but no bash_profile; required when the agent is referenced by host.oracle.ask or host.oracle.decide (those verbs run every Bash command through a profile allowlist)", name))
		}

		// external_side_effect inference: infer from the tool surface when
		// the field is absent, and warn when declared value disagrees with
		// the inferred value.
		inferred := inferExternalSideEffect(decl.Tools)
		if decl.ExternalSideEffect == nil {
			// No explicit declaration — store the inferred value.
			decl.ExternalSideEffect = &inferred
		} else if *decl.ExternalSideEffect != inferred {
			slog.Warn("agent external_side_effect declaration disagrees with inferred value from tool surface",
				"agent", name, "file", file,
				"declared", *decl.ExternalSideEffect, "inferred", inferred)
		}
	}
	return errs
}

// hasTool reports whether tools contains name (exact match after normalisation).
func hasTool(tools []string, name string) bool {
	for _, t := range tools {
		if t == name {
			return true
		}
	}
	return false
}

// inferExternalSideEffect returns true when the tool surface includes
// WebFetch, WebSearch, or any MCP server that isn't known to be read-only.
// The MCP read_only check is best-effort here — full cross-checking against
// declared mcp_servers blocks requires the effect graph and is deferred to
// later phases. For now: any tool named "host.WebFetch" or "host.WebSearch"
// in the tool list implies external side effects.
func inferExternalSideEffect(tools []string) bool {
	for _, t := range tools {
		switch t {
		case "host.WebFetch", "host.WebSearch":
			return true
		}
	}
	return false
}

// collectAskDecideAgents walks the full effect graph and returns the set of
// agent names referenced by a read-only oracle verb (host.oracle.ask or
// host.oracle.decide) via the effect's `with.agent` argument. Those verbs run
// every Bash command through a bash_profile allowlist, so a referenced agent
// that declares Bash without a profile is a load error (see resolveAgentDecls).
// Effects reached only through host.oracle.task — which grants full Bash by
// design — are intentionally not collected.
func collectAskDecideAgents(def *AppDef) map[string]bool {
	out := map[string]bool{}
	if def == nil {
		return out
	}
	var walkEffects func(effs []Effect)
	walkEffects = func(effs []Effect) {
		for _, e := range effs {
			if e.Invoke == "host.oracle.ask" || e.Invoke == "host.oracle.decide" {
				if a, ok := e.With["agent"].(string); ok && a != "" {
					out[a] = true
				}
			}
			walkEffects(e.OnComplete)
		}
	}
	var walkState func(s *State)
	walkState = func(s *State) {
		if s == nil {
			return
		}
		walkEffects(s.OnEnter)
		for _, transitions := range s.On {
			for _, t := range transitions {
				walkEffects(t.Effects)
			}
		}
		for _, child := range s.States {
			walkState(child)
		}
	}
	for _, s := range def.States {
		walkState(s)
	}
	return out
}

// normaliseAgentTool maps a YAML-author-friendly tool name into the
// fully-qualified host.x.y form. Empty strings pass through; names already
// prefixed with "host." are returned unchanged. Logic mirrors
// metamode.NormaliseToolName — duplicated here to keep internal/app free
// of internal/metamode (which imports internal/app, see loader docs).
func normaliseAgentTool(name string) string {
	if name == "" {
		return name
	}
	if strings.HasPrefix(name, "host.") {
		return name
	}
	return "host." + name
}

// LoadBytes reads and validates an AppDef from a YAML byte slice.
func LoadBytes(b []byte) (*AppDef, error) {
	def, errs := loadAndValidate(b, "")
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return def, nil
}

// loadAndValidate parses a YAML byte slice and validates the resulting AppDef.
// It returns all validation errors accumulated, not just the first.
func loadAndValidate(b []byte, file string) (*AppDef, []error) {
	var def AppDef

	// goccy/go-yaml gives us detailed error positions; use strict mode so
	// unknown fields don't silently vanish.
	if err := goyaml.UnmarshalWithOptions(b, &def, goyaml.Strict()); err != nil {
		// Wrap as a ValidationError preserving any line/col info.
		var yamlErr *goyaml.SyntaxError
		ve := &ValidationError{File: file, Message: err.Error()}
		if errors.As(err, &yamlErr) {
			// goccy/go-yaml's LexError / SyntaxError expose Token field.
			// The best we can do without exporting internal fields is to
			// inspect the error message; the token position appears in the
			// formatted string from goccy when present.
			_ = yamlErr
		}
		return nil, []error{ve}
	}

	// LoadBytes skips parseAndMerge, so inject builtin meta_modes here
	// before validation. Same rationale as the Load() path.
	injectBuiltinMetaModes(&def)

	// Resolve agent declarations (tools normalisation, external_side_effect
	// inference, bash_profile checks). baseDir is "" in the LoadBytes path —
	// system_prompt_path resolution is intentionally not supported here;
	// use Load(path) for file-backed apps.
	if agentErrs := resolveAgentDecls(&def, file, ""); len(agentErrs) > 0 {
		return nil, agentErrs
	}

	// Resolve oracle plugin declarations from oracle_plugins: block.
	if pluginErrs := resolveOraclePlugins(&def, file); len(pluginErrs) > 0 {
		return nil, pluginErrs
	}

	// Resolve provider declarations from the providers: block.
	if provErrs := resolveProviders(&def, file); len(provErrs) > 0 {
		return nil, provErrs
	}

	return validateDef(&def, file)
}

// validateDef validates a pre-parsed (and possibly merged) AppDef.
func validateDef(def *AppDef, file string) (*AppDef, []error) {
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: file, Message: msg})
	}

	// ── 1. basic metadata ────────────────────────────────────────────────────
	if def.App.ID == "" {
		addErr("app.id is required")
	}
	// Layer-2 project context: at most one source. (Both empty is fine — the
	// prompts/_project.md convention may still supply it at render time.)
	if def.App.Context != "" && def.App.ContextPath != "" {
		addErr("app.context and app.context_path are mutually exclusive; set only one")
	}

	// ── 2. world schema ──────────────────────────────────────────────────────
	worldKeys := make(map[string]struct{}, len(def.World)+len(ReservedWorldKeys))
	for k := range def.World {
		worldKeys[k] = struct{}{}
	}
	// Reserved engine-owned globals (last_error/host_error) are always valid
	// targets for world.<key> references and relevant_world lists, whether or
	// not the story declares them — the runtime writes them on on_error
	// redirects. See ReservedWorldKeys in imports.go.
	for k := range ReservedWorldKeys {
		worldKeys[k] = struct{}{}
	}

	// ── 3. initial state ─────────────────────────────────────────────────────
	rootName := ""
	switch v := def.Root.(type) {
	case string:
		rootName = v
	case map[string]interface{}:
		// Inline compound/parallel root. For PoC we only validate that
		// "type" is present; deeper validation is a Stage-3 concern.
	default:
		addErr(fmt.Sprintf("root: unsupported value type %T; expected a state name string", def.Root))
	}

	// ── 4. collect all declared state paths ──────────────────────────────────
	allStatePaths := make(map[string]struct{})
	collectStatePaths("", def.States, allStatePaths)

	// Collect per-state-path the set of intent names declared in that
	// state's `on:` block. Used by validateBackgroundEffectAware to
	// statically verify emit_intent references — an emit_intent on a
	// state's on_enter / transition effect must resolve to an intent
	// that the state (or an ancestor compound) handles.
	stateOnKeys := make(map[string]map[string]struct{})
	collectStateOnKeys("", def.States, stateOnKeys)

	// ── 5. validate root exists ───────────────────────────────────────────────
	if rootName != "" {
		if _, ok := allStatePaths[rootName]; !ok {
			addErr(fmt.Sprintf("root: state %q is not declared in states", rootName))
		}
	}

	// ── 6. collect all global intent names ───────────────────────────────────
	globalIntents := make(map[string]struct{}, len(def.Intents))
	for name := range def.Intents {
		globalIntents[name] = struct{}{}
	}

	// ── 7. per-state referential-integrity checks ─────────────────────────────
	// Build the host allow-list set for effect validation.
	allowedHosts := make(map[string]struct{}, len(def.Hosts))
	for _, h := range def.Hosts {
		allowedHosts[h] = struct{}{}
	}
	// Build the declared-agents set so effect-level `agent: <name>` refs in
	// host.oracle.* with: blocks can be statically resolved.
	declaredAgents := make(map[string]struct{}, len(def.Agents))
	for name := range def.Agents {
		declaredAgents[name] = struct{}{}
	}
	validateStates(file, "", def.States, globalIntents, def.Intents, nil, worldKeys, allStatePaths, stateOnKeys, allowedHosts, declaredAgents, &errs)

	// ── 7a'. static expression compile-check ──────────────────────────────────
	// Compile (never evaluate) every effect value and guard expression so a
	// malformed expr-lang expression — e.g. a pongo-only `|default:` filter
	// written into an effect value — fails the load with a precise diagnostic
	// instead of exploding mid-turn the first time its transition fires. See
	// validate_exprs.go.
	validateExprs(file, def, &errs)

	// Validate the engine-driven decider config (execution-modes proposal).
	if d := def.Decider; d != nil {
		if strings.TrimSpace(d.Agent) == "" {
			errs = append(errs, fmt.Errorf("%s: decider.agent is required", file))
		} else if _, ok := declaredAgents[d.Agent]; !ok && len(declaredAgents) > 0 {
			errs = append(errs, fmt.Errorf("%s: decider.agent %q is not declared in agents", file, d.Agent))
		}
		if strings.TrimSpace(d.Schema) == "" {
			errs = append(errs, fmt.Errorf("%s: decider.schema is required", file))
		}
		if d.Threshold < 0 || d.Threshold > 1 {
			errs = append(errs, fmt.Errorf("%s: decider.threshold %.2f out of range [0,1]", file, d.Threshold))
		}
	}

	// ── 7a. Semantic-routing schema checks (see docs/architecture/semantic-routing.md).
	// Validates Intent.Synonyms / Slot.Synonyms / AppDef.Routing against the
	// routing rules. Errors here share the same shape as the
	// surrounding validators (ValidationError via the errs slice).
	validateRouting(file, def, &errs)

	// ── 7b. (removed) off-path agent reference: superseded by step 9b
	// validateAgentReferences which also recognises builtin agent names
	// like `story-author`.

	// ── 8. relevant_world keys exist in world schema ──────────────────────────
	// (already done inside validateStates, which recurses into nested states)

	// ── 9. meta-mode validation ──────────────────────────────────────────────
	validateMetaModes(file, def, &errs)

	// ── 9b. cross-reference: every agent name referenced anywhere in the
	// AppDef must resolve in AppDef.Agents or agents.BuiltinNames().
	validateAgentReferences(file, def, &errs)

	// ── 9c. cross-reference: every provider name referenced by an agent's
	// provider: field or an effect's with.provider must resolve in
	// AppDef.Providers.
	validateProviderReferences(file, def, &errs)

	// ── 9c. reach-into-child guard (see docs/stories/imports.md).
	// Reject parent transitions that target a deep state inside an
	// imported child (any path of the form `<alias>.<X>` where <X> is
	// not the import's declared entry). The import alias itself
	// (`<alias>`) is fine — it's the canonical "invoke the child" form
	// — and `<alias>.<entry>` is allowed too, since that's just the
	// drill-down the alias would have done anyway. Anything deeper
	// couples the parent to the child's internals and is forbidden.
	//
	// Targets inside the child (rewritten to relative `../X` form by
	// the rewriter) are not affected; this guard only fires for
	// authored absolute paths at the parent level.
	validateNoReachIntoChild(file, def, &errs)

	// ── 9d. oracle-split verb × agent.Tools cross-checks (oracle-split
	// proposal invariant 1 / M6 / M3).
	// Enforces: ask/decide/extract → no mutation tools; task → acceptance.schema
	// required; task + external_side_effect:false + WebFetch/WebSearch → error.
	validateOracleVerbCrossChecks(file, def, &errs)

	// ── 9e. grammar-subset check for builtin.local_llm grammar:true effects.
	// Every decide effect whose `oracle:` alias resolves to a builtin.local_llm
	// plugin with grammar: true must point at a schema inside llama.cpp's
	// translatable grammar subset; otherwise grammar would silently fail open at
	// runtime. Reject out-of-subset schemas at load time.
	validateLocalLLMGrammarSubset(file, def, &errs)

	// ── 9f. host.starlark.run effect validation. Every such effect must name a
	// script that — together with its .star.yaml sidecar — exists on disk inside
	// the app root, and whose sidecar parses. Catching this at load time turns a
	// typo'd script path or a malformed sidecar into an actionable boot error
	// rather than a runtime on_error: bounce on the first turn that hits the room.
	validateStarlarkEffects(file, def, &errs)

	// ── 10. proposal execute effect validation ────────────────────────────────
	// ProposalExecute.Background and ProposalExecute.OnComplete are not covered
	// by validateStates (proposals live outside the state tree).  Apply the same
	// rules here: background: true requires invoke:; on_complete: cannot nest
	// background: true; on_complete: invoke: must be in the allow-list.
	for pname, pk := range def.Proposals {
		if pk == nil || pk.Execute == nil {
			continue
		}
		ex := pk.Execute
		loc := fmt.Sprintf("proposal %q execute", pname)
		if ex.Background && ex.Invoke == "" {
			addErr(fmt.Sprintf("%s: background: true requires invoke: to be set", loc))
		}
		for i, child := range ex.OnComplete {
			childLoc := fmt.Sprintf("%s on_complete[%d]", loc, i)
			if child.Background {
				addErr(fmt.Sprintf("%s: background: true is not allowed inside on_complete:", childLoc))
			}
			if child.Invoke != "" && len(allowedHosts) > 0 {
				if _, ok := allowedHosts[child.Invoke]; !ok {
					addErr(fmt.Sprintf("%s: invoke %q is not declared in app hosts", childLoc, child.Invoke))
				}
			}
			validateAgentRef(file, childLoc, child, declaredAgents, &errs)
			// child is already an on_complete entry — use the on_complete-aware
			// entry point so Target rules apply to it directly. Proposal-execute
			// effects have no owning state; pass "" so a relative target: would
			// resolve to the top-level namespace.
			validateOnCompleteEffect(file, childLoc, "", child, allowedHosts, declaredAgents, allStatePaths, stateOnKeys, &errs)
		}
	}

	return def, errs
}

// collectStatePaths walks the state tree and records every path in the form
// "parentPath.stateName" (dot-separated, matching the design's compound
// address scheme). Top-level states have no prefix so their path == their name.
func collectStatePaths(prefix string, states map[string]*State, out map[string]struct{}) {
	for name, s := range states {
		path := joinPath(prefix, name)
		out[path] = struct{}{}
		if s != nil && len(s.States) > 0 {
			collectStatePaths(path, s.States, out)
		}
	}
}

// collectStateOnKeys walks the state tree and records, per state path,
// the set of intent names declared in that state's `on:` block. The
// result is used by validateBackgroundEffectAware to statically
// resolve emit_intent: references — a name must appear on the owning
// state or some ancestor compound.
func collectStateOnKeys(prefix string, states map[string]*State, out map[string]map[string]struct{}) {
	for name, s := range states {
		path := joinPath(prefix, name)
		if s != nil && (len(s.On) > 0 || len(s.IntentAliases) > 0) {
			set := make(map[string]struct{}, len(s.On)+len(s.IntentAliases))
			for intentName := range s.On {
				set[intentName] = struct{}{}
			}
			// Also count IntentAliases entries — they're the author-written
			// intent names BEFORE the import rewriter renamed the on: arc
			// (e.g. `start` was the name in stories/bugfix/rooms/idle.yaml
			// and became `core__bf__start` after the dev-story → kitsoki-dev
			// fold). The runtime emit_intent dispatcher
			// (machine.resolveEmittedIntentName) walks IntentAliases at
			// dispatch time to honour the original name, so the static
			// validator should accept the same set of names — otherwise an
			// imported room's own on_enter `emit_intent: <local-name>` fails
			// to load through the fold while working standalone.
			for aliasName := range s.IntentAliases {
				set[aliasName] = struct{}{}
			}
			out[path] = set
		}
		if s != nil && len(s.States) > 0 {
			collectStateOnKeys(path, s.States, out)
		}
	}
}

// intentReachable returns true when `name` appears on `statePath`'s
// declared `on:` arcs, or on any ancestor compound's `on:`. The
// wildcard `*` arc is intentionally NOT treated as a match for an
// emit_intent value — `*` is a fall-through handler for arbitrary
// names, not an explicit target the author intended for synthetic
// self-dispatch. If the state truly should accept a synthesised
// dynamic intent name, declare a concrete `on: <name>` arc.
func intentReachable(statePath, name string, stateOnKeys map[string]map[string]struct{}) bool {
	if name == "" {
		return false
	}
	path := statePath
	for {
		if set, ok := stateOnKeys[path]; ok {
			if _, found := set[name]; found {
				return true
			}
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			return false
		}
		path = path[:idx]
	}
}

// joinPath combines an optional parent prefix and a child name using dot separator.
// The design uses "bar.dark" style; YAML authors write "../../foyer" for
// relative refs but we validate using the canonical dotted form.
func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// validateStates recurses through the state tree, checking every state for:
//   - Valid intent references in on: blocks (global or local).
//   - Valid target state paths in transitions.
//   - Valid world key references in relevant_world.
//   - compound states: initial child must exist.
//   - invoke: host.* effects reference only declared hosts.
//   - `with.agent:` on host.oracle.* effects resolves to a declared agent.
//
// stateOnKeys maps each state path to the set of intent names declared
// in that state's `on:` block. It is used to statically validate
// `emit_intent:` references on transition / on_enter effects.
// validateStates' ancestorIntents parameter carries the union of all
// `intents:` declarations on every compound-state ancestor on the path
// from the root to the current state. SCXML-style intent inheritance
// means a child state can fire an `on:` arc declared on a parent (the
// runtime machine in internal/machine/ walks the compound stack to
// resolve intents). The loader's choice cross-reference must use the
// same scope; otherwise a child `choice:` referencing an ancestor-
// declared intent would fail load with "intent not declared". A nil /
// empty map is fine — resolution falls through to the global intent
// library declared at the AppDef level.
func validateStates(
	file string,
	prefix string,
	states map[string]*State,
	globalIntents map[string]struct{},
	globalIntentDefs map[string]Intent,
	ancestorIntents map[string]Intent,
	worldKeys map[string]struct{},
	allPaths map[string]struct{},
	stateOnKeys map[string]map[string]struct{},
	allowedHosts map[string]struct{},
	declaredAgents map[string]struct{},
	errs *[]error,
) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	// Sort keys for stable, deterministic error ordering.
	names := sortedKeys(states)
	for _, name := range names {
		s := states[name]
		if s == nil {
			continue
		}
		statePath := joinPath(prefix, name)

		// Gather local intent names for this state.
		localIntents := make(map[string]struct{}, len(s.Intents))
		for k := range s.Intents {
			localIntents[k] = struct{}{}
		}

		// Compose the intents-in-scope map used by the choice cross-ref:
		// every intent declared on any compound-state ancestor PLUS this
		// state's own intents, with state-local entries shadowing
		// ancestor entries when names collide. Mirrors how the runtime
		// machine resolves intents up the compound stack so a child
		// `choice:` may reference an intent declared on a parent.
		inScopeIntents := make(map[string]Intent, len(ancestorIntents)+len(s.Intents))
		for k, v := range ancestorIntents {
			inScopeIntents[k] = v
		}
		for k, v := range s.Intents {
			inScopeIntents[k] = v
		}

		// Validate relevant_world references.
		for _, wk := range s.RelevantWorld {
			if _, ok := worldKeys[wk]; !ok {
				addErr(fmt.Sprintf("state %q: relevant_world key %q is not declared in world schema", statePath, wk))
			}
		}

		// Validate default_intent (the free-text sink): it must be reachable
		// from this state (have an on: arc) and declare exactly one required
		// string slot, since the engine fills that slot with the whole
		// unmatched utterance. Both On keys and DefaultIntent are import-
		// rewritten consistently, so they match here regardless of fold depth.
		if di := s.DefaultIntent; di != "" {
			if _, ok := s.On[di]; !ok {
				addErr(fmt.Sprintf("state %q: default_intent %q has no matching on: arc in this state", statePath, di))
			}
			idef, found := inScopeIntents[di]
			if !found {
				idef, found = globalIntentDefs[di]
			}
			switch {
			case !found:
				addErr(fmt.Sprintf("state %q: default_intent %q is not a declared intent", statePath, di))
			default:
				reqCount, reqString := 0, true
				for _, sl := range idef.Slots {
					if !sl.Required {
						continue
					}
					reqCount++
					if sl.Type != "" && sl.Type != "string" {
						reqString = false
					}
				}
				if reqCount != 1 || !reqString {
					addErr(fmt.Sprintf("state %q: default_intent %q must declare exactly one required string slot to receive the free-text utterance", statePath, di))
				}
			}
		}

		// Validate the typed view payload (Phase A of the view-elements
		// proposal). Catches unknown element kinds, missing required
		// element fields, and non-string kv values at load time so authors
		// don't get a Phase-D renderer panic for a YAML-shape problem.
		if err := s.View.Validate(); err != nil {
			addErr(fmt.Sprintf("state %q: %v", statePath, err))
		} else {
			// Phase A of the choice-widget proposal — cross-reference
			// walk over choice elements. Runs after structural Validate
			// passes; pulls in the surrounding state-local + global
			// intents so item/element intent refs can be resolved to
			// concrete Slot maps for slot-key existence checks.
			for i, el := range s.View.Elements {
				if el.Kind != "choice" {
					continue
				}
				if err := validateChoiceCrossRefs(el, globalIntentDefs, inScopeIntents); err != nil {
					addErr(fmt.Sprintf("state %q: view[%d] (choice): %v", statePath, i, err))
				}
			}
		}

		// Validate transcript/theme: only allowed on top-level (room)
		// states. prefix is empty exactly at the top of def.States; any
		// nested level carries the parent's dotted path. See the
		// top-level (room) states only.
		if prefix != "" {
			if s.Transcript != "" {
				addErr(fmt.Sprintf("state %q: transcript: only allowed on top-level (room) states", statePath))
			}
			if s.Theme != "" {
				addErr(fmt.Sprintf("state %q: theme: only allowed on top-level (room) states", statePath))
			}
		} else {
			switch s.Transcript {
			case "", "persistent", "transient":
				// ok
			default:
				addErr(fmt.Sprintf("state %q: transcript: %q is not one of \"persistent\", \"transient\"", statePath, s.Transcript))
			}
		}

		// Validate on: intent names, transition targets, and effect hosts.
		intentNames := sortedKeys(s.On)
		for _, intentName := range intentNames {
			// Wildcard "*" is always allowed.
			if intentName != "*" {
				_, isGlobal := globalIntents[intentName]
				_, isLocal := localIntents[intentName]
				if !isGlobal && !isLocal {
					addErr(fmt.Sprintf("state %q: on: %q — intent not declared in intents library or state.intents", statePath, intentName))
				}
			}
			for _, tr := range s.On[intentName] {
				if err := validateTransitionTarget(file, statePath, tr.Target, allPaths); err != nil {
					*errs = append(*errs, err)
				}
				if err := tr.View.Validate(); err != nil {
					addErr(fmt.Sprintf("state %q intent %q: transition view: %v", statePath, intentName, err))
				} else {
					// Cross-reference choice elements in transition views
					// against the same intents map (see docs/stories/choice-widget.md).
					for vi, el := range tr.View.Elements {
						if el.Kind != "choice" {
							continue
						}
						if err := validateChoiceCrossRefs(el, globalIntentDefs, inScopeIntents); err != nil {
							addErr(fmt.Sprintf("state %q intent %q: transition view[%d] (choice): %v", statePath, intentName, vi, err))
						}
					}
				}
				// Validate invoke: host.* effects against the allow-list.
				for i, eff := range tr.Effects {
					if eff.Invoke != "" && len(allowedHosts) > 0 {
						if _, ok := allowedHosts[eff.Invoke]; !ok {
							addErr(fmt.Sprintf("state %q intent %q: effect invoke %q is not declared in app hosts", statePath, intentName, eff.Invoke))
						}
					}
					validateAgentRef(file, fmt.Sprintf("state %q intent %q effects[%d]", statePath, intentName, i), eff, declaredAgents, errs)
					validateBackgroundEffect(file, fmt.Sprintf("state %q intent %q effects[%d]", statePath, intentName, i), statePath, eff, allowedHosts, declaredAgents, allPaths, stateOnKeys, errs)
				}
			}
		}
		// Validate on_enter effects.
		for i, eff := range s.OnEnter {
			if eff.Invoke != "" && len(allowedHosts) > 0 {
				if _, ok := allowedHosts[eff.Invoke]; !ok {
					addErr(fmt.Sprintf("state %q: on_enter invoke %q is not declared in app hosts", statePath, eff.Invoke))
				}
			}
			validateAgentRef(file, fmt.Sprintf("state %q on_enter[%d]", statePath, i), eff, declaredAgents, errs)
			validateBackgroundEffect(file, fmt.Sprintf("state %q on_enter[%d]", statePath, i), statePath, eff, allowedHosts, declaredAgents, allPaths, stateOnKeys, errs)
		}

		// Validate the gate decider override (execution-modes proposal).
		switch s.Decider {
		case "", "human", "llm":
		default:
			addErr(fmt.Sprintf("state %q: decider %q is invalid (want \"\", \"human\", or \"llm\")", statePath, s.Decider))
		}

		// Validate Timeout: parse the duration and resolve the target.
		if s.Timeout != nil {
			if s.Timeout.After == "" {
				addErr(fmt.Sprintf("state %q: timeout: missing 'after' field", statePath))
			} else if !strings.Contains(s.Timeout.After, "{{") {
				if _, err := ParseDuration(s.Timeout.After); err != nil {
					addErr(fmt.Sprintf("state %q: timeout.after %q: %v", statePath, s.Timeout.After, err))
				}
			}
			if s.Timeout.Target == "" {
				addErr(fmt.Sprintf("state %q: timeout: missing 'target' field", statePath))
			} else if err := validateTransitionTarget(file, statePath, s.Timeout.Target, allPaths); err != nil {
				*errs = append(*errs, err)
			}
		}

		// Validate compound state's initial child.
		if s.Type == "compound" && s.Initial != "" {
			// Initial may be a template expression; only validate literal references.
			// Templated forms — expr-lang "{{ … }}" or pongo2 block tags "{% … %}"
			// — are evaluated at runtime; skip the literal-child check for those.
			if !strings.Contains(s.Initial, "{{") && !strings.Contains(s.Initial, "{%") {
				childPath := joinPath(statePath, s.Initial)
				if _, ok := allPaths[childPath]; !ok {
					addErr(fmt.Sprintf("state %q: initial child %q does not exist", statePath, s.Initial))
				}
			}
		}

		// Validate parallel state shape: each parallel state
		// must declare ≥2 child regions and must not declare an `initial:`
		// field on the parent (each region picks its own initial).
		if s.Type == "parallel" {
			if len(s.States) < 2 {
				addErr(fmt.Sprintf("state %q: parallel state must declare at least 2 child regions (got %d)", statePath, len(s.States)))
			}
			if s.Initial != "" {
				addErr(fmt.Sprintf("state %q: parallel state must not declare initial: — each region picks its own initial", statePath))
			}
		}

		// Recurse into child states. Pass the in-scope intent set as the
		// new ancestor scope so grandchildren also inherit this state's
		// intent declarations.
		if len(s.States) > 0 {
			validateStates(file, statePath, s.States, globalIntents, globalIntentDefs, inScopeIntents, worldKeys, allPaths, stateOnKeys, allowedHosts, declaredAgents, errs)
		}
	}
}

// validateTransitionTarget checks that a transition target refers to a declared
// state. It handles the special forms:
//   - "." → self; always valid.
//   - ".." prefixes → relative paths resolved against the current state path.
//   - Otherwise treated as an absolute state path.
//   - Targets containing "{{" are template expressions evaluated at runtime; skip validation.
func validateTransitionTarget(file, statePath, target string, allPaths map[string]struct{}) error {
	if target == "" || target == "." {
		return nil
	}
	// Template expressions are evaluated at runtime; cannot validate statically.
	if strings.Contains(target, "{{") {
		return nil
	}
	// Resolve relative targets ("../../foyer").
	resolved := resolveTarget(statePath, target)
	if _, ok := allPaths[resolved]; !ok {
		return &ValidationError{
			File:    file,
			Message: fmt.Sprintf("state %q: transition target %q (resolved: %q) does not exist", statePath, target, resolved),
		}
	}
	return nil
}

// resolveTarget resolves a YAML transition target relative to a state path.
// The design uses slash-separated relative refs like "../../foyer" but
// the internal state paths use dots. We accept both slash-based relative refs
// and direct dotted absolute references.
func resolveTarget(statePath, target string) string {
	// Absolute reference (no leading "..") — try as-is first.
	if !strings.HasPrefix(target, "..") {
		// Some absolute refs may use slash notation; normalise to dots.
		normalised := strings.ReplaceAll(target, "/", ".")
		return normalised
	}
	// Relative reference. Split the current path into segments.
	parts := strings.Split(statePath, ".")
	segs := strings.Split(target, "/")
	for _, seg := range segs {
		if seg == ".." {
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		} else if seg != "." && seg != "" {
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, ".")
}

// validateBackgroundEffect checks load-time rules for background: and on_complete:.
//
//   - background: true requires invoke: to be non-empty.
//   - effects inside on_complete: must NOT have background: true (recursively).
//   - invoke: inside on_complete: must reference only declared hosts (allowedHosts).
//   - `with.agent:` inside on_complete: must resolve to a declared agent.
//   - target: outside on_complete: is rejected (use a normal transition).
//   - inside on_complete:, target: must not be combined with set / increment /
//     say / invoke / bind on the same effect (mixing mutation with a
//     synthetic transition is semantically muddled — declare them on
//     separate effects in the same chain).
//
// The eff argument is always the "outer" effect; on_complete entries are
// validated via the loop over eff.OnComplete with insideOnComplete=true on
// the recursive descent. originStatePath is the dotted-path of the state
// that owns the effect chain (used to resolve relative target: refs); it
// may be empty for proposal-execute effects (no owning state).
func validateBackgroundEffect(file, location, originStatePath string, eff Effect, allowedHosts, declaredAgents, allStatePaths map[string]struct{}, stateOnKeys map[string]map[string]struct{}, errs *[]error) {
	validateBackgroundEffectAware(file, location, originStatePath, eff, false /* outer effect, not inside on_complete */, allowedHosts, declaredAgents, allStatePaths, stateOnKeys, errs)
}

// validateOnCompleteEffect is the entry point used at proposal-execute call
// sites where the iterated effect is already an on_complete entry (the
// caller has unrolled the first level of the on_complete: list before
// invoking). Target rules apply directly to eff in addition to its
// descendants.
func validateOnCompleteEffect(file, location, originStatePath string, eff Effect, allowedHosts, declaredAgents, allStatePaths map[string]struct{}, stateOnKeys map[string]map[string]struct{}, errs *[]error) {
	validateBackgroundEffectAware(file, location, originStatePath, eff, true /* eff is already inside on_complete */, allowedHosts, declaredAgents, allStatePaths, stateOnKeys, errs)
}

// validateBackgroundEffectAware is the on_complete-aware implementation.
// insideOnComplete is true when eff itself is an entry inside a parent's
// on_complete: list — Target is only legal in that case.
func validateBackgroundEffectAware(file, location, originStatePath string, eff Effect, insideOnComplete bool, allowedHosts, declaredAgents, allStatePaths map[string]struct{}, stateOnKeys map[string]map[string]struct{}, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}
	if eff.Background && eff.Invoke == "" {
		addErr(fmt.Sprintf("%s: background: true requires invoke: to be set", location))
	}
	// once: caches the invoke's bind targets — with nothing to cache it is
	// meaningless. Fail fast, mirroring the background: invariant above.
	if eff.Once && len(eff.Bind) == 0 {
		addErr(fmt.Sprintf("%s: once: true requires a non-empty bind: (the bind target is the cache that arms the skip)", location))
	}
	// target: is only meaningful inside on_complete: blocks. The async
	// terminal context is the only place where a synthetic transition
	// makes sense — normal effects should use a regular transition's
	// target: instead.
	if eff.Target != "" && !insideOnComplete {
		addErr(fmt.Sprintf("%s: target: is only allowed inside on_complete: effects (it fires a synthetic transition once the background job terminates); use a normal transition's target: for in-turn transitions", location))
	}
	// target: combined with a mutation on the same effect is muddled —
	// split into two effects so the chain reads top-to-bottom as
	// "mutate, then transition".
	if eff.Target != "" && insideOnComplete {
		var conflicts []string
		if len(eff.Set) > 0 {
			conflicts = append(conflicts, "set")
		}
		if len(eff.Increment) > 0 {
			conflicts = append(conflicts, "increment")
		}
		if eff.Say != "" {
			conflicts = append(conflicts, "say")
		}
		if eff.Invoke != "" {
			conflicts = append(conflicts, "invoke")
		}
		if len(eff.Bind) > 0 {
			conflicts = append(conflicts, "bind")
		}
		if len(conflicts) > 0 {
			addErr(fmt.Sprintf("%s: target: cannot be combined with %s on the same effect (split into separate effects in the on_complete: chain — mutation first, transition last)", location, strings.Join(conflicts, "/")))
		}
		// Resolve and verify the target state exists. Template targets
		// (containing "{{") are evaluated at runtime; skip statically.
		if eff.Target != "" && !strings.Contains(eff.Target, "{{") {
			resolved := resolveTarget(originStatePath, eff.Target)
			if _, ok := allStatePaths[resolved]; !ok {
				addErr(fmt.Sprintf("%s: target %q (resolved: %q) does not exist", location, eff.Target, resolved))
			}
		}
	}

	// emit_intent: validation.
	//   - Mutually exclusive with target: on the same effect (transition
	//     and self-dispatch are different shapes; declare them separately).
	//   - When the value is not a template, the named intent must appear
	//     in the on: arcs of the owning state (or an ancestor compound).
	//     A template value (`{{ ... }}`) is checked at runtime by the
	//     emit dispatch path — the loader can't know what it'll resolve
	//     to.
	if eff.EmitIntent != "" {
		if eff.Target != "" {
			addErr(fmt.Sprintf("%s: emit_intent: cannot be combined with target: on the same effect (split into separate effects)", location))
		}
		if !strings.Contains(eff.EmitIntent, "{{") {
			if !intentReachable(originStatePath, eff.EmitIntent, stateOnKeys) {
				addErr(fmt.Sprintf("%s: emit_intent %q is not declared on state %q's on: arcs (nor any ancestor's)", location, eff.EmitIntent, originStatePath))
			}
		}
	}
	for i, child := range eff.OnComplete {
		loc := fmt.Sprintf("%s on_complete[%d]", location, i)
		if child.Background {
			addErr(fmt.Sprintf("%s: background: true is not allowed inside on_complete:", loc))
		}
		// Validate invoke: host.* inside on_complete: against the allow-list.
		if child.Invoke != "" && len(allowedHosts) > 0 {
			if _, ok := allowedHosts[child.Invoke]; !ok {
				addErr(fmt.Sprintf("%s: invoke %q is not declared in app hosts", loc, child.Invoke))
			}
		}
		// Validate any `agent: <name>` on the child effect's with: block.
		validateAgentRef(file, loc, child, declaredAgents, errs)
		// Recursively reject nested on_complete with background and validate target rules.
		validateBackgroundEffectAware(file, loc, originStatePath, child, true /* this child IS inside on_complete */, allowedHosts, declaredAgents, allStatePaths, stateOnKeys, errs)
	}
}

// validateAgentRef checks that, when a host.oracle.* effect declares
// `with: { agent: <name> }`, the name resolves to an entry in
// AppDef.Agents. Effects that omit `agent:` (or whose Invoke is not a
// host.oracle.* handler) are silently ignored — agent: is host-handler-
// specific metadata, not a global field. Templated values (containing
// "{{") are skipped because they cannot be resolved statically.
func validateAgentRef(file, location string, eff Effect, declaredAgents map[string]struct{}, errs *[]error) {
	if eff.With == nil {
		return
	}
	raw, ok := eff.With["agent"]
	if !ok {
		return
	}
	name, ok := raw.(string)
	if !ok || name == "" {
		return
	}
	if strings.Contains(name, "{{") {
		// Template — evaluated at runtime; cannot validate statically.
		return
	}
	// Only host.oracle.* handlers consume agent:; flag misuse on others as
	// a useful authoring error rather than a silent typo.
	if eff.Invoke != "" && !strings.HasPrefix(eff.Invoke, "host.oracle.") {
		*errs = append(*errs, &ValidationError{
			File:    file,
			Message: fmt.Sprintf("%s: with.agent is only meaningful on host.oracle.* invocations (got invoke %q)", location, eff.Invoke),
		})
		return
	}
	if _, found := declaredAgents[name]; !found {
		*errs = append(*errs, &ValidationError{
			File:    file,
			Message: fmt.Sprintf("%s: with.agent %q is not declared in agents", location, name),
		})
	}
}

// oracleMutationTools is the set of tools that are forbidden for
// host.oracle.ask, host.oracle.decide, and host.oracle.extract (read-only
// verbs). This mirrors the runtime check in oracle_decide.go and
// oracle_ask.go. The loader check here is the primary enforcement point.
var oracleMutationTools = map[string]bool{
	"host.Edit":         true,
	"host.Write":        true,
	"host.NotebookEdit": true,
}

// readOnlyArgv0s is a best-effort allowlist of programs whose argv0 is
// clearly read-only (cat, grep, git, jq, etc.). Used by the decide/extract
// validator warn-line check: a post_cmd whose argv0 is NOT on this list
// gets a warn-line flagging it as potentially mutating.
var readOnlyArgv0s = map[string]bool{
	"cat": true, "grep": true, "rg": true, "ripgrep": true,
	"git": true, "jq": true, "yq": true, "find": true,
	"ls": true, "head": true, "tail": true, "wc": true,
	"diff": true, "awk": true, "sed": true, "sort": true,
	"echo": true, "true": true, "false": true, "stat": true,
	"file": true, "which": true, "type": true,
}

// validateOracleVerbCrossChecks walks every host.oracle.* effect across the
// entire AppDef (states + proposals) and enforces three rules from the
// oracle-split proposal:
//
//  1. (M6a) ask/decide/extract agents must not declare Edit/Write/NotebookEdit.
//  2. (M6b) task effects must have acceptance.schema set in their with: block.
//  3. (M3)  task effects referencing an agent that declares
//     external_side_effect: false but has WebFetch or WebSearch in its tool
//     surface are rejected at load time (the declarations contradict each
//     other; the agent would behave as Mode C despite claiming Mode A/B).
//
// Additionally (M6c), a warn-line is emitted when decide/extract effects
// declare a validator.post_cmd whose argv0 is not on the known-read-only
// allowlist — the runtime sandbox catches actual mutations, but the warning
// surfaces the potential problem at app-load.
func validateOracleVerbCrossChecks(file string, def *AppDef, errs *[]error) {
	if def == nil {
		return
	}
	// Walk all effects in the state tree.
	walkAllEffects(def.States, func(loc string, eff Effect) {
		checkOracleEffect(file, loc, eff, def.Agents, errs)
	})
	// Walk proposal execute effects.
	for pname, pk := range def.Proposals {
		if pk == nil || pk.Execute == nil {
			continue
		}
		loc := fmt.Sprintf("proposal %q execute", pname)
		checkOracleEffect(file, loc, Effect{
			Invoke: pk.Execute.Invoke,
			With:   pk.Execute.With,
		}, def.Agents, errs)
		for i, child := range pk.Execute.OnComplete {
			checkOracleEffect(file, fmt.Sprintf("%s on_complete[%d]", loc, i), child, def.Agents, errs)
		}
	}
}

// walkAllEffects visits every Effect across the state tree, calling fn
// with a human-readable location string and the Effect.
func walkAllEffects(states map[string]*State, fn func(loc string, eff Effect)) {
	walkAllEffectsPrefix("", states, fn)
}

func walkAllEffectsPrefix(prefix string, states map[string]*State, fn func(loc string, eff Effect)) {
	for name, s := range states {
		if s == nil {
			continue
		}
		stateLoc := name
		if prefix != "" {
			stateLoc = prefix + "." + name
		}
		for i, eff := range s.OnEnter {
			fn(fmt.Sprintf("state %q on_enter[%d]", stateLoc, i), eff)
			for j, child := range eff.OnComplete {
				fn(fmt.Sprintf("state %q on_enter[%d] on_complete[%d]", stateLoc, i, j), child)
			}
		}
		for intentName, arcs := range s.On {
			for ai, arc := range arcs {
				for ei, eff := range arc.Effects {
					fn(fmt.Sprintf("state %q intent %q arc[%d] effect[%d]", stateLoc, intentName, ai, ei), eff)
					for j, child := range eff.OnComplete {
						fn(fmt.Sprintf("state %q intent %q arc[%d] effect[%d] on_complete[%d]", stateLoc, intentName, ai, ei, j), child)
					}
				}
			}
		}
		walkAllEffectsPrefix(stateLoc, s.States, fn)
	}
}

// checkOracleEffect enforces M6 and M3 rules on one effect.
func checkOracleEffect(file, loc string, eff Effect, agents map[string]*AgentDecl, errs *[]error) {
	if eff.With == nil {
		return
	}
	verb := eff.Invoke
	if !strings.HasPrefix(verb, "host.oracle.") {
		return
	}
	shortVerb := strings.TrimPrefix(verb, "host.oracle.")

	agentName, _ := eff.With["agent"].(string)
	if strings.Contains(agentName, "{{") {
		// Templated agent name — cannot resolve statically.
		agentName = ""
	}
	var decl *AgentDecl
	if agentName != "" && agents != nil {
		decl = agents[agentName]
	}

	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	switch shortVerb {
	case "ask", "decide", "extract":
		// M6a: reject mutation tools.
		if decl != nil {
			for _, t := range decl.Tools {
				if oracleMutationTools[t] {
					addErr(fmt.Sprintf(
						"%s: agent %q declares mutation tool %q — not permitted for host.oracle.%s (read-only verb); use host.oracle.task for agentic work",
						loc, agentName, t, shortVerb,
					))
				}
			}
		}
		// M6c: warn when decide/extract validator.post_cmd argv0 is not read-only.
		if shortVerb == "decide" || shortVerb == "extract" {
			if validatorBlock, _ := eff.With["validator"].(map[string]any); validatorBlock != nil {
				if postCmd, _ := validatorBlock["post_cmd"].(string); strings.TrimSpace(postCmd) != "" {
					parts := strings.Fields(postCmd)
					if len(parts) > 0 {
						argv0 := filepath.Base(parts[0])
						if !readOnlyArgv0s[argv0] {
							slog.Warn("oracle verb cross-check: decide/extract validator.post_cmd argv0 is not on the read-only allowlist; runtime sandbox enforces isolation",
								"location", loc, "argv0", argv0, "file", file)
						}
					}
				}
			}
		}

	case "task":
		// M6b: require acceptance.schema.
		{
			schemaVal := ""
			if acceptanceBlock, _ := eff.With["acceptance"].(map[string]any); acceptanceBlock != nil {
				schemaVal, _ = acceptanceBlock["schema"].(string)
			}
			if strings.TrimSpace(schemaVal) == "" {
				addErr(fmt.Sprintf(
					"%s: host.oracle.task requires acceptance.schema to be set in the with: block",
					loc,
				))
			}
		}

		// M3: hard-fail when external_side_effect: false contradicts WebFetch/WebSearch.
		if decl != nil && decl.ExternalSideEffect != nil && !*decl.ExternalSideEffect {
			for _, t := range decl.Tools {
				if t == "host.WebFetch" || t == "host.WebSearch" {
					addErr(fmt.Sprintf(
						"%s: agent %q declares external_side_effect: false but has %q in tools — these declarations contradict each other; "+
							"an agent with network tools implies external side effects (Mode C). "+
							"Remove external_side_effect: false or remove the network tool.",
						loc, agentName, t,
					))
				}
			}
		}
	}
}

// validateLocalLLMGrammarSubset enforces, at load time, that every decide
// effect whose `oracle:` alias resolves to a builtin.local_llm plugin with
// grammar: true points at a schema inside llama.cpp's translatable grammar
// subset. Without this, an out-of-subset schema would fail open silently at
// runtime (llama.cpp decodes unconstrained yet returns 200), defeating the
// predictability the grammar tier exists to buy.
//
// Scope: the check covers the decide verb only — its schema is a string path in
// the effect's with.schema. extract (with.schema_path) and ask source differently
// and are deferred as a follow-up. An empty `oracle:` alias resolves to
// oracle.claude (not local_llm) and is unaffected. Schema paths resolve relative
// to def.BaseDir (matching the runtime app-dir resolution); absolute paths are
// read as-is. A schema that cannot be read is reported here too, since the gate
// cannot vouch for a schema it never saw.
func validateLocalLLMGrammarSubset(file string, def *AppDef, errs *[]error) {
	if def == nil || len(def.OraclePlugins) == 0 {
		return
	}
	// Collect aliases that are builtin.local_llm with grammar: true.
	grammarLocalLLM := make(map[string]bool)
	for alias, decl := range def.OraclePlugins {
		if decl != nil && decl.Plugin == "builtin.local_llm" && decl.Grammar {
			grammarLocalLLM[alias] = true
		}
	}
	if len(grammarLocalLLM) == 0 {
		return
	}

	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	walkAllEffects(def.States, func(loc string, eff Effect) {
		if eff.Invoke != "host.oracle.decide" {
			return
		}
		if !grammarLocalLLM[eff.OraclePlugin] {
			return
		}
		schemaPath, _ := eff.With["schema"].(string)
		schemaPath = strings.TrimSpace(schemaPath)
		if schemaPath == "" {
			// Missing schema is reported by the decide handler's own contract;
			// the grammar gate has nothing to inspect.
			return
		}
		if strings.Contains(schemaPath, "{{") {
			// Templated schema path — cannot resolve statically; skip.
			return
		}
		resolved := schemaPath
		if !filepath.IsAbs(resolved) && def.BaseDir != "" {
			resolved = filepath.Join(def.BaseDir, resolved)
		}
		raw, readErr := os.ReadFile(resolved)
		if readErr != nil {
			addErr(fmt.Sprintf(
				"%s: oracle %q is builtin.local_llm with grammar: true but its decide schema %q could not be read: %v",
				loc, eff.OraclePlugin, schemaPath, readErr,
			))
			return
		}
		if subErr := grammar.SubsetOK(raw); subErr != nil {
			addErr(fmt.Sprintf(
				"%s: oracle %q is builtin.local_llm with grammar: true but its decide schema %q is outside the llama.cpp grammar subset: %v",
				loc, eff.OraclePlugin, schemaPath, subErr,
			))
		}
	})
}

// validateStarlarkEffects enforces the load-time contract for every
// host.starlark.run effect: with.script must be a non-empty string, resolve to
// a path inside the app root (no `../` escape), and BOTH the .star file and its
// .star.yaml sidecar must exist and the sidecar must parse. This is the
// fail-fast counterpart to the runtime sandbox — an app with a missing script
// or malformed sidecar refuses to load rather than bouncing through on_error:
// on the first turn that reaches the room.
func validateStarlarkEffects(file string, def *AppDef, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	walkAllEffects(def.States, func(loc string, eff Effect) {
		if eff.Invoke != "host.starlark.run" {
			return
		}
		rawScript, _ := eff.With["script"].(string)
		rawScript = strings.TrimSpace(rawScript)
		if rawScript == "" {
			addErr(fmt.Sprintf("%s: host.starlark.run requires a non-empty with.script", loc))
			return
		}
		// Templated paths cannot be resolved statically; skip (the runtime
		// adapter + sandbox validate them once rendered).
		if strings.Contains(rawScript, "{{") {
			return
		}

		// Resolve against the app root and reject any path that escapes it via
		// `../`. Both story-root and app-level scripts/ dirs are fine — only an
		// escape outside BaseDir is rejected.
		resolved := rawScript
		if !filepath.IsAbs(resolved) && def.BaseDir != "" {
			resolved = filepath.Join(def.BaseDir, resolved)
		}
		if def.BaseDir != "" {
			rel, relErr := filepath.Rel(def.BaseDir, filepath.Clean(resolved))
			if relErr != nil || strings.HasPrefix(rel, "..") {
				addErr(fmt.Sprintf("%s: host.starlark.run script %q resolves outside the app root", loc, rawScript))
				return
			}
		}

		if _, statErr := os.Stat(resolved); statErr != nil {
			addErr(fmt.Sprintf("%s: host.starlark.run script %q not found (resolved to %q)", loc, rawScript, resolved))
			return
		}

		// The sidecar is mandatory: it is authoritative over the script's
		// inputs/outputs. A missing or malformed sidecar is a load error.
		sidecarPath := resolved + ".yaml"
		raw, readErr := os.ReadFile(sidecarPath)
		if readErr != nil {
			addErr(fmt.Sprintf("%s: host.starlark.run script %q has no sidecar (expected %q): %v", loc, rawScript, sidecarPath, readErr))
			return
		}
		if _, parseErr := starlarkhost.ParseSidecar(raw); parseErr != nil {
			addErr(fmt.Sprintf("%s: host.starlark.run sidecar %q is malformed: %v", loc, sidecarPath, parseErr))
		}
	})
}

// metaEnvVarRE matches `$NAME` and `${NAME}` tokens in a cwd: string.
var metaEnvVarRE = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// validateMetaModes checks every entry in def.MetaModes for required fields,
// per-group trigger uniqueness, intent-name collisions, default-verb
// rules, and resolves cwd: env vars. Successful cwd expansion is
// written back into the struct.
//
// Key grammar:
//   - "group.verb" — grouped mode. The part before "." MUST equal
//     m.Group and the part after MUST equal m.Trigger.
//   - bare token (no ".") — back-compat path. m.Group is treated as
//     the key itself if unset; the validator does not error on
//     un-namespaced keys to keep older YAML loading.
//
// Trigger uniqueness is per-group: `story.bug` and `kitsoki.bug` may
// both have Trigger=`bug`, but two modes inside the `story` group
// claiming `bug` is an error.
//
// Default-verb rule: each group with ≥2 modes MUST have exactly one
// mode flagged `default: true` so bare `/meta <group>` has an
// unambiguous target. Groups with a single mode skip the rule (that
// one mode is implicitly the default).
func validateMetaModes(file string, def *AppDef, errs *[]error) {
	if len(def.MetaModes) == 0 {
		return
	}
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	// First pass: per-mode field validation, Group/key consistency, cwd
	// expansion. Track triggers PER GROUP for the uniqueness check.
	// Group → trigger → owning key.
	triggerOwner := make(map[string]map[string]string, len(def.MetaModes))
	// Track per-group census so the default-verb rule can run after.
	type groupRow struct {
		key       string
		isDefault bool
	}
	groupRows := make(map[string][]groupRow)

	for _, name := range sortedKeys(def.MetaModes) {
		m := def.MetaModes[name]
		if m == nil {
			addErr(fmt.Sprintf("meta_mode %q: empty definition", name))
			continue
		}
		if m.Trigger == "" {
			addErr(fmt.Sprintf("meta_mode %q: trigger is required", name))
		}
		if m.Agent == "" {
			addErr(fmt.Sprintf("meta_mode %q: agent is required", name))
		}

		// Determine the effective group key. If the map key contains
		// ".", the part before "." must match m.Group and the part
		// after must match m.Trigger.
		effectiveGroup := m.Group
		if dot := strings.Index(name, "."); dot >= 0 {
			keyGroup := name[:dot]
			keyTrigger := name[dot+1:]
			if m.Group == "" {
				// Auto-fill Group from the key. The user didn't bother
				// to set it explicitly; the key supplies it.
				m.Group = keyGroup
				effectiveGroup = keyGroup
			} else if m.Group != keyGroup {
				addErr(fmt.Sprintf("meta_mode %q: group %q does not match key prefix %q (key must be %q.<trigger>)",
					name, m.Group, keyGroup, m.Group))
			}
			if m.Trigger != "" && m.Trigger != keyTrigger {
				addErr(fmt.Sprintf("meta_mode %q: trigger %q does not match key suffix %q (grouped key must be <group>.%q)",
					name, m.Trigger, keyTrigger, m.Trigger))
			}
		} else if effectiveGroup == "" {
			// Un-namespaced key: treat the key itself as the group for
			// uniqueness / default bookkeeping. This is the back-compat
			// path — an app declaring `meta_modes: { foo: {...} }`
			// keeps working.
			effectiveGroup = name
		}

		if m.Trigger != "" {
			byGroup := triggerOwner[effectiveGroup]
			if byGroup == nil {
				byGroup = make(map[string]string)
				triggerOwner[effectiveGroup] = byGroup
			}
			if prior, ok := byGroup[m.Trigger]; ok {
				addErr(fmt.Sprintf("meta_mode %q: trigger %q already claimed by meta_mode %q in group %q",
					name, m.Trigger, prior, effectiveGroup))
			} else {
				byGroup[m.Trigger] = name
				// Intent-collision check: the trigger only competes
				// with the global intent namespace for UN-NAMESPACED
				// modes (where the user types `/meta <trigger>` as a
				// single token). For grouped modes (`/meta <group>
				// <verb>`) the verb cannot collide with an intent —
				// the parse-time token order disambiguates. This lets
				// a story declare an `ask` intent without it clashing
				// with the builtin `story.ask` meta mode's `ask`
				// trigger.
				isGrouped := strings.Contains(name, ".") || m.Group != ""
				if !isGrouped {
					if _, clash := def.Intents[m.Trigger]; clash {
						addErr(fmt.Sprintf("meta_mode %q: trigger %q collides with a declared intent of the same name", name, m.Trigger))
					}
				}
			}
		}
		if m.Cwd != "" {
			expanded, expErr := expandMetaCwd(m.Cwd)
			if expErr != "" {
				addErr(fmt.Sprintf("meta_mode %q: cwd %q references unset env var %s", name, m.Cwd, expErr))
				continue
			}
			m.Cwd = expanded
		}

		groupRows[effectiveGroup] = append(groupRows[effectiveGroup], groupRow{key: name, isDefault: m.Default})
	}

	// Second pass: default-verb rule. Run in sorted-group order so the
	// error message order is deterministic.
	groupKeys := make([]string, 0, len(groupRows))
	for g := range groupRows {
		groupKeys = append(groupKeys, g)
	}
	sort.Strings(groupKeys)
	for _, g := range groupKeys {
		rows := groupRows[g]
		if len(rows) < 2 {
			// Single-mode groups skip the rule — that one mode is the
			// implicit default.
			continue
		}
		defaults := 0
		var defaultKeys []string
		for _, r := range rows {
			if r.isDefault {
				defaults++
				defaultKeys = append(defaultKeys, r.key)
			}
		}
		switch defaults {
		case 0:
			addErr(fmt.Sprintf("meta_mode group %q: no default verb declared (one of %d modes must set default: true)",
				g, len(rows)))
		case 1:
			// ok
		default:
			sort.Strings(defaultKeys)
			addErr(fmt.Sprintf("meta_mode group %q: %d modes flagged default: true (%s) — exactly one allowed",
				g, defaults, strings.Join(defaultKeys, ", ")))
		}
	}
}

// validateAgentReferences walks every site in the AppDef where an agent name
// can be selected (meta_modes[*].agent, off_path.agent) and asserts the
// referenced name resolves either in def.Agents or in
// agents.BuiltinNames(). Unknown references produce one error per site;
// the error names the offending agent and lists the known agents so the
// author can spot typos at a glance.
//
// Background-jobs sites are NOT walked here because kitsoki has no
// top-level `background_jobs:` YAML block today. When that type is
// introduced, add it to the site list and to the test fixture set.
func validateAgentReferences(file string, def *AppDef, errs *[]error) {
	if def == nil {
		return
	}

	// Build the known-name set: every key in def.Agents plus every
	// builtin name. The known set is the same regardless of which site
	// is referenced, so compute it once.
	known := make(map[string]struct{})
	for name := range def.Agents {
		known[name] = struct{}{}
	}
	for _, name := range agents.BuiltinNames() {
		known[name] = struct{}{}
	}

	// Sort known names once for stable, deterministic error messages.
	knownList := make([]string, 0, len(known))
	for name := range known {
		knownList = append(knownList, name)
	}
	sort.Strings(knownList)
	knownStr := strings.Join(knownList, ", ")

	addUnknown := func(name, site string) {
		*errs = append(*errs, &ValidationError{
			File: file,
			Message: fmt.Sprintf(
				"agent reference %q at %s is undefined (known agents: %s)",
				name, site, knownStr,
			),
		})
	}

	// meta_modes.<name>.agent — sort for stable error order.
	for _, modeName := range sortedKeys(def.MetaModes) {
		m := def.MetaModes[modeName]
		if m == nil || m.Agent == "" {
			continue
		}
		if _, ok := known[m.Agent]; !ok {
			addUnknown(m.Agent, fmt.Sprintf("meta_modes.%s.agent", modeName))
		}
	}

	// off_path.agent — single site.
	if def.OffPath != nil && def.OffPath.Agent != "" {
		if _, ok := known[def.OffPath.Agent]; !ok {
			addUnknown(def.OffPath.Agent, "off_path.agent")
		}
	}

	// background_jobs.<name>.agent is intentionally absent: no first-class
	// background_jobs YAML type exists today. Once it lands, walk it here.
}

// validateNoReachIntoChild implements the reach-into-child guard (see
// docs/stories/imports.md).
// A parent transition targeting `<alias>.<deeper>` couples the parent
// to the child's internals, which the import contract forbids. The canonical
// way to invoke a child is `target: <alias>` (the wrapper itself);
// `target: <alias>.<entry>` is also allowed since it's equivalent.
// Anything deeper is rejected with a clear message naming both the
// offending transition and the import's alias.
//
// Only targets authored at the parent level are walked — child
// transitions were rewritten to relative form (`../X`) by the
// import rewriter and don't hit this check.
func validateNoReachIntoChild(file string, def *AppDef, errs *[]error) {
	if def == nil || len(def.ImportWrappers) == 0 {
		return
	}
	walkTopLevelTargets(def.States, def.ImportWrappers, file, errs)
}

// walkTopLevelTargets visits every parent-level state (i.e. one that
// is not itself nested inside an imported child) and checks each
// transition target against the import-wrapper alias map.
//
// The "parent-level" distinction is by name: a top-level state that
// matches a key in ImportWrappers IS the wrapper (its internals are
// child-authored); every other top-level state is parent-authored.
// Wrapper internals are skipped because the rewriter already
// validated their relative refs at fold time.
func walkTopLevelTargets(states map[string]*State, wrappers map[string]*ImportWrapperInfo, file string, errs *[]error) {
	for name := range states {
		if _, isWrapper := wrappers[name]; isWrapper {
			continue
		}
		s := states[name]
		if s == nil {
			continue
		}
		checkStateTargetsAgainstWrappers(name, s, wrappers, file, errs)
	}
}

// checkStateTargetsAgainstWrappers walks one parent state's
// transitions, timeout, and on_enter on_error refs and rejects every
// target that reaches deeper than `<alias>.<entry>` for any declared
// import alias.
func checkStateTargetsAgainstWrappers(statePath string, s *State, wrappers map[string]*ImportWrapperInfo, file string, errs *[]error) {
	check := func(target, where string) {
		if target == "" || target == "." {
			return
		}
		if strings.Contains(target, "{{") {
			return
		}
		// Normalise slashes to dots — same shape resolveTarget uses.
		norm := strings.ReplaceAll(target, "/", ".")
		// Strip a leading `..` walk: a parent state targeting `../foo`
		// resolves to a sibling at the parent level, not into a child.
		if strings.HasPrefix(target, "..") {
			return
		}
		// Split into segments. Single-segment targets (`<alias>` alone)
		// are the canonical wrapper-invoke form; allow them.
		dot := strings.IndexByte(norm, '.')
		if dot < 0 {
			return
		}
		alias := norm[:dot]
		rest := norm[dot+1:]
		wrapper, isImported := wrappers[alias]
		if !isImported {
			return
		}
		// `<alias>.<entry>` is the entry; allow it. Anything past the
		// entry (e.g., `<alias>.<entry>.<sub>` or `<alias>.<not-entry>`)
		// is a reach-into-child.
		if rest == wrapper.Entry {
			return
		}
		*errs = append(*errs, &ValidationError{
			File: file,
			Message: fmt.Sprintf(
				"state %q %s targets %q which reaches into the imported child %q past its entry %q; use target: %q to invoke the child or have the child expose a new exit",
				statePath, where, target, alias, wrapper.Entry, alias,
			),
		})
	}

	for intent, list := range s.On {
		for i, tr := range list {
			check(tr.Target, fmt.Sprintf("on.%s[%d].target", intent, i))
		}
	}
	if s.Timeout != nil {
		check(s.Timeout.Target, "timeout.target")
	}
	for i, eff := range s.OnEnter {
		if eff.OnError != "" {
			check(eff.OnError, fmt.Sprintf("on_enter[%d].on_error", i))
		}
		if eff.Target != "" {
			check(eff.Target, fmt.Sprintf("on_enter[%d].target", i))
		}
	}
	for _, child := range s.States {
		if child != nil {
			// Nested parent compounds inherit the same rule.
			checkStateTargetsAgainstWrappers(statePath, child, wrappers, file, errs)
		}
	}
}

// expandMetaCwd resolves `$VAR` / `${VAR}` tokens in s against os.Environ.
// Returns (expanded, "") on success, or ("", varName) when any referenced
// var is unset. Bare `$$` literals are passed through.
func expandMetaCwd(s string) (string, string) {
	matches := metaEnvVarRE.FindAllStringSubmatchIndex(s, -1)
	for _, m := range matches {
		name := s[m[2]:m[3]]
		if _, ok := os.LookupEnv(name); !ok {
			return "", name
		}
	}
	return os.ExpandEnv(s), ""
}

// validateRouting walks every Intent (global + per-state) and every Slot
// (on those intents) and asserts the semantic-routing proposal's
// Phase-0 schema rules:
//
//   - Intent.Synonyms entries are non-empty after trim.
//   - Slot.Synonyms is only set on enum slots.
//   - Slot.Synonyms keys are present in Slot.Values.
//   - AppDef.Routing.SemanticHighBar > SemanticMidBar, both in [0, 1].
//
// Errors are appended to *errs as ValidationError, matching the look of
// the other validators in this file. The function visits global intents
// first (sorted) for deterministic error ordering, then descends into
// the state tree to pick up state-local intents.
func validateRouting(file string, def *AppDef, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	// Global intents.
	for _, name := range sortedKeys(def.Intents) {
		intent := def.Intents[name]
		validateIntentSynonyms(file, fmt.Sprintf("intent %q", name), intent, errs)
	}

	// Per-state intents — walk the full tree.
	walkStateIntents(def.States, "", func(statePath, intentName string, intent Intent) {
		loc := fmt.Sprintf("state %q intent %q", statePath, intentName)
		validateIntentSynonyms(file, loc, intent, errs)
	})

	// Routing block bar ordering.
	if def.Routing != nil {
		r := def.Routing
		if r.SemanticHighBar < 0 || r.SemanticHighBar > 1 {
			addErr(fmt.Sprintf("routing.semantic_high_bar: %.4f is outside [0, 1]", r.SemanticHighBar))
		}
		if r.SemanticMidBar < 0 || r.SemanticMidBar > 1 {
			addErr(fmt.Sprintf("routing.semantic_mid_bar: %.4f is outside [0, 1]", r.SemanticMidBar))
		}
		if r.SemanticHighBar > 0 && r.SemanticMidBar > 0 && !(r.SemanticHighBar > r.SemanticMidBar) {
			addErr(fmt.Sprintf("routing.semantic_high_bar (%.4f) must be greater than routing.semantic_mid_bar (%.4f)", r.SemanticHighBar, r.SemanticMidBar))
		}
	}
}

// validateIntentSynonyms checks one intent's Synonyms list and its slot
// Synonyms maps. `where` is the human-readable prefix used in error
// messages (e.g. `intent "ford"` or `state "foo" intent "bar"`).
func validateIntentSynonyms(file, where string, intent Intent, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}
	for i, s := range intent.Synonyms {
		if strings.TrimSpace(s) == "" {
			addErr(fmt.Sprintf("%s: synonyms[%d] is empty", where, i))
		}
	}
	// Iterate slot names sorted for deterministic error ordering.
	for _, slotName := range sortedKeys(intent.Slots) {
		slot := intent.Slots[slotName]
		if len(slot.Synonyms) == 0 {
			continue
		}
		if slot.Type != "enum" {
			addErr(fmt.Sprintf("%s slot %q: synonyms: is only valid on enum slots (got type %q)", where, slotName, slot.Type))
			continue
		}
		valueSet := make(map[string]struct{}, len(slot.Values))
		for _, v := range slot.Values {
			valueSet[v] = struct{}{}
		}
		for _, key := range sortedKeys(slot.Synonyms) {
			if _, ok := valueSet[key]; !ok {
				addErr(fmt.Sprintf("%s slot %q: synonyms key %q is not in values: %v", where, slotName, key, slot.Values))
				continue
			}
			for i, phrase := range slot.Synonyms[key] {
				if strings.TrimSpace(phrase) == "" {
					addErr(fmt.Sprintf("%s slot %q: synonyms[%q][%d] is empty", where, slotName, key, i))
				}
			}
		}
	}
}

// walkStateIntents traverses the state tree and invokes fn for every
// state-local intent. Path is the dot-separated state address (same
// shape as collectStatePaths produces). Used by validateRouting to
// surface per-state synonyms errors with a precise location.
func walkStateIntents(states map[string]*State, prefix string, fn func(statePath, intentName string, intent Intent)) {
	for _, name := range sortedKeys(states) {
		s := states[name]
		if s == nil {
			continue
		}
		path := joinPath(prefix, name)
		for _, in := range sortedKeys(s.Intents) {
			fn(path, in, s.Intents[in])
		}
		if len(s.States) > 0 {
			walkStateIntents(s.States, path, fn)
		}
	}
}

// unmarshalRoutingRaw decodes the YAML body of a `routing:` block into
// the supplied receiver. Lives in loader.go (not types.go) to keep the
// goccy/go-yaml import out of the type-declaration file. Strict mode is
// enabled so typos in routing field names surface as load errors.
func unmarshalRoutingRaw(b []byte, dst interface{}) error {
	return goyaml.UnmarshalWithOptions(b, dst, goyaml.Strict())
}

// sortedKeys returns the keys of any map[string]T sorted alphabetically.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ── App interface implementation ─────────────────────────────────────────────

// appImpl wraps an AppDef and satisfies the App interface declared in
// interfaces.go. It is returned by Load/LoadBytes after successful validation.
type appImpl struct {
	def *AppDef
}

// Compile constructs an appImpl from a validated AppDef. This is called by the
// loader after validation passes; Stage 3 will wire this into the machine.
func Compile(def *AppDef) App {
	return &appImpl{def: def}
}

func (a *appImpl) ID() string { return a.def.App.ID }

func (a *appImpl) Version() string { return a.def.App.Version }

func (a *appImpl) InitialState() StatePath {
	if s, ok := a.def.Root.(string); ok {
		return StatePath(s)
	}
	return ""
}

// LookupState resolves a dot-separated state path through the state tree.
func (a *appImpl) LookupState(p StatePath) (*State, bool) {
	return lookupStateInMap(string(p), a.def.States)
}

// lookupStateInMap walks a dot-separated path through a nested state map.
func lookupStateInMap(path string, states map[string]*State) (*State, bool) {
	if path == "" || len(states) == 0 {
		return nil, false
	}
	idx := strings.IndexByte(path, '.')
	if idx < 0 {
		// Leaf lookup.
		s, ok := states[path]
		return s, ok && s != nil
	}
	parent := path[:idx]
	rest := path[idx+1:]
	s, ok := states[parent]
	if !ok || s == nil {
		return nil, false
	}
	return lookupStateInMap(rest, s.States)
}

// LookupIntent resolves an intent by name, checking the state's local intents
// first and then the global intent library.
func (a *appImpl) LookupIntent(ctx StatePath, name string) (Intent, bool) {
	if s, ok := a.LookupState(ctx); ok && s != nil {
		if intent, ok := s.Intents[name]; ok {
			return intent, true
		}
	}
	if intent, ok := a.def.Intents[name]; ok {
		return intent, true
	}
	return Intent{}, false
}

func (a *appImpl) WorldSchema() WorldSchema { return WorldSchema(a.def.World) }

// TopLevelStateIDs returns every declared top-level state id (room id),
// including states unreachable from the initial state. The order is
// unspecified. This is the enumeration the story-graph tooling
// (internal/app/graph) needs to surface orphaned rooms the App interface's
// path-based LookupState cannot discover on its own. It is additive — callers
// type-assert for it via an optional interface.
func (a *appImpl) TopLevelStateIDs() []string {
	out := make([]string, 0, len(a.def.States))
	for id := range a.def.States {
		out = append(out, id)
	}
	return out
}
