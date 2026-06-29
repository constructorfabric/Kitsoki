# Routing Testing and Tuning

Use this loop when a story should route free-text reliably before it spends an
agent turn. The goal is not to make every phrase deterministic; it is to catch
high-value operator phrases, preserve good off-ramps, and leave a recording that
future no-LLM runs can replay.

## Loop

1. Write 10-15 intent cases under `stories/<story>/intents/`.
   - Include the happy path.
   - Include near misses that must route elsewhere.
   - Keep expected slots explicit when the target intent captures text.
2. Run a cheap plan first:

   ```sh
   go run ./cmd/kitsoki test intents stories/dev-story/app.yaml \
     --intents stories/dev-story/intents/landing_proposal_routing.yaml \
     --dry-run
   ```

3. Run one live tuning pass against the target profile:

   ```sh
   go run ./cmd/kitsoki test intents stories/dev-story/app.yaml \
     --intents stories/dev-story/intents/landing_proposal_routing.yaml \
     --harness claude \
     --profile codex-native \
     --runs 1 \
     --max-cost 5 \
     --json .artifacts/dynamic-workflows/landing-proposal-routing-gpt55-tuned.json \
     --emit-recording .artifacts/dynamic-workflows/landing-proposal-routing-tuned.recording.yaml
   ```

4. Summarize failures:

   ```sh
   python3 tools/routing-tuning/intent_report_summary.py \
     .artifacts/dynamic-workflows/landing-proposal-routing-gpt55-tuned.json
   ```

5. Tune the story, not the test harness.
   - Add or sharpen intent examples.
   - Clarify intent descriptions and boundaries.
   - Raise/lower priority only when the boundary is genuinely wrong.
   - Update wrong expectations when the model found a better existing route.
6. Re-run with `--runs 1` until clean.
7. Run a confidence pass with `--runs 3` or higher only after the one-pass set is
   clean.

## Session Mining Link

Session mining should feed this loop. Mine real transcripts for:

- phrases users typed at a hub or ambiguous room;
- corrections like "no, I meant proposal/design";
- turns that landed in a broad `work` sink but then produced a proposal;
- turns that entered a specialized room accidentally.

Convert those mined phrases into intent fixtures before tuning. Keep the mined
source path or trace id in fixture comments when it is useful evidence.

Useful starting points:

- `stories/dev-story/intents/landing_proposal_routing.yaml`
- `docs/testing/examples/landing-proposal-routing-report.md`
- `docs/architecture/ambient-mining.md`
- `.agents/skills/story-coverage-mining/SKILL.md`
- `.agents/skills/session-idea-mining/SKILL.md`

## Evidence

Store live reports and emitted recordings under `.artifacts/<topic>/`. Do not
commit those generated reports unless a proposal explicitly needs them as a
review artifact. Commit the fixture, story tuning, docs, and any small reusable
tooling.
