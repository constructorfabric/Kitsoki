package bugdeck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Evidence asset filenames the agent keeps per report (mirrors the names the web
// bug-report flow writes under .artifacts/bug-reports/<id>/).
const (
	evidenceRRWeb = "rrweb.json"
	evidenceHAR   = "har.json"
)

// EvidenceStore is the agent's local home for bug-report evidence. The bug-file
// flow deposits each report's assets under <Dir>/<id>/, so the deck reactor
// reads them straight off the agent — no GitHub round-trip, since the assets
// already live here. This is the same surface that also serves the raw assets.
type EvidenceStore struct {
	Dir string
}

// NewEvidenceStore returns a store rooted at dir (created if needed).
func NewEvidenceStore(dir string) (*EvidenceStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("evidence store: dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("evidence store: mkdir: %w", err)
	}
	return &EvidenceStore{Dir: dir}, nil
}

// Load reads the rrweb.json + har.json an agent holds for report id into an
// [Evidence] (each is optional — a missing file is left nil). It errors
// only when neither asset is present, since a deck needs at least one.
func (s *EvidenceStore) Load(id string) (Evidence, error) {
	dir := filepath.Join(s.Dir, SanitizeDeckID(id))
	ev := Evidence{}
	if b, err := os.ReadFile(filepath.Join(dir, evidenceRRWeb)); err == nil {
		ev.RRWeb = b
	}
	if b, err := os.ReadFile(filepath.Join(dir, evidenceHAR)); err == nil {
		ev.HAR = b
	}
	if len(ev.RRWeb) == 0 && len(ev.HAR) == 0 {
		return Evidence{}, fmt.Errorf("evidence store: no rrweb.json or har.json for %q under %s", id, dir)
	}
	return ev, nil
}

// Save writes a report's evidence under <Dir>/<id>/ (used by the bug-file flow
// and by tests to seed the store). Empty assets are skipped.
func (s *EvidenceStore) Save(id string, rrweb, har []byte) error {
	dir := filepath.Join(s.Dir, SanitizeDeckID(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("evidence store: mkdir: %w", err)
	}
	if len(rrweb) > 0 {
		if err := os.WriteFile(filepath.Join(dir, evidenceRRWeb), rrweb, 0o644); err != nil {
			return err
		}
	}
	if len(har) > 0 {
		if err := os.WriteFile(filepath.Join(dir, evidenceHAR), har, 0o644); err != nil {
			return err
		}
	}
	return nil
}
