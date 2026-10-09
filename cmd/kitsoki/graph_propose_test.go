package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/graph"
)

func runGraphProposeCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"graph", "propose"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

const proposeInputDoc = `title: proposed via CLI
operations:
  - kind: added
    after:
      schema: graph/requirement/v0
      id: req-cli-propose
      title: Proposed via CLI
      status: draft
      visibility: public
`

func TestGraphProposeCmd_AppendsProposedChangesetFromStdin(t *testing.T) {
	root := t.TempDir()
	copyDir(t, "../../internal/graph/testdata/apply/bundle", root)

	out, err := runGraphProposeCmd(t, proposeInputDoc, root, "--actor", "cli-actor")
	if err != nil {
		t.Fatalf("graph propose must exit 0, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cs-1 (proposed) appended") {
		t.Fatalf("expected minted cs-1 in output, got:\n%s", out)
	}

	cat, err := graph.LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload catalog: %v", err)
	}
	node, ok := cat.Nodes["cs-1"]
	if !ok {
		t.Fatalf("expected changeset node cs-1 in the catalog after propose")
	}
	if node.Status != "proposed" {
		t.Fatalf("changeset status: expected proposed, got %v", node.Status)
	}
	if got := node.Fields["authored_by"]; got != "cli-actor" {
		t.Fatalf("authored_by: expected cli-actor (from --actor), got %v", got)
	}
	if _, ok := node.Fields["created_at"]; !ok {
		t.Fatalf("expected a created_at stamp on the changeset node")
	}
}

func TestGraphProposeCmd_MintsNextFreeChangesetID(t *testing.T) {
	root := t.TempDir()
	copyDir(t, "../../internal/graph/testdata/apply/bundle", root)

	if out, err := runGraphProposeCmd(t, proposeInputDoc, root); err != nil {
		t.Fatalf("first propose: %v\n%s", err, out)
	}
	second := strings.ReplaceAll(proposeInputDoc, "req-cli-propose", "req-cli-propose-2")
	out, err := runGraphProposeCmd(t, second, root)
	if err != nil {
		t.Fatalf("second propose: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cs-2 (proposed) appended") {
		t.Fatalf("expected the second proposal to mint cs-2, got:\n%s", out)
	}
}

func TestGraphProposeCmd_BareOperationsListNeedsTitleFlag(t *testing.T) {
	root := t.TempDir()
	copyDir(t, "../../internal/graph/testdata/apply/bundle", root)

	bareOps := `- kind: added
  after:
    schema: graph/requirement/v0
    id: req-cli-bare
    title: Bare ops list
    status: draft
    visibility: public
`
	// Without a title anywhere: reject before touching the engine.
	if out, err := runGraphProposeCmd(t, bareOps, root); err == nil {
		t.Fatalf("bare operations without --title must fail, got:\n%s", out)
	}
	out, err := runGraphProposeCmd(t, bareOps, root, "--title", "bare ops via flag")
	if err != nil {
		t.Fatalf("bare operations with --title must succeed, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cs-1 (proposed) appended") {
		t.Fatalf("expected cs-1 minted, got:\n%s", out)
	}
}

func TestGraphProposeCmd_FromFileWithValidateOnlyWritesNothing(t *testing.T) {
	root := t.TempDir()
	copyDir(t, "../../internal/graph/testdata/apply/bundle", root)
	inputPath := filepath.Join(t.TempDir(), "changeset.yaml")
	if err := os.WriteFile(inputPath, []byte(proposeInputDoc), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	out, err := runGraphProposeCmd(t, "", root, "--file", inputPath, "--validate-only")
	if err != nil {
		t.Fatalf("graph propose --validate-only: %v\n%s", err, out)
	}
	if !strings.Contains(out, "validate-only clean, nothing written") {
		t.Fatalf("expected validate-only output, got:\n%s", out)
	}
	cat, err := graph.LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload catalog: %v", err)
	}
	if _, ok := cat.Nodes["cs-1"]; ok {
		t.Fatalf("--validate-only must not write a changeset node")
	}
}

func TestGraphProposeCmd_RejectedProposalTouchesNothing(t *testing.T) {
	root := t.TempDir()
	copyDir(t, "../../internal/graph/testdata/apply/bundle", root)

	// An added op whose after is missing `id` — the exact validation
	// graph.propose rejects (mirrored here through the shared engine op).
	bad := `title: bad proposal
operations:
  - kind: added
    after:
      schema: graph/requirement/v0
      title: no id
`
	out, err := runGraphProposeCmd(t, bad, root)
	if err == nil {
		t.Fatalf("invalid proposal must exit non-zero, got:\n%s", out)
	}
	if !strings.Contains(out, "reject:") {
		t.Fatalf("expected reject reasons in output, got:\n%s", out)
	}
	cat, lerr := graph.LoadCatalog(root)
	if lerr != nil {
		t.Fatalf("reload catalog: %v", lerr)
	}
	if _, ok := cat.Nodes["cs-1"]; ok {
		t.Fatalf("a rejected proposal must not write a changeset node")
	}
}
