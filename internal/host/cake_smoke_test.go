package host_test

import (
	"context"
	"testing"

	"kitsoki/internal/host"
)

// TestCakeProject_AllThreeKindsListed wires the local-files provider
// against testdata/projects/cake to verify the seeded fixtures parse
// and that list_mine returns one row per kind (bug / feature / epic)
// with the type tag the dev-story drive arc routes on.
func TestCakeProject_AllThreeKindsListed(t *testing.T) {
	res, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "list_mine",
		"root": "../../testdata/projects/cake",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 3 {
		t.Fatalf("expected 3 tickets, got %d (%v)", len(tickets), tickets)
	}
	wantByID := map[string]string{
		"B-2026-05-18-list-broken":     "bug",
		"F-2026-05-18-export-csv":      "feature",
		"E-2026-05-18-notes-platform":  "epic",
	}
	for _, row := range tickets {
		id, _ := row["id"].(string)
		typ, _ := row["type"].(string)
		want, ok := wantByID[id]
		if !ok {
			t.Fatalf("unexpected row id %q", id)
		}
		if typ != want {
			t.Fatalf("row %s: type=%q want %q", id, typ, want)
		}
	}
}
