package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

func runGraphLintCmd(t *testing.T, path string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"graph", "lint", path})
	err := root.Execute()
	return stdout.String(), err
}

func TestGraphLintCmd_SeedCatalogClean(t *testing.T) {
	out, err := runGraphLintCmd(t, "../../docs/proposals/project-object-graph/seed-objects.yaml")
	if err != nil {
		t.Fatalf("graph lint on the seed catalog must exit 0, got: %v\n%s", err, out)
	}
}

func TestGraphLintCmd_BadFixtureFails(t *testing.T) {
	out, err := runGraphLintCmd(t, "../../internal/graph/testdata/lint/cycle.yaml")
	if err == nil {
		t.Fatalf("graph lint on a cyclic catalog must exit non-zero; output:\n%s", out)
	}
}

func runGraphApplyCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"graph", "apply"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

func TestGraphApplyCmd_AppliesAuthorizedChangeset(t *testing.T) {
	root := t.TempDir()
	copyDir(t, "../../internal/graph/testdata/apply/bundle", root)
	changeset := "- schema: graph/changeset/v1\n" +
		"  id: change-cli\n" +
		"  title: CLI changeset\n" +
		"  status: authorized\n" +
		"  visibility: internal\n" +
		"  operations:\n" +
		"    - kind: modified\n" +
		"      node: req-one\n" +
		"      changes:\n" +
		"        - path: [status]\n" +
		"          before: draft\n" +
		"          after: satisfied\n"
	if err := os.WriteFile(filepath.Join(root, "nodes", "changeset.yaml"), []byte(changeset), 0o644); err != nil {
		t.Fatalf("write changeset: %v", err)
	}

	out, err := runGraphApplyCmd(t, "change-cli", root)
	if err != nil {
		t.Fatalf("graph apply must exit 0 on an authorized, valid changeset: %v\n%s", err, out)
	}

	lintOut, lintErr := runGraphLintCmd(t, root)
	if lintErr != nil {
		t.Fatalf("catalog must still lint clean after apply: %v\n%s", lintErr, lintOut)
	}
}

func TestGraphApplyCmd_RejectsUnauthorized(t *testing.T) {
	root := t.TempDir()
	copyDir(t, "../../internal/graph/testdata/apply/bundle", root)
	changeset := "- schema: graph/changeset/v1\n" +
		"  id: change-cli-unauthorized\n" +
		"  title: CLI changeset\n" +
		"  status: proposed\n" +
		"  visibility: internal\n" +
		"  operations:\n" +
		"    - kind: modified\n" +
		"      node: req-one\n" +
		"      changes:\n" +
		"        - path: [status]\n" +
		"          before: draft\n" +
		"          after: satisfied\n"
	if err := os.WriteFile(filepath.Join(root, "nodes", "changeset.yaml"), []byte(changeset), 0o644); err != nil {
		t.Fatalf("write changeset: %v", err)
	}

	out, err := runGraphApplyCmd(t, "change-cli-unauthorized", root)
	if err == nil {
		t.Fatalf("graph apply must exit non-zero on an unauthorized changeset; output:\n%s", out)
	}
}

func runGraphLintCmdWithArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"graph", "lint"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

func TestGraphLintCmd_CheckIndexCleanOnRealRepo(t *testing.T) {
	out, err := runGraphLintCmdWithArgs(t,
		"../../docs/proposals/project-object-graph/seed-objects.yaml",
		"--check-index", "--proposals-dir", "../../docs/proposals")
	if err != nil {
		t.Fatalf("--check-index must be clean against the real committed README: %v\n%s", err, out)
	}
}

func TestGraphLintCmd_CheckIndexCatchesDrift(t *testing.T) {
	proposalsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(proposalsDir, "README.md"), []byte("# stale index, no matching entry\n"), 0o644); err != nil {
		t.Fatalf("write stale README: %v", err)
	}
	out, err := runGraphLintCmdWithArgs(t,
		"../../docs/proposals/project-object-graph/seed-objects.yaml",
		"--check-index", "--proposals-dir", proposalsDir)
	if err == nil {
		t.Fatalf("--check-index must fail against a README missing the generated entry; output:\n%s", out)
	}
}
