<!--
EXTRACTOR PROMPT — the reproducible core of session-mining.
prompt_version: "1.0"
Pairs with vocab_version "2026-05-31".

HOW TO USE: substitute the {{...}} placeholders and hand the result to a cheap
model (Haiku-class) as a subagent prompt. Run one agent per 1-2 redacted traces;
fan out in parallel. Reports from the same prompt_version + vocab_version are
directly comparable and mergeable.

DESIGN: this is the "seeded + propose-novel" extractor (one pass). It scores
against the controlled vocabulary AND proposes novel patterns, but novel patterns
are quarantined downstream by aggregate.py's promotion gate — they never pollute
cross-user counts until corroborated. A full open-coding (no-seed) pass is an
optional experiment, not the default; see README "Does it find novel patterns".
-->

You are mining a distilled, **already-redacted** Claude Code session transcript to
find recurring developer workflow patterns — repeatable procedures, each a mostly
mechanical skeleton with a few points of real judgment. Downstream these become
candidates for turning into deterministic scripts with named decision gates.

## Input

Each line of the trace is one of:
- `USER:` — a human prompt
- `AI:` — assistant narration
- `  > Tool: arg` — a tool call (the most reliable signal of what actually happened)

The trace is already redacted: paths are `~`/`<repo>`, secrets/identifiers are
placeholders. **Do not try to recover redacted content. Never reproduce raw user
or assistant prose in your output** (see output rules).

Traces to analyze:
{{TRACE_PATHS}}

## Controlled vocabulary (score against these ids)

These are the canonical pattern ids. An "occurrence" is one contiguous stretch of
turns performing that procedure. Count occurrences per trace; sum across the
traces you were given.

{{VOCAB_IDS_AND_DEFINITIONS}}

If your ecosystem has example signatures, here they are for reference:
{{OVERLAY_SIGNATURES}}

## Also: propose novel patterns

For any recurring procedure that does NOT fit a vocabulary id, emit a record with
`"source": "novel"` and a short, descriptive `id` in kebab-case. Be conservative —
only propose a novel pattern you saw **recur** or that is clearly a distinct
procedure, not a one-off step. Novel patterns are quarantined until independently
corroborated, so over-proposing only adds noise, it does not help.

## Output — STRICT

Emit ONLY a fenced ```json block: an array of records. One record per pattern that
occurred at least once. Schema per record:

```json
{
  "id": "fix-failing-tests",
  "source": "cataloged",            // "cataloged" (a vocabulary id) | "novel"
  "occurrences": 3,                 // integer >= 1, summed across the traces you read
  "corroboration": 1,              // how many distinct traces (of those you read) showed it
  "mechanical_fraction": 0.77,      // 0..1 — estimate how much of the work was rote vs judgment
  "pain": "high",                   // "low" | "med" | "high" — observed friction (retries, wrong turns, manual recovery)
  "decision_points": ["fix code vs fix test expectation"],   // AT MOST 3 — the genuine judgment forks (these become gates)
  "example_signatures": ["go test ./<pkg>/... -run <Test> → Edit <file> → rerun"]
}
```

### Hard rules (a record that breaks these is invalid)
1. **No verbatim quotes.** `example_signatures` are GENERICIZED tool-call sequences
   only — tool names + placeholder args (`<file>`, `<Test>`, `<pkg>`). Never copy a
   `USER:`/`AI:` line, never include real names, paths, or values.
2. **Evidence is the tool sequence**, not prose. If you cannot express a pattern as
   a tool-call signature, you probably haven't found a real pattern.
3. **Count only what is literally in the traces.** Do not infer occurrences you did
   not see. `occurrences` and `corroboration` must be defensible from the lines.
4. **At most 3 `decision_points`, and they are GATES not steps.** A decision point
   is a genuine fork where a human/model must choose between options (e.g. "fix the
   code vs fix the test"). It is NOT a mechanical step ("run the build"). Merge
   near-duplicates; if a pattern has more than 3 real forks, keep the 3 highest-stakes.
   Keep them **abstract** — name the *fork*, never a project/repo/feature name (those
   are content; the gate is the shape of the choice).
5. **Be terse.** No commentary outside the JSON block.
