#!/usr/bin/env bash
# kitsoki-ui-review · STAGE 2 (LLM): multi-agent heuristic vision review.
#
# Reads the deterministic capture (audit.json + frames/) and fans the frames out
# across SEVERAL independent, read-only vision agents — one per shard (default:
# one tour step = that surface across all its viewports, ~3 frames).
# No single agent ever holds the whole frame set in its context; each judges only
# its own handful of frames against the heuristic catalog, with that surface's
# deterministic audit findings handed in as already-known truth. A per-shard
# adversarial skeptic (downgrade-only) then re-checks each agent's own frames.
#
# Uses the first available local agent CLI from Codex, Claude, or agy. This is an
# LLM review by design; it is NOT a no-LLM flow test and must never be wired into
# the automated suite (CLAUDE.md).
#
# Output: vision.json = { shards:[...], findings:[ all per-shard findings ] }.
# report.sh merges this with audit.json into the gated verdict.
#
# Usage:
#   review.sh --audit <audit.json> --frames <dir> --heuristics <file>
#             --out <vision.json> [--design-intent <file>] [--model M]
#             [--reviewer auto|codex|claude|agy|ORDER]
#             [--jobs N] [--shard step|viewport] [--no-adversary]
set -euo pipefail

audit="" frames="" heuristics="" out="" intent=""
model="${KITSOKI_UI_REVIEW_MODEL:-${KITSOKI_UI_QA_MODEL:-}}"
reviewer="${KITSOKI_UI_REVIEW_REVIEWER:-${KITSOKI_UI_QA_REVIEWER:-auto}}"
jobs=4 shard_by="step" adversary=1
while [ $# -gt 0 ]; do
  case "$1" in
    --audit)         audit="$2"; shift 2 ;;
    --frames)        frames="$2"; shift 2 ;;
    --heuristics)    heuristics="$2"; shift 2 ;;
    --out)           out="$2"; shift 2 ;;
    --design-intent) intent="$2"; shift 2 ;;
    --model)         model="$2"; shift 2 ;;
    --reviewer)      reviewer="$2"; shift 2 ;;
    --jobs)          jobs="$2"; shift 2 ;;
    --shard)         shard_by="$2"; shift 2 ;;
    --no-adversary)  adversary=0; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

command -v jq     >/dev/null 2>&1 || { echo "jq not on PATH" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 not on PATH" >&2; exit 1; }
[ -f "$audit" ]      || { echo "no such audit.json: $audit" >&2; exit 1; }
[ -d "$frames" ]     || { echo "no such frames dir: $frames" >&2; exit 1; }
[ -f "$heuristics" ] || { echo "no such heuristics file: $heuristics" >&2; exit 1; }
[ -n "$out" ]        || { echo "--out is required" >&2; exit 1; }

frames="$(cd "$frames" && pwd)"
mkdir -p "$(dirname "$out")"
shard_dir="$(mktemp -d)"; trap 'rm -rf "$shard_dir"' EXIT

append_unique_reviewer() { # <current-list> <candidate>
  local current="$1" cand="$2"
  case " $current " in
    *" $cand "*) printf '%s' "$current" ;;
    *) [ -n "$current" ] && printf '%s %s' "$current" "$cand" || printf '%s' "$cand" ;;
  esac
}

default_reviewers_for_model() {
  case "$model" in
    claude*|opus*|sonnet*|haiku*) printf '%s' "claude codex agy" ;;
    gemini*)                      printf '%s' "agy codex claude" ;;
    gpt*|codex*|o[0-9]*)          printf '%s' "codex claude agy" ;;
    *)                            printf '%s' "codex claude agy" ;;
  esac
}

resolve_reviewers() {
  local requested token out defaults
  out=""
  requested="$(printf '%s' "${reviewer:-auto}" | tr ',' ' ')"
  defaults="$(default_reviewers_for_model)"
  for token in $requested; do
    case "$token" in
      auto) for token in $defaults; do out="$(append_unique_reviewer "$out" "$token")"; done ;;
      codex|claude|agy) out="$(append_unique_reviewer "$out" "$token")" ;;
      *) echo "unknown reviewer '$token' (want auto, codex, claude, agy, or a comma-separated order)" >&2; return 2 ;;
    esac
  done
  for token in codex claude agy; do out="$(append_unique_reviewer "$out" "$token")"; done
  printf '%s' "$out"
}

reviewer_candidates="$(resolve_reviewers)"
[ -n "$reviewer_candidates" ] || { echo "no reviewer candidates resolved" >&2; exit 1; }

model_for_reviewer() { # <reviewer>
  local r="$1" m="$model"
  case "$r" in
    codex)  [ -n "${KITSOKI_UI_REVIEW_CODEX_MODEL:-${KITSOKI_UI_QA_CODEX_MODEL:-}}" ]  && { printf '%s' "${KITSOKI_UI_REVIEW_CODEX_MODEL:-${KITSOKI_UI_QA_CODEX_MODEL:-}}"; return 0; } ;;
    claude) [ -n "${KITSOKI_UI_REVIEW_CLAUDE_MODEL:-${KITSOKI_UI_QA_CLAUDE_MODEL:-}}" ] && { printf '%s' "${KITSOKI_UI_REVIEW_CLAUDE_MODEL:-${KITSOKI_UI_QA_CLAUDE_MODEL:-}}"; return 0; } ;;
    agy)    [ -n "${KITSOKI_UI_REVIEW_AGY_MODEL:-${KITSOKI_UI_QA_AGY_MODEL:-}}" ]    && { printf '%s' "${KITSOKI_UI_REVIEW_AGY_MODEL:-${KITSOKI_UI_QA_AGY_MODEL:-}}"; return 0; } ;;
  esac
  [ -n "$m" ] || return 0
  case "$r:$m" in
    codex:claude*|codex:opus*|codex:sonnet*|codex:haiku*) return 0 ;;
    claude:gpt*|claude:codex*|claude:o[0-9]*|claude:gemini*) return 0 ;;
    agy:claude*|agy:opus*|agy:sonnet*|agy:haiku*|agy:gpt*|agy:codex*|agy:o[0-9]*) return 0 ;;
    *) printf '%s' "$m" ;;
  esac
}

extract_json() {
  python3 -c '
import sys, json
s = sys.stdin.read()
def first_balanced(text):
    start = text.find("{")
    while start != -1:
        depth = 0; instr = False; esc = False
        for i in range(start, len(text)):
            c = text[i]
            if instr:
                if esc: esc = False
                elif c == "\\": esc = True
                elif c == "\"": instr = False
            else:
                if c == "\"": instr = True
                elif c == "{": depth += 1
                elif c == "}":
                    depth -= 1
                    if depth == 0:
                        cand = text[start:i+1]
                        try:
                            json.loads(cand)
                            print(cand, end="")
                            return
                        except Exception:
                            break
        start = text.find("{", start + 1)
    sys.exit(1)
first_balanced(s)
'
}

findings_schema="$shard_dir/.findings.schema.json"
cat > "$findings_schema" <<'JSON'
{
  "type": "object",
  "additionalProperties": false,
  "required": ["findings"],
  "properties": {
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["check", "severity", "frame", "viewport", "observation", "recommendation", "confidence"],
        "properties": {
          "check": {"type": "string"},
          "severity": {"type": "string", "enum": ["error", "warn", "info"]},
          "frame": {"type": "string"},
          "viewport": {"type": "string"},
          "observation": {"type": "string"},
          "recommendation": {"type": "string"},
          "confidence": {"type": "number"}
        }
      }
    }
  }
}
JSON

# Shard keys: a step id (default) or a viewport name. Each shard reviews a
# coherent, small set of frames so a single agent's context stays light.
shards=()
if [ "$shard_by" = "viewport" ]; then
  while IFS= read -r shard; do [ -n "$shard" ] && shards+=("$shard"); done < <(jq -r '[.captures[]|select(.captured)|.viewport]|unique|.[]' "$audit")
  sel_field=".viewport"
else
  while IFS= read -r shard; do [ -n "$shard" ] && shards+=("$shard"); done < <(jq -r '[.captures[]|select(.captured)|.step]|unique|.[]' "$audit")
  sel_field=".step"
fi
[ "${#shards[@]}" -gt 0 ] || { echo "no captured frames in $audit" >&2; exit 1; }

intent_block=""
[ -n "$intent" ] && [ -f "$intent" ] && intent_block="$intent"

# ---- one read-only vision-agent call over a shard's frames; echoes JSON -------
call_vision() { # <promptfile> <result-out> <frame-list>
  local pf="$1" rf="$2" shard_frames="$3" attempt selected raw result json
  local result_stem
  result_stem="$(basename "$rf" .json)"
  for attempt in 1 2; do
    for selected in $reviewer_candidates; do
      if ! command -v "$selected" >/dev/null 2>&1; then
        echo "  skipping $selected: CLI not on PATH" >&2
        continue
      fi
      result=""
      case "$selected" in
        codex)
          local codex_out="$shard_dir/$result_stem.codex-${attempt}.txt"
          local codex_err="$shard_dir/$result_stem.codex-${attempt}.stderr.txt"
          local image_args=()
          local cmd=(codex exec)
          local model_arg
          model_arg="$(model_for_reviewer codex || true)"
          [ -n "$model_arg" ] && cmd+=( --model "$model_arg" )
          while IFS= read -r frame; do
            [ -n "$frame" ] && image_args+=( --image "$frames/$frame" )
          done <<< "$shard_frames"
          if ! "${cmd[@]}" \
                --cd "$(pwd)" \
                --sandbox read-only \
                --ephemeral \
                --output-schema "$findings_schema" \
                --output-last-message "$codex_out" \
                "${image_args[@]}" \
                - < "$pf" >/dev/null 2>"$codex_err"; then
            echo "  codex invocation failed (attempt $attempt)" >&2
            continue
          fi
          result="$(cat "$codex_out" 2>/dev/null || true)"
          ;;
        claude)
          local claude_status=0
          local cmd=(claude -p --output-format json)
          local model_arg
          model_arg="$(model_for_reviewer claude || true)"
          [ -n "$model_arg" ] && cmd+=( --model "$model_arg" )
          raw="$("${cmd[@]}" \
                  --permission-mode bypassPermissions \
                  --allowedTools "Read" \
                  --add-dir "$frames" \
                  < "$pf" 2>"$shard_dir/$result_stem.claude-${attempt}.stderr.txt")" || claude_status=$?
          if [ "$claude_status" -ne 0 ]; then
            echo "  claude invocation failed (attempt $attempt)" >&2
            continue
          fi
          result="$(printf '%s' "$raw" | jq -r '.result // .text // empty')"
          [ -n "$result" ] || result="$raw"
          ;;
        agy)
          local agy_status=0
          local prompt_text
          prompt_text="$(cat "$pf")"
          local cmd=(agy --print "$prompt_text" --output-format json --dangerously-skip-permissions --add-dir "$frames")
          local model_arg
          model_arg="$(model_for_reviewer agy || true)"
          [ -n "$model_arg" ] && cmd+=( --model "$model_arg" )
          raw="$("${cmd[@]}" 2>"$shard_dir/$result_stem.agy-${attempt}.stderr.txt")" || agy_status=$?
          if [ "$agy_status" -ne 0 ]; then
            echo "  agy invocation failed (attempt $attempt)" >&2
            continue
          fi
          result="$(printf '%s' "$raw" | jq -r '.response // .result // .text // empty' 2>/dev/null || true)"
          [ -n "$result" ] || result="$raw"
          ;;
      esac
      if json="$(printf '%s' "$result" | extract_json)"; then
        printf '%s' "$json" | jq . > "$rf"
        echo "  reviewer: $selected" >&2
        return 0
      fi
      printf '%s' "$result" > "$shard_dir/$result_stem.$selected-${attempt}.raw.txt" 2>/dev/null || true
      echo "  no parseable JSON from $selected (attempt $attempt); trying next reviewer" >&2
    done
  done
  echo '{"findings":[]}' > "$rf"     # never let one bad shard fail the run
}

# ---- review one shard: grounded pass, then optional adversarial downgrade -----
review_shard() { # <shard-key>
  local key="$1" safe rev_prompt rev_json adv_prompt
  safe="$(printf '%s' "$key" | tr -c 'a-zA-Z0-9_.-' '_')"
  rev_prompt="$shard_dir/$safe.review.txt"
  rev_json="$shard_dir/$safe.json"

  local frame_list audit_findings
  frame_list="$(jq -r --arg k "$key" ".captures[]|select(.captured and ($sel_field==\$k))|.frame" "$audit")"
  audit_findings="$(jq -c --arg k "$key" "[.findings[]|select($sel_field==\$k)]" "$audit")"
  [ -n "$frame_list" ] || { echo '{"findings":[]}' > "$rev_json"; return 0; }

  {
    cat <<'HEAD'
You are a senior product designer doing a LAYOUT & USABILITY review of a few
screenshots ("frames") of one surface of a web UI, captured at one or more
viewport widths. Judge how well-designed and usable each frame is. Apply the
heuristic catalog below.

EVIDENCE RULES (these make the review trustworthy — follow them exactly):
1. The frame PNG files are the ONLY admissible evidence. The images may be
   attached to the review and are also available by filename if your runtime
   exposes a file-read/image-read tool. Inspect every frame listed below.
2. Every finding MUST cite the frame filename and quote what is LITERALLY
   visible that constitutes the defect. No frame, no finding. Do not infer
   beyond the pixels or invent UI you did not see.
3. Honour each check's `not_this` guard — do NOT report a false positive. When
   unsure whether something is a real defect, DROP it. A short, high-precision
   list beats a long speculative one.
4. The "ALREADY-KNOWN AUDIT FINDINGS" below were measured deterministically from
   the live DOM (overflow, off-screen, tiny text/targets, stray tokens, WCAG
   contrast/labels). Treat them as TRUE — do not re-report them. You may
   reference one for context, but your job is the JUDGEMENT calls metrics can't
   make (hierarchy, balance, affordance, responsive quality, empty states).
5. Use the check `id` and `severity` from the catalog. Pick the viewport the
   frame was captured at (it is in the filename after "@").

OUTPUT: print ONLY a single raw JSON object (no prose, no ``` fences):
{ "findings": [
    { "check":"<catalog id>", "severity":"error|warn|info",
      "frame":"02-home-story-cards@mobile.png", "viewport":"mobile",
      "observation":"<literal, what is visible that is wrong>",
      "recommendation":"<one concrete fix>", "confidence":0.0 }
] }
If the surface looks good, return {"findings":[]}.
HEAD
    echo; echo "## HEURISTIC CATALOG"; echo; echo '```yaml'; cat "$heuristics"; echo '```'
    if [ -n "$intent_block" ]; then
      echo; echo "## DESIGN INTENT (context — what this UI is for)"; echo; cat "$intent_block"
    fi
    echo; echo "## ALREADY-KNOWN AUDIT FINDINGS (measured, treat as true, do NOT re-report)"
    echo; echo '```json'; echo "$audit_findings"; echo '```'
    echo; echo "## FRAMES TO REVIEW"
    echo "Located in: $frames. Cite by these exact filenames."
    echo "$frame_list" | sed 's/^/  - /'
  } > "$rev_prompt"

  call_vision "$rev_prompt" "$rev_json" "$frame_list"

  if [ "$adversary" -eq 1 ]; then
    local cur; cur="$(cat "$rev_json")"
    # Only bother with the skeptic if there is something to challenge.
    if printf '%s' "$cur" | jq -e '.findings | length > 0' >/dev/null 2>&1; then
      adv_prompt="$shard_dir/$safe.adv.txt"
      {
        cat <<'HEAD'
You are an adversarial verifier guarding against false positives in a UI review.
Below is a prior set of findings for a few frames, plus the frames themselves.
For EACH finding: Read its cited frame and confirm the defect is ACTUALLY,
clearly visible there and is not excused by the catalog's `not_this` guard.

You may ONLY remove findings or LOWER their severity (error→warn→info). Never add
a finding and never raise a severity. If a finding is real and well-cited, keep
it unchanged. Delete any finding whose frame doesn't clearly show it, that
restates an already-known measured audit finding, or that the `not_this` guard
excuses.

OUTPUT: print ONLY the revised object {"findings":[...]} (same shape, raw JSON,
no prose, no ``` fences).
HEAD
        echo; echo "## HEURISTIC CATALOG (for the not_this guards)"; echo; echo '```yaml'; cat "$heuristics"; echo '```'
        echo; echo "## PRIOR FINDINGS"; echo; echo '```json'; printf '%s' "$cur"; echo; echo '```'
        echo; echo "## FRAMES"
        echo "Located in: $frames. Cite by these exact filenames."
        echo "$frame_list" | sed 's/^/  - /'
      } > "$adv_prompt"
      call_vision "$adv_prompt" "$shard_dir/$safe.verified.json" "$frame_list"
      mv "$shard_dir/$safe.verified.json" "$rev_json"
    fi
  fi
  # Tag every finding with its shard for traceability.
  jq --arg s "$key" '{shard:$s, findings:(.findings // [] | map(. + {shard:$s}))}' "$rev_json" \
    > "$rev_json.tagged" && mv "$rev_json.tagged" "$rev_json"
}

display_model="$model"
[ -n "$display_model" ] || display_model="configured-default"
echo "▸ reviewing ${#shards[@]} shards (by $shard_by) · reviewers: $reviewer_candidates · model: $display_model · up to $jobs parallel agents…" >&2

# Fan out with a simple concurrency throttle.
pids=()
for key in "${shards[@]}"; do
  review_shard "$key" &
  pids+=("$!")
  while [ "$(jobs -rp | wc -l | tr -d ' ')" -ge "$jobs" ]; do sleep 0.2; done
done
wait

# Merge all shard verdicts into one vision.json.
jq -s '{
  shards:   [ .[].shard ],
  findings: [ .[].findings[]? ]
}' "$shard_dir"/*.json > "$out"

n="$(jq '.findings | length' "$out")"
echo "▸ ${#shards[@]} shards reviewed → $n vision findings → $out" >&2
