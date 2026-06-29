#!/usr/bin/env bash
# edge-scan.sh — deterministic rendered-frame clipping detector.
#
# Flags frames where meaningful non-background pixels touch the left, right, or
# top frame edge. This catches title/subtitle text that has shifted off-canvas or
# been clipped by the MP4 frame. Bottom is ignored by default because many demos
# carry a recorder/progress/status line there.
set -euo pipefail

command -v ffmpeg >/dev/null 2>&1 || { echo "ffmpeg not on PATH" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 not on PATH" >&2; exit 2; }

src="${1:-}"; shift || true
[ -n "$src" ] || { echo "usage: edge-scan.sh <frames-dir|image> [--out scan.json] [--edge-px N] [--min-ratio F] [--contrast D] [--fail-on-find]" >&2; exit 2; }

out="" edge_px=3 min_ratio="0.006" contrast=48 fail_on_find=0
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out="$2"; shift 2 ;;
    --edge-px) edge_px="$2"; shift 2 ;;
    --min-ratio) min_ratio="$2"; shift 2 ;;
    --contrast) contrast="$2"; shift 2 ;;
    --fail-on-find) fail_on_find=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

frames=()
if [ -d "$src" ]; then
  while IFS= read -r f; do frames+=("$f"); done < <(find "$src" -maxdepth 1 -type f -name '*.png' | sort)
else
  frames+=("$src")
fi
[ "${#frames[@]}" -gt 0 ] || { echo "no frames to scan under $src" >&2; exit 2; }

python3 - "$edge_px" "$min_ratio" "$contrast" "$fail_on_find" "$out" "${frames[@]}" <<'PY'
import json, subprocess, sys
from collections import Counter

edge_px = max(1, int(sys.argv[1]))
min_ratio = float(sys.argv[2])
contrast = float(sys.argv[3])
fail_on_find = sys.argv[4] == "1"
out = sys.argv[5]
frames = sys.argv[6:]

def dist(a, b):
    return ((a[0]-b[0])**2 + (a[1]-b[1])**2 + (a[2]-b[2])**2) ** 0.5

def decode(path):
    probe = subprocess.run(
        ["ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height",
         "-of", "csv=s=x:p=0", path],
        capture_output=True, text=True)
    if probe.returncode != 0 or "x" not in probe.stdout:
        return None
    w, h = [int(x) for x in probe.stdout.strip().split("x")[:2]]
    p = subprocess.run(
        ["ffmpeg", "-loglevel", "error", "-i", path, "-f", "rawvideo", "-pix_fmt", "rgb24", "-"],
        capture_output=True)
    if p.returncode != 0 or len(p.stdout) < w * h * 3:
        return None
    return w, h, p.stdout

def px(buf, w, x, y):
    i = (y * w + x) * 3
    return (buf[i], buf[i+1], buf[i+2])

def quant(c):
    return tuple((v // 16) * 16 for v in c)

results = []
flagged = []
for path in frames:
    name = path.rsplit("/", 1)[-1]
    decoded = decode(path)
    if not decoded:
        results.append({"frame": name, "error": "decode-failed"})
        continue
    w, h, buf = decoded
    # Dominant color from a sparse grid, enough to identify the deck/page bg.
    samples = []
    sx = max(1, w // 64)
    sy = max(1, h // 36)
    for y in range(0, h, sy):
        for x in range(0, w, sx):
            samples.append(quant(px(buf, w, x, y)))
    bg, _ = Counter(samples).most_common(1)[0]

    sides = {}
    for side in ("left", "right", "top"):
        count = 0
        total = 0
        if side in ("left", "right"):
            xs = range(edge_px) if side == "left" else range(max(0, w - edge_px), w)
            for y in range(h):
                for x in xs:
                    total += 1
                    if dist(px(buf, w, x, y), bg) >= contrast:
                        count += 1
        else:
            for y in range(edge_px):
                for x in range(w):
                    total += 1
                    if dist(px(buf, w, x, y), bg) >= contrast:
                        count += 1
        ratio = count / max(1, total)
        sides[side] = {"ratio": round(ratio, 5), "pixels": count}

    rec = {"frame": name, "width": w, "height": h, "background": "#%02x%02x%02x" % bg, "edges": sides}
    reasons = [
        f"{side} edge has {info['pixels']} high-contrast pixel(s) in the outer {edge_px}px ({info['ratio']*100:.2f}%)"
        for side, info in sides.items() if info["ratio"] >= min_ratio
    ]
    results.append(rec)
    if reasons:
        bad = dict(rec)
        bad["issue"] = "; ".join(reasons) + " — content appears to touch the frame edge and may be clipped"
        flagged.append(bad)

report = {
    "edge_px": edge_px,
    "min_ratio": min_ratio,
    "contrast": contrast,
    "frames_scanned": len(frames),
    "flagged": flagged,
    "frames": results,
}
text = json.dumps(report, indent=2)
if out:
    with open(out, "w") as f:
        f.write(text + "\n")
else:
    print(text)

if flagged:
    print(f"edge-scan: {len(flagged)} frame(s) with content touching frame edge — review:", file=sys.stderr)
    for r in flagged:
        print(f"  {r['frame']}: {r['issue']}", file=sys.stderr)
else:
    print(f"edge-scan: no edge-clipped content in {len(frames)} frame(s)", file=sys.stderr)
sys.exit(3 if flagged and fail_on_find else 0)
PY
