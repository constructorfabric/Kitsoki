// Package app — story-imports overrides.
//
// See docs/stories/imports.md for the import/override authoring model.
//
// Overrides patch a child app's states / intents / prompts at import time.
// The import declares:
//
//	imports:
//	  bf:
//	    overrides:
//	      states:   { applying: {...replacement state...} }
//	      intents:  { trigger_deploy: {...replacement intent...} }
//	      prompts:  { shell_repair.md: ./prompts/custom_shell_repair.md }
//
// Semantics: whole-element replacement, not deep-merge. Validation fails
// when an override targets a name the child does not actually declare —
// this catches typos at load time rather than letting them silently no-op.
//
// Override is applied BEFORE the child is namespace-flattened so the
// override.states / override.intents keys reference child-local names
// (not <alias>/<name>). The child rewriter then walks the overridden
// shape during the normal fold pass.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// applyOverrides walks imp.Overrides and patches child in place. Errors
// are aggregated.
//
//	parentBaseDir  — directory of the parent app.yaml (for prompt path
//	                 resolution; override.prompts paths are author-relative
//	                 to the parent).
//	childBaseDir   — directory of the child app.yaml (where the prompt
//	                 file would live unaltered; we replace its contents on
//	                 disk-relative reads at load time by remapping the
//	                 path the child loader reads from).
func applyOverrides(child *AppDef, ov *ImportOverrides, file, alias, parentBaseDir, childBaseDir string) []error {
	if child == nil || ov == nil {
		return nil
	}
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("imports.%s: overrides: %s", alias, msg)})
	}

	// State overrides — replace by child-local name. The match must exist
	// somewhere in the child's state tree (top-level or nested); we only
	// replace top-level matches in v1 because nested replacement gets
	// surprising (the override contract says "replaces the child's state of
	// that name" without disambiguating nesting; we pick the safe rule).
	for _, name := range sortedKeys(ov.States) {
		newState := ov.States[name]
		if _, ok := child.States[name]; !ok {
			addErr(fmt.Sprintf("states.%s: child does not declare a top-level state named %q", name, name))
			continue
		}
		child.States[name] = newState
	}

	// Intent overrides — replace named child intents. The intent must
	// already exist in the child's library; if not, this is a typo.
	for _, name := range sortedKeys(ov.Intents) {
		if _, ok := child.Intents[name]; !ok {
			addErr(fmt.Sprintf("intents.%s: child does not declare an intent named %q", name, name))
			continue
		}
		if child.Intents == nil {
			child.Intents = make(map[string]Intent)
		}
		child.Intents[name] = ov.Intents[name]
	}

	// Prompt overrides — copy parent's file into the location the child
	// reads from. The simplest correct implementation: read the override
	// file from the parent's dir, write it to the child's expected path
	// in a temp area, then point any host.agent.* invocation that reads
	// that path at the override.
	//
	// In practice prompt-bearing invocations carry the path as a
	// `with: { prompt: "<relative>" }` arg. We rewrite those arg values
	// from <relative> to the override's resolved path when a match exists.
	if len(ov.Prompts) > 0 {
		// Validate every override path exists; resolve to absolute.
		resolved := make(map[string]string, len(ov.Prompts))
		for _, rel := range sortedKeys(ov.Prompts) {
			overridePath := ov.Prompts[rel]
			if !filepath.IsAbs(overridePath) {
				overridePath = filepath.Join(parentBaseDir, overridePath)
			}
			if _, statErr := os.Stat(overridePath); statErr != nil {
				addErr(fmt.Sprintf("prompts.%s: %v", rel, statErr))
				continue
			}
			// child path resolves relative to childBaseDir for matching.
			childRel := rel
			if !filepath.IsAbs(childRel) {
				childRel = filepath.Join(childBaseDir, rel)
			}
			resolved[childRel] = overridePath
			// Also key by the bare relative form so authors can write
			// either form in `with: { prompt: ... }`.
			resolved[rel] = overridePath
		}
		if len(resolved) > 0 {
			applyPromptOverridesToStates(child.States, resolved)
		}
	}

	return errs
}

// rebaseEffectPaths walks an imported child's state tree and rewrites
// every relative `prompt:` / `schema:` / `script:` arg in effect `with:` blocks to
// an absolute path rooted at the child's directory. Without this, the
// runtime joins the relative path against $KITSOKI_APP_DIR (the parent
// app's directory) and fails to find files that live in the child
// story's prompts/, schemas/, or scripts/ tree.
//
// Idempotent: paths already absolute or containing template syntax
// (`{{`) are left alone — the latter because we can't resolve them
// statically and the runtime renders them at dispatch time.
func rebaseEffectPaths(states map[string]*State, childDir string) {
	if childDir == "" {
		return
	}
	// Absolutize childDir so rebased paths become absolute. This is what makes
	// the rebase idempotent across TRANSITIVE imports: when story A imports B
	// which imports C, C's prompt paths are first rebased to C's dir, then B
	// (with C folded in) is rebased again at A's level. If the first rebase
	// left a RELATIVE path (which it does when the app was loaded via a relative
	// path, e.g. `stories/pets-dev`), the second pass re-prefixes it with B's
	// dir — producing `stories/A/stories/C/prompts/...`. Making the first rebase
	// absolute means the second pass's filepath.IsAbs guard (in rebaseWithMap)
	// skips the already-rebased path, so C's prompts resolve to C's real dir.
	if !filepath.IsAbs(childDir) {
		if abs, err := filepath.Abs(childDir); err == nil {
			childDir = abs
		}
	}
	for _, s := range states {
		if s == nil {
			continue
		}
		rebaseWorkbenchPaths(s.Workbench, childDir)
		rebaseEffectPathsInEffects(s.OnEnter, childDir)
		for _, list := range s.On {
			for i := range list {
				rebaseEffectPathsInEffects(list[i].Effects, childDir)
			}
		}
		if len(s.States) > 0 {
			rebaseEffectPaths(s.States, childDir)
		}
	}
}

func rebaseWorkbenchPaths(decl *WorkbenchDecl, childDir string) {
	if decl == nil {
		return
	}
	decl.Prompt = rebasePathValue(decl.Prompt, childDir)
	decl.AcceptanceSchema = rebasePathValue(decl.AcceptanceSchema, childDir)
}

func rebaseEffectPathsInEffects(effs []Effect, childDir string) {
	for i := range effs {
		rebaseWithMap(effs[i].With, childDir)
		rebaseEffectPathsInEffects(effs[i].Effects, childDir)
		for j := range effs[i].OnComplete {
			rebaseWithMap(effs[i].OnComplete[j].With, childDir)
			rebaseEffectPathsInEffects(effs[i].OnComplete[j].Effects, childDir)
		}
	}
}

func rebaseWithMap(with map[string]any, childDir string) {
	for _, key := range []string{"prompt", "prompt_path", "schema", "script"} {
		raw, ok := with[key].(string)
		if !ok {
			continue
		}
		with[key] = rebasePathValue(raw, childDir)
	}
	// host.agent.task nests prompt/prompt_path under with.context and the
	// acceptance schema under with.acceptance.schema. Both must rebase to the
	// defining story's dir, else the runtime joins them against the PARENT
	// app dir ($KITSOKI_APP_DIR) and the file isn't found.
	if ctx, ok := with["context"].(map[string]any); ok {
		rebaseWithMap(ctx, childDir)
	}
	if acc, ok := with["acceptance"].(map[string]any); ok {
		rebaseWithMap(acc, childDir)
	}
}

func rebasePathValue(raw, childDir string) string {
	if raw == "" {
		return raw
	}
	if filepath.IsAbs(raw) {
		return raw
	}
	if containsTemplate(raw) {
		return raw
	}
	return filepath.Join(childDir, raw)
}

// containsTemplate reports whether s carries a pongo2/expr template
// delimiter — `{{` or `{%`. Used to guard static path rewrites from
// touching dynamic expressions the runtime renders at dispatch time.
func containsTemplate(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "{%")
}

// applyPromptOverridesToStates walks every Effect.With["prompt"] in the
// child's state tree and rewrites the value when a key matches `resolved`.
// `resolved` keys are both the relative path the child author wrote and
// the abs path the loader would resolve to; the lookup tries both.
func applyPromptOverridesToStates(states map[string]*State, resolved map[string]string) {
	for _, s := range states {
		if s == nil {
			continue
		}
		applyPromptOverridesToEffects(s.OnEnter, resolved)
		for _, list := range s.On {
			for i := range list {
				applyPromptOverridesToEffects(list[i].Effects, resolved)
			}
		}
		if len(s.States) > 0 {
			applyPromptOverridesToStates(s.States, resolved)
		}
	}
}

func applyPromptOverridesToEffects(effs []Effect, resolved map[string]string) {
	for i := range effs {
		if len(effs[i].With) > 0 {
			if raw, ok := effs[i].With["prompt"]; ok {
				if s, isStr := raw.(string); isStr {
					if newPath, hit := resolved[s]; hit {
						effs[i].With["prompt"] = newPath
					}
				}
			}
		}
		if len(effs[i].OnComplete) > 0 {
			applyPromptOverridesToEffects(effs[i].OnComplete, resolved)
		}
		if len(effs[i].Effects) > 0 {
			applyPromptOverridesToEffects(effs[i].Effects, resolved)
		}
	}
}
