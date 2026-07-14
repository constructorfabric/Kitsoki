---
id: constructorfabric/Kitsoki#64
title: 'tui: dev-story landing quick actions wrap awkwardly'
source_kind: github
source_repo: constructorfabric/Kitsoki
source_url: https://github.com/constructorfabric/Kitsoki/issues/64
baseline: main
repro_command: go test ./internal/tui/...
---

# Dev-story landing quick actions wrap awkwardly

The landing-room quick actions run together in a narrow terminal, making the
dev-story workbench hard to scan. The original issue was filed from TUI context.
