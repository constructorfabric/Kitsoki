package ghagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/ghagent/bugdeck"
	"kitsoki/internal/host"
)

// TestE2E_BugReportDeck is a throwaway, manually-gated end-to-end run of the
// real reactor: it renders a real self-contained deck from a real rrweb capture
// + HAR through `slidey bundle`, hosts it in a DeckStore, and (when
// KITSOKI_DECK_COMMENT is set) posts the real no-LLM comment on a test issue.
//
//	SLIDEY_DIR=~/code/slidey KITSOKI_DECK_E2E=1 \
//	  go test ./internal/ghagent/ -run TestE2E_BugReportDeck -v -count=1
//
// Add KITSOKI_DECK_COMMENT=1 and KITSOKI_DECK_REPO/ISSUE to also post a comment.
func TestE2E_BugReportDeck(t *testing.T) {
	if os.Getenv("KITSOKI_DECK_E2E") == "" {
		t.Skip("set KITSOKI_DECK_E2E=1 (and SLIDEY_DIR) to run the live deck e2e")
	}
	slidey := strings.TrimSpace(os.Getenv("SLIDEY_DIR"))
	if slidey == "" {
		t.Fatal("SLIDEY_DIR required")
	}

	rrweb, err := os.ReadFile("../../docs/decks/clips/pet-bug-report-review.rrweb.json")
	if err != nil {
		t.Fatalf("read real clip: %v", err)
	}
	har := []byte(`{"log":{"version":"1.2","entries":[
		{"time":12.4,"request":{"method":"POST","url":"https://app/rpc","postData":{"text":"{\"jsonrpc\":\"2.0\",\"method\":\"runstatus.session.get\"}"}},"response":{"status":200}},
		{"time":48.1,"request":{"method":"POST","url":"https://app/rpc","postData":{"text":"{\"jsonrpc\":\"2.0\",\"method\":\"runstatus.session.drive\"}"}},"response":{"status":200}},
		{"time":210.0,"request":{"method":"POST","url":"https://app/rpc","postData":{"text":"{\"jsonrpc\":\"2.0\",\"method\":\"runstatus.bug.report\"}"}},"response":{"status":500}}
	]}}`)

	// Host decks into a stable, inspectable artifacts dir (gitignored).
	outDir, err := filepath.Abs("../../.artifacts/bug-deck-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewDeckStore(outDir)
	if err != nil {
		t.Fatal(err)
	}

	repo := envOr("KITSOKI_DECK_REPO", "bsacrobatix/Kitsoki")
	issue := envOr("KITSOKI_DECK_ISSUE", "62")

	var commentBody string
	var ticket host.Handler
	if os.Getenv("KITSOKI_DECK_COMMENT") != "" {
		ticket = host.GitHubTicketHandler
	} else {
		ticket = func(_ context.Context, args map[string]any) (host.Result, error) {
			commentBody, _ = args["body"].(string)
			return host.Result{Data: map[string]any{"url": "(dry-run, no comment posted)"}}, nil
		}
	}

	d := &DeckReactor{
		Ticket:        ticket,
		Renderer:      bugdeck.SlideyRenderer{Dir: slidey},
		Store:         store,
		Repo:          repo,
		PublicBaseURL: envOr("KITSOKI_DECK_BASE_URL", "https://kitsoki-agent.example"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	url, err := d.React(ctx, issue, bugdeck.Evidence{
		RRWeb:       rrweb,
		HAR:         har,
		Title:       "Redundant Observe ↗ link confuses the trace toggle",
		IssueNumber: issue,
		IssueURL:    "https://github.com/" + repo + "/issues/" + issue,
	})
	if err != nil {
		t.Fatalf("React: %v", err)
	}

	deckFile := filepath.Join(outDir, DeckID(repo, issue)+".html")
	info, err := os.Stat(deckFile)
	if err != nil {
		t.Fatalf("deck not hosted: %v", err)
	}
	t.Logf("DECK_URL=%s", url)
	t.Logf("DECK_FILE=%s (%d bytes)", deckFile, info.Size())
	if commentBody != "" {
		t.Logf("DRY_RUN_COMMENT:\n%s", commentBody)
	}
	if info.Size() < 200_000 {
		t.Fatalf("deck too small (%d) — bundle may have failed to inline", info.Size())
	}
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// TestE2E_WebhookTriggerRealRender drives the full webhook path — a real
// issues.opened payload through DeckTrigger.Handle with on-agent evidence and
// the real slidey renderer — proving the auto-trigger hosts a deck end-to-end.
//
//	SLIDEY_DIR=~/code/slidey KITSOKI_DECK_E2E=1 \
//	  go test ./internal/ghagent/ -run TestE2E_WebhookTriggerRealRender -v -count=1
func TestE2E_WebhookTriggerRealRender(t *testing.T) {
	if os.Getenv("KITSOKI_DECK_E2E") == "" {
		t.Skip("set KITSOKI_DECK_E2E=1 (and SLIDEY_DIR) to run")
	}
	slidey := strings.TrimSpace(os.Getenv("SLIDEY_DIR"))
	if slidey == "" {
		t.Fatal("SLIDEY_DIR required")
	}
	rrweb, err := os.ReadFile("../../docs/decks/clips/pet-bug-report-review.rrweb.json")
	if err != nil {
		t.Fatal(err)
	}

	evStore, _ := NewEvidenceStore(t.TempDir())
	deckStore, _ := NewDeckStore(t.TempDir())
	repo, num := "bsacrobatix/Kitsoki", "62"
	if err := evStore.Save(DeckID(repo, num), rrweb, []byte(`{"log":{"entries":[{"time":9,"request":{"method":"POST","url":"/rpc"},"response":{"status":500}}]}}`)); err != nil {
		t.Fatal(err)
	}

	var commented bool
	tr := &DeckTrigger{
		Evidence:      evStore,
		Store:         deckStore,
		Renderer:      bugdeck.SlideyRenderer{Dir: slidey},
		Ticket:        func(_ context.Context, args map[string]any) (host.Result, error) { commented = args["op"] == "comment"; return host.Result{}, nil },
		PublicBaseURL: "https://kitsoki-agent.example",
	}

	payload := []byte(`{"action":"opened","repository":{"full_name":"` + repo + `"},"issue":{"number":62,"title":"web: redundant Observe link","html_url":"https://github.com/` + repo + `/issues/62","body":"A bug.\n\n` + "```kitsoki\\nfiled_by: brad\\n```" + `"}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	url, handled, err := tr.Handle(ctx, payload)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !handled || !commented {
		t.Fatalf("handled=%v commented=%v", handled, commented)
	}
	if !deckStore.Exists(DeckID(repo, num)) {
		t.Fatal("deck not hosted by trigger")
	}
	t.Logf("WEBHOOK_DECK_URL=%s", url)
}
