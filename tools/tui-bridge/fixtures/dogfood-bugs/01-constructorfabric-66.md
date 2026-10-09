---
id: constructorfabric/Kitsoki#66
title: 'tui: conversation lane AskOffPath error renders raw orchestrator payload'
source_kind: github
source_repo: constructorfabric/Kitsoki
source_url: https://github.com/constructorfabric/Kitsoki/issues/66
baseline: main
repro_command: go test ./cmd/kitsoki -run TestRunFlags
---

# TUI AskOffPath error renders raw orchestrator payload

The TUI surfaced a raw conversation-lane orchestration error instead of a
reviewable operator message. The issue came from the public GitHub queue and is
mirrored here so the demo can run without network or LLM calls.
