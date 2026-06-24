// Throwaway prototype for continue-mode-spike R2.
// Verifies that compound dotted paths and parallel-encoded paths
// round-trip through a JSON-Pointer-valued RFC-6902 replace op without
// loss, including RFC-6901 escaping of `/` and `~`.
//
// The state-path representation uses `#` and `|` as parallel-encoding
// sigils (internal/machine/parallel.go:77-80). YAML state keys cannot
// contain those characters, but they DO appear in the value side of
// a state.transition op — we need to confirm JSON treats them as
// ordinary string bytes (it does; only `/` and `~` are RFC-6901-
// special, and only in the `path:` field, not the `value:` field).
//
// Run with: go run ./docs/proposals/notes/spike/r2-state-path
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

// rfc6901escape escapes the two RFC-6901 reserved characters inside a
// JSON-Pointer segment: `~` becomes `~0`, `/` becomes `~1`. Order
// matters — escape `~` first.
func rfc6901escape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

func roundtripStatePath(p string) (ok bool, decoded string, err error) {
	// Seed doc: { "path": "" }
	seed := []byte(`{"path":""}`)
	// Build the op. The path "/path" is the JSON-Pointer; the value
	// is the encoded state-path string. The value side is plain JSON
	// string — no special encoding needed for `#`, `|`, `.`, etc.
	op := []map[string]any{
		{"op": "replace", "path": "/path", "value": p},
	}
	patchBytes, err := json.Marshal(op)
	if err != nil {
		return false, "", err
	}
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		return false, "", err
	}
	out, err := patch.Apply(seed)
	if err != nil {
		return false, "", err
	}
	var got struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		return false, "", err
	}
	return got.Path == p, got.Path, nil
}

func roundtripPathInJSONPointer(segment string) (ok bool, err error) {
	// Edge case: an RFC-6901 escape of the *segment* itself.
	// We don't write state-path strings into JSON-Pointer paths today
	// (state-path always rides on the value side of a single fixed
	// pointer like "/path"), but confirm the escape is round-trip-safe
	// in case the document model ever changes.
	seed := []byte(`{}`)
	pointer := "/" + rfc6901escape(segment)
	op := []map[string]any{
		{"op": "add", "path": pointer, "value": "ok"},
	}
	patchBytes, _ := json.Marshal(op)
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		return false, err
	}
	out, err := patch.Apply(seed)
	if err != nil {
		return false, err
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		return false, err
	}
	return doc[segment] == "ok", nil
}

func main() {
	// Sample paths drawn from in-tree testdata. Compound dotted: cloak.
	// Parallel-encoded: synthesized from internal/machine/parallel.go's
	// own doc-comment example.
	paths := []string{
		"foyer",
		"cloakroom",
		"bar.dark",
		"bar.lit",
		"ended",
		"clock",
		"clock.calendar.day1",
		"clock.weather.dry",
		// Parallel-encoded example (parallel.go:25-28).
		"clock#clock.calendar.day1|clock.weather.dry",
		"clock#clock.calendar.soggy|clock.weather.rain",
		// Hypothetical 3-region for stress.
		"root#root.r1.x|root.r2.y|root.r3.z",
	}
	totalFail := 0
	fmt.Println("=== R2.a path values on /path ===")
	for _, p := range paths {
		ok, got, err := roundtripStatePath(p)
		if err != nil {
			fmt.Printf("  ERROR p=%q: %v\n", p, err)
			totalFail++
			continue
		}
		if !ok {
			fmt.Printf("  FAIL p=%q got=%q\n", p, got)
			totalFail++
			continue
		}
		fmt.Printf("  PASS p=%q\n", p)
	}

	fmt.Println("\n=== R2.b RFC-6901 escape (segments containing / and ~) ===")
	for _, seg := range []string{"plain", "with/slash", "with~tilde", "with~/both"} {
		ok, err := roundtripPathInJSONPointer(seg)
		if err != nil {
			fmt.Printf("  ERROR seg=%q: %v\n", seg, err)
			totalFail++
			continue
		}
		if !ok {
			fmt.Printf("  FAIL seg=%q\n", seg)
			totalFail++
			continue
		}
		fmt.Printf("  PASS seg=%q -> %q\n", seg, "/"+rfc6901escape(seg))
	}

	if totalFail > 0 {
		os.Exit(1)
	}
	fmt.Println("\nALL PASS")
}
