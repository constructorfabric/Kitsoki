# Punch-list Story

`stories/punch-list/` is a generic YAML-driven worklist runner. It exists so a
ranked list of real work can be dogfooded through Kitsoki Studio MCP without
hard-coding a one-off "top 10" workflow.

The manifest format is `punch-list/v1`. Each item names a target story, optional
implementation story, model/profile policy, and deterministic verification. The
story enforces `profile: codex-native` and `model: gpt-5.5` for live work when
that policy is required, then records trace paths, findings, verifier results,
and a markdown report under `.artifacts/punch-list/`.

Automated flow tests stay no-LLM. Live Studio MCP driving happens only through
the `host.agent.task` delegation rooms and should be run intentionally.

Dogfood note: the story enforces trace/model evidence before verification. If an
in-story driver lacks Studio MCP control tools, it parks at `needs-human` with a
missing-evidence message instead of marking the item complete.

The top-10 GPT-5.5 dogfood list is captured as
`stories/punch-list/testdata/top10_gpt55.yaml`. Its deterministic no-LLM
regression and demo fixture is `stories/punch-list/flows/happy_top10_gpt55.yaml`.
