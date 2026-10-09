---
id: bsacrobatix/Kitsoki#1197
title: 'web trace user-subsystem rows render with empty event label'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1197
baseline: main
repro_command: pnpm -C tools/runstatus test
---

# Web trace user rows render without labels

Collapsed trace rows for user-intent events can show the `user` subsystem chip
with no event label, even though the intent data is present in expanded attrs.
