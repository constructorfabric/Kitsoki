---
id: bsacrobatix/Kitsoki#1194
title: 'bug create GitHub label-recovery burst can trip secondary rate limits'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1194
baseline: main
repro_command: go test ./cmd/kitsoki -run TestBug
---

# Bug create label recovery can trip rate limits

The GitHub filing path can burst label recovery calls and trigger secondary
rate limits, causing later bug filing to degrade or lose labels.
