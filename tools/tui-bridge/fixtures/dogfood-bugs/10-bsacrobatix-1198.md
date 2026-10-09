---
id: bsacrobatix/Kitsoki#1198
title: 'web report-bug modal network trace rows all read POST /rpc 200'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1198
baseline: main
repro_command: pnpm -C tools/runstatus test
---

# Report-bug modal hides JSON-RPC method names

The network trace list renders many identical `POST /rpc 200` rows instead of
surfacing the JSON-RPC method, which removes review signal from captured HARs.
