// Package oracle defines the plugin contract — the [Oracle] interface plus its
// [AskRequest]/[AskResponse] wire format — that every external LLM or decision
// system speaks to kitsoki. It is the narrow seam between kitsoki's
// deterministic state machine and any plugin: kitsoki owns intents,
// transitions, world bindings, schema validation, and the audit trace; the
// plugin owns only the reasoning that turns a rendered prompt into a
// schema-shaped JSON submission.
//
// Room dispatch (see [kitsoki/internal/host]) resolves a room's `oracle:`
// alias to an [Oracle] via a [Registry], calls [Oracle.Ask], validates the
// returned submission with [ValidateSubmission], and binds it to world. The
// orchestrator never sees a transport; it sees only this interface.
//
// # Transports
//
// One contract, four interchangeable transports, each built by a different
// constructor but indistinguishable to the caller:
//
//   - In-process Go — [New](AskFunc). Tests, stubs, and compiled-in
//     deterministic oracles; zero subprocess or network overhead.
//   - Subprocess JSON-RPC over stdio — [NewSubprocess]. A binary speaks
//     newline-framed JSON-RPC 2.0 (method "oracle.ask").
//   - MCP-over-HTTP — [NewMCPHTTP]. A long-running service exposes a single
//     MCP tool (default "ask") over HTTP.
//   - Harness adapter — [FromHarness]. Wraps an existing
//     [kitsoki/internal/harness.Harness] so the legacy claude-CLI path serves
//     as the default oracle (alias "oracle.claude") with no story changes.
//
// # Algorithm
//
// A turn that reaches an oracle host call runs:
//
//  1. Room dispatch derives a deterministic CallID and assembles an
//     [AskRequest] (rendered prompt, optional schema, with-args, world
//     snapshot, deadline).
//  2. The [Registry] resolves the room's oracle alias to an [Oracle],
//     falling back to [DefaultOracleName] when the alias is empty or absent.
//  3. [Oracle.Ask] runs the transport and returns an [AskResponse] or an
//     [AskError]. The transport translates its own failures (broken pipe,
//     HTTP 5xx, context cancel) into a typed [AskError.Kind].
//  4. Kitsoki calls [ValidateSubmission] with the request's schema. A schema
//     of nil skips validation; a failure becomes an [AskError] with Kind
//     "schema_invalid".
//  5. The validated submission is bound to world; OracleReturned (or
//     OracleError) is appended to the trace, paired to the CallID.
//
// # Invariants
//
//   - Every [AskRequest] and [AskResponse] field round-trips through JSON.
//     The subprocess and HTTP transports serialize them, so the in-process
//     and adapter transports must agree on the same shapes. The conformance
//     test pins this: all transports return the same Submission for the same
//     request.
//   - Kitsoki is the sole validation authority. Plugins MAY pre-validate as a
//     fast-fail UX but are never trusted to; [ValidateSubmission] is the gate.
//   - CallID pairs OracleCalled with OracleReturned/OracleError in the trace;
//     the caller derives it, not the transport.
//   - A nil schema is an explicit opt-out of validation, not an error.
//
// # Worked example
//
// A room declares an in-process stub oracle that always answers with a fixed
// submission, and dispatch asks it under a schema requiring `decision`:
//
//	req:  AskRequest{ Verb:"decide", PromptText:"ship it?",
//	                  SchemaJSON: {"type":"object","required":["decision"]} }
//	stub: returns AskResponse{ Submission: {"decision":"go"} }
//	Ask:  (AskResponse{Submission:{"decision":"go"}}, nil)
//	ValidateSubmission(schema, submission) -> nil   (decision present)
//	bind: world["decision"] = "go"
//
// Had the stub returned `{"reason":"..."}` (no decision), Ask would still
// succeed but [ValidateSubmission] would return
// *AskError{Kind:"schema_invalid"} naming the missing-property path. A
// runnable form of the happy path lives in [ExampleNew].
//
// # Lifecycle
//
// [BuildRegistryFromDef] constructs the [Registry] once at session
// construction from the app's `oracle_plugins:` declarations. Each [Oracle] is
// reused for the session lifetime; transports that hold OS resources (a
// subprocess, idle HTTP connections) spawn lazily on first [Oracle.Ask] and
// release them on [Registry.Close] -> [Oracle.Close] at session end. Ask after
// Close has undefined behaviour.
//
// # Non-goals
//
//   - No plugin-behaviour validation. Plugins own their reasoning; kitsoki
//     validates only the submission's shape against the schema, never whether
//     the decision was "correct."
//   - No schema-compile cache. Each [ValidateSubmission] compiles the schema
//     fresh — at one oracle call per turn, a cache does not earn its
//     invalidation complexity. Add one only if profiling shows it hot.
//   - No subprocess multiplexing. A subprocess has one stdin/stdout pipe;
//     concurrent Ask calls serialize through a mutex rather than interleaving
//     JSON-RPC ids on a shared stream.
//   - No retry or backoff. A failed Ask returns a typed [AskError] and the
//     turn surfaces it; deciding whether to retry is the state machine's job,
//     not the transport's.
//   - No $ref network fetch in [ValidateSchemaRefs]. Schema references must
//     resolve to files inside the story directory; out-of-tree and absolute
//     refs are rejected at story-load time.
//
// # Reference
//
// The operator-facing plugin specification — the `oracle_plugins:` YAML block,
// the ask/return contract, and sub-event ordering — is
// docs/architecture/oracle-plugin.md. The transport catalogue and session
// model are in docs/architecture/transports.md. The trace events each call
// emits are documented in docs/tracing/trace-format.md.
package oracle
