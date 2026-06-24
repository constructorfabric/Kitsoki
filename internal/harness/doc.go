// Package harness provides pluggable LLM routing backends — Live (Anthropic
// Messages API), ClaudeCLI (the `claude -p` subprocess), Replay (recorded
// fixtures), and Recording (a capture wrapper) — that all turn one user
// utterance into a tool call naming an intent and its slots. It sits at the
// bottom of the routing stack documented in docs/architecture/semantic-routing.md:
// the orchestrator reaches a [Harness] only after the deterministic and
// semantic tiers miss, and the harness is the tier that actually consults a
// language model.
//
// The package also carries [ConversationalHarness], a separate read-only
// Q&A backend for the Agent Room (mode: conversational) that answers in
// Markdown rather than routing to an intent.
//
// # Algorithm
//
// Every routing harness implements the same two-method [Harness] interface:
// RunTurn maps a [TurnInput] to a transition tool call, and Close releases
// resources. What differs is how the tool call is obtained:
//
//   - [LiveHarness] declares a single local "transition" tool, forces
//     tool_choice onto it, and reads the tool_use block straight out of the
//     Anthropic Messages response. No MCP dependency.
//   - [ClaudeCLIHarness] shells out to `claude -p` and avoids needing an API
//     key by reusing the user's Claude Code login. Slot extraction rides on
//     MCP: the harness spawns the kitsoki binary itself as a validator
//     subprocess, tells the model to call mcp__kitsoki-validator__submit,
//     and reads the schema-validated payload from a side-channel file rather
//     than scraping stdout.
//   - [ReplayHarness] looks the (state, input) pair up in a recording YAML
//     and returns the recorded call with no LLM at all — the backbone of the
//     deterministic cassette tests in docs/tracing/cassettes.md.
//   - [RecordingHarness] wraps any inner harness and appends each successful
//     call as a JSONL record, later compiled into a Replay recording.
//
// Both LLM-backed harnesses share two pieces so their prompts and schemas
// stay byte-identical for a given app: [BuildTransitionSchema] is the single
// source of truth for the transition tool's JSON Schema, and the unexported
// prompt builders (buildStablePrefix / buildDynamicSuffix) compose the
// system prompt. The stable prefix never changes for a given app, which is
// what lets [LiveHarness] mark it cache_control: ephemeral.
//
// # Invariants
//
//   - RunTurn blocks. It returns only once the model has produced a tool
//     call (or the harness has given up). It never streams partial output.
//   - The returned [mcp.CallToolParams] is NOT validated here. Slot types,
//     enums, and required fields are the downstream host-call validator's
//     responsibility; the harness only guarantees the call names the
//     "transition" tool and carries an "intent" string.
//   - SetAppDef is not safe to call concurrently with RunTurn. The
//     orchestrator's reload path guarantees no in-flight turn while swapping
//     the app definition, so the harnesses take no lock around appDef.
//   - [RecordingHarness] serialises its JSONL writes under a mutex, so its
//     output file is safe for concurrent tail/streaming readers.
//   - A missing or empty validator capture file (ClaudeCLI) means the model
//     answered without calling submit; that surfaces as a [*ClarifyResponse],
//     not a hard error, so the orchestrator can show the model's prose as a
//     soft clarification rather than a red technical failure.
//
// # Worked example
//
// A general-store state allows the propose_purchase intent, which declares
// an `items` (string) and a `total_cost` (int) slot. The user types a
// purchase line. A routing harness turns that into a transition call:
//
//	in (TurnInput):
//	  StatePath:       "general_store"
//	  AllowedIntents:  ["propose_purchase", "leave"]
//	  UserText:        "buy 6 oxen for 240"
//
//	BuildTransitionSchema unions every allowed intent's slots into one
//	flat object and bakes the allowed-intent enum into the tool schema:
//	  { intent: enum[propose_purchase, leave],
//	    slots:  { items: {type:string}, total_cost: {type:integer} },
//	    confidence: {type:number, 0..1} }
//
//	out (mcp.CallToolParams):
//	  Name:      "transition"
//	  Arguments: { intent: "propose_purchase",
//	               slots:  { items: "6 oxen", total_cost: 240 } }
//
// A runnable, LLM-free form of this round-trip — schema build plus a Replay
// lookup — lives in the package's Example functions ([ExampleBuildTransitionSchema],
// [ExampleReplayHarness]).
//
// # Lifecycle
//
// Construct a harness once per app load ([NewLive], [NewClaudeCLI],
// [NewReplay], [NewRecording], [NewConversationalHarness]). Call RunTurn per
// turn for the lifetime of the session; the harness rebuilds the per-turn
// tool schema each call so a state's changing allowed-intents set always
// flows through. On reload, the orchestrator calls SetAppDef (Live / ClaudeCLI)
// to swap the app definition and recompute the cached stable prefix. Call
// Close at session teardown — a no-op for the stateless harnesses, a file
// flush for [RecordingHarness].
//
// # Non-goals
//
//   - No streaming. RunTurn blocks until a complete tool call is in hand;
//     incremental token delivery is out of scope for an intent router whose
//     entire output is one small structured call.
//   - No slot validation. [BuildTransitionSchema] shapes the tool so the
//     model's output is well-formed, but type/enum/format enforcement that
//     gates a transition is the host-call validator's job (and the ClaudeCLI
//     MCP validator's), which is the single source of truth — duplicating it
//     here would let the two drift.
//   - No tool-definition caching. The schema is rebuilt every turn on
//     purpose, because allowed_intents changes per state; a cache keyed on
//     "the app" would serve a stale enum the moment the user changes rooms.
//   - No conversation-history management. Harnesses render [TurnInput.RecentTurns]
//     into the prompt but do not persist or trim it; the orchestrator owns
//     the session event log that feeds RecentTurns.
//
// # Reference
//
//   - docs/architecture/semantic-routing.md — the four-tier routing stack and
//     where harness.RunTurn (the LLM tier) sits in it.
//   - docs/architecture/transports.md — sessions and the turn loop that drives
//     RunTurn.
//   - docs/tracing/cassettes.md — recorded host/turn fixtures, the consumer of
//     the Replay/Recording pair.
//   - docs/architecture/agent-plugin.md — the Agent Room that
//     [ConversationalHarness] backs.
package harness
