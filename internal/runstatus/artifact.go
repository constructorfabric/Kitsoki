package runstatus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArtifactOptions controls how [RenderArtifact] assembles the self-contained
// HTML. All fields are optional except where noted; the zero value produces a
// valid artifact with an unlabelled banner.
type ArtifactOptions struct {
	// Name is the banner "fixture:" label — usually the artifact base name.
	Name string
	// Commit and Branch populate the banner provenance fields when non-empty.
	Commit string
	Branch string
	// BuiltAt is the banner "built:" timestamp. A zero value omits the field.
	BuiltAt time.Time
	// SidecarDir, when non-empty, is the directory against which event
	// prompt_file / system_prompt_file references are resolved and inlined so
	// the artifact is self-contained under file:// (where a relative fetch of
	// the sidecar is blocked). Leave empty to skip sidecar inlining (e.g. when
	// the snapshot was built in-process and already carries inline prompts).
	SidecarDir string
	// RegenComment, when non-empty, is prepended verbatim at the top of the
	// file as an HTML comment documenting how to rebuild the artifact. Build
	// one with [RegenComment].
	RegenComment string
}

// RenderArtifact assembles a self-contained runstatus HTML artifact from the
// bundled single-file SPA (index, from
// [kitsoki/internal/runstatus/web.IndexHTML]) and a snapshot JSON document. It
// is the Go port of tools/runstatus/scripts/build-artifact.mjs: it inlines any
// prompt sidecars, injects the snapshot as a <script id="kitsoki-snapshot">
// tag (read by the SPA's main.ts bootstrap), prepends an optional regenerate
// comment, and inserts a fixed provenance banner as the first child of <body>.
//
// snapshotJSON is embedded verbatim (after optional sidecar inlining) — the
// caller owns its shape; RenderArtifact only manipulates the events[].attrs
// prompt sidecar fields when opts.SidecarDir is set. The result opens with no
// server: all CSS/JS are already inlined in index by vite-plugin-singlefile,
// and the snapshot rides along in the injected tag.
func RenderArtifact(index, snapshotJSON []byte, opts ArtifactOptions) ([]byte, error) {
	if len(index) == 0 {
		return nil, fmt.Errorf("runstatus: empty SPA index (run `make build` to bundle the UI)")
	}

	snap := snapshotJSON
	if opts.SidecarDir != "" {
		snap = inlinePromptSidecars(snap, opts.SidecarDir)
	}

	// The snapshot tag the SPA's bootstrap (main.ts) looks up by id.
	tag := `<script type="application/json" id="kitsoki-snapshot">` + string(snap) + `</script>`

	html := string(index)

	// Prepend the regenerate comment, if any, at the very top — before the
	// <script> scan below so the scan still lands on the first real SPA script.
	if opts.RegenComment != "" {
		html = opts.RegenComment + "\n" + html
	}

	// Inject the snapshot tag immediately before the first <script> so it is
	// in the DOM before the SPA boots. Fall back to just-before-</body> when
	// the bundle somehow carries no script tag.
	if i := strings.Index(html, "<script"); i != -1 {
		html = html[:i] + tag + "\n" + html[i:]
	} else {
		html = strings.Replace(html, "</body>", tag+"\n</body>", 1)
	}

	// Insert the provenance banner as the first child of <body>.
	html = strings.Replace(html, "<body>", "<body>\n"+buildBanner(opts), 1)

	return []byte(html), nil
}

// buildBanner returns the fixed-position provenance banner markup (a <div>
// plus a <style> that pushes #app down so the banner doesn't overlap it).
// Mirrors build-artifact.mjs's buildBanner so artifacts render identically.
func buildBanner(opts ArtifactOptions) string {
	type kv struct{ k, v string }
	parts := []kv{{"fixture", opts.Name}}
	if opts.Commit != "" {
		parts = append(parts, kv{"commit", opts.Commit})
	}
	if opts.Branch != "" {
		parts = append(parts, kv{"branch", opts.Branch})
	}
	if !opts.BuiltAt.IsZero() {
		parts = append(parts, kv{"built", opts.BuiltAt.UTC().Format(time.RFC3339)})
	}

	var spans strings.Builder
	for _, p := range parts {
		spans.WriteString(`<span><span style="color:#334155">`)
		spans.WriteString(p.k)
		spans.WriteString(`:</span> <span style="color:#64748b">`)
		spans.WriteString(p.v)
		spans.WriteString(`</span></span>`)
	}

	return `<div id="kitsoki-artifact-banner" style="` +
		`position:fixed;top:0;left:0;right:0;z-index:9999;` +
		`background:#0f172a;border-bottom:1px solid #1e293b;` +
		`padding:0.25rem 0.75rem;font-family:ui-monospace,monospace;` +
		`font-size:0.7rem;color:#475569;display:flex;gap:1.5rem;` +
		`align-items:center;` +
		`">` + spans.String() + `</div>` +
		// push app content down so it doesn't hide under the banner
		`<style>#app{padding-top:1.75rem}</style>`
}

// RegenComment builds the HTML comment prepended to an artifact documenting
// how to rebuild it. snapshotRel and outRel are paths (typically relative to
// the repo root) of the source snapshot JSON and the output HTML.
func RegenComment(snapshotRel, outRel string) string {
	return "<!--\n" +
		"  REGENERATE THIS ARTIFACT\n" +
		"  snapshot : " + snapshotRel + "\n" +
		"\n" +
		"  Rebuild the HTML from the snapshot:\n" +
		"      kitsoki export-status --from-snapshot " + snapshotRel + " -o " + outRel + "\n" +
		"-->"
}

// inlinePromptSidecars rewrites prompt_file / system_prompt_file references in
// each event's attrs into inline prompt / system_prompt text, reading each
// sidecar relative to baseDir, then re-marshals the document compactly. A
// snapshot that does not parse as the expected shape is returned unchanged.
//
// This makes the artifact self-contained under file://, where a relative
// fetch() of a .txt sidecar is blocked by the browser so the SPA's prompt
// loader could never resolve prompt_file. Sidecars that cannot be read are
// left as references (the SPA degrades to showing the path).
func inlinePromptSidecars(snapshotJSON []byte, baseDir string) []byte {
	var doc map[string]any
	if err := json.Unmarshal(snapshotJSON, &doc); err != nil {
		return snapshotJSON
	}
	events, ok := doc["events"].([]any)
	if !ok {
		return snapshotJSON
	}

	readSidecar := func(rel string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(baseDir, rel))
		if err != nil {
			return "", false
		}
		return string(b), true
	}

	changed := false
	for _, e := range events {
		ev, ok := e.(map[string]any)
		if !ok {
			continue
		}
		attrs, ok := ev["attrs"].(map[string]any)
		if !ok {
			continue
		}
		for fileKey, textKey := range map[string]string{
			"prompt_file":        "prompt",
			"system_prompt_file": "system_prompt",
		} {
			rel, ok := attrs[fileKey].(string)
			if !ok || rel == "" {
				continue
			}
			if _, present := attrs[textKey]; present {
				continue // inline text already wins
			}
			if txt, ok := readSidecar(rel); ok {
				attrs[textKey] = txt
				delete(attrs, fileKey)
				changed = true
			}
		}
	}

	if !changed {
		return snapshotJSON
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return snapshotJSON
	}
	return out
}
