package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runGraphMaterializeCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"graph", "materialize"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

// copyMaterializeTestdataFixture copies internal/materialize/testdata/ (the
// fixture catalog + story) into a fresh t.TempDir() and returns the copy's
// root. graph materialize's write-back (internal/materialize/writeback.go,
// slice 6) edits the catalog file in place and writes an artifact under
// RepoRoot/.artifacts/ — a test that drives a job to completion must run
// against a disposable copy, not the checked-in fixture, or every run would
// leave it mutated with the previous run's job id/timestamp and a stray
// .artifacts/ directory.
func copyMaterializeTestdataFixture(t *testing.T) string {
	t.Helper()
	const src = "../../internal/materialize/testdata"
	dstRoot := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy materialize testdata fixture: %v", err)
	}
	return dstRoot
}

func TestGraphMaterializeCmd_DrivesFixtureStoryToDone(t *testing.T) {
	fixtureRoot := copyMaterializeTestdataFixture(t)
	out, err := runGraphMaterializeCmd(t,
		filepath.Join(fixtureRoot, "catalog.yaml"),
		"wi-ready",
		"--repo-root", fixtureRoot,
		"--param", "depth=3",
		"--param", "audience=public",
	)
	if err != nil {
		t.Fatalf("graph materialize on a ready node must exit 0, got: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "stages: gather -> draft -> done") {
		t.Errorf("output missing stage sequence:\n%s", out)
	}
	if !strings.Contains(out, "done, artifact .artifacts/wi-ready/brief.md") {
		t.Errorf("output missing completed artifact path:\n%s", out)
	}

	// Write-back (slice 6): artifact bytes on disk + catalog evidence/
	// materialization: block, both under the disposable fixture copy.
	if _, statErr := os.Stat(filepath.Join(fixtureRoot, ".artifacts", "wi-ready", "brief.md")); statErr != nil {
		t.Errorf("expected artifact written to disk: %v", statErr)
	}
	catalogRaw, readErr := os.ReadFile(filepath.Join(fixtureRoot, "catalog.yaml"))
	if readErr != nil {
		t.Fatalf("read written-back catalog: %v", readErr)
	}
	if !strings.Contains(string(catalogRaw), "materialization:") {
		t.Errorf("catalog missing written-back materialization: block:\n%s", catalogRaw)
	}
}

func TestGraphMaterializeCmd_RejectsUnmetGates(t *testing.T) {
	out, err := runGraphMaterializeCmd(t,
		"../../internal/materialize/testdata/catalog.yaml",
		"wi-not-ready",
		"--repo-root", "../../internal/materialize/testdata",
	)
	if err == nil {
		t.Fatalf("graph materialize on a node with unmet gates must exit non-zero; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "gate") || !strings.Contains(err.Error(), "owner") {
		t.Errorf("error should list unmet gate fields (gate, owner); got: %v", err)
	}
}

func TestGraphMaterializeCmd_UnknownType_Errors(t *testing.T) {
	out, err := runGraphMaterializeCmd(t,
		"../../internal/materialize/testdata/catalog.yaml",
		"plain-one",
		"--repo-root", "../../internal/materialize/testdata",
	)
	if err == nil {
		t.Fatalf("graph materialize on a node whose type declares no materialize binding must exit non-zero; output:\n%s", out)
	}
}

func TestGraphMaterializeCmd_BadParam_Errors(t *testing.T) {
	_, err := runGraphMaterializeCmd(t,
		"../../internal/materialize/testdata/catalog.yaml",
		"wi-ready",
		"--repo-root", "../../internal/materialize/testdata",
		"--param", "no-equals-sign",
	)
	if err == nil {
		t.Fatal("graph materialize with a malformed --param must exit non-zero")
	}
}
