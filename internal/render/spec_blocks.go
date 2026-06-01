package render

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Specialization convention: a {% block %} whose name carries the `spec_`
// prefix marks a *specialization surface* — a section whose default is
// provisional. An empty body is a hole the story expects a project to fill; a
// non-empty body is a working default a project may refine in an overlay.
// Non-spec_ blocks are structural and are not specialization targets.
//
// pongo2 exposes no public API to enumerate a parsed template's blocks, so the
// surface is discovered by scanning prompt source text. The scan is the
// contract three consumers read: authors via `kitsoki prompts spec`, the
// improvement/meta agents' system prompts, and the trace (which spec_ blocks
// defaulted vs. were overridden on a run). See docs/stories/prompts.md.

// SpecBlockPrefix is the block-name marker for a specialization surface.
const SpecBlockPrefix = "spec_"

// SpecBlock is one specialization-surface block discovered in a prompt file.
type SpecBlock struct {
	// Name is the full block name including the spec_ prefix.
	Name string
	// File is the prompt file the block was found in.
	File string
	// Hole is true when the default body is empty (a project MUST fill it);
	// false when the block ships a provisional default (a project MAY refine).
	Hole bool
	// Default is the trimmed default body (empty for a hole). Truncation is
	// the caller's concern; this is the verbatim trimmed text.
	Default string
}

// (?s) dotall so a multi-line block body is captured; the body is non-greedy
// so adjacent blocks in one file don't merge. An optional `{% endblock name %}`
// trailing name is tolerated.
var specBlockRe = regexp.MustCompile(`(?s){%-?\s*block\s+(` + SpecBlockPrefix + `[A-Za-z0-9_]+)\s*-?%}(.*?){%-?\s*endblock(?:\s+[A-Za-z0-9_]+)?\s*-?%}`)

// EnumerateSpecBlocksInSource scans one prompt source string for spec_ blocks.
// file is used only to label the results.
func EnumerateSpecBlocksInSource(file, src string) []SpecBlock {
	matches := specBlockRe.FindAllStringSubmatch(src, -1)
	out := make([]SpecBlock, 0, len(matches))
	for _, m := range matches {
		body := strings.TrimSpace(m[2])
		out = append(out, SpecBlock{
			Name:    m[1],
			File:    file,
			Hole:    body == "",
			Default: body,
		})
	}
	return out
}

// anyBlockRe matches any {% block NAME %} declaration (spec_ or not) — used to
// discover which blocks an overlay file overrides.
var anyBlockRe = regexp.MustCompile(`{%-?\s*block\s+([A-Za-z0-9_]+)\s*-?%}`)

// extendsTargetRe captures the target of a {% extends "X" %} tag.
var extendsTargetRe = regexp.MustCompile(`{%-?\s*extends\s+"([^"]+)"\s*-?%}`)

// blockNamesInSource returns the set of block names declared in src.
func blockNamesInSource(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range anyBlockRe.FindAllStringSubmatch(src, -1) {
		out[m[1]] = true
	}
	return out
}

// extendsTargetInSource returns the {% extends %} target of src, or "".
func extendsTargetInSource(src string) string {
	if m := extendsTargetRe.FindStringSubmatch(src); m != nil {
		return m[1]
	}
	return ""
}

// EnumerateSpecBlocks scans the given prompt files for spec_ blocks and returns
// them sorted by (file, name) for deterministic output. A file that cannot be
// read is reported as an error; remaining files are still scanned.
func EnumerateSpecBlocks(files []string) ([]SpecBlock, error) {
	var all []SpecBlock
	var firstErr error
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("enumerate spec blocks: read %q: %w", f, err)
			}
			continue
		}
		all = append(all, EnumerateSpecBlocksInSource(f, string(raw))...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].File != all[j].File {
			return all[i].File < all[j].File
		}
		return all[i].Name < all[j].Name
	})
	return all, firstErr
}
