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
	"time"

	"kitsoki/internal/agent/grammar"
	"kitsoki/internal/agents"
	"kitsoki/internal/effect"
	starlarkhost "kitsoki/internal/host/starlark"

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
	return LoadWithResolver(path, ifaceOverrides, nil)
}

// LoadWithResolver is LoadWithOverrides plus an injected ImportResolver — the
// DI seam (CLAUDE.md, no package globals) through which the `--kitsoki-repo`
// override and the embedded story library reach the import system. A nil
// resolver is identical to LoadWithOverrides: an `@kitsoki/<name>` source with
// no on-disk kitsoki checkout errors as before. See [ImportResolver] and
// resolveImportSource for the resolution order.
//
// Intended caller: cmd/kitsoki, which builds the resolver from the
// `--kitsoki-repo` flag / KITSOKI_REPO env and basestories.Materialize, then
// threads it into every load. The resolver is passed unchanged into recursive
// child loads so an embedded base story can itself import siblings via
// `@kitsoki/<name>`.
func LoadWithResolver(path string, ifaceOverrides map[string]string, resolver ImportResolver) (*AppDef, error) {
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
	return runLoadPipeline(merged, path, baseDir, ifaceOverrides, resolver)
}

// runLoadPipeline takes a parsed (or in-memory synthesized) AppDef and runs the
// full import-fold → phase-expand → interface-resolve → validate chain,
// returning the finished AppDef or the joined error set. It is the shared body
// of LoadWithOverrides (file-backed) and SynthesizeRoot (config-synthesized) —
// the implicit-project-root slice synthesizes a one-import AppDef and runs the
// IDENTICAL pipeline so a malformed synthesized root is caught by the same
// validators that catch a malformed imports: block. See
// docs/stories/imports.md "The blank root that grows".
//
// path/baseDir play their usual roles: baseDir roots @kitsoki/<name> + relative
// import resolution and is stashed on AppDef.BaseDir; path is the canonical key
// seeded into LoadedManifests and used in error messages. A synthesized root
// passes a synthetic path (no file on disk) with the repo root as baseDir.
func runLoadPipeline(merged *AppDef, path, baseDir string, ifaceOverrides map[string]string, resolver ImportResolver) (*AppDef, error) {
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
	if importErrs := resolveImports(merged, path, baseDir, []string{canonical}, resolver); len(importErrs) > 0 {
		return nil, errors.Join(importErrs...)
	}

	// Expand phase templates into concrete states before validation so the
	// referential-integrity pass sees the synthesised states.
	if expandErrs := expandPhases(merged, path); len(expandErrs) > 0 {
		return nil, errors.Join(expandErrs...)
	}

	// Expand `workbench:` blocks into write_mode/agent_off_ramp/on_enter/
	// default_intent before the roomDispatchesAgent / write-mode
	// precondition pass (inside validateDef below) and agent
	// effect-taxonomy resolution see them — see workbench.go.
	if workbenchErrs := expandWorkbenches(merged, path); len(workbenchErrs) > 0 {
		return nil, errors.Join(workbenchErrs...)
	}

	// Expand agent_off_ramp capture_free_text declarations into the
	// synthesized <room>_discuss intent + default_intent — after
	// expandWorkbenches so workbench-synthesized off-ramps (which never set
	// capture) are already in place. See offramp_capture.go.
	if captureErrs := expandOffRampCaptures(merged, path); len(captureErrs) > 0 {
		return nil, errors.Join(captureErrs...)
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

	injectBuiltinStoryAuthoringRoom(merged, path, baseDir)

	// Rewrite any remaining @exit:<name> targets in the top-level app. For
	// the root manifest these are terminal sentinels — the app is loaded
	// standalone, so an exit means "stop here." We synthesise a terminal
	// state per exit and rewrite refs accordingly.
	if exitErrs := materialiseStandaloneExits(merged, path); len(exitErrs) > 0 {
		return nil, errors.Join(exitErrs...)
	}

	// Resolve agent plugin declarations from agent_plugins: block. This
	// validates plugin names, performs ${VAR} substitution in env/headers, and
	// injects the default agent.claude entry when absent.
	if pluginErrs := resolveAgentPlugins(merged, path); len(pluginErrs) > 0 {
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
			if yamlErr.Token != nil && yamlErr.Token.Position != nil {
				ve.Line = yamlErr.Token.Position.Line
				ve.Column = yamlErr.Token.Position.Column
			}
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
	for k, v := range src.Toolboxes {
		if _, exists := dst.Toolboxes[k]; exists {
			addErr(fmt.Sprintf("include: toolbox %q is already declared", k))
			continue
		}
		if dst.Toolboxes == nil {
			dst.Toolboxes = make(map[string]*ToolboxDecl)
		}
		dst.Toolboxes[k] = v
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
	if def == nil {
		return nil
	}
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: file, Message: msg})
	}

	if boxErrs := resolveToolboxes(def, file); len(boxErrs) > 0 {
		errs = append(errs, boxErrs...)
	}
	if len(def.Agents) == 0 {
		return errs
	}

	// Set of agents referenced by a read-only agent verb (ask/decide), where
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
		if decl.Permissions != nil {
			switch decl.Permissions.Mode {
			case "", "ask", "denyAll", "default", "bypassPermissions", "acceptEdits", "auto", "dontAsk", "plan":
			default:
				addErr(fmt.Sprintf("agent %q: permissions.mode %q is not valid", name, decl.Permissions.Mode))
				continue
			}
		}

		if decl.TokenBudget != nil {
			if decl.TokenBudget.WarnTokens <= 0 || decl.TokenBudget.RefuseTokens < decl.TokenBudget.WarnTokens {
				addErr(fmt.Sprintf("agent %q: token_budget must set warn_tokens > 0 and refuse_tokens >= warn_tokens (got warn_tokens=%d refuse_tokens=%d)",
					name, decl.TokenBudget.WarnTokens, decl.TokenBudget.RefuseTokens))
				continue
			}
		}

		if decl.Toolbox != "" && len(decl.Tools) > 0 {
			addErr(fmt.Sprintf("agent %q: toolbox and tools are mutually exclusive; use toolbox with tools_add/tools_remove or inline tools, not both", name))
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
		if len(decl.ToolsAdd) > 0 {
			out := make([]string, len(decl.ToolsAdd))
			for i, t := range decl.ToolsAdd {
				out[i] = normaliseAgentTool(t)
			}
			decl.ToolsAdd = out
		}
		if len(decl.ToolsRemove) > 0 {
			out := make([]string, len(decl.ToolsRemove))
			for i, t := range decl.ToolsRemove {
				out[i] = normaliseAgentTool(t)
			}
			decl.ToolsRemove = out
		}
		if decl.Toolbox != "" {
			box := def.Toolboxes[decl.Toolbox]
			if box == nil {
				addErr(fmt.Sprintf("agent %q: toolbox %q is not declared in toolboxes", name, decl.Toolbox))
				continue
			}
			decl.Tools = applyToolboxSpecialization(box.Tools, decl.ToolsAdd, decl.ToolsRemove)
		}
		if decl.MCP != nil && len(decl.MCP.Tools) > 0 {
			out := make([]string, len(decl.MCP.Tools))
			for i, t := range decl.MCP.Tools {
				out[i] = normaliseAgentTool(t)
			}
			decl.MCP.Tools = out
		}

		// bash_profile validation: Bash in the tool surface requires a
		// bash_profile *only* when the agent is actually referenced by a
		// read-only agent verb (host.agent.ask / host.agent.decide), where
		// every Bash invocation must pass through ApplyBashProfile. Agents used
		// solely with host.agent.task get full, unprofiled Bash by design, so
		// the bare presence of Bash is not a problem there. Cross-check the
		// effect graph rather than warning unconditionally.
		if hasTool(decl.Tools, "host.Bash") && decl.BashProfile == nil && askDecideAgents[name] {
			addErr(fmt.Sprintf("agent %q declares Bash but no bash_profile; required when the agent is referenced by host.agent.ask or host.agent.decide (those verbs run every Bash command through a profile allowlist)", name))
		}

		// Effect taxonomy resolution (effect-taxonomy.md): resolve the
		// agent's effect class from its declared `effect:` (or the
		// deprecated `external_side_effect:` alias) against the JOIN
		// computed over its tool surface, enforcing the taxonomy's
		// invariants. See resolveAgentEffect.
		if msg := resolveAgentEffect(name, decl); msg != "" {
			addErr(msg)
			continue
		}
	}
	return errs
}

func resolveToolboxes(def *AppDef, file string) []error {
	if def == nil || len(def.Toolboxes) == 0 {
		return nil
	}
	var errs []error
	for _, name := range sortedKeys(def.Toolboxes) {
		box := def.Toolboxes[name]
		if box == nil {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("toolbox %q: empty definition", name)})
			continue
		}
		out := make([]string, len(box.Tools))
		for i, t := range box.Tools {
			out[i] = normaliseAgentTool(t)
		}
		box.Tools = out
		if box.Effect != "" {
			if !box.Effect.Valid() {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("toolbox %q: effect %q is not one of pure|read|write|external", name, box.Effect)})
				continue
			}
			joined := effect.FromTools(box.Tools)
			if box.Effect != joined {
				errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("toolbox %q: declares effect %q but tools %v join to %q", name, box.Effect, box.Tools, joined)})
			}
		}
	}
	return errs
}

func applyToolboxSpecialization(base, add, remove []string) []string {
	removed := make(map[string]bool, len(remove))
	for _, t := range remove {
		removed[t] = true
	}
	var out []string
	for _, t := range base {
		if !removed[t] {
			out = append(out, t)
		}
	}
	out = append(out, add...)
	return dedupeAgentTools(out)
}

func dedupeAgentTools(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
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

// resolveAgentEffect resolves decl's effect class in place (mirroring the
// pattern resolveAgentDecls already uses for tools/bash_profile), enforcing
// effect-taxonomy.md's invariants:
//
//   - `effect:` must be one of pure|read|write|external when set.
//   - `effect:` and the deprecated `external_side_effect:` are mutually
//     exclusive — declaring both is a load error.
//   - A declared value (via effect: or, mapped through
//     effect.FromLegacyBool, via external_side_effect:) that is <= read
//     while the tool-surface join is > read is a HARD ERROR: the agent
//     claims a posture its own tools contradict (the teeth the old
//     boolean never had — it would have caught the dead proposal_author
//     declaration).
//   - Any other declared/joined disagreement is a warn-line, not an error
//     (over-declaring privilege is a safe, if noisy, author mistake).
//   - No declaration resolves silently to the tool-surface join.
//
// After resolution decl.Effect is always populated with a valid class, and
// decl.ExternalSideEffect is mirrored from it (true iff Effect == external)
// so pre-taxonomy consumers that still read the boolean directly (the
// write_mode: read_only contradiction check in validateWriteMode) keep
// working unchanged for both old- and new-style declarations.
//
// Returns a non-empty error message on a hard-fail; decl is left unmodified
// in that case (the caller aborts the whole load).
func resolveAgentEffect(name string, decl *AgentDecl) string {
	if decl.Effect != "" && !decl.Effect.Valid() {
		return fmt.Sprintf("agent %q: effect %q is not one of pure|read|write|external", name, decl.Effect)
	}
	if decl.Effect != "" && decl.ExternalSideEffect != nil {
		return fmt.Sprintf("agent %q: declares both effect: and the deprecated external_side_effect: — remove external_side_effect (use effect: only)", name)
	}

	toolSurface := agentToolSurface(decl)
	joined := effect.FromTools(toolSurface)

	var declared effect.Effect
	switch {
	case decl.Effect != "":
		declared = decl.Effect
	case decl.ExternalSideEffect != nil:
		declared = effect.FromLegacyBool(*decl.ExternalSideEffect, toolSurface)
		slog.Warn("agent external_side_effect: is deprecated; use effect: instead",
			"agent", name)
	}

	if declared != "" {
		if declared.LessEqual(effect.Read) && !joined.LessEqual(effect.Read) {
			return fmt.Sprintf(
				"agent %q: declares effect %q but its tool surface %v joins to %q (includes a write/external-tier tool) — "+
					"these contradict each other; an agent with that tool surface cannot be %s. "+
					"Remove the declaration or the tool.",
				name, declared, toolSurface, joined, declared)
		}
		if declared != joined {
			slog.Warn("agent effect declaration disagrees with inferred value from tool surface",
				"agent", name, "declared", declared, "inferred", joined)
		}
		decl.Effect = declared
	} else {
		decl.Effect = joined
	}

	mirrored := decl.Effect == effect.External
	decl.ExternalSideEffect = &mirrored
	return ""
}

func agentToolSurface(decl *AgentDecl) []string {
	if decl == nil {
		return nil
	}
	out := append([]string(nil), decl.Tools...)
	if decl.MCP != nil {
		out = append(out, decl.MCP.Tools...)
	}
	return out
}

// collectAskDecideAgents walks the full effect graph and returns the set of
// agent names referenced by a read-only agent verb (host.agent.ask or
// host.agent.decide) via the effect's `with.agent` argument. Those verbs run
// every Bash command through a bash_profile allowlist, so a referenced agent
// that declares Bash without a profile is a load error (see resolveAgentDecls).
// Effects reached only through host.agent.task — which grants full Bash by
// design — are intentionally not collected.
func collectAskDecideAgents(def *AppDef) map[string]bool {
	out := map[string]bool{}
	if def == nil {
		return out
	}
	var walkEffects func(effs []Effect)
	walkEffects = func(effs []Effect) {
		for _, e := range effs {
			if e.Invoke == "host.agent.ask" || e.Invoke == "host.agent.decide" {
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
	if strings.HasPrefix(name, "host.") || strings.HasPrefix(name, "mcp__") {
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
		if errors.As(err, &yamlErr) && yamlErr.Token != nil && yamlErr.Token.Position != nil {
			ve.Line = yamlErr.Token.Position.Line
			ve.Column = yamlErr.Token.Position.Column
		}
		return nil, []error{ve}
	}

	// Resolve agent declarations (tools normalisation, external_side_effect
	// inference, bash_profile checks). baseDir is "" in the LoadBytes path —
	// system_prompt_path resolution is intentionally not supported here;
	// use Load(path) for file-backed apps.
	if agentErrs := resolveAgentDecls(&def, file, ""); len(agentErrs) > 0 {
		return nil, agentErrs
	}

	// LoadBytes skips runLoadPipeline, so run the same local macro expansions
	// here before validation. Import folding and file-backed path resolution
	// remain Load-only concerns.
	if expandErrs := expandPhases(&def, file); len(expandErrs) > 0 {
		return nil, expandErrs
	}
	if workbenchErrs := expandWorkbenches(&def, file); len(workbenchErrs) > 0 {
		return nil, workbenchErrs
	}
	if captureErrs := expandOffRampCaptures(&def, file); len(captureErrs) > 0 {
		return nil, captureErrs
	}

	injectBuiltinStoryAuthoringRoom(&def, file, "")

	// LoadBytes skips parseAndMerge/runLoadPipeline, so inject builtin
	// meta_modes here before validation. Same rationale as the Load() path.
	injectBuiltinMetaModes(&def)

	// Resolve agent plugin declarations from agent_plugins: block.
	if pluginErrs := resolveAgentPlugins(&def, file); len(pluginErrs) > 0 {
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

	// App-level free-form fallback: inject the configured work-intake arc into
	// strict/menu rooms before collecting on: keys and running referential
	// validation, so every downstream surface sees the same graph.
	applyFreeFormFallback(def)

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
	// host.agent.* with: blocks can be statically resolved.
	declaredAgents := make(map[string]struct{}, len(def.Agents))
	for name := range def.Agents {
		declaredAgents[name] = struct{}{}
	}
	validateStates(file, "", def.States, globalIntents, def.Intents, nil, worldKeys, allStatePaths, stateOnKeys, allowedHosts, declaredAgents, &errs)

	// Agent off-ramp: normalize an explicit `false` to a nil pointer and
	// enforce the terminal/conversational placement invariants. Runs before
	// validateAgentReferences (step 9b) so the agent check sees the
	// normalized pointers. See docs/stories/meta-mode.md.
	normalizeAndValidateOffRamps(file, def, &errs)

	// write_mode: posture (agent-write-mode-opt-in proposal). Validates the
	// field value, that read_only is only on an agent-dispatch room, the
	// static-write contradiction, and that no story `set:`s the engine-reserved
	// write_mode_scope world key. Walks the full state tree once with def in
	// scope (it needs AgentDef.ExternalSideEffect, which validateStates does not
	// carry).
	validateWriteMode(file, def, &errs)
	validateOperations(file, def, &errs)
	validateOperationPolicies(file, def, &errs)

	// intercept_drive: multi-turn drive flag (conflict-capable intercept). The
	// only valid value is "rest"; only meaningful on a top-level room.
	validateInterceptDrive(file, def, &errs)

	// ── 7a'. static expression compile-check ──────────────────────────────────
	// Compile (never evaluate) every effect value and guard expression so a
	// malformed expr-lang expression — e.g. a pongo-only `|default:` filter
	// written into an effect value — fails the load with a precise diagnostic
	// instead of exploding mid-turn the first time its transition fires. See
	// validate_exprs.go.
	validateExprs(file, def, &errs)

	// Workbench owns its synthesized request/note world keys and capture intent,
	// but author-provided context_args can still point at arbitrary world keys.
	// Validate those references after import/workbench expansion so namespace
	// drift is caught at load time.
	validateWorkbenchContextArgs(file, def, worldKeys, &errs)

	// ── 7a''. view ↔ on_enter bind-target fallback advisory ───────────────────
	// Emit a NON-FATAL warning when a state's inline view reads a world key that
	// is only filled by an on_enter invoke/bind host call without an explicit
	// fallback (`?? …` / `| default(…)`). The runtime now defends the first
	// frame (machine.Turn skips the pre-bind render when host calls will bind),
	// but a fallback-less template is still fragile — this restores the
	// authoring-time signal. Does NOT append to errs. See validate_exprs.go.
	validateViewBindFallbacks(file, def, &errs)

	// ── 7a'''. unknown template-namespace advisory ────────────────────────────
	// Emit a NON-FATAL warning when an inline view template or a `with:
	// {prompt: "..."}` agent-prompt file references a `{{ }}`/`{% %}`
	// top-level namespace render.ToContext never populates (e.g. the
	// historical `{{ context.* }}` — see .context/scenario-qa-prd-transports-
	// kinks.md). pongo2 renders an unknown namespace as an empty string with
	// no error, so this is otherwise only discoverable live, after an agent
	// receives a blank handoff. Does NOT append to errs. See
	// validate_template_namespaces.go.
	validateTemplateNamespaces(file, def, &errs)

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

	// ── 9d. agent-split verb × agent.Tools cross-checks (agent-split
	// proposal invariant 1 / M6 / M3).
	// Enforces: ask/decide/extract → no mutation tools; task → acceptance.schema
	// required; task + external_side_effect:false + WebFetch/WebSearch → error.
	validateAgentVerbCrossChecks(file, def, &errs)

	// ── 9d'. host_interfaces op effect: override validation (effect-taxonomy.md).
	// A declared `effect:` must be one of pure|read|write|external; the
	// builtin classification table (internal/effect.ClassifyVerb) is the
	// default for every op that doesn't set this override.
	validateHostInterfaceEffects(file, def, &errs)

	// ── 9e. grammar-subset check for builtin.local_llm grammar:true effects.
	// Every decide effect whose `agent:` alias resolves to a builtin.local_llm
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

func applyFreeFormFallback(def *AppDef) {
	cfg, ok := effectiveFreeFormFallback(def)
	if !ok {
		return
	}
	statePath := strings.TrimSpace(cfg.State)
	intentName := strings.TrimSpace(cfg.Intent)
	if statePath == "" || intentName == "" {
		return
	}
	src, ok := lookupStateInMap(statePath, def.States)
	if !ok || src == nil || len(src.On[intentName]) == 0 {
		return
	}
	fallback := cloneTransitions(src.On[intentName])
	for i := range fallback {
		fallback[i].Target = statePath
	}
	walkStates(def.States, "", func(path string, s *State) {
		if s == nil || path == statePath || s.Terminal {
			return
		}
		if s.DefaultIntent != "" || s.AgentOffRamp != nil {
			return
		}
		if s.On == nil {
			s.On = map[string][]Transition{}
		}
		if _, exists := s.On[intentName]; exists {
			return
		}
		s.On[intentName] = cloneTransitions(fallback)
	})
}

func effectiveFreeFormFallback(def *AppDef) (FreeFormFallbackConfig, bool) {
	if def == nil {
		return FreeFormFallbackConfig{}, false
	}
	if def.Routing != nil && def.Routing.FreeFormFallback != nil {
		cfg := *def.Routing.FreeFormFallback
		return cfg, strings.TrimSpace(cfg.State) != "" || strings.TrimSpace(cfg.Intent) != ""
	}
	type candidate struct {
		path   string
		intent string
	}
	var candidates []candidate
	walkStates(def.States, "", func(path string, s *State) {
		if s == nil || baseStateName(path) != "landing" {
			return
		}
		if _, ok := s.On["work"]; ok {
			candidates = append(candidates, candidate{path: path, intent: "work"})
			return
		}
		for intentName := range s.On {
			if strings.HasSuffix(intentName, "__work") {
				candidates = append(candidates, candidate{path: path, intent: intentName})
			}
		}
	})
	if len(candidates) == 0 {
		return FreeFormFallbackConfig{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].path == candidates[j].path {
			return candidates[i].intent < candidates[j].intent
		}
		return candidates[i].path < candidates[j].path
	})
	return FreeFormFallbackConfig{State: candidates[0].path, Intent: candidates[0].intent}, true
}

func baseStateName(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func parentStatePath(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[:i]
	}
	return ""
}

func walkStates(states map[string]*State, prefix string, fn func(path string, s *State)) {
	for name, s := range states {
		path := joinPath(prefix, name)
		fn(path, s)
		if s != nil && len(s.States) > 0 {
			walkStates(s.States, path, fn)
		}
	}
}

func cloneTransitions(in []Transition) []Transition {
	out := make([]Transition, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Effects = cloneEffects(in[i].Effects)
		out[i].Emit = append([]string(nil), in[i].Emit...)
	}
	return out
}

func cloneEffects(in []Effect) []Effect {
	out := make([]Effect, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Set = cloneAnyMap(in[i].Set)
		out[i].Increment = cloneIntMap(in[i].Increment)
		out[i].With = cloneAnyMap(in[i].With)
		out[i].Bind = cloneStringMap(in[i].Bind)
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
//   - `with.agent:` on host.agent.* effects resolves to a declared agent.
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

		// Validate room prerequisites. Expressions are compiled again by
		// validateExprs for full expression diagnostics; this pass handles the
		// structural contract and intent cross-reference.
		seenPrereqIDs := map[string]struct{}{}
		for i, pr := range s.Prerequisites {
			loc := fmt.Sprintf("state %q prerequisites[%d]", statePath, i)
			if strings.TrimSpace(pr.ID) == "" {
				addErr(fmt.Sprintf("%s: id is required", loc))
			} else {
				if _, exists := seenPrereqIDs[pr.ID]; exists {
					addErr(fmt.Sprintf("%s: duplicate id %q", loc, pr.ID))
				}
				seenPrereqIDs[pr.ID] = struct{}{}
			}
			if strings.TrimSpace(pr.Title) == "" {
				addErr(fmt.Sprintf("%s: title is required", loc))
			}
			switch pr.Severity {
			case "", "info", "warning", "warn", "error":
				// ok
			default:
				addErr(fmt.Sprintf("%s: severity %q is not one of info, warning, warn, error", loc, pr.Severity))
			}
			if strings.TrimSpace(pr.SatisfiedWhen) == "" {
				addErr(fmt.Sprintf("%s: satisfied_when is required", loc))
			}
			if pr.Action != nil && strings.TrimSpace(pr.Action.Intent) != "" {
				intentName := pr.Action.Intent
				_, inScope := inScopeIntents[intentName]
				_, inGlobal := globalIntentDefs[intentName]
				if !inScope && !inGlobal {
					addErr(fmt.Sprintf("%s: action.intent %q is not a declared intent", loc, intentName))
				}
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

		// Validate contextual_routing: when enabled, each declared lane must
		// have a backing intent with a reachable on: arc (room_chat), or a
		// declared name (help_chat, meta_chat — full cross-reference is group 2).
		// Mirrors the default_intent cross-reference at loader.go:1300.
		if cr := s.ContextualRouting; cr != nil && cr.Enabled {
			if cr.RoomChat == "" && cr.HelpChat == "" && cr.MetaChat == "" {
				addErr(fmt.Sprintf("state %q: contextual_routing.enabled requires at least one lane (room_chat, help_chat, or meta_chat)", statePath))
			}
			if cr.RoomChat != "" {
				if _, hasArc := s.On[cr.RoomChat]; !hasArc {
					addErr(fmt.Sprintf("state %q: contextual_routing.room_chat %q has no matching on: arc in this state", statePath, cr.RoomChat))
				}
				_, inScope := inScopeIntents[cr.RoomChat]
				_, inGlobal := globalIntentDefs[cr.RoomChat]
				if !inScope && !inGlobal {
					addErr(fmt.Sprintf("state %q: contextual_routing.room_chat %q is not a declared intent", statePath, cr.RoomChat))
				}
			}
			if cr.PlanAcceptIntent != "" {
				if _, hasArc := s.On[cr.PlanAcceptIntent]; !hasArc {
					addErr(fmt.Sprintf("state %q: contextual_routing.plan_accept_intent %q has no matching on: arc in this state", statePath, cr.PlanAcceptIntent))
				}
				_, inScope := inScopeIntents[cr.PlanAcceptIntent]
				_, inGlobal := globalIntentDefs[cr.PlanAcceptIntent]
				if !inScope && !inGlobal {
					addErr(fmt.Sprintf("state %q: contextual_routing.plan_accept_intent %q is not a declared intent", statePath, cr.PlanAcceptIntent))
				}
			}
			if cr.PlanRefineIntent != "" {
				if _, hasArc := s.On[cr.PlanRefineIntent]; !hasArc {
					addErr(fmt.Sprintf("state %q: contextual_routing.plan_refine_intent %q has no matching on: arc in this state", statePath, cr.PlanRefineIntent))
				}
				_, inScope := inScopeIntents[cr.PlanRefineIntent]
				_, inGlobal := globalIntentDefs[cr.PlanRefineIntent]
				if !inScope && !inGlobal {
					addErr(fmt.Sprintf("state %q: contextual_routing.plan_refine_intent %q is not a declared intent", statePath, cr.PlanRefineIntent))
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

func validateOperations(file string, def *AppDef, errs *[]error) {
	var walkEffects func(location, statePath string, inOperation bool, effects []Effect)
	walkEffects = func(location, statePath string, inOperation bool, effects []Effect) {
		for i, eff := range effects {
			loc := fmt.Sprintf("%s[%d]", location, i)
			if inOperation && eff.Background {
				*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("%s: background jobs inside operation states need an explicit policy", loc)})
			}
			if eff.CommitOperation != nil && !inOperation {
				*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("%s: commit_operation is only valid inside a state with operation:", loc)})
			}
			if eff.PersistDraft != nil {
				if !inOperation {
					*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("%s: persist_draft is only valid inside a state with operation:", loc)})
				}
				if strings.TrimSpace(eff.PersistDraft.ID) == "" {
					*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("%s: persist_draft requires id:", loc)})
				}
			}
			if eff.DiscardOperation != nil && !inOperation {
				*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("%s: discard_operation is only valid inside a state with operation:", loc)})
			}
			if len(eff.OnComplete) > 0 {
				walkEffects(loc+" on_complete", statePath, inOperation, eff.OnComplete)
			}
			if len(eff.Effects) > 0 {
				walkEffects(loc+" effects", statePath, inOperation, eff.Effects)
			}
		}
	}
	var walkStates func(prefix string, states map[string]*State, inheritedOperation bool)
	walkStates = func(prefix string, states map[string]*State, inheritedOperation bool) {
		for name, s := range states {
			if s == nil {
				continue
			}
			statePath := joinPath(prefix, name)
			inOperation := inheritedOperation || s.Operation != nil
			if s.Operation != nil {
				scope := strings.TrimSpace(s.Operation.Scope)
				if strings.Contains(scope, "{{") || strings.Contains(scope, "{%") {
					*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("state %q: operation.scope must be a stable literal, got %q", statePath, scope)})
				}
			}
			walkEffects(fmt.Sprintf("state %q on_enter", statePath), statePath, inOperation, s.OnEnter)
			for intentName, transitions := range s.On {
				for ti, tr := range transitions {
					walkEffects(fmt.Sprintf("state %q intent %q transitions[%d] effects", statePath, intentName, ti), statePath, inOperation, tr.Effects)
				}
			}
			if len(s.States) > 0 {
				walkStates(statePath, s.States, inOperation)
			}
		}
	}
	walkStates("", def.States, false)
}

func validateOperationPolicies(file string, def *AppDef, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	validModes := map[string]struct{}{
		"interactive": {},
		"autonomous":  {},
		"supervised":  {},
	}
	validExecutionModes := map[string]struct{}{
		"":         {},
		"one-shot": {},
		"staged":   {},
	}
	validReasons := map[string]struct{}{
		"needs-human":               {},
		"uncertain":                 {},
		"gate-failed":               {},
		"host-error":                {},
		"conflict":                  {},
		"budget-exhausted":          {},
		"policy-denied":             {},
		"operator-ask":              {},
		"protected-branch-approval": {},
	}

	for _, id := range sortedKeys(def.Operations) {
		policy := def.Operations[id]
		loc := fmt.Sprintf("operations.%s", id)
		if strings.TrimSpace(id) == "" {
			addErr("operations: policy id must not be empty")
			continue
		}
		if policy == nil {
			addErr(fmt.Sprintf("%s: policy must not be null", loc))
			continue
		}
		mode := strings.TrimSpace(policy.Mode)
		if _, ok := validModes[mode]; !ok {
			addErr(fmt.Sprintf("%s.mode %q is invalid (want interactive, autonomous, or supervised)", loc, policy.Mode))
		}
		execMode := strings.TrimSpace(policy.ExecutionMode)
		if _, ok := validExecutionModes[execMode]; !ok {
			addErr(fmt.Sprintf("%s.execution_mode %q is invalid (want \"\", one-shot, or staged)", loc, policy.ExecutionMode))
		}
		for _, reason := range policy.StopOn {
			if _, ok := validReasons[strings.TrimSpace(reason)]; !ok {
				addErr(fmt.Sprintf("%s.stop_on reason %q is invalid", loc, reason))
			}
		}
		for _, reason := range policy.PauseOn {
			if _, ok := validReasons[strings.TrimSpace(reason)]; !ok {
				addErr(fmt.Sprintf("%s.pause_on reason %q is invalid", loc, reason))
			}
		}
	}

	var walkStates func(prefix string, states map[string]*State)
	walkStates = func(prefix string, states map[string]*State) {
		for name, s := range states {
			if s == nil {
				continue
			}
			statePath := joinPath(prefix, name)
			for intentName, transitions := range s.On {
				for ti, tr := range transitions {
					opID := strings.TrimSpace(tr.Operation)
					if opID == "" {
						continue
					}
					if _, ok := def.Operations[opID]; !ok {
						addErr(fmt.Sprintf("state %q intent %q transitions[%d]: operation %q is not declared in top-level operations", statePath, intentName, ti, tr.Operation))
					}
				}
			}
			if len(s.States) > 0 {
				walkStates(statePath, s.States)
			}
		}
	}
	walkStates("", def.States)
}

// validateWriteMode enforces the write_mode: room-posture invariants
// (agent-write-mode-opt-in proposal). It walks the full state tree (def in
// scope so it can read AgentDef.ExternalSideEffect, which validateStates does
// not carry) and on every state:
//
//   - rejects a write_mode value other than "", "open", or "read_only";
//   - for write_mode: read_only, requires the room to actually dispatch an agent
//     (mode: conversational, an agent_off_ramp, or a host.agent.task /
//     host.agent.converse effect) — declaring it on a non-agent room is rejected
//     because it would silently do nothing (principle of least surprise);
//   - for write_mode: read_only, rejects a contradiction with a statically
//     write-capable agent (an effect's agent declaring external_side_effect: true):
//     the static and runtime postures must agree at the read-only floor;
//   - on every state, rejects any effect that `set:`s the engine-reserved
//     engine-owned world keys that stories must not forge.
func validateWriteMode(file string, def *AppDef, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}
	var walk func(prefix string, states map[string]*State)
	walk = func(prefix string, states map[string]*State) {
		for _, name := range sortedKeys(states) {
			s := states[name]
			if s == nil {
				continue
			}
			statePath := joinPath(prefix, name)

			// The engine-reserved `set:` guard applies to EVERY state regardless
			// of its own write_mode (any room could try to forge runtime state).
			for _, eff := range s.OnEnter {
				checkEngineReservedSet(file, fmt.Sprintf("state %q on_enter", statePath), eff, errs)
			}
			for _, intentName := range sortedKeys(s.On) {
				for _, tr := range s.On[intentName] {
					for _, eff := range tr.Effects {
						checkEngineReservedSet(file, fmt.Sprintf("state %q intent %q", statePath, intentName), eff, errs)
					}
				}
			}

			switch s.WriteMode {
			case "", WriteModeOpen, WriteModeReadOnly:
			default:
				addErr(fmt.Sprintf("state %q: write_mode %q is invalid (want \"\", %q, or %q)",
					statePath, s.WriteMode, WriteModeOpen, WriteModeReadOnly))
			}

			if s.WriteMode == WriteModeReadOnly {
				if !roomDispatchesAgent(s) {
					addErr(fmt.Sprintf("state %q: write_mode: read_only is only meaningful on a room that dispatches an agent "+
						"(mode: conversational, an agent_off_ramp, or a host.agent.task / host.agent.converse effect); "+
						"it would silently do nothing here", statePath))
				}
				for _, agentName := range roomDispatchedAgents(s) {
					ad, ok := def.Agents[agentName]
					if !ok || ad.ExternalSideEffect == nil {
						continue
					}
					if *ad.ExternalSideEffect {
						addErr(fmt.Sprintf("state %q: write_mode: read_only contradicts agent %q which declares external_side_effect: true "+
							"(statically write-capable); the static and runtime postures must agree — drop write_mode: read_only or set the agent read-only",
							statePath, agentName))
					}
				}
			}

			if len(s.States) > 0 {
				walk(statePath, s.States)
			}
		}
	}
	walk("", def.States)
}

// InterceptDriveRest is the only valid value of a state's intercept_drive flag:
// it marks a room whose entry begins a multi-turn sub-flow the pre-LLM intercept
// gate must drive to rest. See State.InterceptDrive and
// docs/architecture/prompt-intercept.md §"Multi-turn commands".
const InterceptDriveRest = "rest"

// validateInterceptDrive enforces the intercept_drive flag's contract: the only
// valid value is "rest". An invalid value is a load error so a typo can't
// silently disable the multi-turn drive at runtime. The flag is NOT restricted
// to top-level rooms: import-folding legitimately nests a source story's
// top-level room under an alias (e.g. git-ops's `conflict` becomes
// `gitops.conflict` when dev-story imports it), and the gate's reachability
// walk (Orchestrator.HasInterceptDriveRoom / isInterceptDriveRoom) descends into
// nested states, so a flagged room remains fully functional after folding.
func validateInterceptDrive(file string, def *AppDef, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}
	var walk func(prefix string, states map[string]*State)
	walk = func(prefix string, states map[string]*State) {
		for _, name := range sortedKeys(states) {
			s := states[name]
			if s == nil {
				continue
			}
			statePath := joinPath(prefix, name)
			if s.InterceptDrive != "" && s.InterceptDrive != InterceptDriveRest {
				addErr(fmt.Sprintf("state %q: intercept_drive %q is invalid (the only valid value is %q)",
					statePath, s.InterceptDrive, InterceptDriveRest))
			}
			if len(s.States) > 0 {
				walk(statePath, s.States)
			}
		}
	}
	walk("", def.States)
}

// checkEngineReservedSet rejects effects that `set:` engine-owned world keys.
// location prefixes the error.
func checkEngineReservedSet(file, location string, eff Effect, errs *[]error) {
	if eff.Set == nil {
		return
	}
	if _, ok := eff.Set[WriteModeScopeWorldKey]; ok {
		*errs = append(*errs, &ValidationError{
			File: file,
			Message: fmt.Sprintf("%s: set: %q is engine-reserved — a story may not self-grant write mode by writing the scope key; "+
				"it is set only by the write-mode gate on an operator grant", location, WriteModeScopeWorldKey),
		})
	}
	if _, ok := eff.Set[OperationRunWorldKey]; ok {
		*errs = append(*errs, &ValidationError{
			File: file,
			Message: fmt.Sprintf("%s: set: %q is engine-reserved — start or update operation runs with transition operation policies instead",
				location, OperationRunWorldKey),
		})
	}
}

// roomDispatchesAgent reports whether a state runs a dispatched agent — the
// precondition for write_mode: read_only to be meaningful. True when the state
// is a conversational room, declares an agent off-ramp, or has any on_enter /
// transition effect invoking host.agent.task or host.agent.converse.
func roomDispatchesAgent(s *State) bool {
	if s.Mode == "conversational" || s.AgentOffRamp != nil {
		return true
	}
	dispatch := func(eff Effect) bool {
		return eff.Invoke == "host.agent.task" || eff.Invoke == "host.agent.converse"
	}
	for _, eff := range s.OnEnter {
		if dispatch(eff) {
			return true
		}
	}
	for _, trs := range s.On {
		for _, tr := range trs {
			for _, eff := range tr.Effects {
				if dispatch(eff) {
					return true
				}
			}
		}
	}
	return false
}

// roomDispatchedAgents returns the set of statically-named agents this room
// dispatches via host.agent.task / host.agent.converse effects (the with.agent
// arg, when a literal string). Templated agent names are skipped — they cannot
// be resolved at load time. Used by the write_mode static-contradiction check.
func roomDispatchedAgents(s *State) []string {
	seen := map[string]struct{}{}
	var out []string
	collect := func(eff Effect) {
		if eff.Invoke != "host.agent.task" && eff.Invoke != "host.agent.converse" {
			return
		}
		name, _ := eff.With["agent"].(string)
		if name == "" || strings.Contains(name, "{{") {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, eff := range s.OnEnter {
		collect(eff)
	}
	for _, trs := range s.On {
		for _, tr := range trs {
			for _, eff := range tr.Effects {
				collect(eff)
			}
		}
	}
	return out
}

// validateAgentRef checks that, when a host.agent.* effect declares
// `with: { agent: <name> }`, the name resolves to an entry in AppDef.Agents.
// Task-style handlers may also name builtin agents because they run through
// the same registry meta modes use. Effects that omit `agent:` (or whose
// Invoke is not a host.agent.* handler) are silently ignored — agent: is
// host-handler-specific metadata, not a global field. Templated values
// (containing "{{") are skipped because they cannot be resolved statically.
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
	// Only host.agent.* handlers consume agent:; flag misuse on others as
	// a useful authoring error rather than a silent typo.
	if eff.Invoke != "" && !strings.HasPrefix(eff.Invoke, "host.agent.") {
		*errs = append(*errs, &ValidationError{
			File:    file,
			Message: fmt.Sprintf("%s: with.agent is only meaningful on host.agent.* invocations (got invoke %q)", location, eff.Invoke),
		})
		return
	}
	if _, found := declaredAgents[name]; !found {
		if hostAgentInvokeAllowsBuiltin(eff.Invoke) && isBuiltinAgentName(name) {
			return
		}
		*errs = append(*errs, &ValidationError{
			File:    file,
			Message: fmt.Sprintf("%s: with.agent %q is not declared in agents or builtin agents", location, name),
		})
	}
}

func hostAgentInvokeAllowsBuiltin(invoke string) bool {
	return invoke == "host.agent.task" || invoke == "host.agent.converse"
}

func isBuiltinAgentName(name string) bool {
	for _, builtin := range agents.BuiltinNames() {
		if name == builtin {
			return true
		}
	}
	return false
}

// agentMutationTools is the set of tools that are forbidden for
// host.agent.ask, host.agent.decide, and host.agent.extract (read-only
// verbs). This mirrors the runtime check in agent_decide.go and
// agent_ask.go. The loader check here is the primary enforcement point.
var agentMutationTools = map[string]bool{
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

// validateAgentVerbCrossChecks walks every host.agent.* effect across the
// entire AppDef (states + proposals) and enforces three rules from the
// agent-split proposal:
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
// validateHostInterfaceEffects rejects a declared HostInterfaceOp.Effect
// outside the four recognised taxonomy tiers (effect-taxonomy.md). It does
// NOT cross-check the override against the eventual bound handler's builtin
// classification — host_interfaces bindings resolve to a concrete handler
// only through the import/dev-story synthesis machinery, well after this
// validation pass, so that check is deferred (see the epic's cross-cutting
// open question 1: the builtin table is the default, this override is an
// escape hatch the author is trusted to use correctly).
func validateHostInterfaceEffects(file string, def *AppDef, errs *[]error) {
	if def == nil {
		return
	}
	for _, ifaceName := range sortedKeys(def.HostInterfaces) {
		iface := def.HostInterfaces[ifaceName]
		if iface == nil {
			continue
		}
		for _, opName := range sortedKeys(iface.Operations) {
			op := iface.Operations[opName]
			if op == nil || op.Effect == "" {
				continue
			}
			if !op.Effect.Valid() {
				*errs = append(*errs, &ValidationError{
					File: file,
					Message: fmt.Sprintf("host_interfaces %q op %q: effect %q is not one of pure|read|write|external",
						ifaceName, opName, op.Effect),
				})
			}
		}
	}
}

func validateAgentVerbCrossChecks(file string, def *AppDef, errs *[]error) {
	if def == nil {
		return
	}
	// Walk all effects in the state tree.
	walkAllEffects(def.States, func(loc string, eff Effect) {
		checkAgentEffect(file, loc, eff, def.Agents, errs)
	})
	// Walk proposal execute effects.
	for pname, pk := range def.Proposals {
		if pk == nil || pk.Execute == nil {
			continue
		}
		loc := fmt.Sprintf("proposal %q execute", pname)
		checkAgentEffect(file, loc, Effect{
			Invoke: pk.Execute.Invoke,
			With:   pk.Execute.With,
		}, def.Agents, errs)
		for i, child := range pk.Execute.OnComplete {
			checkAgentEffect(file, fmt.Sprintf("%s on_complete[%d]", loc, i), child, def.Agents, errs)
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

// checkAgentEffect enforces M6 and M3 rules on one effect.
func checkAgentEffect(file, loc string, eff Effect, agents map[string]*AgentDecl, errs *[]error) {
	if eff.With == nil {
		return
	}
	verb := eff.Invoke
	if !strings.HasPrefix(verb, "host.agent.") {
		return
	}
	shortVerb := strings.TrimPrefix(verb, "host.agent.")

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
	if shortVerb == "task" || shortVerb == "converse" || shortVerb == "decide" {
		if msg := validateSandboxBlock(loc, eff.With["sandbox"]); msg != "" {
			addErr(msg)
		}
	}
	if shortVerb == "codeact" {
		if eff.With["sandbox"] != nil {
			addErr(fmt.Sprintf(
				"%s: sandbox: is not valid for host.agent.codeact — sandbox is an external-agent subprocess knob; codeact uses its own bounded-Starlark-loop sandboxing (internal/host/codeact)",
				loc,
			))
		}
	}

	switch shortVerb {
	case "codeact":
		// with.capabilities uses the shared structured Starlark capability
		// schema, and runtime enforcement comes from the normalized spec rather
		// than a prompt-only allow-list.
		if rawCaps, present := eff.With["capabilities"]; present {
			if _, err := starlarkhost.ParseCapabilities(rawCaps); err != nil {
				addErr(fmt.Sprintf("%s: with.capabilities is invalid for host.agent.codeact: %v", loc, err))
			}
		}
	case "ask", "decide", "extract":
		// M6a: reject mutation tools.
		if decl != nil {
			for _, t := range decl.Tools {
				if agentMutationTools[t] {
					addErr(fmt.Sprintf(
						"%s: agent %q declares mutation tool %q — not permitted for host.agent.%s (read-only verb); use host.agent.task for agentic work",
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
							slog.Warn("agent verb cross-check: decide/extract validator.post_cmd argv0 is not on the read-only allowlist; runtime sandbox enforces isolation",
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
					"%s: host.agent.task requires acceptance.schema to be set in the with: block",
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

func validateSandboxBlock(loc string, raw any) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok && strings.Contains(s, "{{") {
		return ""
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Sprintf("%s: sandbox must be a mapping", loc)
	}
	if v, _ := m["min_strength"].(string); strings.TrimSpace(v) != "" {
		switch v {
		case "none", "supervised", "fs_confined", "os_confined", "vm_confined":
		default:
			return fmt.Sprintf("%s: sandbox.min_strength %q is not one of none|supervised|fs_confined|os_confined|vm_confined", loc, v)
		}
	}
	if v, _ := m["repo"].(string); strings.TrimSpace(v) != "" {
		switch v {
		case "none", "read_only":
		default:
			return fmt.Sprintf("%s: sandbox.repo %q is not one of none|read_only", loc, v)
		}
	}
	if v, _ := m["network"].(string); strings.TrimSpace(v) != "" {
		switch v {
		case "inherit", "deny", "model_only", "allowlist":
		default:
			return fmt.Sprintf("%s: sandbox.network %q is not one of inherit|deny|model_only|allowlist", loc, v)
		}
	}
	if v, _ := m["degrade"].(string); strings.TrimSpace(v) != "" {
		switch v {
		case "fail", "warn":
		default:
			return fmt.Sprintf("%s: sandbox.degrade %q is not one of fail|warn", loc, v)
		}
	}
	for _, key := range []string{"rw", "hidden"} {
		if msg := validateSandboxPathList(loc, key, m[key]); msg != "" {
			return msg
		}
	}
	if resources, ok := m["resources"].(map[string]any); ok {
		if timeout, _ := resources["timeout"].(string); strings.TrimSpace(timeout) != "" {
			if _, err := time.ParseDuration(timeout); err != nil {
				return fmt.Sprintf("%s: sandbox.resources.timeout: %v", loc, err)
			}
		}
	}
	return ""
}

func validateSandboxPathList(loc, key string, raw any) string {
	if raw == nil {
		return ""
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Sprintf("%s: sandbox.%s must be a list", loc, key)
	}
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return fmt.Sprintf("%s: sandbox.%s entries must be strings", loc, key)
		}
		if strings.TrimSpace(s) == "" {
			return fmt.Sprintf("%s: sandbox.%s entries must be non-empty", loc, key)
		}
	}
	return ""
}

// validateLocalLLMGrammarSubset enforces, at load time, that every decide
// effect whose `agent:` alias resolves to a builtin.local_llm plugin with
// grammar: true points at a schema inside llama.cpp's translatable grammar
// subset. Without this, an out-of-subset schema would fail open silently at
// runtime (llama.cpp decodes unconstrained yet returns 200), defeating the
// predictability the grammar tier exists to buy.
//
// Scope: the check covers the decide verb only — its schema is a string path in
// the effect's with.schema. extract (with.schema_path) and ask source differently
// and are deferred as a follow-up. An empty `agent:` alias resolves to
// agent.claude (not local_llm) and is unaffected. Schema paths resolve relative
// to def.BaseDir (matching the runtime app-dir resolution); absolute paths are
// read as-is. A schema that cannot be read is reported here too, since the gate
// cannot vouch for a schema it never saw.
func validateLocalLLMGrammarSubset(file string, def *AppDef, errs *[]error) {
	if def == nil || len(def.AgentPlugins) == 0 {
		return
	}
	// Collect aliases that are builtin.local_llm with grammar: true.
	grammarLocalLLM := make(map[string]bool)
	for alias, decl := range def.AgentPlugins {
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
		if eff.Invoke != "host.agent.decide" {
			return
		}
		if !grammarLocalLLM[eff.AgentPlugin] {
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
				"%s: agent %q is builtin.local_llm with grammar: true but its decide schema %q could not be read: %v",
				loc, eff.AgentPlugin, schemaPath, readErr,
			))
			return
		}
		if subErr := grammar.SubsetOK(raw); subErr != nil {
			addErr(fmt.Sprintf(
				"%s: agent %q is builtin.local_llm with grammar: true but its decide schema %q is outside the llama.cpp grammar subset: %v",
				loc, eff.AgentPlugin, schemaPath, subErr,
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

		// Resolve relative paths against the app root and reject any relative path
		// that escapes it via `../`. Imported stories have their `script:` values
		// rebased to absolute child-story paths before folding; those are allowed
		// because they have already been rooted by the import loader.
		resolved := rawScript
		rawWasAbs := filepath.IsAbs(resolved)
		if !rawWasAbs && def.BaseDir != "" {
			resolved = filepath.Join(def.BaseDir, resolved)
		}
		if def.BaseDir != "" && !pathWithinAnyStoryRoot(filepath.Clean(resolved), def) {
			addErr(fmt.Sprintf("%s: host.starlark.run script %q resolves outside the app root or imported story roots", loc, rawScript))
			return
		}

		if _, statErr := os.Stat(resolved); statErr != nil {
			addErr(fmt.Sprintf("%s: host.starlark.run script %q not found (resolved to %q)", loc, rawScript, resolved))
			return
		}
		source, sourceErr := os.ReadFile(resolved)
		if sourceErr != nil {
			addErr(fmt.Sprintf("%s: host.starlark.run script %q could not be read (resolved to %q): %v", loc, rawScript, resolved, sourceErr))
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
		sc, parseErr := starlarkhost.ParseSidecar(raw)
		if parseErr != nil {
			addErr(fmt.Sprintf("%s: host.starlark.run sidecar %q is malformed: %v", loc, sidecarPath, parseErr))
			return
		}
		capabilities, capErr := starlarkhost.ParseCapabilities(eff.With["capabilities"])
		if capErr != nil {
			addErr(fmt.Sprintf("%s: host.starlark.run capabilities are invalid: %v", loc, capErr))
			return
		}
		validateStarlarkScriptCapabilityUse(loc, string(source), capabilities, addErr)

		// Statically vet the wired `inputs:` against how the machine actually
		// resolves them. host.starlark.run does NOT expr-evaluate inputs — the
		// machine resolves each value first, and ONLY a string wrapping the whole
		// value in `{{ }}` is evaluated (a sole `{{ expr }}` preserves the typed
		// value). A BARE `world.foo` is therefore passed to the script VERBATIM as
		// the literal string "world.foo", which silently breaks resolution
		// (deck-not-found, "expected int, got string" at runtime, deep in an
		// on_error arc). Catch both shapes here, at load, where the fix is one edit.
		inputs, _ := eff.With["inputs"].(map[string]any)
		for name, val := range inputs {
			s, isStr := val.(string)
			// (a) heuristic: a string value that is a bare expression (refs a world
			// root, or uses the `??` / `?.` expr operators) with no `{{ }}` (or `{%
			// %}`) template — the unambiguous footgun. Scoped to `inputs:`, so prose
			// fields elsewhere (description:, summary_markdown:, …) never trip it.
			if isStr && looksLikeBareStarlarkInputExpr(s) {
				addErr(fmt.Sprintf("%s: host.starlark.run input %q is a bare expression %q — it reaches the script as that literal string, not its value. Template it: %q (a sole {{ }} preserves the typed value).",
					loc, name, s, "{{ "+strings.TrimSpace(s)+" }}"))
				continue
			}
			// (b) type check: a NON-templated literal wired to a declared input
			// whose type it can never satisfy (e.g. a string literal to a declared
			// `int`). Templated values (`{{ … }}` / `{% … %}`) resolve at runtime, so
			// skip them. Zero false positives — there is no string→int coercion.
			spec, declared := sc.Inputs[name]
			if !declared {
				continue // the engine ignores undeclared inputs
			}
			if isStr && (strings.Contains(s, "{{") || strings.Contains(s, "{%")) {
				continue
			}
			if !starlarkhost.ValueMatchesType(val, spec.Type) {
				addErr(fmt.Sprintf("%s: host.starlark.run input %q is declared %s but is wired to a non-template %T (%v) that can never satisfy it — pass a %s literal or a {{ }} template.",
					loc, name, starlarkhost.NormType(spec.Type), val, val, starlarkhost.NormType(spec.Type)))
			}
		}
	})
}

func validateStarlarkScriptCapabilityUse(loc, source string, cap starlarkhost.CapabilitySpec, addErr func(string)) {
	scan := stripStarlarkLineComments(source)
	if strings.Contains(scan, "ctx.http") && !cap.NeedsHTTP() {
		addErr(fmt.Sprintf("%s: script uses ctx.http but with.capabilities does not grant http", loc))
	}
	if strings.Contains(scan, "ctx.fs.write") && len(cap.FS.WritePatterns) == 0 {
		addErr(fmt.Sprintf("%s: script uses ctx.fs.write but with.capabilities.fs.write is empty", loc))
	}
	if (strings.Contains(scan, "ctx.fs.read") || strings.Contains(scan, "ctx.fs.exists") || strings.Contains(scan, "ctx.fs.glob")) && len(cap.FS.ReadPatterns) == 0 {
		addErr(fmt.Sprintf("%s: script uses ctx.fs read/exists/glob but with.capabilities.fs.read is empty", loc))
	}
	if strings.Contains(scan, "ctx.probe") && !cap.AllowsProbe() {
		addErr(fmt.Sprintf("%s: script uses ctx.probe but with.capabilities does not grant probe/vcs/github", loc))
	}
	if strings.Contains(scan, "gh.issue.list") && !containsString(cap.Probe.Names, "gh.issue.list") {
		addErr(fmt.Sprintf("%s: script uses gh.issue.list but with.capabilities.github.issues is not read", loc))
	}
	if strings.Contains(scan, "ctx.host") && !cap.AllowsHost() {
		addErr(fmt.Sprintf("%s: script uses ctx.host but with.capabilities.host.verbs is empty", loc))
	}
}

func stripStarlarkLineComments(source string) string {
	var b strings.Builder
	for _, line := range strings.Split(source, "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func pathWithinAnyStoryRoot(path string, def *AppDef) bool {
	if pathWithinRoot(path, def.BaseDir) {
		return true
	}
	for _, manifest := range def.LoadedManifests {
		if pathWithinRoot(path, filepath.Dir(manifest)) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// bareStarlarkInputRootRE matches a value that STARTS WITH a world-root accessor
// (world./slots./args./form. and the optional-chain/index forms) — the dominant
// shape of an unevaluated `inputs:` expression.
var bareStarlarkInputRootRE = regexp.MustCompile(`^(world|slots|args|form)\s*[.?\[]`)

// looksLikeBareStarlarkInputExpr reports whether a host.starlark.run `inputs:`
// string value is a bare (untemplated) expression. High-precision by design — it
// only fires inside an `inputs:` block, where values are never prose: a leading
// world-root accessor, or the `??` / `?.` expr operators that do not occur in
// real literals. A `{{ }}` / `{% %}` template is correct and never flagged.
func looksLikeBareStarlarkInputExpr(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || strings.Contains(t, "{{") || strings.Contains(t, "{%") {
		return false
	}
	if bareStarlarkInputRootRE.MatchString(t) {
		return true
	}
	return strings.Contains(t, "??") || strings.Contains(t, "?.")
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

// normalizeAndValidateOffRamps walks every state in the tree and enforces the
// agent off-ramp's load-time invariants (see docs/stories/meta-mode.md and
// the OffRampDef doc):
//
//   - An explicit `agent_off_ramp: false` is normalized to a nil pointer so
//     every downstream reader can treat `State.AgentOffRamp != nil` as "the
//     off-ramp fires." (goccy allocates the pointer even for `false`; the
//     def's enabled flag is the discriminant.)
//   - An off-ramp on a `terminal: true` state is rejected: a terminal state
//     ends the journey and never routes free text, so the flag is meaningless.
//   - An off-ramp on a `mode: conversational` state is rejected: that harness
//     is already free-form, so the flag is redundant and would shadow it.
//
// The off-ramp's agent: reference is validated separately in
// validateAgentReferences (the single agent-resolution site), so this pass
// only owns the placement invariants. Agent names are also collected here for
// that pass to consume.
func normalizeAndValidateOffRamps(file string, def *AppDef, errs *[]error) {
	if def == nil {
		return
	}
	var walk func(prefix string, states map[string]*State)
	walk = func(prefix string, states map[string]*State) {
		// Iterate in sorted name order for deterministic error ordering.
		for _, name := range sortedStateNames(states) {
			s := states[name]
			if s == nil {
				continue
			}
			path := joinPath(prefix, name)
			if s.AgentOffRamp != nil {
				if !s.AgentOffRamp.Enabled() {
					// Explicit `false`: drop the def so the runtime sees no
					// off-ramp. No further checks apply.
					s.AgentOffRamp = nil
				} else {
					if s.Terminal {
						*errs = append(*errs, &ValidationError{
							File: file,
							Message: fmt.Sprintf(
								"state %q declares agent_off_ramp on a terminal: true state; a terminal state never routes free text",
								path),
						})
					}
					if s.Mode == "conversational" {
						*errs = append(*errs, &ValidationError{
							File: file,
							Message: fmt.Sprintf(
								"state %q declares agent_off_ramp on a mode: conversational state; that harness is already free-form, so the flag is meaningless there",
								path),
						})
					}
				}
			}
			if len(s.States) > 0 {
				walk(path, s.States)
			}
		}
	}
	walk("", def.States)
}

// sortedStateNames returns the keys of a state map in lexical order so callers
// that emit per-state diagnostics produce deterministic output.
func sortedStateNames(states map[string]*State) []string {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

	// states.<path>.agent_off_ramp.agent — one site per off-ramp room. Walk
	// the full state tree (off-ramps may sit on nested states) in sorted path
	// order for stable error ordering. Mirrors the off_path.agent check.
	var walkOffRampAgents func(prefix string, states map[string]*State)
	walkOffRampAgents = func(prefix string, states map[string]*State) {
		for _, name := range sortedStateNames(states) {
			s := states[name]
			if s == nil {
				continue
			}
			path := joinPath(prefix, name)
			if s.AgentOffRamp != nil && s.AgentOffRamp.Agent != "" {
				if _, ok := known[s.AgentOffRamp.Agent]; !ok {
					addUnknown(s.AgentOffRamp.Agent, fmt.Sprintf("states.%s.agent_off_ramp.agent", path))
				}
			}
			if len(s.States) > 0 {
				walkOffRampAgents(path, s.States)
			}
		}
	}
	walkOffRampAgents("", def.States)

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
		if r.FreeFormFallback != nil {
			validateFreeFormFallback(file, def, errs)
		}
	}
}

func validateFreeFormFallback(file string, def *AppDef, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}
	cfg := def.Routing.FreeFormFallback
	statePath := strings.TrimSpace(cfg.State)
	intentName := strings.TrimSpace(cfg.Intent)
	if statePath == "" {
		addErr("routing.free_form_fallback.state is required")
		return
	}
	if intentName == "" {
		addErr("routing.free_form_fallback.intent is required")
		return
	}
	st, ok := lookupStateInMap(statePath, def.States)
	if !ok || st == nil {
		addErr(fmt.Sprintf("routing.free_form_fallback.state %q is not declared in states", statePath))
		return
	}
	if _, ok := st.On[intentName]; !ok {
		addErr(fmt.Sprintf("routing.free_form_fallback.intent %q has no matching on: arc in state %q", intentName, statePath))
		return
	}
	idef, found := intentDefInScope(def, statePath, intentName)
	if !found {
		addErr(fmt.Sprintf("routing.free_form_fallback.intent %q is not a declared intent", intentName))
		return
	}
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
		addErr(fmt.Sprintf("routing.free_form_fallback.intent %q must declare exactly one required string slot to receive the free-text utterance", intentName))
	}
}

func intentDefInScope(def *AppDef, statePath, intentName string) (Intent, bool) {
	parts := strings.Split(statePath, ".")
	for i := len(parts); i >= 1; i-- {
		p := strings.Join(parts[:i], ".")
		if st, ok := lookupStateInMap(p, def.States); ok && st != nil {
			if ix, found := st.Intents[intentName]; found {
				return ix, true
			}
		}
	}
	ix, ok := def.Intents[intentName]
	return ix, ok
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
	return a.def.LookupState(p)
}

// LookupState resolves a dot-separated state path (e.g. "core.landing", the
// shape a compound import wrapper produces) through def's nested state tree.
// Callers outside this package that hold a bare *AppDef (no appImpl) — e.g.
// internal/orchestrator's workbench_gate_signal.go, which must resolve a
// dispatching state that may sit under an imported alias's compound wrapper —
// need this directly rather than reaching for the flat, single-level
// def.States[...] index, which only ever resolves a TOP-LEVEL state name and
// silently misses (nil, false) anything nested under a compound wrapper.
func (def *AppDef) LookupState(p StatePath) (*State, bool) {
	if def == nil {
		return nil, false
	}
	return lookupStateInMap(string(p), def.States)
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
