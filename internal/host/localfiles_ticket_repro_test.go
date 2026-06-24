package host_test

// Reproduction for bug
// 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency.
//
// Two distinct defects in internal/host/localfiles_ticket.go:
//
//  1. bugSummary projects frontmatter key "priority", but bug files
//     (per docs/stories/bugs.md and issues/README.md)
//     use "severity: P0|P1|P2|P3". As a result, the ticket summary
//     returned from iface.ticket.search has an empty priority field for
//     every local-files ticket, and views that branch on `t.priority`
//     never render the badge.
//
//  2. listAllBugs sorts strictly by ID ASC. Because IDs begin with an
//     ISO-UTC timestamp, this is effectively filed_at-ASC (oldest
//     first), and severity has no effect on order. The expected
//     ordering is severity ASC (P0 first), then filed_at DESC
//     (newest first within a severity bucket).
//
// Both assertions encode the EXPECTED behaviour and therefore FAIL on
// the current head — this file IS the deterministic reproduction.

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// bugFixture produces a bug file with the given id, severity, and
// filed_at timestamp.  Mirrors the canonical bug-format
// frontmatter (severity:, not priority:).
func bugFixture(title, severity, filedAt string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("title: \"" + title + "\"\n")
	sb.WriteString("status: open\n")
	sb.WriteString("severity: " + severity + "\n")
	sb.WriteString("filed_at: " + filedAt + "\n")
	sb.WriteString("assignee: brad\n")
	sb.WriteString("---\n\n# " + title + "\n\nbody.\n")
	return sb.String()
}

// TestRepro_BugSummary_ProjectsSeverity confirms defect #1: the
// summary handed to consumers must expose the bug's severity, not an
// empty `priority` string.
func TestRepro_BugSummary_ProjectsSeverity(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-18T010000Z-a.md": bugFixture("Alpha", "P0", "2026-05-18T01:00:00Z"),
	})
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
	if len(tickets) != 1 {
		t.Fatalf("want 1 ticket, got %d", len(tickets))
	}
	got, _ := tickets[0]["severity"].(string)
	if got != "P0" {
		// Defect #1 — bugSummary reads "priority"; severity is dropped.
		t.Fatalf("expected severity P0 in summary, got %q (full summary: %#v)", got, tickets[0])
	}
}

// TestRepro_ListAllBugs_OrdersBySeverityThenRecency confirms defect
// #2: ticket.search must return the highest-severity bugs first, and
// within a severity bucket the newest first.
func TestRepro_ListAllBugs_OrdersBySeverityThenRecency(t *testing.T) {
	// Three fixtures designed so that ID-ASC, severity-ASC, and
	// filed_at-DESC produce distinct orderings — any of the wrong
	// keys will mis-order this set.
	//
	// IDs are ISO-prefixed; under the current id-ASC sort the order
	// is oldP3, midP2, newP0 (oldest → newest). Under the expected
	// severity-then-recency sort it is newP0, midP2, oldP3.
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-10T000000Z-old.md": bugFixture("Old low-sev", "P3", "2026-05-10T00:00:00Z"),
		"2026-05-12T000000Z-mid.md": bugFixture("Mid medium-sev", "P2", "2026-05-12T00:00:00Z"),
		"2026-05-17T000000Z-new.md": bugFixture("New high-sev", "P0", "2026-05-17T00:00:00Z"),
	})
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
	if len(tickets) != 3 {
		t.Fatalf("want 3 tickets, got %d", len(tickets))
	}

	wantIDs := []string{
		"2026-05-17T000000Z-new", // P0
		"2026-05-12T000000Z-mid", // P2
		"2026-05-10T000000Z-old", // P3
	}
	var gotIDs []string
	for _, tk := range tickets {
		id, _ := tk["id"].(string)
		gotIDs = append(gotIDs, id)
	}
	for i, want := range wantIDs {
		if gotIDs[i] != want {
			t.Fatalf("ordering mismatch at index %d: want %q, got %q (full order: %v)\n  defect: listAllBugs sorts by ID ASC; expected severity ASC, filed_at DESC", i, want, gotIDs[i], gotIDs)
		}
	}
}
