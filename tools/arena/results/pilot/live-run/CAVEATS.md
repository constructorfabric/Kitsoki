# Reading this pilot's rollup honestly

`rollup.md`'s `kitsoki-codex-native` row blends **2 real, live-validated cells**
(post-fix, real dispatch through the actual kitsoki bugfix pipeline) with
**1 stale cell** (pre-fix, produced by the old broken dispatch that ran an
identical raw `codex exec` call to the `single-briefed` arm — not a real
kitsoki-pipeline result). Do not read the blended `avg cost` for
`kitsoki-codex-native` as a real number; read the two real cells directly:

| task | kitsoki (real) | single-briefed | ratio |
|---|---|---|---|
| query-string-qs1-bugfix-test-repair | $0.44 | $0.48 | ~0.9x (kitsoki cheaper) |
| nextjs-05-story-runtime | $1.57 | $0.68 | ~2.3x |
| vscode-01-docs-site-release | $1.18 (refreshed, see below) | $0.27 | ~4.4x |

Both real cells: `verdict: solved`, real cost computed cache-aware from the
kitsoki session trace's own per-call usage (`cached_input_tokens` billed at
`pricing.py`'s `cache_read` rate, not the full input rate — see
`real_trace_metrics` in `tools/arena/lib/paired_task_runner.py`).

The initial (now-corrected) pass of this validation reported $5.24 and $35.03
for these two cells — a >20x overstatement caused by a real cost-reporting
bug (summing `input_tokens` while discarding `cached_input_tokens`, then
pricing everything at the full input rate). See goal log
`.artifacts/goal/generalized-usage-live/log.jsonl` (seq 67+) for the full
trail: the dispatch-routing fix, the cost-accounting fix, and this correction,
in that order.

`vscode-01-docs-site-release`'s `kitsoki-codex-native` cell was refreshed in
a later session: real cost $1.177037 (cache-aware, 386,844 fresh input +
3,629,696 cache-read input + 23,977 output tokens), `drive.sh exit=0`,
wall_s=987.6. The pipeline drove to completion but `verdict: failed` — the
`github_content` oracle scored `green=False` (the produced doc content
didn't match the expected upstream commit's text). This is a real substance
result, not an infra failure, so this pilot's 3-task comparison is now
complete with all-real data.

**Held-out-split note (important for WB.5):** `vscode-01-docs-site-release`
is marked `split: heldout` in `tools/arena/corpus/cost-bench.manifest.yaml`,
not a training task. It has now been executed live **twice** under this
pilot's "dispatch-mechanism/infra validation" framing (the original pilot
run, and this refresh) — neither of which is the official WB.5 confirmation
run per `docs/research/cost-efficiency-benchmark.md` §7.3 ("the first and
only time held-out tasks are executed"). When WB.5 runs, do not describe
this task's confirmation-run execution as a fresh first look at held-out
data — it isn't. Whether this prior exposure meaningfully taints the WB.5
result for this specific task (no training patches were derived from either
pilot run, so the *process* wasn't tuned against it) is a judgment call for
whoever writes the WB.5 report; it should be disclosed there either way.
