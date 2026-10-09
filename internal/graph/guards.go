package graph

// guards.go implements the P1 hazard guards (graph-mcp plan §3.4 red-team
// amendments #2-#4) shared by Propose/Authorize/Withdraw (via
// commitScratchOperations, propose.go) and Apply (apply.go): file-level CAS
// around the load->scratch->copy-back window, a lint-diff gate that only
// blocks on NEW error-severity issues (not pre-existing catalog dirt), and
// a canonicality pre-check that refuses to write through a file yaml.v3
// would silently reflow on its next touch-and-remarshal.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// casMaxAttempts bounds the load->scratch->copy-back retry loop (hazard
// guard #2): a concurrent writer's edit to the real catalog, detected via
// content-digest mismatch immediately before copy-back, causes the whole
// operation (fresh LoadCatalog, re-validate, rebuild scratch) to be retried
// up to this many times before giving up with a CONFLICT reject reason.
// This also covers cs-<n> id allocation races: a retry re-runs
// nextChangesetID against a freshly reloaded catalog.
const casMaxAttempts = 3

// errCASConflict is the sentinel commitWithCAS/commitScratchOperations
// return when the real catalog's on-disk content no longer matches the
// digest captured at LoadCatalog time. Callers (Propose/Authorize/
// Withdraw/Apply) check errors.Is(err, errCASConflict) to distinguish "retry
// the whole operation" from a genuine I/O error.
var errCASConflict = errors.New("graph: catalog content changed concurrently (CAS conflict)")

// catalogFiles enumerates the real on-disk files backing cat: the single
// file for a single-file catalog, or the bundle's catalog.yaml/
// type_registry.yaml/sources.yaml plus every file in cat.NodeFile for a
// bundle — the same file set copyCatalogTree stages into a scratch
// directory. Returned sorted for deterministic hashing.
func catalogFiles(cat *Catalog) ([]string, error) {
	info, err := os.Stat(cat.RootPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{cat.RootPath}, nil
	}
	files := map[string]bool{}
	for _, f := range cat.NodeFile {
		files[f] = true
	}
	for _, extra := range []string{"catalog.yaml", "type_registry.yaml", "sources.yaml"} {
		p := filepath.Join(cat.RootPath, extra)
		if _, err := os.Stat(p); err == nil {
			files[p] = true
		}
	}
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// computeContentDigest hashes the current on-disk content of every file
// catalogFiles enumerates for cat — a deterministic fingerprint of "the
// real catalog as it stands right now", used both to stamp Catalog.
// ContentDigest at load time and to re-check immediately before copy-back
// (hazard guard #2). A file that has disappeared since load contributes a
// distinct "MISSING:" marker rather than being silently skipped, so a
// concurrent delete still changes the digest.
func computeContentDigest(cat *Catalog) (string, error) {
	files, err := catalogFiles(cat)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(h, "MISSING:%s\n", f)
				continue
			}
			return "", err
		}
		fmt.Fprintf(h, "%s:%d\n", f, len(raw))
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// casTestHook, when non-nil, is invoked by commitWithCAS immediately before
// it re-reads the real catalog's on-disk digest — a test-only seam letting
// tests deterministically simulate a concurrent writer landing in the
// load->scratch->copy-back window (e.g. by mutating rootPath's file from
// the hook) without real goroutine racing/flakiness. Nil in production.
var casTestHook func()

// commitWithCAS re-verifies the real catalog's content digest immediately
// before copying scratchRoot's changed files over it. cat.ContentDigest was
// captured by LoadCatalog at the top of this operation; if the live files
// no longer match, a concurrent writer landed in the load->scratch->
// copy-back window and this returns errCASConflict instead of clobbering
// the concurrent change — the caller is expected to reload and retry the
// whole operation (bounded by casMaxAttempts).
func commitWithCAS(cat *Catalog, scratchRoot string, changedFiles []string) error {
	if casTestHook != nil {
		casTestHook()
	}
	current, err := computeContentDigest(cat)
	if err != nil {
		return err
	}
	if current != cat.ContentDigest {
		return errCASConflict
	}
	return commitChangedFiles(cat.RootPath, scratchRoot, changedFiles)
}

// newErrorIssues is the lint-diff gate (hazard guard #3): baseline is
// ErrorIssues(Lint(cat)) computed on the just-loaded, pre-write catalog;
// candidate is ErrorIssues(Lint(candidate)) computed on the post-operation
// scratch catalog. Only issues present in candidate but NOT in baseline —
// i.e. genuinely NEW error-severity problems this write would introduce —
// are returned; pre-existing dirt in baseline never blocks a write. Issue
// identity is (Node, Kind, Message), the same fields graph_handlers.go
// already serializes for every LintIssue.
func newErrorIssues(baseline, candidate []LintIssue) []LintIssue {
	seen := make(map[lintIssueKey]int, len(baseline))
	for _, iss := range baseline {
		seen[lintIssueKeyOf(iss)]++
	}
	var fresh []LintIssue
	for _, iss := range candidate {
		key := lintIssueKeyOf(iss)
		if seen[key] > 0 {
			seen[key]--
			continue
		}
		fresh = append(fresh, iss)
	}
	return fresh
}

type lintIssueKey struct {
	Node    NodeID
	Kind    string
	Message string
}

func lintIssueKeyOf(iss LintIssue) lintIssueKey {
	return lintIssueKey{Node: iss.Node, Kind: iss.Kind, Message: iss.Message}
}

// checkCanonical verifies that every file backing cat is already in the
// exact byte-for-byte form yaml.v3 would re-marshal it to (marshalYAMLNode's
// 2-space-indent convention — the same re-serialization applyOperations
// performs on any touched file) — but ONLY for files that actually contain
// a block scalar (literal `|` or folded `>` style), the specific hazard
// docs/plan.md's canonicality fixture exists to catch ("yaml.v3 re-marshal
// reflows hand-wrapped block scalars"). A file with no block scalars is
// exempt from the byte-compare even if it differs cosmetically from
// yaml.v3's own serialization conventions (flow-mapping padding, quote
// style, key ordering that Go's map iteration doesn't preserve, ...) —
// those differences are not the hazard this guard exists to prevent, and a
// literal whole-file byte-compare would false-block on virtually every
// hand-authored catalog including ones with zero block scalars, defeating
// "before any catalog write" without actually protecting anything a block
// scalar's line-wrap width doesn't already cover.
func checkCanonical(cat *Catalog) []string {
	files, err := catalogFiles(cat)
	if err != nil {
		return []string{fmt.Sprintf("NEEDS_CANONICALIZATION: failed to enumerate catalog files: %v", err)}
	}
	var reasons []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("NEEDS_CANONICALIZATION: %s: %v", f, err))
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			// Not this guard's concern — LoadCatalog already succeeded, so
			// a re-parse failure here would be surprising; skip rather than
			// false-block on an unrelated parse quirk.
			continue
		}
		if !hasBlockScalar(&doc) {
			continue
		}
		out, err := marshalYAMLNode(&doc)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("NEEDS_CANONICALIZATION: %s: re-marshal failed: %v", f, err))
			continue
		}
		if !bytes.Equal(raw, out) {
			reasons = append(reasons, fmt.Sprintf("NEEDS_CANONICALIZATION: %s: file is not in canonical re-marshal form — yaml.v3 would reflow a hand-wrapped block scalar in this file on next write; canonicalize it out-of-band before proposing/applying a changeset that touches this file", f))
		}
	}
	return reasons
}

// hasBlockScalar reports whether n (or any descendant) is a literal (`|`)
// or folded (`>`) style scalar node — the class of YAML formatting whose
// re-marshal can silently change meaning-bearing line-wrap width (the
// hazard checkCanonical exists to catch), as opposed to purely cosmetic
// flow-style/quoting differences elsewhere in a hand-authored file.
func hasBlockScalar(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode && n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return true
	}
	for _, c := range n.Content {
		if hasBlockScalar(c) {
			return true
		}
	}
	return false
}
