// Package starlark is the deterministic sandbox behind the host.starlark.run
// capability. It runs a small, author-supplied Starlark script in a tightly
// restricted interpreter and returns a map of named outputs.
//
// Why this exists: kitsoki's value proposition is separating interpretive
// decisions (the LLM agent) from deterministic execution. A great deal of
// "glue" in a story — shaping a payload, calling a plain HTTP API, deriving a
// few fields — is deterministic but too fiddly to express in the YAML effect
// vocabulary and too small to justify a bespoke Go host handler. host.run can
// shell out, but a shell command is opaque to the trace, unsandboxed, and
// non-portable. This package gives authors a real expression language that is
// nonetheless deterministic, introspectable, and replayable.
//
// # The contract
//
// An author writes two files that live beside the story:
//
//	derive.star        — the script; must define main(ctx) -> dict
//	derive.star.yaml   — the sidecar; declares typed inputs and outputs
//
// Run loads the script, validates the effect's inputs against the sidecar,
// evaluates main(ctx), then validates the returned dict against the sidecar's
// declared outputs. The returned outputs become the host.Result.Data the
// orchestrator binds into world per the effect's bind: spec.
//
// # The ctx surface (deliberately narrow)
//
// The single argument to main is a struct with exactly three attributes. No
// filesystem, no environment, no subprocess, no clock, no randomness — anything
// nondeterministic is intentionally absent so a recorded run replays byte-for-
// byte:
//
//	ctx.inputs.<name>            typed inputs resolved from the effect's with.inputs
//	ctx.world.get("key")         read-only snapshot of world; None when absent
//	ctx.http.get(url, headers={})            -> response
//	ctx.http.post(url, body=..., headers={}) -> response
//
// An http response exposes .status (int), .headers (dict), .text() (string),
// and .json() (parsed value). body on post may be a dict (JSON-encoded with an
// application/json content-type) or a string (sent verbatim).
//
// Outputs flow ONLY through main()'s return dict. There is deliberately no
// ctx.world.set — a Starlark effect cannot mutate world out-of-band; everything
// it produces is named, typed, and visible to bind:.
//
// # I/O boundary and record/replay
//
// All network access goes through the HTTPClient interface (see http.go). In
// production the orchestrator injects a recording client backed by net/http;
// in flow tests the testrunner injects a replay client backed by a cassette so
// no real network call is made and the run is deterministic. The client is
// supplied via WithHTTP on the context; HTTPFromContext resolves it (defaulting
// to a client that refuses all requests, so a script that does I/O without an
// injected client fails loudly rather than escaping the sandbox).
//
// Each exchange is recorded as a summary {method, url, status}. The summaries
// — never full request/response bodies — are surfaced for the trace under the
// reserved output key documented on Run. Full bodies live only in cassettes.
//
// # Non-goals
//
//   - No general-purpose plugin host: the ctx surface is fixed in this package,
//     not extensible per-story.
//   - No mutable world, no side effects other than HTTP.
//   - No nondeterministic stdlib (time, random) — only json and math are enabled.
//   - This package never imports internal/host; the host.Handler adapter lives
//     in package host (internal/host/starlark_run.go) to avoid an import cycle.
package starlark
