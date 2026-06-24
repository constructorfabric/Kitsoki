// Package host — the semantic-sidecar reader (v2 annotation contract).
//
// A rendered artifact may ship a sibling `<name>.semantic.json` declaring the
// addressable elements a producer (slidey, a diagram renderer, …) baked into the
// pixels, so the web layer can offer SEMANTIC-element picks instead of only
// raw-pixel regions. The sidecar is the producer's contract; kitsoki is strictly
// producer-AGNOSTIC: it parses the envelope and round-trips each element's `ref`
// VERBATIM (never interprets it), exactly as the AnnotationAnchor semantic_element
// target does. Pairing rule: for `out.mp4`, the sidecar is `out.semantic.json`
// (the rendered file's path with its extension swapped for `.semantic.json`).
//
// The contract (design doc, semantic-sidecar):
//
//	{
//	  "plugin": "slidey",
//	  "schema_version": 1,
//	  "elements": [
//	    {"ref": "scene-3.title", "label": "Title", "selector": "[data-slidey-el=...]",
//	     "bbox": [x,y,w,h], "t_ms": 4200}
//	  ]
//	}
//
// Only `plugin` + `elements[].ref` are load-bearing for kitsoki; `label`,
// `selector`, `bbox`, `t_ms` are optional hints the picker overlays. A missing
// sidecar is NOT an error — it just means the artifact has no declared semantics
// (Found == false), the same graceful posture the FrameResolver seam takes when
// unwired.
//
// DI mirrors FrameResolver: a SemanticSidecarReader interface + a
// SemanticSidecarReaderFunc adapter, so a room/template path takes the reader by
// injection and a test wires a temp-dir fixture without the package importing a
// filesystem walk. The default DiskSemanticSidecarReader reads from disk.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SemanticSidecar is the parsed `<name>.semantic.json` envelope. It is the
// producer's declared list of addressable elements for one rendered artifact.
type SemanticSidecar struct {
	// Plugin names the producer that emitted the sidecar (e.g. "slidey"). Opaque
	// provenance for kitsoki — surfaced to the picker/template, never branched on.
	Plugin string `json:"plugin"`
	// SchemaVersion versions the sidecar shape so a producer can evolve it.
	SchemaVersion int `json:"schema_version"`
	// Elements are the addressable elements the producer declared.
	Elements []SemanticElement `json:"elements"`
}

// SemanticElement is one producer-declared addressable element. Ref is the only
// field kitsoki round-trips into a semantic_element anchor; it is OPAQUE and never
// interpreted. The rest are optional overlay/label hints for the picker.
type SemanticElement struct {
	// Ref is the producer's opaque element id, round-tripped verbatim into the
	// semantic_element anchor's `ref`. kitsoki never parses or validates it.
	Ref string `json:"ref"`
	// Label is a human-readable name for the element (picker affordance text).
	Label string `json:"label,omitempty"`
	// Selector is an optional DOM/CSS selector when the artifact is html/rrweb.
	Selector string `json:"selector,omitempty"`
	// Bbox is the element's box [x,y,w,h] in frame pixels, when the producer
	// supplied one (lets the picker draw the overlay without resolving anything).
	Bbox [4]int `json:"bbox,omitempty"`
	// TMs is the element's timestamp in ms when the artifact is timeline-based.
	TMs int `json:"t_ms,omitempty"`
}

// SemanticSidecarReader locates and parses the sibling `<name>.semantic.json` for
// a rendered artifact path. Found == false (with a nil error) means the artifact
// has no sidecar — the graceful "no declared semantics" case, distinct from a
// parse/IO error. The read side of the producer's semantic contract, injected so
// a template/room path and a test both consume the same seam.
type SemanticSidecarReader interface {
	// ReadSemanticSidecar returns the parsed sidecar for the artifact at
	// artifactPath, found=false when no sidecar exists, and a non-nil error only
	// when a sidecar is present but unreadable/malformed.
	ReadSemanticSidecar(artifactPath string) (sidecar SemanticSidecar, found bool, err error)
}

// SemanticSidecarReaderFunc adapts a plain func to SemanticSidecarReader, the
// mirror of FrameResolverFunc / http.HandlerFunc.
type SemanticSidecarReaderFunc func(artifactPath string) (SemanticSidecar, bool, error)

// ReadSemanticSidecar calls f(artifactPath).
func (f SemanticSidecarReaderFunc) ReadSemanticSidecar(artifactPath string) (SemanticSidecar, bool, error) {
	return f(artifactPath)
}

// SemanticSidecarPath returns the sibling sidecar path for a rendered artifact:
// the artifact path with its final extension replaced by `.semantic.json`. For
// `dir/out.mp4` it returns `dir/out.semantic.json`. An extension-less path simply
// gets `.semantic.json` appended.
func SemanticSidecarPath(artifactPath string) string {
	ext := filepath.Ext(artifactPath)
	return strings.TrimSuffix(artifactPath, ext) + ".semantic.json"
}

// PosterSidecarPath returns the sibling poster-frame path for a rendered
// artifact: the artifact path with its final extension replaced by
// `.poster.png`. For `dir/out.mp4` it returns `dir/out.poster.png`. A producer
// (slidey today) emits this still alongside a slideshow/video so a semantic
// overlay can float over a fixed frame rather than the un-addressable media.
func PosterSidecarPath(artifactPath string) string {
	ext := filepath.Ext(artifactPath)
	return strings.TrimSuffix(artifactPath, ext) + ".poster.png"
}

// DiskSemanticSidecarReader reads `<name>.semantic.json` from the filesystem
// beside the artifact. The zero value is usable. This is the default production
// reader; tests inject a SemanticSidecarReaderFunc over a temp dir instead.
type DiskSemanticSidecarReader struct{}

// ReadSemanticSidecar finds and parses the sibling sidecar. A non-existent
// sidecar returns (zero, false, nil) — not every artifact declares semantics.
func (DiskSemanticSidecarReader) ReadSemanticSidecar(artifactPath string) (SemanticSidecar, bool, error) {
	p := SemanticSidecarPath(strings.TrimSpace(artifactPath))
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return SemanticSidecar{}, false, nil
		}
		return SemanticSidecar{}, false, fmt.Errorf("read semantic sidecar %q: %w", p, err)
	}
	var sc SemanticSidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return SemanticSidecar{}, false, fmt.Errorf("parse semantic sidecar %q: %w", p, err)
	}
	return sc, true, nil
}

// semanticSidecarReaderKey is the unexported context key for the injected reader.
type semanticSidecarReaderKey struct{}

// WithSemanticSidecarReader injects r so a room/template/web path can offer
// semantic-element picks. Nil is a safe no-op (no reader wired ⇒ no semantic
// picks offered), the same posture WithFrameResolver takes.
func WithSemanticSidecarReader(ctx context.Context, r SemanticSidecarReader) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, semanticSidecarReaderKey{}, r)
}

// SemanticSidecarReaderFromCtx returns the reader previously injected with
// WithSemanticSidecarReader, or nil if none was injected.
func SemanticSidecarReaderFromCtx(ctx context.Context) SemanticSidecarReader {
	r, _ := ctx.Value(semanticSidecarReaderKey{}).(SemanticSidecarReader)
	return r
}

// asMap renders the sidecar as a template-/web-facing map (the elements a picker
// offers). Round-trips `ref` verbatim. Returns nil for an empty sidecar so an
// artifact with no declared semantics surfaces nothing.
func (sc SemanticSidecar) asMap() map[string]any {
	if len(sc.Elements) == 0 && sc.Plugin == "" {
		return nil
	}
	els := make([]map[string]any, 0, len(sc.Elements))
	for _, e := range sc.Elements {
		m := map[string]any{"ref": e.Ref}
		if e.Label != "" {
			m["label"] = e.Label
		}
		if e.Selector != "" {
			m["selector"] = e.Selector
		}
		if e.Bbox != ([4]int{}) {
			m["bbox"] = bboxSlice(e.Bbox)
		}
		if e.TMs != 0 {
			m["t_ms"] = e.TMs
		}
		els = append(els, m)
	}
	return map[string]any{
		"plugin":         sc.Plugin,
		"schema_version": sc.SchemaVersion,
		"elements":       els,
	}
}

// AsMap exposes the template-/web-facing rendering of the sidecar (see asMap).
func (sc SemanticSidecar) AsMap() map[string]any { return sc.asMap() }
