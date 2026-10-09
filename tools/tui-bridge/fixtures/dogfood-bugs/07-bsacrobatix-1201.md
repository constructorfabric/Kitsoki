---
id: bsacrobatix/Kitsoki#1201
title: 'scenario-qa live drive requires outer session harness=live'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1201
baseline: main
repro_command: go test ./stories/scenario-qa/...
---

# Scenario QA live drive requires live outer harness

The scenario-qa instructions allowed a replay outer session to request a live
nested driver, then failed late when the host.agent.task dispatch needed a live
harness.
