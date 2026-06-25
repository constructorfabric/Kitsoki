# gears-rust bugfix marathon — status

**Shipped (independent-verify PASS): 0 / 10**

Generated deterministically by `gen_table.py` from `cases.yaml` + `attempts.jsonl`.

| bug | title | fix_sha | RED? | cand | exit | verify | tokens | cost$ | wall_s | notes |
|---|---|---|---|---|---|---|---|---|---|---|
| bug1 | gh-4115: normalize underscore→dash for k8s env-var ove | a7080261 | false |  |  | PENDING |  |  |  | baseline RED confirmed (100!=50); drivin |
| bug2 | oagw: close Pingora read pipe on streaming body limit  | 7845d2e5 | false |  |  |  |  |  |  |  |
| bug3 | oagw: chunked transfer encoding for streaming request  | 9ca475ed | false |  |  |  |  |  |  |  |
| bug4 | errors: support convert for different error types to C | e21d79ab | false |  |  |  |  |  |  |  |
| bug5 | resource-group: drop RG-prefix requirement for allowed | 8737281d | false |  |  |  |  |  |  |  |
| bug6 | account-management: derive effective realm + children- | 26ad613f | false |  |  |  |  |  |  |  |
| bug7 | account-management: claim/due predicate fences + IdP-r | f0873f75 | false |  |  |  |  |  |  |  |
| bug8 | oagw: use gts id for upstream_id in API | 216a9ccd | false |  |  |  |  |  |  |  |
| bug9 | resource-group: validate allowed_memberships via gts-r | 5a58a219 | false |  |  |  |  |  |  |  |
| bug10 | modkit-db: match PG serialization/deadlock by message  | c3c96ac7 | false |  |  |  |  |  |  |  |
