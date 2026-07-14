package bugdeck

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestProduce_RealSlideyBundle renders a real self-contained deck through the
// actual `slidey bundle` subprocess. It is gated on SLIDEY_DIR (the slidey
// project dir, e.g. ~/code/slidey) so the default `go test` run needs neither
// node nor slidey. It performs no LLM/TTS/network call — `bundle` only inlines
// assets.
//
//	SLIDEY_DIR=~/code/slidey go test ./internal/ghagent/bugdeck/ -run RealSlidey -v
func TestProduce_RealSlideyBundle(t *testing.T) {
	dir := strings.TrimSpace(os.Getenv("SLIDEY_DIR"))
	if dir == "" {
		t.Skip("set SLIDEY_DIR=path/to/slidey to run the real render")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	html, err := Produce(ctx, Evidence{
		RRWeb:       []byte(`[{"type":4,"data":{"href":"http://x","width":1280,"height":720},"timestamp":1},{"type":2,"data":{"node":{"type":0,"childNodes":[],"id":1},"initialOffset":{"left":0,"top":0}},"timestamp":2}]`),
		HAR:         []byte(`{"log":{"version":"1.2","entries":[{"time":12,"request":{"method":"POST","url":"https://x/rpc","postData":{"text":"{\"method\":\"runstatus.session.get\"}"}},"response":{"status":200}}]}}`),
		Title:       "Real render smoke",
		IssueURL:    "https://github.com/o/r/issues/1",
		IssueNumber: "1",
	}, SlideyRenderer{Dir: dir}, t.TempDir())
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	info, err := os.Stat(html)
	if err != nil {
		t.Fatalf("stat deck: %v", err)
	}
	// A bundled deck inlines the slidey runtime + clip — it is large.
	if info.Size() < 100_000 {
		t.Fatalf("deck unexpectedly small (%d bytes) — bundle may not have inlined assets", info.Size())
	}
	data, err := os.ReadFile(html)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<html") && !strings.Contains(string(data), "<!doctype") && !strings.Contains(string(data), "<!DOCTYPE") {
		t.Fatalf("deck does not look like HTML: %.120s", data)
	}
	t.Logf("rendered self-contained deck: %s (%d bytes)", html, info.Size())
}
