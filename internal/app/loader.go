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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"kitsoki/internal/agents"

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
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	// Parse just enough to find the include: list before full validation.
	baseDir := filepath.Dir(path)
	merged, mergeErrs := parseAndMerge(b, path, baseDir)
	if len(mergeErrs) > 0 {
		return nil, errors.Join(mergeErrs...)
	}

	// Resolve imports recursively, folding each child into merged.
	canonical := canonicalPath(path)
	// Seed LoadedManifests with the root manifest's canonical path; each
	// folded import appends itself + its own transitive manifests. The
	// metamode controller reads this list to auto-watch every file the
	// loader actually touched (proposal §16.4).
	merged.LoadedManifests = appendUnique(merged.LoadedManifests, canonical)
	if importErrs := resolveImports(merged, path, baseDir, []string{canonical}); len(importErrs) > 0 {
		return nil, errors.Join(importErrs...)
	}

	// Expand phase templates into concrete states before validation so the
	// referential-integrity pass sees the synthesised states (proposal §5).
	if expandErrs := expandPhases(merged, path); len(expandErrs) > 0 {
		return nil, errors.Join(expandErrs...)
	}

	// Inject builtin meta_modes (`self`, `bug`) that the app didn't
	// declare itself. Done before validation so trigger collisions and
	// missing-env-var diagnostics fire the same way as for app-declared
	// modes.
	injectBuiltinMetaModes(merged)

	// Final host_interface resolution: rewrites every remaining
	// iface.<name>.<op> reference to a concrete <binding>.<op> host
	// invocation, after all imports have folded their iface declarations
	// into def.HostInterfaces (under alias-prefixed names) and after
	// each layer's host_bindings have had a chance to override defaults.
	// This is the surface that makes multi-layer rebinding compose per
	// proposal §11.2.
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
// Collisions are errors (§9.1).
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
	}
	return errs
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

	// ── 2. world schema ──────────────────────────────────────────────────────
	worldKeys := make(map[string]struct{}, len(def.World))
	for k := range def.World {
		worldKeys[k] = struct{}{}
	}

	// ── 3. initial state ─────────────────────────────────────────────────────
	rootName := ""
	switch v := def.Root.(type) {
	case string:
		rootName = v
	case map[string]interface{}:
		// Inline compound/parallel root — §3.3. For PoC we only validate that
		// "type" is present; deeper validation is a Stage-3 concern.
	default:
		addErr(fmt.Sprintf("root: unsupported value type %T; expected a state name string", def.Root))
	}

	// ── 4. collect all declared state paths ──────────────────────────────────
	allStatePaths := make(map[string]struct{})
	collectStatePaths("", def.States, allStatePaths)

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
	validateStates(file, "", def.States, globalIntents, worldKeys, allStatePaths, allowedHosts, declaredAgents, &errs)

	// ── 7b. (removed) off-path agent reference: superseded by §9b
	// validateAgentReferences which also recognises builtin agent names
	// like `story-author`.

	// ── 8. relevant_world keys exist in world schema ──────────────────────────
	// (already done inside validateStates, which recurses into nested states)

	// ── 9. meta-mode validation ──────────────────────────────────────────────
	validateMetaModes(file, def, &errs)

	// ── 9b. cross-reference: every agent name referenced anywhere in the
	// AppDef must resolve in AppDef.Agents or agents.BuiltinNames().
	validateAgentReferences(file, def, &errs)

	// ── 9c. reach-into-child guard (story-imports proposal §8 / §16.7).
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
			validateOnCompleteEffect(file, childLoc, "", child, allowedHosts, declaredAgents, allStatePaths, &errs)
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

// joinPath combines an optional parent prefix and a child name using dot separator.
// The design uses "bar.dark" style (§3.1); YAML authors write "../../foyer" for
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
func validateStates(
	file string,
	prefix string,
	states map[string]*State,
	globalIntents map[string]struct{},
	worldKeys map[string]struct{},
	allPaths map[string]struct{},
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

		// Validate relevant_world references.
		for _, wk := range s.RelevantWorld {
			if _, ok := worldKeys[wk]; !ok {
				addErr(fmt.Sprintf("state %q: relevant_world key %q is not declared in world schema", statePath, wk))
			}
		}

		// Validate on: intent names, transition targets, and effect hosts.
		intentNames := sortedKeys(s.On)
		for _, intentName := range intentNames {
			// Wildcard "*" is always allowed (§3.1).
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
				// Validate invoke: host.* effects against the allow-list.
				for i, eff := range tr.Effects {
					if eff.Invoke != "" && len(allowedHosts) > 0 {
						if _, ok := allowedHosts[eff.Invoke]; !ok {
							addErr(fmt.Sprintf("state %q intent %q: effect invoke %q is not declared in app hosts", statePath, intentName, eff.Invoke))
						}
					}
					validateAgentRef(file, fmt.Sprintf("state %q intent %q effects[%d]", statePath, intentName, i), eff, declaredAgents, errs)
					validateBackgroundEffect(file, fmt.Sprintf("state %q intent %q effects[%d]", statePath, intentName, i), statePath, eff, allowedHosts, declaredAgents, allPaths, errs)
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
			validateBackgroundEffect(file, fmt.Sprintf("state %q on_enter[%d]", statePath, i), statePath, eff, allowedHosts, declaredAgents, allPaths, errs)
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
			// Expressions contain "{{" and are skipped here (evaluated at runtime).
			if !strings.Contains(s.Initial, "{{") {
				childPath := joinPath(statePath, s.Initial)
				if _, ok := allPaths[childPath]; !ok {
					addErr(fmt.Sprintf("state %q: initial child %q does not exist", statePath, s.Initial))
				}
			}
		}

		// Validate parallel state shape (proposal §9.4): each parallel state
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

		// Recurse into child states.
		if len(s.States) > 0 {
			validateStates(file, statePath, s.States, globalIntents, worldKeys, allPaths, allowedHosts, declaredAgents, errs)
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
// The design uses slash-separated relative refs (§3.1) like "../../foyer" but
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
func validateBackgroundEffect(file, location, originStatePath string, eff Effect, allowedHosts, declaredAgents, allStatePaths map[string]struct{}, errs *[]error) {
	validateBackgroundEffectAware(file, location, originStatePath, eff, false /* outer effect, not inside on_complete */, allowedHosts, declaredAgents, allStatePaths, errs)
}

// validateOnCompleteEffect is the entry point used at proposal-execute call
// sites where the iterated effect is already an on_complete entry (the
// caller has unrolled the first level of the on_complete: list before
// invoking). Target rules apply directly to eff in addition to its
// descendants.
func validateOnCompleteEffect(file, location, originStatePath string, eff Effect, allowedHosts, declaredAgents, allStatePaths map[string]struct{}, errs *[]error) {
	validateBackgroundEffectAware(file, location, originStatePath, eff, true /* eff is already inside on_complete */, allowedHosts, declaredAgents, allStatePaths, errs)
}

// validateBackgroundEffectAware is the on_complete-aware implementation.
// insideOnComplete is true when eff itself is an entry inside a parent's
// on_complete: list — Target is only legal in that case.
func validateBackgroundEffectAware(file, location, originStatePath string, eff Effect, insideOnComplete bool, allowedHosts, declaredAgents, allStatePaths map[string]struct{}, errs *[]error) {
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}
	if eff.Background && eff.Invoke == "" {
		addErr(fmt.Sprintf("%s: background: true requires invoke: to be set", location))
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
		validateBackgroundEffectAware(file, loc, originStatePath, child, true /* this child IS inside on_complete */, allowedHosts, declaredAgents, allStatePaths, errs)
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

// metaEnvVarRE matches `$NAME` and `${NAME}` tokens in a cwd: string.
var metaEnvVarRE = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// validateMetaModes checks every entry in def.MetaModes for required fields,
// trigger uniqueness, intent-name collisions, and resolves cwd: env vars.
// Successful cwd expansion is written back into the struct.
func validateMetaModes(file string, def *AppDef, errs *[]error) {
	if len(def.MetaModes) == 0 {
		return
	}
	addErr := func(msg string) {
		*errs = append(*errs, &ValidationError{File: file, Message: msg})
	}

	// Track triggers across modes to detect duplicates. Map trigger → first
	// mode that claimed it, in declaration-sorted order for determinism.
	triggerOwner := make(map[string]string, len(def.MetaModes))
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
		if m.Trigger != "" {
			if prior, ok := triggerOwner[m.Trigger]; ok {
				addErr(fmt.Sprintf("meta_mode %q: trigger %q already claimed by meta_mode %q", name, m.Trigger, prior))
			} else {
				triggerOwner[m.Trigger] = name
				// Note: no warning channel exists in the loader. Per the
				// meta-mode plan we error on collisions with the global
				// intents library.
				if _, clash := def.Intents[m.Trigger]; clash {
					addErr(fmt.Sprintf("meta_mode %q: trigger %q collides with a declared intent of the same name", name, m.Trigger))
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

// validateNoReachIntoChild implements story-imports proposal §8/§16.7.
// A parent transition targeting `<alias>.<deeper>` couples the parent
// to the child's internals, which the proposal forbids. The canonical
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
				"state %q %s targets %q which reaches into the imported child %q past its entry %q; use target: %q to invoke the child or have the child expose a new exit (proposal §8/§16.7)",
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
// first and then the global intent library (§3.4).
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
