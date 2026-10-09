package kittrial

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	yaml "github.com/goccy/go-yaml"

	"kitsoki/internal/taskcase"
)

// The validation ledger is the no-wasted-LLM-spend mechanism: a git-tracked
// INDEX (`.kitsoki/qa/validation-ledger.yaml`) recording which baseline
// task cases already validated against which exact kit content. `kit trial`
// consults it before running anything — a hit at (case, staged kit tree
// hash, instance digest, oracle hash) with result pass is reported as
// skipped_already_validated with the entry as the receipt, the exact
// analogue of corpusproof's KindAlreadyGreen and dogfood-marathon's
// results.items[] resume-skip.
//
// The index is a source (tracked, survives machines and teammates); the
// traces it cites live in gitignored .artifacts/ and are corroboration,
// not the record itself — a vanished trace never invalidates an entry.
// Entries are append-only; Load dedupes last-write-wins per key so merge
// conflicts resolve trivially.

// LedgerSchema versions the ledger document.
const LedgerSchema = "kitsoki/validation-ledger/v1"

// LedgerRelPath is the ledger's path relative to a project root.
var ledgerRelPath = filepath.Join(".kitsoki", "qa", "validation-ledger.yaml")

// LedgerPath returns the ledger location for a project root.
func LedgerPath(projectRoot string) string {
	return filepath.Join(projectRoot, ledgerRelPath)
}

// OracleRef identifies WHAT was validated: the oracle kind, its ref (the
// comparator/fixture path or command), and a content hash so an edited
// oracle invalidates old entries.
type OracleRef struct {
	Kind   string `yaml:"kind" json:"kind"`
	Ref    string `yaml:"ref,omitempty" json:"ref,omitempty"`
	SHA256 string `yaml:"sha256" json:"sha256"`
}

// LedgerEntry records one validation outcome.
type LedgerEntry struct {
	CaseID string `yaml:"case_id" json:"case_id"`
	Kit    string `yaml:"kit" json:"kit"`
	// KitTreeHash pins the exact kit content the validation ran against
	// (the staged candidate's tree hash during a trial).
	KitTreeHash string `yaml:"kit_tree_hash" json:"kit_tree_hash"`
	// InstanceDigest pins the consumer overlay (the instance app.yaml
	// bytes) — a consumer-side edit invalidates prior validations even
	// when the kit itself is unchanged.
	InstanceDigest string    `yaml:"instance_digest" json:"instance_digest"`
	Oracle         OracleRef `yaml:"oracle" json:"oracle"`
	Result         string    `yaml:"result" json:"result"` // pass | fail
	Mode           string    `yaml:"mode" json:"mode"`     // replay | live
	CostUSD        float64   `yaml:"cost_usd" json:"cost_usd"`
	DurationMS     int64     `yaml:"duration_ms,omitempty" json:"duration_ms,omitempty"`
	// Trace/Receipt point at machine-local evidence under .artifacts/.
	Trace      string `yaml:"trace,omitempty" json:"trace,omitempty"`
	Receipt    string `yaml:"receipt,omitempty" json:"receipt,omitempty"`
	RecordedAt string `yaml:"recorded_at" json:"recorded_at"`
}

// Key is the ledger lookup identity.
func (e *LedgerEntry) Key() string {
	return ledgerKey(e.CaseID, e.KitTreeHash, e.InstanceDigest, e.Oracle.SHA256)
}

func ledgerKey(caseID, kitTree, instanceDigest, oracleSHA string) string {
	return caseID + "\x00" + kitTree + "\x00" + instanceDigest + "\x00" + oracleSHA
}

// Ledger is the parsed validation ledger.
type Ledger struct {
	Schema  string        `yaml:"schema"`
	Entries []LedgerEntry `yaml:"entries"`
}

// LoadLedger reads the ledger at path; a missing file returns an empty
// ledger. Duplicate keys keep the LAST entry (append-only discipline).
func LoadLedger(path string) (*Ledger, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Ledger{Schema: LedgerSchema}, nil
		}
		return nil, fmt.Errorf("kittrial: read ledger %q: %w", path, err)
	}
	l := &Ledger{}
	if err := yaml.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("kittrial: parse ledger %q: %w", path, err)
	}
	if l.Schema == "" {
		l.Schema = LedgerSchema
	}
	return l, nil
}

// SaveLedger writes the ledger with entries sorted by (case, recorded_at)
// for stable diffs.
func SaveLedger(path string, l *Ledger) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("kittrial: create %q: %w", filepath.Dir(path), err)
	}
	sort.SliceStable(l.Entries, func(i, j int) bool {
		if l.Entries[i].CaseID != l.Entries[j].CaseID {
			return l.Entries[i].CaseID < l.Entries[j].CaseID
		}
		return l.Entries[i].RecordedAt < l.Entries[j].RecordedAt
	})
	b, err := yaml.MarshalWithOptions(l, yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return fmt.Errorf("kittrial: marshal ledger: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("kittrial: write ledger %q: %w", path, err)
	}
	return nil
}

// Lookup returns the latest entry matching the key, or nil.
func (l *Ledger) Lookup(caseID, kitTree, instanceDigest, oracleSHA string) *LedgerEntry {
	key := ledgerKey(caseID, kitTree, instanceDigest, oracleSHA)
	for i := len(l.Entries) - 1; i >= 0; i-- {
		if l.Entries[i].Key() == key {
			return &l.Entries[i]
		}
	}
	return nil
}

// Append records a new outcome (last-write-wins on Lookup).
func (l *Ledger) Append(e LedgerEntry) {
	l.Entries = append(l.Entries, e)
}

// OracleSHA256 hashes a task case's oracle identity: the canonical JSON of
// the Oracle struct plus, when the oracle references a file (a flow-fixture
// comparator), that file's bytes — editing either invalidates prior ledger
// entries. paths in Oracle.Comparator resolve against projectRoot.
func OracleSHA256(o taskcase.Oracle, projectRoot string) (string, error) {
	h := sha256.New()
	b, err := json.Marshal(o)
	if err != nil {
		return "", fmt.Errorf("kittrial: hash oracle: %w", err)
	}
	h.Write(b)
	if o.Comparator != "" {
		p := o.Comparator
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectRoot, p)
		}
		if fb, err := os.ReadFile(p); err == nil {
			h.Write([]byte{0})
			h.Write(fb)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// InstanceDigest hashes the consumer instance manifests (sorted paths +
// contents) — the consumer-overlay half of a ledger key.
func InstanceDigest(instanceApps []string) (string, error) {
	sorted := append([]string(nil), instanceApps...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, p := range sorted {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("kittrial: read instance %q: %w", p, err)
		}
		h.Write([]byte(filepath.Base(p)))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
