---
id: bsacrobatix/Kitsoki#1196
title: 'web tour observe-mode popup occludes its own anchor'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1196
baseline: main
repro_command: pnpm -C tools/runstatus test -- --grep tour
---

# Observe-mode tour popup occludes its own anchor

The onboarding tour can dead-end when the observe-mode popover covers the
interactive target and provides no Next button.
