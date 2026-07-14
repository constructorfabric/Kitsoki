package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposalPublishHandlerMovesDraftAndWritesTicket(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "docs", "proposals", ".workspace", "demo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(workspace, "004-proposal.md")
	if err := os.WriteFile(draft, []byte("# A Better Proposal\n\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := ProposalPublishHandler(context.Background(), map[string]any{
		"workspace": workspace, "slug": "demo", "workdir": root,
		"durable": "docs/proposals", "ticket_dir": "issues/features",
		"idea": "Improve the workflow.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["design_file"] != "docs/proposals/a-better-proposal.md" {
		t.Fatalf("design_file = %v", result.Data["design_file"])
	}
	if result.Data["ticket_title"] != "A Better Proposal" {
		t.Fatalf("ticket_title = %v", result.Data["ticket_title"])
	}
	ticket, err := os.ReadFile(filepath.Join(root, result.Data["ticket_path"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ticket), "A Better Proposal") || !strings.Contains(string(ticket), "a-better-proposal.md") {
		t.Fatalf("ticket missing proposal link: %s", ticket)
	}
	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Fatalf("draft still exists: %v", err)
	}
}

func TestProposalPublishHandlerEmptyTicketDirSkipsTicket(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "004-proposal.md"), []byte("# No Ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := ProposalPublishHandler(context.Background(), map[string]any{
		"workspace": workspace, "slug": "no-ticket", "workdir": root,
		"durable": "docs/proposals", "ticket_dir": "", "ticket_repo": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["ticket_id"] != "" || result.Data["ticket_path"] != "" {
		t.Fatalf("ticket fields = %v, want empty", result.Data)
	}
}
