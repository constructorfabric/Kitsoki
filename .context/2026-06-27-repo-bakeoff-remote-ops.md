# Repo-bakeoff remote operations (SSH)

Use this from SSH to check and control the VM bakeoff state from artifacts only.

```bash
cd /Users/brad/code/Kitsoki
PROJECT=${1:-gears-rust}
STATE_FILE=${2:-"$(find .artifacts/external-bakeoff -type f -path "*/status/repo-bakeoff-completion-${PROJECT}.json" | head -n 1)"}

printf "State: %s\n" "$STATE_FILE"
cat "$STATE_FILE"
printf "\nCompleted: %s\n" "$(jq -r '.completed // false' "$STATE_FILE")"
printf "Status: %s\n" "$(jq -r '.status // "(unknown)"' "$STATE_FILE")"
printf "Requires_drive: %s\n" "$(jq -r '.requires_drive // false' "$STATE_FILE")"
printf "Repairable: %s\n" "$(jq -r '.repairable // false' "$STATE_FILE")"
printf "Blockers:\n"
jq -r '.blockers[]?' "$STATE_FILE"
```

```bash
# Poll for completion (returns as soon as completed=true)
while true; do
  COMPLETED=$(jq -r '.completed // false' "$STATE_FILE")
  printf "%s completed=%s status=%s requires_drive=%s repairable=%s\n" \
    "$(date --iso-8601=seconds)" \
    "$(jq -r '.completed // false' "$STATE_FILE")" \
    "$(jq -r '.status // "(unknown)"' "$STATE_FILE")" \
    "$(jq -r '.requires_drive // false' "$STATE_FILE")" \
    "$(jq -r '.repairable // false' "$STATE_FILE")"
  [ "$COMPLETED" = "true" ] && break
  sleep 30
done
```

```bash
# If completion says manual drives are needed, extract executable commands directly:
jq -r '.drive_commands[]?' "$STATE_FILE"
# Or pending-only commands:
jq -r '.pending_commands[]? | .command' "$STATE_FILE"
```

```bash
# Useful companion artifacts:
cat .artifacts/external-bakeoff/readiness/repo-bakeoff-handoffs.md
cat .artifacts/external-bakeoff/report/completion.md
cat .artifacts/external-bakeoff/report/report.md
```

