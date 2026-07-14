#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$ROOT/policy/python-exceptions.tsv"
TMP="$(mktemp -d)"
cleanup() {
	rm -rf "$TMP"
}
trap cleanup EXIT

if [ ! -f "$MANIFEST" ]; then
	echo "missing Python exception manifest: $MANIFEST" >&2
	exit 1
fi

manifest_paths="$TMP/manifest-paths.txt"
manifest_invalid="$TMP/manifest-invalid.txt"
manifest_dupes="$TMP/manifest-dupes.txt"
tracked_py="$TMP/tracked-python.txt"
present_py="$TMP/present-python.txt"

awk -F '\t' '
	BEGIN { ok = 1 }
	/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
	NF < 2 {
		print "line " NR ": expected path<TAB>justification" > invalid
		ok = 0
		next
	}
	$1 == "" {
		print "line " NR ": empty path" > invalid
		ok = 0
		next
	}
	$2 == "" {
		print "line " NR ": empty justification for " $1 > invalid
		ok = 0
		next
	}
	{
		seen[$1]++
		print $1
		paths++
	}
END {
	for (p in seen) {
		if (seen[p] > 1) {
			print p > dupes
			ok = 0
		}
	}
	if (paths == 0) {
		print "manifest is empty" > invalid
		ok = 0
	}
	if (!ok) {
		exit 1
	}
}
' invalid="$manifest_invalid" dupes="$manifest_dupes" "$MANIFEST" | sort -u >"$manifest_paths"

if [ -s "$manifest_invalid" ] || [ -s "$manifest_dupes" ]; then
	echo "Python exception manifest is invalid:" >&2
	[ ! -s "$manifest_invalid" ] || sed 's/^/  /' "$manifest_invalid" >&2
	if [ -s "$manifest_dupes" ]; then
		echo "  duplicate path entries:" >&2
		sed 's/^/    /' "$manifest_dupes" >&2
	fi
	exit 1
fi

git -C "$ROOT" ls-files '*.py' | sort >"$tracked_py"
git -C "$ROOT" ls-files --cached --others --exclude-standard '*.py' | sort >"$present_py"

missing_manifest="$TMP/missing-manifest.txt"
stale_manifest="$TMP/stale-manifest.txt"

comm -23 "$present_py" "$manifest_paths" >"$missing_manifest"
comm -23 "$manifest_paths" "$tracked_py" >"$stale_manifest"

if [ -s "$missing_manifest" ] || [ -s "$stale_manifest" ]; then
	echo "Python policy check failed." >&2
	if [ -s "$missing_manifest" ]; then
		echo "  Python files missing from policy/python-exceptions.tsv:" >&2
		sed 's/^/    /' "$missing_manifest" >&2
	fi
	if [ -s "$stale_manifest" ]; then
		echo "  Manifest entries that are no longer tracked Python files:" >&2
		sed 's/^/    /' "$stale_manifest" >&2
	fi
	echo "  New Python requires an explicit manifest entry with justification." >&2
	exit 1
fi

echo "python file policy: $(wc -l <"$tracked_py" | tr -d ' ') tracked .py file(s), all justified"
