---
name: routing-tuning
description: Build and tune Kitsoki free-text routing fixtures. Use when a user reports a phrase routing to the wrong room, asks to benchmark route quality against a live profile/model, or wants session-mined phrases turned into intent fixtures.
---

# Routing Tuning

Use this skill to make free-text routing reliable and evidence-backed.

## Procedure

1. Inspect the target story's room and intent definitions.
2. Write 10-15 cases under `stories/<story>/intents/`.
   - Include target-route positives.
   - Include near-miss negatives that should route elsewhere.
   - Include slots for text-capturing intents.
3. Run `kitsoki test intents` in a cheap mode first:

   ```sh
   go run ./cmd/kitsoki test intents stories/dev-story/app.yaml \
     --intents stories/dev-story/intents/landing_proposal_routing.yaml \
     --dry-run
   ```

4. If live model tuning is explicitly requested, run one pass against the target
   profile:

   ```sh
   go run ./cmd/kitsoki test intents stories/dev-story/app.yaml \
     --intents stories/dev-story/intents/landing_proposal_routing.yaml \
     --harness claude \
     --profile codex-native \
     --runs 1 \
     --max-cost 5 \
     --json .artifacts/<topic>/routing.json \
     --emit-recording .artifacts/<topic>/routing.recording.yaml
   ```

5. Summarize with:

   ```sh
   python3 tools/routing-tuning/intent_report_summary.py .artifacts/<topic>/routing.json
   ```

6. Tune story intent examples/descriptions/priorities. Do not tune prompts around
   a bad story boundary.
7. Re-run. Use `--runs 3` only for a final confidence pass after `--runs 1`
   passes.

## Session Mining

Use session mining to source future cases:

- phrases typed at hubs or broad conversational rooms;
- user corrections after a wrong route;
- broad `work` turns whose result shows they should have entered a specialized
  room;
- specialized-room entries that should have stayed in `work`.

Then convert mined phrases into intent fixtures and cite the source transcript or
trace when helpful.

See `docs/testing/routing-tuning.md` for the full runbook.
