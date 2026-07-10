// Package host — system-prompt composition seam for the agent verb handlers.
//
// Every agent verb (ask, decide, task, converse, extract, ask_with_mcp,
// ask_structured) builds its `claude` system prompt through one path here, so
// the layered kitsoki → project → task grounding (see internal/sysprompt) is
// applied uniformly and the older "--append-system-prompt onto Claude Code's
// default" posture survives only behind an explicit per-agent escape hatch.
//
// The Layer-2 (project) body is resolved here, through the SAME prompt renderer
// the handlers already use for prompt files (PromptRendererFromCtx), so an
// app's project context can {% include "@shared/…" %} like any other prompt.
// The pure ordering/join/policy lives in internal/sysprompt; this file only does
// the I/O (read app.context / context_path / the prompts/_project.md convention,
// render it) that a leaf package must not.
package host

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/sysprompt"
)

// projectContextKey is the unexported context key for the injected project
// context config.
type projectContextKey struct{}

// ProjectContext carries the app's Layer-2 authoring config, translated from
// AppMeta by the orchestrator. At most one field is set; both empty means the
// app authored no project context (the prompts/_project.md convention may still
// supply one).
type ProjectContext struct {
	Inline string // app.context — inline template string
	Path   string // app.context_path — prompt reference (overlay/@shared-resolved)
}

// WithProjectContext injects the app's Layer-2 config into ctx so the agent
// handlers can compose it into the system prompt. Mirrors WithPromptRenderer /
// WithAgents — the orchestrator sets it per host-call dispatch. Passing an empty
// ProjectContext is safe (handlers then fall back to the prompts/_project.md
// convention, then to no project layer).
func WithProjectContext(ctx context.Context, pc ProjectContext) context.Context {
	if pc.Inline == "" && pc.Path == "" {
		return ctx
	}
	return context.WithValue(ctx, projectContextKey{}, pc)
}

func projectContextFromCtx(ctx context.Context) ProjectContext {
	pc, _ := ctx.Value(projectContextKey{}).(ProjectContext)
	return pc
}

// projectConventionRef is the optional Layer-2 source discovered when neither
// app.context nor app.context_path is set — resolved through the prompt
// renderer's search path like any other reference.
const projectConventionRef = "prompts/_project.md"

// kitsokiOverlayRef lets a project shadow the embedded Layer-1 kitsoki fragment
// with its own copy on the prompt search path (an experiment), without
// recompiling. Empty result → the embedded default is used.
const kitsokiOverlayRef = "sysprompt/kitsoki.md"

// projectSystemPromptRel is the transparent, project-owned Layer-1 source.
// A story rooted below the project searches upward for this file before falling
// back to the story prompt search path and finally the embedded default.
const projectSystemPromptRel = ".kitsoki/system-prompt.md"

// composeAgentSystemPrompt builds the layered system prompt for a verb and the
// resolved Layer-3 persona, pulling Layer 2 (project) from ctx and Layer 1
// (kitsoki) from the engine default (or a @shared overlay if the project ships
// one). Pure assembly is delegated to sysprompt.Compose.
//
// Stable-prefix invariant (docs/architecture/system-prompt.md;
// docs/architecture/hosts.md#cache-usage-visibility-and-the-pre-dispatch-budget-gate):
// every
// layer here is static for a given (verb, agent) pair — kitsoki is a compiled
// constant, project is the app's declared context, and persona is the
// author's declared agent.SystemPrompt / call-site system_prompt: override.
// None of the three may carry per-call volatile data (turn numbers,
// timestamps, artifact IDs, story/journey snapshots) — that data belongs in
// the verb's PROMPT (the user message built by each handler's own
// prompt/context resolution, e.g. resolveDecidePrompt), never in the system
// prompt, so the composed prefix stays byte-identical (and thus
// cache-eligible) across every call to the same agent.
func composeAgentSystemPrompt(ctx context.Context, verb sysprompt.Verb, persona string) sysprompt.Composed {
	return sysprompt.Compose(sysprompt.Spec{
		Verb:    verb,
		Kitsoki: resolveKitsokiOverride(ctx),
		Project: resolveProjectLayer(ctx),
		Task:    persona,
	})
}

// appendComposedSystemPrompt appends the system-prompt CLI flags for a verb.
//
// Default (inheritDefault false): the kitsoki → project → persona layers are
// composed and passed via --system-prompt, which REPLACES Claude Code's default;
// --exclude-dynamic-system-prompt-sections is added for every verb except task
// (whose agentic repo work wants cwd/git/env). Because Layer 1 is always
// present, the composed prompt is never empty — every agent call is grounded.
//
// Escape hatch (inheritDefault true, from agent.inherit_claude_default): the
// persona is appended via --append-system-prompt onto Claude Code's default, the
// legacy posture, and no kitsoki/project grounding is prepended.
//
// Returns the updated args and the Composed result (zero-valued on the legacy
// path) so callers can record the layer manifest on the trace.
func appendComposedSystemPrompt(ctx context.Context, cliArgs []string, verb sysprompt.Verb, persona string, inheritDefault bool) ([]string, sysprompt.Composed) {
	if inheritDefault {
		if strings.TrimSpace(persona) != "" {
			cliArgs = append(cliArgs, "--append-system-prompt", persona)
		}
		return cliArgs, sysprompt.Composed{}
	}
	composed := composeAgentSystemPrompt(ctx, verb, persona)
	if strings.TrimSpace(composed.SystemPrompt) != "" {
		cliArgs = append(cliArgs, "--system-prompt", composed.SystemPrompt)
		if composed.ExcludeDynamic {
			cliArgs = append(cliArgs, "--exclude-dynamic-system-prompt-sections")
		}
	}
	slog.DebugContext(ctx, "sysprompt.composed",
		"verb", string(verb),
		"layers", sysprompt.LayerNames(composed.Layers),
		"bytes", len(composed.SystemPrompt),
		"exclude_dynamic", composed.ExcludeDynamic,
	)
	return cliArgs, composed
}

// resolveProjectLayer renders the project's Layer-2 grounding (may be ""):
// app.context inline → app.context_path file → prompts/_project.md convention.
// Rendered through the ctx prompt renderer so {% include "@shared/…" %} resolves;
// source-color sentinels are stripped like every other rendered prompt.
func resolveProjectLayer(ctx context.Context) string {
	pc := projectContextFromCtx(ctx)
	if strings.TrimSpace(pc.Inline) != "" {
		return renderProjectSource(ctx, pc.Inline, "app.context", true)
	}
	if ref := strings.TrimSpace(pc.Path); ref != "" {
		return renderProjectFile(ctx, ref, true)
	}
	// Convention fallback: silently absent when the file doesn't exist.
	return renderProjectFile(ctx, projectConventionRef, false)
}

// renderProjectFile resolves ref through the prompt search path, reads it, and
// renders it. warnMissing controls whether a read failure is logged (a declared
// context_path that's missing is worth a warn; the optional convention file is
// not).
func renderProjectFile(ctx context.Context, ref string, warnMissing bool) string {
	abs := resolvePromptPathCtx(ctx, ref)
	body, err := os.ReadFile(abs)
	if err != nil {
		if warnMissing {
			slog.WarnContext(ctx, "sysprompt: read project context failed; project layer omitted",
				"ref", ref, "error", err)
		}
		return ""
	}
	return renderProjectSource(ctx, string(body), ref, warnMissing)
}

// renderProjectSource renders a project-context template body through the ctx
// renderer and strips source-color. warnOnError logs render failures.
func renderProjectSource(ctx context.Context, src, ref string, warnOnError bool) string {
	out, err := renderPromptBytes(ctx, src, nil)
	if err != nil {
		if warnOnError {
			slog.WarnContext(ctx, "sysprompt: render project context failed; project layer omitted",
				"ref", ref, "error", err)
		}
		return ""
	}
	return strings.TrimSpace(sourcecolor.Strip(out))
}

// resolveKitsokiOverride returns a project-visible Layer-1 kitsoki fragment
// when one is available, else "" (use the embedded default). Resolution order:
// a nearest-upward .kitsoki/system-prompt.md from the story/project root, then
// the legacy prompt-search-path sysprompt/kitsoki.md override. Both render
// through the prompt TemplateSet, so {% include %} / {% extends %} keep the same
// extension semantics as story prompts.
func resolveKitsokiOverride(ctx context.Context) string {
	if abs := findProjectSystemPrompt(ctx); abs != "" {
		if rendered := renderProjectFile(ctx, abs, false); strings.TrimSpace(rendered) != "" {
			return rendered
		}
	}

	pr := PromptRendererFromCtx(ctx)
	if pr == nil {
		return ""
	}
	abs, ok := pr.ResolvePromptName(kitsokiOverlayRef)
	if !ok {
		return ""
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return renderProjectSource(ctx, string(body), kitsokiOverlayRef, false)
}

func findProjectSystemPrompt(ctx context.Context) string {
	if pr := PromptRendererFromCtx(ctx); pr != nil && strings.TrimSpace(pr.RootDir()) != "" {
		if abs, err := filepath.Abs(pr.RootDir()); err == nil {
			return findUpward(abs, projectSystemPromptRel)
		}
		return ""
	}
	var starts []string
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	seen := map[string]bool{}
	for _, start := range starts {
		if start == "" {
			continue
		}
		abs, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		if found := findUpward(abs, projectSystemPromptRel); found != "" {
			return found
		}
	}
	return ""
}

func findUpward(start, rel string) string {
	dir := start
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
