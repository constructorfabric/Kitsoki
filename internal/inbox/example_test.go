// Runnable godoc examples for the inbox summary and teleport surfaces.
// Each Example function's // Output: block is checked by
// `go test -run "^Example" ./internal/inbox/...`.
package inbox_test

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	"kitsoki/internal/world"

	_ "modernc.org/sqlite"
)

// ExampleFromNotification is the teleport worked example: a stored
// notification is projected into the TeleportTarget the orchestrator lands
// on. It touches no store and never errors — the canonical pure path.
func ExampleFromNotification() {
	n := jobs.Notification{
		TeleportState:      "general_store.reviewing",
		TeleportSlots:      map[string]any{"items": "6 oxen"},
		TeleportJobID:      "job-xyz",
		TeleportProposalID: "",
	}

	target := inbox.FromNotification(n)

	fmt.Println("state:   ", target.State)
	fmt.Println("job:     ", target.JobID)
	fmt.Println("items:   ", target.Slots["items"])
	// Output:
	// state:    general_store.reviewing
	// job:      job-xyz
	// items:    6 oxen
}

// ExampleRefreshSummary is the summary worked example: two unread
// notifications (one info, one action_required) fold into $inbox counts of
// unread=2, needs_attention=1.
func ExampleRefreshSummary() {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	js, err := jobs.NewJobStore(db)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	sid := app.SessionID("s1")

	for _, sev := range []jobs.NotificationSeverity{
		jobs.SeverityInfo,
		jobs.SeverityActionRequired,
	} {
		if err := js.InsertNotification(ctx, &jobs.Notification{
			SessionID:     sid,
			CreatedAt:     time.Now(),
			Severity:      sev,
			Title:         string(sev),
			TeleportState: "main",
			OriginKind:    "job",
			OriginRef:     "job:1",
		}); err != nil {
			panic(err)
		}
	}

	w, err := inbox.RefreshSummary(ctx, js, sid, world.New())
	if err != nil {
		panic(err)
	}

	m := w.Vars[inbox.WorldKey].(map[string]any)
	fmt.Println("unread:         ", m["unread"])
	fmt.Println("needs_attention:", m["needs_attention"])
	// Output:
	// unread:          2
	// needs_attention: 1
}
