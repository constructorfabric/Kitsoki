package orchestrator

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"kitsoki/internal/app"
)

// ValidatePromptExtensions walks every effect's static prompt reference in the
// story and loads+parses it through the prompt renderer, so a malformed prompt
// extension — a missing file, an unresolved {% extends %} / {% include %}, an
// unknown @import alias, a self-reference, or a syntax error — fails fast at
// load with a located message instead of surfacing only when that oracle
// effect first fires. Templated refs (those containing {{ / {%, resolved per
// turn) and non-prompt effects are skipped. Returns one error per bad ref,
// sorted; nil/empty when the story has no on-disk prompt renderer (LoadBytes /
// tests) or every reference is valid. See docs/stories/prompts.md.
func (o *Orchestrator) ValidatePromptExtensions() []error {
	if o == nil || o.promptRenderer == nil || o.def == nil {
		return nil
	}
	var errs []error
	// A configured overlay dir that doesn't exist is almost always a typo'd
	// --prompt-overlay path; without this the overlay silently falls back to
	// the story base and the project's specialization just never applies.
	if ov := o.promptRenderer.OverlayDir(); ov != "" {
		if info, err := os.Stat(ov); err != nil || !info.IsDir() {
			errs = append(errs, fmt.Errorf("prompt overlay %q is not a readable directory (typo'd --prompt-overlay or prompts.overlay?)", ov))
		}
	}
	refs := map[string]bool{}
	collectPromptRefs(o.def.States, refs)

	ordered := make([]string, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Strings(ordered)

	for _, ref := range ordered {
		if err := o.promptRenderer.ValidatePrompt(ref); err != nil {
			errs = append(errs, err)
			continue // a parse failure makes block analysis meaningless
		}
		// Catch a silent specialization no-op: an override block that no
		// template in the extends chain declares (pongo2 ignores it).
		if dead := o.promptRenderer.OverrideIssues(ref); len(dead) > 0 {
			errs = append(errs, fmt.Errorf(
				"prompt %q overrides block(s) %s that no base in its {%% extends %%} chain declares — the override would silently do nothing (typo, or a block renamed in the base?)",
				ref, strings.Join(dead, ", ")))
		}
	}
	return errs
}

// collectPromptRefs walks the state tree and records every static
// prompt / prompt_path arg found in effect with: blocks.
func collectPromptRefs(states map[string]*app.State, out map[string]bool) {
	for _, s := range states {
		if s == nil {
			continue
		}
		collectPromptRefsInEffects(s.OnEnter, out)
		for _, list := range s.On {
			for i := range list {
				collectPromptRefsInEffects(list[i].Effects, out)
			}
		}
		if len(s.States) > 0 {
			collectPromptRefs(s.States, out)
		}
	}
}

func collectPromptRefsInEffects(effs []app.Effect, out map[string]bool) {
	for i := range effs {
		// Top-level prompt refs (host.oracle.ask / ask_with_mcp / decide).
		collectPromptRefsFromMap(effs[i].With, out)
		// host.oracle.task nests the prompt under with.context.prompt.
		if ctx, ok := effs[i].With["context"].(map[string]any); ok {
			collectPromptRefsFromMap(ctx, out)
		}
		collectPromptRefsInEffects(effs[i].OnComplete, out)
		collectPromptRefsInEffects(effs[i].Effects, out)
	}
}

// collectPromptRefsFromMap pulls static prompt / prompt_path string refs from a
// with: (or with.context:) map, skipping inline/templated values.
func collectPromptRefsFromMap(m map[string]any, out map[string]bool) {
	for _, key := range []string{"prompt", "prompt_path"} {
		raw, ok := m[key].(string)
		if !ok {
			continue
		}
		ref := strings.TrimSpace(raw)
		// Skip inline/templated values: a multi-line inline prompt, or a path
		// built from {{ … }} that only resolves per turn.
		if ref == "" || strings.ContainsAny(ref, "\n\r") || strings.Contains(ref, "{{") || strings.Contains(ref, "{%") {
			continue
		}
		out[ref] = true
	}
}

// PromptValidationError wraps the aggregated prompt-extension load errors into
// one error suitable for aborting a run at startup.
func PromptValidationError(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "prompt extension: %d invalid prompt reference(s):", len(errs))
	for _, e := range errs {
		fmt.Fprintf(&b, "\n  - %v", e)
	}
	return fmt.Errorf("%s", b.String())
}
