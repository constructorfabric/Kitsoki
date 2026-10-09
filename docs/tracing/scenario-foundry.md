# Scenario Foundry: the mined `kind: conversation` IR

Every existing harness runs *clean, pre-authored* interactions —
exact-match flow recordings, scripted swarm intents, hand-authored
`tools/product-journey/scenarios.json`. The Scenario Foundry mines real
Claude Code and codex sessions instead, so the richest real signal —
**corrections, retries, and abandonment** — becomes runnable, no-LLM
test input. It sits one layer above the JSONL trace format: not a new
trace event type, but an IR *compiled from* `tools/session-mining/`'s
`emit.py --outcomes` output (see [`../../tools/session-mining/README.md`](../../tools/session-mining/README.md)).

## The IR (`kind: conversation`)

Schema: `tools/session-mining/schema/scenario_ir.schema.json`. Worked
example: `tools/session-mining/examples/scenario-ir.example.json`.

```jsonc
{
  "schema_version": "1.0",
  "kind": "conversation",
  "id": "scn-git-ops-0007",
  "source": "mined",
  "provenance": { "corpus": "claude-code", "session_id": "sess-git", "span_idx": 0 },
  "persona": "core-maintainer",
  "goal": "rebase a feature branch onto main without losing local commits",
  "turns": [
    { "role": "user", "text": "rebase this onto main", "corrected": true,
      "corrective_ops": ["git rebase --abort"], "followup_text_head": "no wait, stash first" },
    { "role": "user", "text": "ok stash my changes then rebase" }
  ],
  "expected_effects": ["git.rebase completed"],
  "abandoned": false
}
```

| Field | When present | Carries |
|---|---|---|
| `source` | always | `mined` (compiled from a real corpus) vs `hand-authored` (gap-filler) |
| `provenance` | always on `source: mined` | corpus + session id + span index — traces back to the source session for audit/redaction review |
| `persona` | always | a coarse actor label; mined scenarios derive it heuristically from the first turn's action tag (`scenario_compiler.py`'s `_persona_for`), hand-checked at calibration — not a final taxonomy |
| `turns[].corrected` / `corrective_ops` / `followup_text_head` | when `emit.py --outcomes` detected a correction | the signal a hand-authored scenario structurally can't express — a wrong turn, its fix, and the user's own follow-up words. A recall-biased *review flag*, not a verdict (mirrors `emit.py`'s own satisfaction docstring) |
| `expected_effects` | always (may be empty) | deterministic, checkable outcomes derived only from **grounded** mined actions — never invented; empty means nothing was groundable |
| `abandoned` | always | true when the mined span ends mid-correction with no further follow-up — such a scenario should assert the workbench does **not** silently claim success |

## Determinism

Compilation is a pure function of the mined `intents.json` +
`analysis.json` bytes plus the `corpus` label — no timestamps, no
randomness, no reliance on input ordering (spans are re-sorted by their
`instance_id` suffix). Same input corpus + same redaction pass → same
IR bytes. The IR itself carries no live-call data; every downstream
compiler consumes it exactly like a hand-authored fixture.

## Producers

- **Codex adapter** (`codex_adapter.py` / `codex_prep.py` /
  `codex_outcomes.py`) — a second mining source alongside Claude Code,
  normalizing `~/.codex/sessions/**/*.jsonl` into the *same* distilled-
  trace and outcomes-join shapes `prep.py`/`outcomes.py` produce, so
  `ground.py`/`tag_score.py`/`emit.py`/`redact.py` run unchanged. See
  ["Codex adapter" in the session-mining README](../../tools/session-mining/README.md#codex-adapter-a-second-source-same-intermediate-shapes)
  for the corpus-shape gotchas (originator filtering, call_id joins,
  the lexical-satisfaction gap).
- **Scenario compiler** (`scenario_compiler.py`) — reads one job's
  `emit.py --outcomes` output, walks each session's intent instances in
  span order, folds corrected turns into the same scenario as follow-up
  turns, and closes each scenario `abandoned` or not per the table
  above. Emits one IR document per calibration-worthy span.
- **Redaction gate** (`redact.py --scenario`) — genericizes every
  free-text IR field (`goal`, `turns[].text`, `corrective_ops`,
  `followup_text_head`, `expected_effects`), then re-runs the HIGH-RISK
  `scan()` gate and fails closed (exit 1, empty stdout) if anything
  secret-shaped survives. Unredacted IR never leaves a local,
  gitignored job dir; only the redacted, gated documents are
  committable. `tools/session-mining/calibration/` + its `MANIFEST.md`
  is the first hand-checked set produced this way.

## Compilers (consumers)

Three compilers project the IR onto harnesses that already exist but
previously had no realistic multi-turn input:

| Compiler | Projects IR onto | Notes |
|---|---|---|
| `flow_fixture_compiler.py` | a `kitsoki test flows` flow fixture + host cassette, against the purpose-built `stories/scenario-foundry-harness/` (one explicit `ask` intent, `slots.text` carrying the verbatim utterance) | **Not** `trace to-flow` reuse — that converter needs an already-resolved `intent`/`slots` per turn, which a mined scenario doesn't have. One flow turn per mined turn; one `host.agent.converse` cassette episode per turn, its canned answer derived deterministically from the scenario's own fields (never invented). |
| `tools/swarm/tiers/tier2.ts` | a swarm tier-2 scripted-user recording | Reads `SWARM_FIXTURE` (a scenario-IR file or directory) and `SWARM_PERSONA_MIX` (comma-separated persona selection/ordering) — env vars the arena swarm plugin already threaded through (`tools/arena/arena/plugins/swarm.py`) but tier 2 didn't consume before this. Falls back to the prior hardcoded fixture when `SWARM_FIXTURE` is unset or yields nothing usable. |
| `product_journey_compiler.py` | `tools/product-journey/{personas,scenarios}.json` entries, tagged `"source": "mined"` | Clusters scenarios by `persona` into new `personas.json` entries (skipping ids that collide with an existing hand-authored persona); each scenario becomes its own `scenarios.json` entry with `stage: "mined_from_sessions"` — a sentinel deliberately outside any project's configured stage ids, so `run.py`'s live dispatch never auto-selects a mined entry; browsing/audit only until a human promotes one. |

All three are pure functions of the IR document's bytes: same input,
byte-identical output, so a reviewer can regenerate and diff the
compiled artifacts against a source IR change.

## Paraphrase tier (2.5)

Free-text realism without a live-model dependency: an optional
`paraphrases:` list on a flow-fixture recording `input:` entry, indexed
under the same exact/case-insensitive match discipline as `input:`
itself — see [`testing.md#paraphrase-tier-25`](testing.md#paraphrase-tier-25-a-pre-recorded-free-text-pool).

## See also

- [`docs/proposals/scenario-foundry.md`](../proposals/scenario-foundry.md)
  — the originating proposal (task list, open questions, non-goals).
- [`../../tools/session-mining/README.md`](../../tools/session-mining/README.md)
  — the full mining pipeline this IR sits on top of.
- [`testing.md`](testing.md) — flow fixtures, host cassettes, the
  paraphrase tier.
