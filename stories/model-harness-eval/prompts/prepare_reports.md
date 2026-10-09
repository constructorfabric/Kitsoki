Prepare model/harness evaluation artifacts for this operator question:

`{{ args.question }}`

{% if args.refine_feedback %}Refine feedback from the operator: {{ args.refine_feedback }}{% endif %}

Override request:

- Apply mode: `{{ args.apply_mode }}`
- Override path: `{{ args.override_path }}`
- Target story: `{{ args.target_story }}`
- Target call: `{{ args.target_call }}`

Write or refresh these artifacts:

- Markdown report: `{{ args.markdown_path }}`
- Slide-style HTML deck or rendered review artifact: `{{ args.deck_path }}`
- Slidey JSON deck source: `{{ args.slidey_spec_path }}`
- Durable case study: `{{ args.case_study_path }}`
- Machine summary JSON: `{{ args.summary_path }}`
- Live evidence policy: `{{ args.live_policy }}`

Use the reusable offline pilot process in `docs/testing/model-harness-eval-pilot.md`,
then extend the result into a concrete selection proposal and override.

Configuration discovery:

1. Read the checked-in `.kitsoki.yaml`.
2. Read the machine-local override from `{{ args.override_path }}`. When this
   story is run from a worktree, the default is
   `/Users/brad/code/Kitsoki/.kitsoki.local.yaml`; use that main-checkout file
   as the source of truth for configured local profiles instead of a worktree
   copy.
3. Merge the two conceptually using Kitsoki's documented rules: local scalar
   values win, local harness profiles are added by name, and a local profile
   replaces a baseline profile of the same name whole.
4. List every effective configured option in `configured_options`, including
   profile, backend, default model, model catalog, effort, effort catalog, and
   whether the option came from baseline or local config.

Default no-cost evidence collection:

1. Create `{{ args.output_root }}/intent-reports/`.
2. Run static intent suites that already have local recordings. At minimum, run:
   `go run ./cmd/kitsoki test intents stories/oregon-trail/app.yaml --harness static --json {{ args.output_root }}/intent-reports/oregon-trail.json`
   A non-zero exit is acceptable if the command writes JSON; failed fixtures are evidence.
3. Run the committed transcript-derived coverage flagship:
   `bash tools/session-mining/examples/git-ops/run.sh --keep {{ args.output_root }}/git-ops-coverage`
4. Aggregate all local evidence:
   `host.session_mining.run --op eval_pilot_report --root stories --intent-root {{ args.output_root }}/intent-reports --coverage-root {{ args.output_root }} --markdown {{ args.markdown_path }} --deck {{ args.deck_path }} --summary {{ args.summary_path }}`

Live evidence policy:

- If `{{ args.live_policy }}` is `no_cost`, do not call live LLM providers or
  run a paid live benchmark matrix. If the operator's question asks for live
  provider performance, answer from accepted local evidence and list the
  missing live collection step as a limitation.
- If `{{ args.live_policy }}` is `operator_approved`, live LLM/provider
  evidence is allowed for this run. Keep it bounded: run the smallest
  benchmark or spot-check matrix that materially answers the question, record
  the exact commands, write raw live outputs under `{{ args.output_root }}`, and
  make clear which conclusions are measured vs inferred. Never put live calls in
  committed flow fixtures or automated tests.

Routing/case-study mode:

- If the question asks about routing, room-by-room model selection, cost
  savings, or case-study material, mine real session inputs first and make the
  corpus part of the evidence. Prefer `tools/session-mining/prep.py` over raw
  transcript reads, keep raw/distilled private outputs under `.artifacts/` or
  `/tmp`, and record the session count, user-turn count, routing-eval item
  count, correction/hard-negative count, and where the corpus artifact lives.
- Build the report around Kitsoki's routing tiers: deterministic, semantic,
  turn-cache/default-intent, embedding/contextual, and final LLM fallback. The
  recommendation must be room/call-site scoped, not a global downgrade.
- Candidate cheap routers should include configured Haiku, synthetic small text,
  and GPT mini/Codex mini style models when configured or explicitly requested.
  If a candidate is not currently configured, mark it as a configuration gap
  instead of inventing measurements.
- The case study at `{{ args.case_study_path }}` must be narrative docs-quality:
  problem, mined corpus, method, current architecture, candidate model tiers,
  what is safe to switch now, what needs live eval, expected cost mechanism,
  risks, and the next implementation path.
- The Slidey source at `{{ args.slidey_spec_path }}` must be a real Slidey JSON
  deck, not a markdown outline. It should be suitable for review/rendering with
  `slidey {{ args.slidey_spec_path }} --validate` and should summarize the case
  study with crisp slides, tables, and a final recommendation.

Selection proposal:

- Recommend `fastest`, `cheapest`, `best`, and `selected`.
- Prefer measured eval report data when it exists. Include median, p5, p95, pass
  rate, and cost fields from evidence when available.
- The Markdown report and HTML deck must include a numeric comparison table with
  at least: call, profile/model, effort, observations, examples run,
  acceptance-bar pass rate, effectiveness median/p5/p95, p95 latency
  median/p5/p95, and average cost median/p5/p95. Include
  configured-but-unmeasured candidates as explicit `missing` rows instead of
  omitting them.
- Explain the difference between a candidate failing the acceptance bar and a
  model having zero successful examples. A row can fail the bar while still
  showing non-zero comparator effectiveness.
- Include a confidence-threshold sweep when per-example actual confidence values
  are available: threshold, accepted count, true accepts, false accepts,
  precision, and coverage. Use this to recommend whether the confidence bar can
  be lowered. If current reports only contain aggregate candidate rows, state
  that the sweep is blocked until reports record per-example actual confidence.
- Compare story-local eval matrices against configured profile model catalogs.
  If a configured model such as `haiku` is absent because the eval matrix only
  listed profiles or provider-specific models, call that out as a coverage gap.
- When evidence is missing for a configured option, set `evidence_status` to
  `missing` or `inferred` and say exactly what is missing. Do not invent
  measurements.
- If only static/no-cost evidence exists, make a conservative selection from the
  configured options and explain the tradeoff. For this machine, synthetic
  profiles are local test/provider options, `codex-native` is the native Codex
  profile, and `claude-native` is the checked-in baseline. Choose the option
  that best satisfies the operator's question, not merely the current default.
- The Markdown report and HTML deck must include a plain recommendation section:
  fastest, cheapest, best, selected, why, the table row that supports each
  measured recommendation, and the override applied.

Override application:

- `report`: do not apply a default-profile override. Write the report, Slidey
  source, rendered review artifact, case study, and summary JSON only. Set
  `override.applied` to false and explain that no runtime default was changed.
- `local`: update `{{ args.override_path }}` so new Kitsoki sessions use the
  selected profile/model/effort by default. Preserve unrelated keys such as
  `intercept:` and existing `harness_profiles:`. Do not write secrets. If the
  file does not exist, create a minimal local override.
- `project`: update the project override path named by `{{ args.override_path }}`.
  If no path is provided, return `status: "blocked"` and do not guess.
- `author`: update the base story call-site `selection:` only when
  `{{ args.target_story }}` and `{{ args.target_call }}` identify a real story
  and call. Otherwise return `status: "blocked"` with the missing target. Use
  call-site `selection:` metadata, not a local profile default, for author mode.

Return a JSON object matching `schemas/report_artifact.json`. Every count and
path must be read from the generated files or command results; do not invent
numbers. `summary_markdown` should include:

- the question answered;
- the headline recommendation;
- fastest, cheapest, best, and selected configured options;
- the override path and whether it was applied;
- the case-study path and Slidey source path when produced;
- effectiveness/speed/cost evidence available;
- intent-suite and transcript-coverage results;
- missing evidence/readiness gaps;
- the exact artifact paths.
