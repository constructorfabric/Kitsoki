package ghagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/ghagent/bugdeck"
	"kitsoki/internal/host"
)

// fakeRenderer writes a recognizable HTML file (no node/slidey).
type fakeRenderer struct{ body string }

func (f fakeRenderer) Bundle(_ context.Context, _, outPath string) error {
	body := f.body
	if body == "" {
		body = "<!doctype html><html>deck</html>"
	}
	return os.WriteFile(outPath, []byte(body), 0o644)
}

func TestDeckID_StableAndSafe(t *testing.T) {
	if got := DeckID("bsacrobatix/Kitsoki", "62"); got != "bsacrobatix-Kitsoki-62" {
		t.Fatalf("DeckID = %q", got)
	}
	if DeckID("o/r", "62") != DeckID("o/r", "62") {
		t.Fatal("DeckID must be stable")
	}
	if strings.ContainsAny(DeckID("a/../b", "1/2"), "/") {
		t.Fatal("DeckID must be a single safe path segment")
	}
}

func TestDeckComment_StatesNoLLM(t *testing.T) {
	c := DeckComment("https://agent/decks/o-r-62")
	for _, want := range []string{"https://agent/decks/o-r-62", "No LLM was used", "llm_used: false", "no download"} {
		if !strings.Contains(c, want) {
			t.Errorf("comment missing %q:\n%s", want, c)
		}
	}
}

func TestDeckReactor_React_HostsAndComments(t *testing.T) {
	store, err := NewDeckStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var commentBody, commentRepo, commentID string
	ticket := func(_ context.Context, args map[string]any) (host.Result, error) {
		if args["op"] == "comment" {
			commentBody, _ = args["body"].(string)
			commentRepo, _ = args["repo"].(string)
			commentID, _ = args["id"].(string)
		}
		return host.Result{Data: map[string]any{"url": "https://github.com/o/r/issues/62#c1"}}, nil
	}
	d := &DeckReactor{
		Ticket:        ticket,
		Renderer:      fakeRenderer{body: "<!doctype html><html>HOSTED DECK</html>"},
		Store:         store,
		Repo:          "o/r",
		PublicBaseURL: "https://agent.example.com/",
	}
	url, err := d.React(context.Background(), "62", bugdeck.Evidence{
		RRWeb: []byte(`[{"type":2}]`),
		HAR:   []byte(`{"log":{"entries":[]}}`),
		Title: "t",
	})
	if err != nil {
		t.Fatalf("React: %v", err)
	}
	if url != "https://agent.example.com/decks/o-r-62" {
		t.Fatalf("deck url = %q", url)
	}
	// Deck stored on the agent.
	if _, err := os.Stat(filepath.Join(store.Dir, "o-r-62.html")); err != nil {
		t.Fatalf("deck not stored: %v", err)
	}
	// Comment posted to the right issue, links the deck, states no-LLM.
	if commentRepo != "o/r" || commentID != "62" {
		t.Errorf("comment target repo=%q id=%q", commentRepo, commentID)
	}
	if !strings.Contains(commentBody, url) || !strings.Contains(commentBody, "No LLM was used") {
		t.Errorf("comment body wrong:\n%s", commentBody)
	}
}

func TestDeckReactor_CommentFailure_StillReturnsURL(t *testing.T) {
	store, _ := NewDeckStore(t.TempDir())
	ticket := func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Error: "rate limited"}, nil
	}
	d := &DeckReactor{Ticket: ticket, Renderer: fakeRenderer{}, Store: store, Repo: "o/r", PublicBaseURL: "https://a"}
	url, err := d.React(context.Background(), "9", bugdeck.Evidence{RRWeb: []byte(`[{"type":2}]`), Title: "t"})
	if err == nil {
		t.Fatal("expected comment error surfaced")
	}
	if url != "https://a/decks/o-r-9" {
		t.Fatalf("url should still be returned, got %q", url)
	}
}

func TestDeckStore_ServesHTMLInline(t *testing.T) {
	store, _ := NewDeckStore(t.TempDir())
	src := filepath.Join(t.TempDir(), "deck.html")
	os.WriteFile(src, []byte("<!doctype html><html>VIEW ME</html>"), 0o644)
	if _, err := store.Put("o-r-62", src); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(store.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/decks/o-r-62")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q (must render inline, not download)", ct)
	}
	// Unknown deck → 404.
	r2, _ := http.Get(srv.URL + "/decks/nope")
	if r2.StatusCode != 404 {
		t.Fatalf("unknown deck status = %d", r2.StatusCode)
	}
	// Path traversal rejected.
	r3, _ := http.Get(srv.URL + "/decks/../etc/passwd")
	if r3.StatusCode == 200 {
		t.Fatal("path traversal must not 200")
	}
}

func TestEvidenceStore_RoundTrip(t *testing.T) {
	es, err := NewEvidenceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := es.Save("o-r-62", []byte(`[{"type":2}]`), []byte(`{"log":{"entries":[]}}`)); err != nil {
		t.Fatal(err)
	}
	ev, err := es.Load("o-r-62")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.RRWeb) == 0 || len(ev.HAR) == 0 {
		t.Fatalf("evidence not loaded: %+v", ev)
	}
	if _, err := es.Load("missing"); err == nil {
		t.Fatal("expected error loading absent evidence")
	}
}
