package effect

import "strings"

// Effect classifies what a host-call operation or an agent touches — the
// four-tier ladder every consumer (converse permission, task replay mode,
// future cache/replay) reads instead of re-interpreting a single overloaded
// boolean. See the package doc for the model.
type Effect string

const (
	// Pure touches nothing — a pure transform (a tool-less LLM call, a
	// formatter).
	Pure Effect = "pure"
	// Read reads environment state with no change (git log, Read, Grep, a
	// GraphQL query).
	Read Effect = "read"
	// Write mutates local/in-domain state, replayable from a diff
	// (Write/Edit, git commit, host.chat.create).
	Write Effect = "write"
	// External is an irreversible external action (host.transport.post, a
	// PR, an email, WebFetch).
	External Effect = "external"
)

// rank orders the ladder so Join/LessEqual can compare tiers. Unlisted
// values are intentionally absent — see Valid.
var rank = map[Effect]int{
	Pure:     0,
	Read:     1,
	Write:    2,
	External: 3,
}

// Valid reports whether e is one of the four recognised tiers.
func (e Effect) Valid() bool {
	_, ok := rank[e]
	return ok
}

// LessEqual reports whether e is no more privileged than other on the
// ladder. An unrecognised value on either side returns false — a caller
// comparing against an invalid Effect never mistakes "unknown" for "safely
// read-only" (fail closed).
func (e Effect) LessEqual(other Effect) bool {
	er, ok := rank[e]
	if !ok {
		return false
	}
	or, ok := rank[other]
	if !ok {
		return false
	}
	return er <= or
}

// Join returns the more-privileged (higher-blast-radius) of a and b — the
// least-upper-bound the taxonomy's tool-surface join uses. An unrecognised
// value on either side fails closed to External.
func Join(a, b Effect) Effect {
	ar, aok := rank[a]
	br, bok := rank[b]
	if !aok || !bok {
		return External
	}
	if ar >= br {
		return a
	}
	return b
}

// mcpToolClass classifies kitsoki-internal MCP infrastructure tools, which
// stay in-process (they forward the agent's own output or a question, never
// a durable/external effect). An mcp__ tool absent from this table is an
// external/custom MCP server the taxonomy doesn't know about, and fails
// closed to External — mirroring the proposal's "agent tools [...,
// WebFetch/ext-MCP] -> join -> external" example.
var mcpToolClass = map[string]Effect{
	"mcp__validator__submit":  Pure,
	"mcp__operator__ask":      Pure,
	"mcp__kitsoki-bash__Bash": Write,

	"mcp__slidey__workspace_tree": Read,
	"mcp__slidey__read_spec":      Read,
	"mcp__slidey__layout_gallery": Read,
	"mcp__slidey__schema":         Read,
	"mcp__slidey__validate":       Read,
	"mcp__slidey__docs":           Read,
	"mcp__slidey__write_spec":     Write,
	"mcp__slidey__patch_spec":     Write,
	"mcp__slidey__remove_slide":   Write,
	"mcp__slidey__meme_search":    External,
	"mcp__slidey__add_meme":       External,
}

// builtinToolClass classifies a built-in Claude Code tool by its lower-cased,
// "host."-prefix-stripped name. Keys are bare (e.g. "read", "webfetch") so
// ToolClass accepts either the YAML-author-facing bare name ("Read") or the
// "host."-normalised form internal/app's loader stores ("host.Read").
//
// Bash classifies as Write, not External: it is arbitrary code execution
// that can mutate local state (the leak task-fs-sandbox.md calls out), but
// by itself it is not the irreversible-external-action tier — that stays
// reserved for WebFetch/WebSearch and other true network egress.
var builtinToolClass = map[string]Effect{
	"read":         Read,
	"grep":         Read,
	"glob":         Read,
	"notebookread": Read,
	"todoread":     Read,
	"todowrite":    Read, // session-local scratch list, not a durable side effect
	"write":        Write,
	"edit":         Write,
	"multiedit":    Write,
	"notebookedit": Write,
	"bash":         Write,
	"webfetch":     External,
	"websearch":    External,
}

// ToolClass returns the default effect class for a single tool name, in
// either the bare Claude-CLI form ("Read", "WebFetch") or the
// "host."-normalised form internal/app's loader stores ("host.Read"). An mcp__
// tool is looked up in the kitsoki-internal MCP table; anything else absent
// from the builtin table — an unrecognised tool, or an external MCP server
// this package doesn't know about — fails closed to External.
func ToolClass(tool string) Effect {
	if strings.HasPrefix(tool, "mcp__") {
		if e, ok := mcpToolClass[tool]; ok {
			return e
		}
		return External
	}
	name := strings.ToLower(strings.TrimPrefix(tool, "host."))
	if e, ok := builtinToolClass[name]; ok {
		return e
	}
	return External
}

// FromTools computes an agent's effect class as the join over its tool
// surface — the most-privileged tool wins. A tool-less surface is Pure
// (an API-only agent with no Read/MCP access).
func FromTools(tools []string) Effect {
	class := Pure
	for _, t := range tools {
		class = Join(class, ToolClass(t))
	}
	return class
}

// FromLegacyBool maps the deprecated `external_side_effect: bool` to an
// Effect for the one-release migration window: true -> External; false ->
// Write when tools includes a mutator (Write/Edit/MultiEdit/NotebookEdit/
// Bash), else Read.
//
// This intentionally does NOT consider network tools (WebFetch/WebSearch)
// when mapping false — a false declaration alongside a network tool is
// exactly the contradiction internal/app's load-time hard-fail exists to
// catch (the resulting Read/Pure mapping will disagree with the real
// tool-surface join, which IS External), not something this mapping should
// silently paper over.
func FromLegacyBool(external bool, tools []string) Effect {
	if external {
		return External
	}
	for _, t := range tools {
		if ToolClass(t) == Write {
			return Write
		}
	}
	return Read
}
