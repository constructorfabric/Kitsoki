---
id: bsacrobatix/Kitsoki#1202
title: 'bug file-findings silently drops status=blocked findings'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1202
baseline: main
repro_command: go test ./internal/host -run TestFindings
---

# Findings filing silently drops blocked findings

The JSON output can omit blocked findings without an excluded outcome row,
making dogfood accounting look complete when items were filtered.
