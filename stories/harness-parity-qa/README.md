# Harness Parity QA

This story turns backend parity into a deterministic operator workflow. It is
aimed at regressions where Claude and Codex both complete a task, but only one
backend exposes live agent activity such as thinking text or tool-use
breadcrumbs in the TUI, web UI, or VS Code.

The default `start` path runs no LLM and incurs no cost. It shells through
`host.run` to `scripts/harness_parity_report.py`, which writes:

- `.context/harness-parity-qa.md`
- `.artifacts/harness-parity-qa/summary.json`

The critical regression test is:

```sh
go test ./internal/host -run TestAgentStream_HarnessParityThinkingAndToolUse
```

That test feeds synthetic Claude and Codex JSONL through the real backend stream
parsers and asserts they normalize to the same ordered activity feed:

1. full thinking text
2. matching tool breadcrumb

The story also records the expected downstream surface checks for TUI, web, and
VS Code. LLM visual judging is deliberately not part of automated tests; use the
`kitsoki-ui-qa` style review as a gated manual proof over deterministic
screenshots or videos when a UI change needs visual signoff.
