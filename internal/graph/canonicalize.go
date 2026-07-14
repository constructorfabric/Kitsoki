package graph

// canonicalize.go — the out-of-band fix for checkCanonical (guards.go)
// rejections. Every lifecycle verb refuses to write through a catalog file
// whose bytes differ from marshalYAMLNode's re-serialization when the file
// contains a block scalar (the yaml.v3 reflow hazard), and until now the
// only remedy was a manual no-op changeset round-trip (POG docs/plan.md §9).
// Canonicalize performs exactly that re-serialization deliberately, once,
// for every file checkCanonical would flag — after which the whole
// propose/authorize/apply lifecycle passes the guard again.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CanonicalizeResult reports what Canonicalize did: the files it rewrote
// into canonical re-marshal form, and the files it could not write (e.g. a
// chmod-locked catalog) — surfaced as skips rather than a hard error so one
// read-only file doesn't abort canonicalizing its writable siblings.
type CanonicalizeResult struct {
	ChangedFiles []string
	Skipped      []string // "path: reason" entries for files that needed canonicalization but couldn't be written
}

// Canonicalize rewrites every file backing the catalog at rootPath that
// checkCanonical would flag — a block-scalar-bearing file whose bytes
// differ from marshalYAMLNode's output — into that canonical form, via an
// atomic same-directory temp+rename preserving the original file mode.
// Files checkCanonical exempts (no block scalars, or already canonical)
// are left byte-for-byte untouched. Idempotent: a second run changes
// nothing. With dryRun, ChangedFiles reports what WOULD be rewritten and
// nothing touches disk.
func Canonicalize(rootPath string, dryRun bool) (*CanonicalizeResult, error) {
	cat, err := LoadCatalog(rootPath)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	files, err := catalogFiles(cat)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: enumerate catalog files: %w", err)
	}
	res := &CanonicalizeResult{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			// Same stance as checkCanonical: LoadCatalog already succeeded,
			// so a re-parse quirk here is not this tool's concern.
			continue
		}
		if !hasBlockScalar(&doc) {
			continue
		}
		out, err := marshalYAMLNode(&doc)
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: re-marshal failed: %v", f, err))
			continue
		}
		if bytes.Equal(raw, out) {
			continue
		}
		if !dryRun {
			if err := writeFileAtomic(f, out); err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %v", f, err))
				continue
			}
		}
		res.ChangedFiles = append(res.ChangedFiles, f)
	}
	return res, nil
}

// writeFileAtomic replaces path's content via a same-directory temp file +
// rename, preserving the original mode. A read-only target is refused up
// front the same way a direct write would fail, without ever leaving a
// half-written catalog file behind.
func writeFileAtomic(path string, content []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode&0o200 == 0 {
		return fmt.Errorf("file is read-only (mode %v)", info.Mode())
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".canon-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// RejectDetail is the structured form of a lifecycle-verb reject reason —
// the classification the MCP surface has always had (graphsrv's
// classifyWriteReject) but the bare graph.* RPC carrier lacked, leaving
// browser clients to regex raw strings. Code is one of
// "needs_canonicalization" (repo-level: fix with Canonicalize, not by
// editing the changeset), "conflict" (a stale Before guard or concurrent
// write), or "validation" (everything else).
type RejectDetail struct {
	Code    string `json:"code"`
	File    string `json:"file,omitempty"`
	Message string `json:"message"`
}

// ClassifyRejectReason maps one raw reject-reason string to its structured
// form. NEEDS_CANONICALIZATION reasons carry the offending file path as the
// second colon-delimited segment (see checkCanonical's fmt.Sprintf shapes).
func ClassifyRejectReason(reason string) RejectDetail {
	switch {
	case strings.HasPrefix(reason, "NEEDS_CANONICALIZATION:"):
		d := RejectDetail{Code: "needs_canonicalization", Message: reason}
		// "<file>: <detail>" — the file segment ends at the first ": ".
		// checkCanonical's enumerate-failure variant has no file path;
		// leaving File empty is correct there.
		rest := strings.TrimLeft(strings.TrimPrefix(reason, "NEEDS_CANONICALIZATION:"), " ")
		if i := strings.Index(rest, ": "); i > 0 {
			candidate := rest[:i]
			if ext := filepath.Ext(candidate); ext == ".yaml" || ext == ".yml" {
				d.File = candidate
			}
		}
		return d
	case strings.HasPrefix(reason, "CONFLICT:"):
		return RejectDetail{Code: "conflict", Message: reason}
	default:
		return RejectDetail{Code: "validation", Message: reason}
	}
}
