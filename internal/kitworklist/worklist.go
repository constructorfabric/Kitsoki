// Package kitworklist implements the migration worklist a kit trial leaves
// behind (`.kitsoki/kit-update/<name>/worklist.yaml`): the addressable list
// of everything standing between a staged kit candidate and `kit accept`.
//
// The worklist is the S7 answer to "a failed check must become actionable
// work, not a wall of test output" (docs/proposals/kits.md, lifecycle).
// Three properties carry that:
//
//   - Stable item ids. An id is derived from (kind, subject, normalized
//     detail) with volatile fragments (absolute paths, line/column numbers,
//     digits) stripped, so the same failure keeps the same id across
//     re-trials even when messages shift cosmetically.
//   - Idempotent re-check. Each trial rebuilds the list fresh and merges
//     against the previous file: items no longer produced flip to
//     `resolved` (kept, so the operator sees progress); items still
//     produced carry the operator's `accepted` status and notes forward.
//   - Audited waivers. An operator may set `status: accepted` on an error
//     item; it then stops blocking accept, but stays in the file and is
//     digest-pinned into the trial receipt.
package kitworklist

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "github.com/goccy/go-yaml"

	"kitsoki/internal/kitstage"
)

// Schema versions the worklist document.
const Schema = "kitsoki/kit-migration-worklist/v1"

// FileName is the worklist's file name inside the kit's update workdir.
const FileName = "worklist.yaml"

// Item statuses.
const (
	StatusOpen     = "open"
	StatusResolved = "resolved"
	StatusAccepted = "accepted" // operator waiver — audited, not silent
)

// Severities.
const (
	SeverityError = "error"
	SeverityWarn  = "warn"
	SeverityInfo  = "info"
)

// Item is one addressable piece of migration work.
type Item struct {
	// ID is stable across re-trials: <kind>/<subject-base>/<hash12>.
	ID       string `yaml:"id"`
	Kind     string `yaml:"kind"`     // contract | flow | load | rename | cassette_miss | acceptance_violation | stale_baseline_leg | onboarding | case
	Severity string `yaml:"severity"` // error | warn | info
	Status   string `yaml:"status"`   // open | resolved | accepted
	// Subject names what the item is about (a fixture path, an import
	// alias, a check id, a case id).
	Subject string `yaml:"subject"`
	Detail  string `yaml:"detail"`
	// SuggestedAction is the concrete edit when one is mechanically known
	// (e.g. a compat.renamed hint's exact rename).
	SuggestedAction string `yaml:"suggested_action,omitempty"`
	// Evidence points at the artifact that produced the item (gate report,
	// fixture file, trace path).
	Evidence string `yaml:"evidence,omitempty"`
	// Notes is operator-owned free text, carried forward across re-trials.
	Notes string `yaml:"notes,omitempty"`
}

// File is the persisted worklist.
type File struct {
	Schema      string            `yaml:"schema"`
	Kit         string            `yaml:"kit"`
	From        kitstage.Snapshot `yaml:"from"`
	To          kitstage.Snapshot `yaml:"to"`
	GeneratedAt string            `yaml:"generated_at,omitempty"`
	Items       []Item            `yaml:"items"`
}

// Path returns the worklist path inside a kit's update workdir.
func Path(projectRoot, kitName string) string {
	return filepath.Join(kitstage.WorkDir(projectRoot, kitName), FileName)
}

// ItemID derives the stable id for (kind, subject, detail). subject
// contributes only its base name (paths move between machines); detail is
// normalized by dropping digits (line/column/counter churn), lowercasing,
// collapsing whitespace, sorting "; "-separated fragments (producers that
// assemble a detail from map iteration emit fragments in varying order —
// the id must not flip with them), and truncating — cosmetically shifting
// messages keep their identity, genuinely different failures diverge.
func ItemID(kind, subject, detail string) string {
	base := filepath.Base(strings.TrimSpace(subject))
	h := sha256.Sum256([]byte(kind + "\x00" + base + "\x00" + normalizeDetail(detail)))
	return fmt.Sprintf("%s/%s/%s", kind, base, hex.EncodeToString(h[:])[:12])
}

func normalizeDetail(detail string) string {
	// Order-insensitive over "; "-joined fragments: a multi-failure detail
	// hashes identically regardless of the order its fragments arrived in.
	if parts := strings.Split(detail, "; "); len(parts) > 1 {
		sort.Strings(parts)
		detail = strings.Join(parts, "; ")
	}
	return normalizeDetailText(detail)
}

func normalizeDetailText(detail string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(strings.TrimSpace(detail)) {
		switch {
		case r >= '0' && r <= '9':
			continue
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	s := b.String()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// NewItem builds an open Item with its derived id.
func NewItem(kind, severity, subject, detail string) Item {
	return Item{
		ID:       ItemID(kind, subject, detail),
		Kind:     kind,
		Severity: severity,
		Status:   StatusOpen,
		Subject:  subject,
		Detail:   detail,
	}
}

// Merge reconciles freshly produced items with the previous worklist:
//
//   - fresh item with a previous `accepted` counterpart keeps the waiver
//     (and notes);
//   - fresh item with any other previous counterpart keeps notes, status
//     open;
//   - previous non-resolved item that is NOT reproduced flips to resolved
//     and is kept (progress stays visible);
//   - previous resolved items are kept as-is unless reproduced, in which
//     case they reopen (regression).
//
// The result is sorted by (severity rank, kind, subject, id) for stable
// files.
func Merge(fresh []Item, prev *File) []Item {
	prevByID := map[string]Item{}
	if prev != nil {
		for _, it := range prev.Items {
			prevByID[it.ID] = it
		}
	}
	seen := map[string]bool{}
	var out []Item
	for _, it := range fresh {
		if seen[it.ID] {
			continue // duplicate finding within one trial collapses
		}
		seen[it.ID] = true
		if old, ok := prevByID[it.ID]; ok {
			it.Notes = old.Notes
			if old.Status == StatusAccepted {
				it.Status = StatusAccepted
			}
		}
		out = append(out, it)
	}
	for _, it := range prevItemsSorted(prev) {
		if seen[it.ID] {
			continue
		}
		if it.Status != StatusResolved {
			it.Status = StatusResolved
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := severityRank(out[i].Severity), severityRank(out[j].Severity); a != b {
			return a < b
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func prevItemsSorted(prev *File) []Item {
	if prev == nil {
		return nil
	}
	items := append([]Item(nil), prev.Items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func severityRank(s string) int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarn:
		return 1
	default:
		return 2
	}
}

// OpenErrors counts items that block `kit accept`: open items with error
// severity (accepted waivers and resolved items don't block).
func (f *File) OpenErrors() int {
	if f == nil {
		return 0
	}
	n := 0
	for _, it := range f.Items {
		if it.Status == StatusOpen && it.Severity == SeverityError {
			n++
		}
	}
	return n
}

// Counts returns (open, resolved, accepted) item counts for report lines.
func (f *File) Counts() (open, resolved, accepted int) {
	if f == nil {
		return 0, 0, 0
	}
	for _, it := range f.Items {
		switch it.Status {
		case StatusOpen:
			open++
		case StatusResolved:
			resolved++
		case StatusAccepted:
			accepted++
		}
	}
	return open, resolved, accepted
}

// Load reads a worklist; a missing file returns (nil, nil).
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kitworklist: read %q: %w", path, err)
	}
	f := &File{}
	if err := yaml.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("kitworklist: parse %q: %w", path, err)
	}
	return f, nil
}

// Save writes f, creating the workdir if needed.
func Save(path string, f *File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("kitworklist: create %q: %w", filepath.Dir(path), err)
	}
	b, err := yaml.MarshalWithOptions(f, yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return fmt.Errorf("kitworklist: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("kitworklist: write %q: %w", path, err)
	}
	return nil
}
