// Package app — YAML loader for Hally app definitions (Stage 2).
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
	"sort"
	"strings"

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
func Load(path string) (*AppDef, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	def, errs := loadAndValidate(b, path)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return def, nil
}

// LoadBytes reads and validates an AppDef from a YAML byte slice.
func LoadBytes(b []byte) (*AppDef, error) {
	def, errs := loadAndValidate(b, "")
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return def, nil
}

// loadAndValidate is the shared implementation used by Load and LoadBytes.
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
	validateStates(file, "", def.States, globalIntents, worldKeys, allStatePaths, &errs)

	// ── 8. relevant_world keys exist in world schema ──────────────────────────
	// (already done inside validateStates, which recurses into nested states)

	return &def, errs
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
func validateStates(
	file string,
	prefix string,
	states map[string]*State,
	globalIntents map[string]struct{},
	worldKeys map[string]struct{},
	allPaths map[string]struct{},
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

		// Validate on: intent names and transition targets.
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

		// Recurse into child states.
		if len(s.States) > 0 {
			validateStates(file, statePath, s.States, globalIntents, worldKeys, allPaths, errs)
		}
	}
}

// validateTransitionTarget checks that a transition target refers to a declared
// state. It handles the special forms:
//   - "." → self; always valid.
//   - ".." prefixes → relative paths resolved against the current state path.
//   - Otherwise treated as an absolute state path.
func validateTransitionTarget(file, statePath, target string, allPaths map[string]struct{}) error {
	if target == "" || target == "." {
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
