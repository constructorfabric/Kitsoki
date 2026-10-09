---
name: llm-usage-optimization
description: Audit and reduce LLM cost in kitsoki stories by measuring real usage from traces (tokens, cache read ratio, cost, tool-call/continuation counts) and then restructuring prompts and story shapes — front-load context instead of letting agents tool-call for it, replace conversational multi-turn asks with a schema-forced single-shot list that the host handles deterministically, and construct prompts for maximum cache reuse (stable-prefix ordering, batching within the cache TTL, continuing a warm cached conversation instead of rebuilding a differently-shaped request). Use when the user says "this story is expensive", "reduce token usage", "too many tool calls / continuations", "optimize prompts for cache", or wants a cost audit of a story, session, or trace. Distinct from routing-tuning (route QUALITY, not cost) and model-task-engineering (making a weak model SUCCEED at a task, not making a working task cheaper).
---

# LLM Usage Optimization

Make a kitsoki story do the same work for fewer tokens and fewer LLM round-trips. Always measure before and after — every claim of "cheaper" must be backed by trace `meta.usage` numbers, never vibes.

## Cost model (the rules of thumb everything below serves)

1. **The continuation penalty dominates.** Every tool call / agent turn resends the full conversation as input. Cache softens this but does not remove it (cache reads still bill at ~10% and output/thinking recur per turn). An agent that makes 6 tool calls to gather context costs far more than one whose prompt already contains that context, even if the front-loaded prompt is 5× larger — input tokens in the *initial* context are paid once and become a cacheable prefix; tool-call turns pay the whole transcript again each time.
2. **Preference order** for getting a piece of work done:
   1. **No LLM call at all** — deterministic route tier, turncache, starlark glue, host call. A turn resolved deterministically emits *zero* `agent.call.*` events.
   2. **One single-shot structured call** — `decide` / `extract` / `ask --schema`, zero tool calls, schema-forced output.
   3. **A continuation on a warm cache** — reuse an existing conversation *as-is* (same prefix bytes) when it's good enough.
   4. **A fresh, differently-constructed request** — full cache bust; last resort.
3. **Cache is byte-exact and prefix-only.** One changed byte early in the prompt invalidates everything after it. Anthropic caches only prefixes ≥ ~1024 tokens, with a ~5-minute TTL refreshed on each hit. So: stable content first, volatile content last, and batch calls that share a prefix close together in time.
4. **Multi-turn conversation is an interaction pattern, not a requirement.** Anything an agent would "discuss" can usually be reshaped as: LLM emits one discrete list (schema-forced) → host handles each item deterministically → host goes back with ONE prepared single-turn response. Two cheap calls beat N conversational turns.

## Step 1 — Measure (deterministic, no LLM)

Locate a representative trace: a live session's trace JSONL (via the studio MCP `trace_read` tool with `session_id`/`app`, or `kitsoki export-status`), or run the story live once if no trace exists. Then extract the usage profile straight from the events — `agent.call.complete` carries `meta: {usage: {input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens}, cost_usd}` (see `docs/tracing/trace-format.md`).

```bash
# Per-call cost table: verb, agent, model, tokens, cache ratio, cost
jq -r 'select(.kind=="agent.call.complete") | .payload
  | [.verb, .agent, .model, (.meta.usage.input_tokens//0), (.meta.usage.output_tokens//0),
     (.meta.usage.cache_read_input_tokens//0), (.meta.usage.cache_creation_input_tokens//0),
     (.meta.cost_usd//0)] | @tsv' trace.jsonl | column -t

# Session totals + cache hit ratio (cache_read / (cache_read + input + cache_creation))
jq -s '[.[] | select(.kind=="agent.call.complete").payload.meta]
  | {calls: length,
     cost: (map(.cost_usd//0) | add),
     input: (map(.usage.input_tokens//0) | add),
     output: (map(.usage.output_tokens//0) | add),
     cache_read: (map(.usage.cache_read_input_tokens//0) | add),
     cache_write: (map(.usage.cache_creation_input_tokens//0) | add)}
  | . + {cache_ratio: (.cache_read / ((.cache_read + .input + .cache_write) | if .==0 then 1 else . end))}' trace.jsonl

# How many turns avoided the LLM entirely (routed_by tier histogram)
jq -r 'select(.payload.routed_by != null) | .payload.routed_by' trace.jsonl | sort | uniq -c
```

Also pull, per expensive call, the **agent-action transcript sidecar** (`transcript_ref` on `agent.call.complete`) and count tool-call turns — that's the continuation count you're trying to drive to zero. The reserved world vars `turn_cost_usd` / `session_cost_usd` give a quick live read; `tools/bugfix-bakeoff/aggregate.py` + `results/SCHEMA.md` are the model for fleet-level aggregation.

**Write the audit to `.context/llm-usage-audit-<story>.md`** before changing anything: per-call table, the 2–3 dominant costs, and for each dominant cost *why* it's expensive (tool-call loop? cold cache every call? conversational back-and-forth? oversized prompt template?). Optimize the top of that list only.

## Step 2 — Optimization playbook

Apply in preference order. Each pattern names the kitsoki mechanism that implements it.

### 1. Eliminate the call

- Can a deterministic route tier, an intent fixture, or the turncache absorb this turn? (`routed_by` histogram from Step 1 shows what's already free; `docs/architecture/prompt-intercept.md` covers the intercept tiers; the routing-tuning skill covers fixtures.)
- Can `host.starlark.run` or a plain host call compute it? String munging, JSON shaping, gating, file collation, and diff summarization do not need an LLM. See the starlark skill.

### 2. Front-load context — zero tool calls is the target

An agent turn that starts with "go read X, then look at Y" is a paid crawl. Instead, gather X and Y with deterministic effects *before* the agent call and inject them into the prompt via the pongo2 template (`prompts/*.md` — see `docs/stories/prompts.md`):

- Room effects run host calls / starlark to collect files, diffs, prior-phase outputs into world vars; the prompt template renders them inline.
- Prefer verbs with no mutation tools: `decide` and `extract` are single-shot by construction; `ask --schema` forces output through the validator `submit` tool. Reserve `task` for work that genuinely must touch the tree — and even then, front-load the context so its tool calls are *actions*, not *reads*.
- Non-`task` verbs already run with `ExcludeDynamic` (no cwd/git/env noise) — don't re-add environment dumps to their prompts.
- Verify: the transcript sidecar for the call should show 0 tool-use turns (or only genuine mutations for `task`).

More tokens in the initial prompt is the *correct* trade whenever it removes even one read-back turn.

### 3. Replace conversational asks with the list → handle → single-turn pattern

`host.agent.converse` (persistent chat transcript, N turns) is the most expensive shape in the system and is almost never required. Reshape:

1. **One structured call**: `decide`/`extract`/`ask` with a `schema:` that returns the *complete* discrete list up front — all questions, all findings, all requested items — not one at a time.
2. **Host handles the list deterministically**: iterate in effects/starlark, gather answers/files/results, run commands.
3. **One prepared single-turn response**: a second structured call whose prompt contains the original list plus everything the host gathered, asking for the final synthesis.

For operator questions specifically, the same rule holds on the story side: collect *all* questions into one schema'd emission and surface them together (the operator-ask bridge handles delivery), rather than a question-per-turn drip. Keep `converse` only where a human is genuinely in an open-ended loop.

### 4. Construct prompts for cache

The runtime already does its part — the sysprompt composes most-stable-first (`kitsoki` → `project` → `task` layers, byte-stable separators; `internal/sysprompt/sysprompt.go`) and the router puts one cache breakpoint on a stable prefix (`internal/harness/prompt.go`, `internal/harness/live.go`). Story-side rules that preserve it:

- **Order every prompt template stable → volatile.** Persona, contract, reference material, schemas, few-shot examples first; per-item data (the diff, the ticket, the world snapshot) last. Never interpolate timestamps, session IDs, item names, or counters into the early/stable portion.
- **Keep the stable portion identical across calls that share an agent.** Ten review calls with the same first 3k tokens and different tails pay the prefix once. Ten calls whose templates differ trivially (reordered sections, per-item headers up top) pay it ten times.
- **Batch within the TTL.** The cache lives ~5 minutes, refreshed per hit. Structure loops so same-agent calls run back-to-back, not interleaved with slow non-LLM phases that let the prefix expire.
- **Batch items into one call** when they're independent and small: one `extract` with an array schema over N items beats N calls — the prompt overhead and prefix are paid once. Split only when items are large enough to threaten output quality or context limits.
- **Continuation vs rebuild:** if a follow-up can be expressed as *appending* to an existing conversation whose prefix is warm and whose content is still accurate, continue it — a cached continuation is cheap. If the follow-up needs a different prompt structure, different context, or the old transcript would mislead, rebuild fresh and eat the cache miss once rather than dragging a stale transcript through every future turn. "Good enough and cached" beats "perfect but cold" — but stale or misleading context is never worth the discount.

Confirm with the Step 1 numbers: after the change, `cache_read_input_tokens` should dominate `input_tokens + cache_creation_input_tokens` on every call after the first per agent.

### 5. Trim the prompts themselves

Last, because structure beats wordsmithing — but once shape is right: cut duplicated instructions already carried by the verb contract or kitsoki layer, collapse few-shot examples that no longer earn their tokens, and check `kitsoki prompts spec <app.yaml>` for overlay `spec_` blocks injecting content twice. Output tokens cost ~5× input: tighten schemas (enums over prose, no "explain your reasoning" fields unless a judge reads them) to cut the expensive side.

## Step 3 — Verify (no live LLM unless asked)

1. **Behavior unchanged:** `go run ./cmd/kitsoki test flows stories/<name>/app.yaml` — restructured prompts/schemas usually change cassette episodes; re-record affected cassettes deliberately and make flows reflect the new shape.
2. **Cost improved:** re-run the same scenario that produced the baseline trace (live run only with the user's go-ahead, bounded by `--max-cost` where the intents runner is involved) and produce a before/after table from the Step 1 jq recipes: total cost, calls, tool-call turns per call, cache ratio.
3. **No quality regression:** if the story has judge gates or intent fixtures, they must still pass; a cheaper story that fails its judge is a regression, not a win.
4. Append the before/after table to the `.context` audit file.

## Key references

- `docs/tracing/trace-format.md` — `agent.call.*` events, `meta.usage`, `routed_by`, transcript sidecars
- `docs/stories/prompts.md`, `docs/architecture/system-prompt.md` — template/overlay system, layered sysprompt
- `docs/architecture/prompt-intercept.md` — the no-LLM tiers that make calls free
- `internal/sysprompt/sysprompt.go`, `internal/harness/prompt.go`, `internal/harness/live.go` — where cache-friendliness is implemented
- `internal/host/agent_event_sink.go` — where `meta.usage` / `cost_usd` come from
- `docs/case-studies/git-ops-cost.md`, `docs/case-studies/routing-model-cost-study.md` — prior cost investigations
- `tools/bugfix-bakeoff/results/SCHEMA.md` — fleet-level cost aggregation shape
