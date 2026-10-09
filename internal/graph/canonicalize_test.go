package graph

import (
	"os"
	"strings"
	"testing"

	"kitsoki/internal/clock"
)

// Canonicalize is the remedy for exactly the state blockScalarFixture
// (hazard_guards_test.go) puts a catalog in: hand-wrapped block scalar,
// checkCanonical rejecting every lifecycle verb.

func TestCanonicalize_RewritesFlaggedFileAndUnblocksLifecycle(t *testing.T) {
	root := writeBlockScalarFixture(t)

	res, err := Canonicalize(root, false)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if len(res.ChangedFiles) != 1 || res.ChangedFiles[0] != root {
		t.Fatalf("expected exactly the fixture file rewritten, got: %+v", res)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("unexpected skips: %v", res.Skipped)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog after canonicalize: %v", err)
	}
	if reasons := checkCanonical(cat); len(reasons) > 0 {
		t.Fatalf("checkCanonical still rejects after Canonicalize: %v", reasons)
	}
	// Content survived the rewrite (only formatting changed).
	if _, ok := cat.Nodes["req-block"]; !ok {
		t.Fatal("req-block node lost during canonicalize")
	}

	// The lifecycle verb that the guard blocked now goes through.
	pres, err := Propose(root, ProposeInput{
		Title: "Unblocked after canonicalize",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-unblocked", "title": "Lands now", "status": "draft", "visibility": "internal"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose after canonicalize: %v", err)
	}
	for _, r := range pres.RejectReasons {
		if strings.Contains(r, "NEEDS_CANONICALIZATION") {
			t.Fatalf("Propose still rejected on canonicality after Canonicalize: %v", pres.RejectReasons)
		}
	}
}

func TestCanonicalize_IdempotentSecondRunChangesNothing(t *testing.T) {
	root := writeBlockScalarFixture(t)
	if _, err := Canonicalize(root, false); err != nil {
		t.Fatalf("first Canonicalize: %v", err)
	}
	after1, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Canonicalize(root, false)
	if err != nil {
		t.Fatalf("second Canonicalize: %v", err)
	}
	if len(res.ChangedFiles) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("second run should be a no-op, got: %+v", res)
	}
	after2, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(after1) != string(after2) {
		t.Fatal("second run changed bytes")
	}
}

func TestCanonicalize_DryRunReportsWithoutWriting(t *testing.T) {
	root := writeBlockScalarFixture(t)
	before, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Canonicalize(root, true)
	if err != nil {
		t.Fatalf("Canonicalize dry-run: %v", err)
	}
	if len(res.ChangedFiles) != 1 {
		t.Fatalf("dry-run should report the flagged file, got: %+v", res)
	}
	after, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("dry-run touched disk")
	}
}

func TestCanonicalize_NoBlockScalarFileUntouched(t *testing.T) {
	root := copySingleFileFixture(t)
	before, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Canonicalize(root, false)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if len(res.ChangedFiles) != 0 {
		t.Fatalf("block-scalar-free fixture must not be rewritten, got: %+v", res)
	}
	after, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("file bytes changed despite no block scalars")
	}
}

func TestCanonicalize_ReadOnlyFileReportedAsSkip(t *testing.T) {
	root := writeBlockScalarFixture(t)
	if err := os.Chmod(root, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o644) })
	res, err := Canonicalize(root, false)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if len(res.ChangedFiles) != 0 {
		t.Fatalf("read-only file must not be reported as changed: %+v", res)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "read-only") {
		t.Fatalf("expected a read-only skip entry, got: %+v", res.Skipped)
	}
}

func TestClassifyRejectReason(t *testing.T) {
	cases := []struct {
		reason   string
		code     string
		wantFile string
	}{
		{
			reason:   "NEEDS_CANONICALIZATION: /abs/path/pog/catalog.yaml: file is not in canonical re-marshal form — yaml.v3 would reflow a hand-wrapped block scalar in this file on next write; canonicalize it out-of-band before proposing/applying a changeset that touches this file",
			code:     "needs_canonicalization",
			wantFile: "/abs/path/pog/catalog.yaml",
		},
		{
			reason: "NEEDS_CANONICALIZATION: failed to enumerate catalog files: boom",
			code:   "needs_canonicalization",
			// "failed to enumerate..." is not a file path — File stays empty.
			wantFile: "",
		},
		{
			reason: "CONFLICT: catalog content changed concurrently",
			code:   "conflict",
		},
		{
			reason: "Before guard mismatch on req-one.status",
			code:   "validation",
		},
	}
	for _, c := range cases {
		d := ClassifyRejectReason(c.reason)
		if d.Code != c.code {
			t.Errorf("%q: code = %q, want %q", c.reason, d.Code, c.code)
		}
		if d.File != c.wantFile {
			t.Errorf("%q: file = %q, want %q", c.reason, d.File, c.wantFile)
		}
		if d.Message != c.reason {
			t.Errorf("%q: message must carry the raw reason verbatim", c.reason)
		}
	}
}
