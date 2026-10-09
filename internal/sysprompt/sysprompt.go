package sysprompt

import (
	_ "embed"
	"strings"
)

// Verb identifies the kind of claude invocation a system prompt is being
// composed for. It is the one knob that varies engine policy: which short verb
// contract leads the task layer, and whether Claude Code's dynamic
// system-prompt sections (cwd / git / env / memory) are kept or excluded.
//
// The values are stable, lowercase strings so they read cleanly in the trace
// (the AgentCalled breadcrumb records the verb alongside the layer manifest).
type Verb string

const (
	Route         Verb = "route"          // intent routing (the harness)
	Ask           Verb = "ask"            // host.agent.ask
	Decide        Verb = "decide"         // host.agent.decide
	Task          Verb = "task"           // host.agent.task (agentic, repo-aware)
	Converse      Verb = "converse"       // host.agent.converse
	Extract       Verb = "extract"        // host.agent.extract
	AskWithMCP    Verb = "ask_with_mcp"   // host.agent.ask_with_mcp
	AskStructured Verb = "ask_structured" // host.agent.ask_structured
	Codeact       Verb = "codeact"        // host.agent.codeact
)

//go:embed templates/kitsoki.md
var kitsokiMD string

// KitsokiLayer returns the engine's built-in Layer-1 grounding: what kitsoki is,
// the operator's role, and how to behave. It is identical for every call
// everywhere, which is what makes it the top of the cache-friendly prefix.
// Callers may override it (an experiment shadowing the embedded fragment via a
// `@shared/` overlay) by setting Spec.Kitsoki; an empty override falls back to
// this default.
func KitsokiLayer() string { return strings.TrimSpace(kitsokiMD) }

// verbContract returns a one-or-two line, verb-specific reminder that leads the
// task layer. It is deliberately terse and non-overlapping with KitsokiLayer
// (which already covers the general output discipline) — it only states what
// *this* verb's call is for. Empty for verbs whose own task text (e.g. routing's
// Intent Library + Tool Contract) already makes the contract explicit.
func verbContract(v Verb) string {
	switch v {
	case Decide:
		return "This call resolves a single decision gate. Return exactly one verdict that conforms to the provided schema."
	case Ask, AskWithMCP:
		return "This call answers one question. Return the answer directly, grounded only in the given context and any tools provided."
	case AskStructured:
		return "This call answers one question as structured data. Return a single object that conforms to the provided schema."
	case Extract:
		return "This call extracts structured data from the given input. Return only fields supported by that input, conforming to the provided schema."
	case Task:
		return "This call performs scoped agentic work in a working directory. Use your tools to accomplish exactly the stated task and nothing beyond it."
	case Converse:
		return "This is a turn in a bounded conversation. Stay in character and in scope; advance the exchange without taking on work outside it."
	case Codeact:
		return "This call runs a bounded code-acting loop. Emit Starlark snippets against the declared capability allowlist, observe each structured result, and terminate by calling done() with output conforming to the provided schema."
	case Route:
		// Routing supplies its own Intent Library + Tool Contract in the task
		// layer; a generic contract here would only be redundant.
		return ""
	default:
		return ""
	}
}

// Spec is the input to Compose: the verb plus the three already-resolved layer
// bodies. Keeping the layers as plain strings (resolved by the caller through
// the app's prompt renderer) is what lets this package stay a pure,
// dependency-free, table-testable leaf — it owns ordering, the verb contract,
// the embedded Layer 1, and the dynamic-sections policy, and nothing else.
type Spec struct {
	Verb Verb
	// Kitsoki overrides the embedded Layer-1 grounding when non-empty (an
	// overlay experiment); empty uses KitsokiLayer().
	Kitsoki string
	// Project is the rendered Layer-2 project grounding (app.context /
	// context_path / prompts/_project.md). May be empty.
	Project string
	// Task is the rendered Layer-3 task text: the agent persona
	// (system_prompt) plus any per-call extras the caller appends (routing's
	// Intent Library + Tool Contract, etc.). May be empty.
	Task string
}

// Layer records one composed layer for the trace breadcrumb: its name and byte
// count. It never carries the layer text — the full composed prompt is recorded
// separately (and subject to the same secret-handling discipline); this is just
// the manifest so a timeline can show which layers were present.
type Layer struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

// Composed is the result of Compose: the joined system prompt, the per-layer
// manifest, and whether the caller should pass
// --exclude-dynamic-system-prompt-sections.
type Composed struct {
	// SystemPrompt is the full layered prompt, ready to pass to claude via
	// --system-prompt (it REPLACES Claude Code's default).
	SystemPrompt string
	// Layers is the manifest of non-empty layers, in prompt order.
	Layers []Layer
	// ExcludeDynamic is true when Claude Code's dynamic sections (cwd / git /
	// env / memory) should be dropped — true for every verb except Task, whose
	// agentic repo work legitimately wants that context.
	ExcludeDynamic bool
}

// layerSep separates layers in the composed prompt. A horizontal rule makes the
// kitsoki | project | task boundaries legible to the model and to a human
// reading the trace, and is byte-stable so the prefix still caches.
const layerSep = "\n\n---\n\n"

// Compose joins the three layers most-stable → least-stable (kitsoki → project →
// task) into one cache-friendly system prompt, prepends the verb contract to the
// task layer, drops empty layers, and applies the per-verb dynamic-sections
// policy. It is pure: same Spec in, same Composed out — no I/O, no clock, no
// randomness.
func Compose(spec Spec) Composed {
	kitsoki := strings.TrimSpace(spec.Kitsoki)
	if kitsoki == "" {
		kitsoki = KitsokiLayer()
	}

	task := joinNonEmpty("\n\n", verbContract(spec.Verb), strings.TrimSpace(spec.Task))

	var (
		parts  []string
		layers []Layer
	)
	add := func(name, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		parts = append(parts, body)
		layers = append(layers, Layer{Name: name, Bytes: len(body)})
	}
	add("kitsoki", kitsoki)
	add("project", spec.Project)
	add("task", task)

	return Composed{
		SystemPrompt:   strings.Join(parts, layerSep),
		Layers:         layers,
		ExcludeDynamic: spec.Verb != Task,
	}
}

// LayerNames returns just the ordered layer names from a manifest — a
// convenience for the trace breadcrumb and for tests.
func LayerNames(layers []Layer) []string {
	out := make([]string, len(layers))
	for i, l := range layers {
		out[i] = l.Name
	}
	return out
}

// joinNonEmpty joins the non-empty, space-trimmed pieces with sep.
func joinNonEmpty(sep string, pieces ...string) string {
	var kept []string
	for _, p := range pieces {
		if t := strings.TrimSpace(p); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, sep)
}
