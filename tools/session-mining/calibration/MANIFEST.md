# Scenario Foundry calibration set (task 2.4)

18 real, hand-checked `kind: conversation` scenario IR documents (schema
`schema/scenario_ir.schema.json`), compiled by `scenario_compiler.py` from a
real run of the session-mining pipeline over local corpora, redacted through
`redact.py --scenario` (task 2.3's mined-IR gate), and individually hand-checked
below.

**20 candidates were compiled, 18 survived hand-check — this file records both
exclusions honestly rather than padding to 20.**

## Provenance — how this set was produced

- **claude-code corpus**: `~/.claude/projects/<this-repo-slug>/` (this
  project's own real Claude Code sessions). 10 small (~150-420KB) real
  `entrypoint==cli` session files were staged into a local, gitignored
  selection dir; `prep.py` dropped 7 more that were dispatched
  agent/sub-agent runs (`entrypoint!=cli`), leaving **9 genuine human-driven
  sessions** (job `calib-claude`). Step B (the one LLM step, normally
  `intents.workflow.js`) was performed BY HAND over the 9 real distilled
  traces — a human-reviewed span/tag/action plan
  (`.artifacts/session-mining/build_agent_batch.py`, not committed — ad hoc
  and local) — rather than dispatching a live model call, since the acting
  agent doing this task IS the reader for this one-off calibration run. All 16
  spans grounded cleanly through `ground.py` (0 quarantined). Ran the full
  spine: `ground.py` -> `tag_score.py` -> `outcomes.py` -> `emit.py --outcomes`
  -> `verify_link.py` -> `validate_reports.py` (schema-valid, cross-link OK) ->
  `scenario_compiler.py --corpus claude-code`. **15 scenarios compiled.**

- **codex corpus**: `~/.codex/sessions/**` filtered to `session_meta.cwd`
  containing `Kitsoki`. A real, actionable finding surfaced here (see "codex
  corpus filtering gap" below): of the 13 `originator==codex-tui` (genuine
  human) candidate sessions in the working-size band, **7 were NOT real
  conversations** — they were codex's own approval-assessment sidecar prompts
  ("The following is the Codex agent history whose request action you are
  assessing...") logged as separate rollout files under the SAME originator
  tag. These were manually identified (`grep -c "whose request action you are
  assessing"`) and excluded; only the 6 remaining genuine sessions were staged
  (job `calib-codex`; 1 dropped by `codex_prep.py` as near-empty, leaving 5).
  Same hand-as-reader-agent step B, same deterministic spine
  (`codex_outcomes.py` instead of `outcomes.py`) -> `scenario_compiler.py
  --corpus codex`. **5 scenarios compiled.**

- **Redaction gate (task 2.3)**: all 20 raw scenario IR documents (kept LOCAL
  under `.artifacts/session-mining/scenarios-raw/`, gitignored — see
  `.gitignore`) were piped through the new `redact.py --scenario` mode (see
  `redact.py` module docstring + `clean_scenario()`), which genericizes
  `goal`, `turns[].text`, `turns[].corrective_ops[]`,
  `turns[].followup_text_head`, and `expected_effects[]`, then re-runs the
  HIGH-RISK `scan()` gate over the fully-redacted document and refuses to
  emit anything (exit 1, empty stdout) if a secret-shaped pattern survives.
  All 20 passed the gate cleanly (0 failures) — see
  `tools/session-mining/tests/test_redact_scenario.py` for the no-LLM unit
  tests over this gate (fail-closed path exercised via a secret planted
  OUTSIDE the scrub allowlist, proving the final `scan()` re-check is real
  defense-in-depth, not just theater over the same allowlist).

- **Hand-check (task 2.4, this file)**: every one of the 20 redacted documents
  was read in full by a human-reviewing pass (below) for (a) whether the
  turns/goal/expected_effects/corrected-flags accurately reflect what really
  happened in the source session, and (b) any surviving privacy leak (home
  paths, IPs, secrets, real names) the automated gate might have missed.
  A `grep` sweep for the raw home path, both real VM IPs, and secret-shaped
  strings across every committed file confirmed zero leaks survive.

## Excluded (2)

| id | reason |
|---|---|
| `scn-1b4ace86-f192-43b0-ab86-16142fec0079-0000` | The real user turn was a bare context-file reference (a lone `@<path>` mention) with no other prose. Once the required home+repo path redaction runs, `goal`/`turns[0].text` collapse to `"@<path>"` — technically correct (that IS all the user said) but useless as a calibration example: there is no recoverable signal about what the goal even was without the (redacted) file content. Excluded for insufficient standalone signal, not for a privacy failure. |
| `scn-567c65c2-2f1d-4fee-b0a4-e125ffb160ef-0000` | Same shape: real turn was a `@<path>` mention plus a two-word imperative → redacts to `"<path> <imperative>"`. Marginally more signal than the one above (the imperative survives) but still not independently interpretable. Excluded for the same reason. |

**Note for future work (not fixed here, out of scope for 2.3/2.4):** both
exclusions share a root cause worth designing for later — `@file` context
references are a common real Claude Code usage pattern, and the scenario
compiler/redactor currently has no way to say "this turn's real content lives
in an attached file, inline a genericized summary of that file's gist instead
of collapsing to a bare path token." Flagged for whoever picks up the next
scenario-foundry slice.

## Hand-check notes for the 18 that survived

Format: `id` — goal (redacted) — note.

1. `scn-1b4ace86-...-0001` — "resolve the red gate test that's already
   written but not committed" — Accurate: this really is a follow-up in the
   SAME session where the assistant had already committed+landed the fix one
   turn earlier; the reply correctly says "already done," and `corrected:
   false` is right (nothing needed fixing). Clean, no leaks.
2. `scn-240464bf-...-0000` — "just file the ticket, we'll handle it async..."
   — Accurate directive-turn text. Note: this turn is a follow-up elaboration
   on an in-progress proposal draft from EARLIER in the same session (the
   compiler correctly starts the scenario here because the earlier lines had
   no real verbatim `USER:` line to anchor on — see scenario_compiler's
   documented behavior of dropping spans with no recoverable user text). The
   scenario is self-consistent but assumes an unseen "draft a proposal for
   X" precursor; kept as a real, if partial-context, example.
3. `scn-2e107a60-...-0000` — "i want to dogfood kitsoki to build a critical
   new feature..." — Accurate and rich: real clarifying-pushback scenario
   (assistant investigated read-only via MCP tools, then declined to
   implement without more scoping/authorization — `expected_effects` is
   correctly READ-ONLY tool calls only, no code-change effects, matching what
   really happened). No leaks. Good calibration example of a "pump the
   brakes" agent response.
4. `scn-687e848b-...-0000` — "why do we keep losing the mcp tools..." —
   **Caveat, kept anyway**: `corrected: true` / `corrective_ops` here is a
   confirmed FALSE POSITIVE in `emit.py`'s structural satisfaction heuristic
   — the cited commit action's own commit-message PROSE contains the phrase
   "survives git clean" (describing the bug, not performing a `git clean`),
   which matched `CORRECTIVE_RE`'s `git\s+clean\b` pattern. Real prose, real
   commit, but not a real correction — this is exactly the "recall-biased
   review flag, not a verdict" the schema documents, and it's a genuinely
   useful worked example of that exact false-positive shape for whoever
   tunes `emit.py`'s corrective-op grounding next. Not excluded (the turns/
   goal/expected_effects are all otherwise accurate); flagged here instead.
5. `scn-74252840-...-0000` — "this repo is fresh on this machine - i want
   make install to run cleanly..." — Accurate, rich multi-step build-tooling
   scenario (write script, edit Makefile, run+verify, commit). No leaks.
6. `scn-74252840-...-0001` — "so make install will work now?" — Accurate:
   purely informational follow-up, correctly has empty `expected_effects`
   (no tool calls in that span — an honest empty signal, not a mining gap).
7. `scn-74252840-...-0002` — "if make install fails, give a message that
   maybe make setup is needed" — Accurate follow-on feature ask + commit.
8. `scn-c4d281a2-...-0000` — ".kitsoki-owner problem..." — Accurate
   investigation scenario (worktree/PR/git-log spelunking via MCP tools; no
   personal names, PR numbers 51/52 are innocuous). Minor cosmetic-only note:
   one `expected_effects` string reads slightly mangled after
   genericization (`"...{<val>:<val>,<val>:<val>.kitsoki-owner<val>..."`) —
   ugly but not a privacy issue, and not worth excluding over.
9. `scn-c4d281a2-...-0001` — "file the bug, then land the fixes" — Accurate
   and honest: the real session hit a raw API error immediately after this
   turn and produced ZERO actions; `expected_effects: []` correctly reflects
   that nothing actually happened. Good calibration example of an honest
   "nothing to show" outcome (distinct from a fabricated success).
10. `scn-c4ef173f-...-0000` — "check the .worktrees for things that are
    either garbage or already merged..." — Accurate, long investigation +
    cleanup scenario with 2 real `AskUserQuestion` judgment gates (matches
    the real transcript's two confirm-before-delete moments). Branch/
    worktree slugs retained (e.g. `bf-B-x`) are internal dev-branch names,
    not personal/sensitive.
11. `scn-f7b1da36-...-0000` — "i have a VM for the kitsoki-github-agent -
    gracefully shut it down" — Both real VM IPs (`206.189.84.218` for the
    gh-agent droplet, `167.172.149.45` for a different box mentioned in the
    trace) genericized to `<IP>` correctly; verified by grep the raw octets
    do not survive anywhere in the committed set.
12. `scn-f7b1da36-...-0001` — "ok it has been resized - i turned it on,
    check that it starts up properly" — Accurate follow-up, IP redacted.
13. `scn-f7b1da36-...-0002` — "delete them - it's a testing node so it's ok"
    — Accurate follow-up (deleting stale queued DB rows), IP redacted.
14. `scn-rollout-...20t12-11-15...-0000` (codex) — "this folder is supposed
    to be pinned to the main branch..." — Accurate branch-restore scenario;
    codex's real `call_id`-joined tool calls all grounded cleanly.
15. `scn-rollout-...20t13-01-55...-0000` (codex) — "somehow main got checked
    out to a fix branch..." — Accurate, same shape as #14 (a recurring real
    bug pattern — main getting de-pinned — independently reported across two
    different codex sessions weeks apart, a genuine repeat-pain signal).
16. `scn-rollout-...22t14-52-39...-0000` (codex) — "is the agent-contract-eval
    worktree already merged? can it be removed?" — Accurate read-only
    worktree-safety-check scenario.
17. `scn-rollout-...22t17-54-26...-0000` (codex) — "what does it take to get
    an extension uploaded to vs code marketplace?" — Accurate, purely
    informational (no tool calls in the real trace either — the assistant
    answered directly), `expected_effects: []` correctly empty.
18. `scn-rollout-...26t15-21-48...-0000` (codex) — "is there an rrweb viewer
    extension for vs code?" — Same shape as #17, accurate, empty effects.

## Real findings surfaced by this run (not fixed here — logged for follow-up)

1. **codex corpus filtering gap**: `codex_prep.py`'s `originator` filter
   (`codex_exec`/`exec` dropped) is necessary but not sufficient —
   `codex-tui`-originated sessions can ALSO be non-conversational
   approval-assessment sidecar logs. A future codex-adapter hardening pass
   should additionally drop sessions whose `user_message` text matches the
   `"The following is the Codex agent history whose request action you are
   assessing"` framing (or more robustly, sessions with `originator ==
   codex-tui` where every `user_message` is harness-injected review
   boilerplate rather than free human prose).
2. **`emit.py` corrective-op false positive**: see hand-check note #4 above
   (`CORRECTIVE_RE`'s `git\s+clean\b` matched commit-message PROSE, not an
   actual corrective action). Worth tightening (e.g. requiring the match to
   be in the actual `Bash` command's leading shell invocation, not anywhere
   in the full genericized signature+parameters+trace-line concatenation)
   next time someone tunes the satisfaction heuristic.
3. **`@file` reference turns lose all signal after redaction** — see the
   Excluded table above.

## Reproduction

The unredacted intermediates (raw `.jsonl` selections, distilled traces,
hand-built agent-batch plans, `intents.json`/`analysis.json`, and the 20 raw
scenario IR docs) live under `.artifacts/session-mining/` in the
`s4/scenario-foundry` worktree — gitignored, not committed, per the redaction
gate's local/committable split (task 2.3). To reproduce from scratch, see the
provenance section above plus `tools/session-mining/README.md`'s Quickstart;
the ad hoc step-B plan script is `.artifacts/session-mining/build_agent_batch.py`
(kept local, not part of the shipped pipeline).
