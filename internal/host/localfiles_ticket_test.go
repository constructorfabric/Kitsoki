package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

const sampleBug = `---
title: "Esc in foyer hangs the TUI"
status: open
severity: P2
assignee: brad
url: ""
component: tui
---

# Esc in foyer hangs the TUI

Expected: Esc returns to the foyer menu.
Actual:   TUI freezes; only Ctrl-C exits.
`

const sampleBugWithComment = `---
title: "PR refinement loops on stale CI"
status: in_progress
severity: P0
assignee: brad
---

# PR refinement loops on stale CI

The CI check stays "pending" indefinitely.

## Comment 2026-05-14T10:32:05Z by brad

I think this is the same as PLTFRM-89912.
`

// seed creates a bugs root with one or more files under issues/bugs/.
// Returns the root directory.
func seedTicketsRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "issues", "bugs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// seedMultiKindRoot creates a root with one ticket each under
// issues/{bugs,features,epics}/.
func seedMultiKindRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, kd := range []struct{ dir, name string }{
		{"bugs", "B-001.md"},
		{"features", "F-001.md"},
		{"epics", "E-001.md"},
	} {
		d := filepath.Join(root, "issues", kd.dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
		body := strings.Replace(sampleBug, "Esc in foyer hangs the TUI", strings.TrimSuffix(kd.name, ".md"), -1)
		if err := os.WriteFile(filepath.Join(d, kd.name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", kd.name, err)
		}
	}
	return root
}

func TestLocalFilesTicket_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	for _, op := range []string{
		"host.local_files.ticket",
		"host.local_files.ticket.search",
		"host.local_files.ticket.get",
		"host.local_files.ticket.comment",
		"host.local_files.ticket.transition",
		"host.local_files.ticket.list_mine",
	} {
		if _, ok := r.Get(op); !ok {
			t.Fatalf("registry: %s missing (prefix-fallback should resolve)", op)
		}
	}
}

func TestLocalFilesTicket_Search_Happy(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-14T10-tui-hangs.md": sampleBug,
		"2026-05-14T11-ci-loop.md":   sampleBugWithComment,
	})
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":    "search",
		"root":  root,
		"query": "esc",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 1 {
		t.Fatalf("expected 1 hit, got %d (%v)", len(tickets), res.Data)
	}
	if tickets[0]["title"] != "Esc in foyer hangs the TUI" {
		t.Fatalf("title: %v", tickets[0]["title"])
	}
}

func TestLocalFilesTicket_Search_EmptyRootIsEmptyList(t *testing.T) {
	root := t.TempDir()
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "search",
		"root": root,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 0 {
		t.Fatalf("expected 0, got %d", len(tickets))
	}
}

func TestLocalFilesTicket_Get_Happy(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-14T11-ci-loop.md": sampleBugWithComment,
	})
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "get",
		"root": root,
		"id":   "2026-05-14T11-ci-loop",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	if res.Data["title"] != "PR refinement loops on stale CI" {
		t.Fatalf("title: %v", res.Data["title"])
	}
	if res.Data["status"] != "in_progress" {
		t.Fatalf("status: %v", res.Data["status"])
	}
	if res.Data["severity"] != "P0" {
		t.Fatalf("severity: %v", res.Data["severity"])
	}
	if _, present := res.Data["priority"]; present {
		t.Fatalf("priority should no longer be projected, got %v", res.Data["priority"])
	}
	body, _ := res.Data["body"].(string)
	if !strings.Contains(body, "The CI check stays") {
		t.Fatalf("body missing prose: %q", body)
	}
	comments, _ := res.Data["comments"].([]map[string]any)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d (%v)", len(comments), comments)
	}
	if comments[0]["author"] != "brad" {
		t.Fatalf("author: %v", comments[0]["author"])
	}
}

func TestLocalFilesTicket_Get_NotFound(t *testing.T) {
	root := seedTicketsRoot(t, nil)
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "get",
		"root": root,
		"id":   "no-such-bug",
	})
	if err != nil {
		t.Fatalf("infra error not expected: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing bug")
	}
}

func TestLocalFilesTicket_Comment_AppendsBlock(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-14T10-tui-hangs.md": sampleBug,
	})
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":     "comment",
		"root":   root,
		"id":     "2026-05-14T10-tui-hangs",
		"body":   "Reproduction confirmed on Linux + tmux.",
		"author": "llm-judge",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok=true expected: %v", res.Data["ok"])
	}
	// File should now contain the comment block.
	data, err := os.ReadFile(filepath.Join(root, "issues", "bugs", "2026-05-14T10-tui-hangs.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "## Comment ") {
		t.Fatalf("missing comment heading: %s", got)
	}
	if !strings.Contains(got, "by llm-judge") {
		t.Fatalf("missing author tag: %s", got)
	}
	if !strings.Contains(got, "Reproduction confirmed on Linux + tmux.") {
		t.Fatalf("missing body: %s", got)
	}
}

func TestLocalFilesTicket_Comment_ToThreadPath(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-14T10-tui-hangs.md": sampleBug,
	})
	target := filepath.Join(root, "issues", "bugs", "2026-05-14T10-tui-hangs.md")
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":     "comment",
		"root":   root, // root supplied but should NOT be used when thread is a path
		"thread": target,
		"body":   "Thread-routed comment.",
		"author": "kitsoki",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	raw, _ := os.ReadFile(target)
	if !strings.Contains(string(raw), "Thread-routed comment.") {
		t.Fatalf("body missing in file: %s", raw)
	}
}

func TestLocalFilesTicket_Comment_RequiresBody(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"x.md": sampleBug,
	})
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "comment",
		"root": root,
		"id":   "x",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for empty body")
	}
}

func TestLocalFilesTicket_Transition_RewritesStatus(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-14T10-tui-hangs.md": sampleBug,
	})
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "transition",
		"root": root,
		"id":   "2026-05-14T10-tui-hangs",
		"to":   "resolved",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
	// Re-get and check.
	got, _ := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "get",
		"root": root,
		"id":   "2026-05-14T10-tui-hangs",
	})
	if got.Data["status"] != "resolved" {
		t.Fatalf("status after transition: %v", got.Data["status"])
	}
	// Unknown frontmatter (component:tui) should survive the round-trip.
	data, _ := os.ReadFile(filepath.Join(root, "issues", "bugs", "2026-05-14T10-tui-hangs.md"))
	if !strings.Contains(string(data), "component: tui") {
		t.Fatalf("unknown frontmatter dropped during transition: %s", data)
	}
}

func TestLocalFilesTicket_ListMine_FilterMatches(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"a.md": sampleBug,            // assignee: brad
		"b.md": sampleBugWithComment, // assignee: brad
		"c.md": withFront(sampleBug, "assignee", "alice"),
	})
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":     "list_mine",
		"root":   root,
		"filter": "brad",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 2 {
		t.Fatalf("expected 2, got %d (%v)", len(tickets), tickets)
	}
}

func TestLocalFilesTicket_UnknownOpRejected(t *testing.T) {
	root := seedTicketsRoot(t, nil)
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "smoke",
		"root": root,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for unknown op")
	}
}

func TestLocalFilesTicket_MissingOp(t *testing.T) {
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when op absent")
	}
}

func TestLocalFilesTicket_Search_TagsByKind(t *testing.T) {
	root := seedMultiKindRoot(t)
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "search",
		"root": root,
	})
	if err != nil || res.Error != "" {
		t.Fatalf("search: %v / %s", err, res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 3 {
		t.Fatalf("expected 3 (one per kind), got %d", len(tickets))
	}
	want := map[string]string{"B-001": "bug", "F-001": "feature", "E-001": "epic"}
	for _, row := range tickets {
		id, _ := row["id"].(string)
		typ, _ := row["type"].(string)
		if want[id] != typ {
			t.Fatalf("row %s: want type=%q got %q", id, want[id], typ)
		}
	}
}

func TestLocalFilesTicket_Get_LocatesAcrossKinds(t *testing.T) {
	root := seedMultiKindRoot(t)
	for id, wantKind := range map[string]string{"B-001": "bug", "F-001": "feature", "E-001": "epic"} {
		res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
			"op":   "get",
			"root": root,
			"id":   id,
		})
		if err != nil || res.Error != "" {
			t.Fatalf("get %s: %v / %s", id, err, res.Error)
		}
		if got, _ := res.Data["type"].(string); got != wantKind {
			t.Fatalf("get %s: type=%q want %q", id, got, wantKind)
		}
	}
}

func TestLocalFilesTicket_Transition_LocatesAcrossKinds(t *testing.T) {
	root := seedMultiKindRoot(t)
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "transition",
		"root": root,
		"id":   "F-001",
		"to":   "resolved",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("transition F-001: %v / %s", err, res.Error)
	}
	data, _ := os.ReadFile(filepath.Join(root, "issues", "features", "F-001.md"))
	if !strings.Contains(string(data), "status: resolved") {
		t.Fatalf("status not rewritten: %s", data)
	}
}

// withFront returns sample with the named frontmatter key replaced.
// Crude string-replace — only safe for our sample fixtures.
func withFront(sample, key, value string) string {
	lines := strings.Split(sample, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, key+":") {
			lines[i] = key + ": " + value
			return strings.Join(lines, "\n")
		}
	}
	return sample
}
