---
id: constructorfabric/Kitsoki#63
title: 'tui: inbox notification repeats on landing'
source_kind: github
source_repo: constructorfabric/Kitsoki
source_url: https://github.com/constructorfabric/Kitsoki/issues/63
baseline: main
repro_command: go test ./cmd/kitsoki -run TestInbox
---

# Inbox notification repeats on landing

The landing screen showed the same inbox notification multiple times, cluttering
the prompt and quick-action area.
