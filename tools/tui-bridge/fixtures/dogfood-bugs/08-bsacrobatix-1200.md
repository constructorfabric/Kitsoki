---
id: bsacrobatix/Kitsoki#1200
title: 'scenario-qa bugfix x tui leg cannot provision a ticket'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1200
baseline: main
repro_command: go test ./stories/bugfix/...
---

# Scenario QA bugfix TUI leg cannot provision a ticket

The bugfix story can exit to the ticket picker with no ticket queue, preventing
candidate diff, oracle, and full-suite evidence from being produced.
