package bugdeck

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSpec_FullEvidence(t *testing.T) {
	har := `{"log":{"version":"1.2","entries":[
		{"time":12.4,"request":{"method":"POST","url":"https://x/rpc","postData":{"text":"{\"jsonrpc\":\"2.0\",\"method\":\"runstatus.session.get\"}"}},"response":{"status":200}},
		{"time":48.9,"request":{"method":"POST","url":"https://x/rpc"},"response":{"status":500}}
	]}}`
	spec, clip, err := BuildSpec(Evidence{
		RRWeb:       []byte(`[{"type":2,"data":{}}]`),
		HAR:         []byte(har),
		Title:       "Foyer button does nothing",
		IssueURL:    "https://github.com/o/r/issues/62",
		IssueNumber: "62",
	})
	if err != nil {
		t.Fatalf("BuildSpec: %v", err)
	}
	if clip == nil {
		t.Fatal("expected clip bytes when rrweb present")
	}

	var doc map[string]any
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if doc["meta"].(map[string]any)["mode"] != "pitch" {
		t.Errorf("meta.mode must be pitch, got %v", doc["meta"])
	}
	s := string(spec)
	// No narration anywhere — that would trigger TTS (a paid network call).
	if strings.Contains(s, "narration") || strings.Contains(s, "voice") {
		t.Errorf("spec must not contain narration/voice (no-TTS contract):\n%s", s)
	}
	for _, want := range []string{
		`"type": "title"`,
		`"type": "video"`,
		`"rrweb": "` + ClipFilename + `"`,
		`"type": "table"`,
		`runstatus.session.get`, // rpc method extracted from postData
		`/rpc`,
		`"500"`,
		`"type": "cta"`,
		"Issue #62",
		"https://github.com/o/r/issues/62",
		"Foyer button does nothing",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("spec missing %q\n%s", want, s)
		}
	}
}

func TestBuildSpec_Deterministic(t *testing.T) {
	ev := Evidence{RRWeb: []byte(`[{"type":2}]`), HAR: []byte(`{"log":{"entries":[]}}`), Title: "x"}
	a, _, err := BuildSpec(ev)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := BuildSpec(ev)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("BuildSpec must be byte-identical for identical evidence")
	}
}

func TestBuildSpec_NoRRWeb_DegradesGracefully(t *testing.T) {
	spec, clip, err := BuildSpec(Evidence{HAR: []byte(`{"log":{"entries":[{"time":1,"request":{"method":"GET","url":"/x"},"response":{"status":200}}]}}`), Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if clip != nil {
		t.Error("clip must be nil with no rrweb")
	}
	if strings.Contains(string(spec), `"type": "video"`) {
		t.Error("no video scene without rrweb evidence")
	}
	if !strings.Contains(string(spec), "No rrweb replay") {
		t.Error("expected a graceful no-replay note")
	}
}

func TestBuildSpec_InvalidRRWebRejected(t *testing.T) {
	if _, _, err := BuildSpec(Evidence{RRWeb: []byte("not json"), Title: "t"}); err == nil {
		t.Fatal("expected error for invalid rrweb JSON")
	}
}

func TestSummarizeHAR_Truncates(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"log":{"entries":[`)
	for i := 0; i < maxHARRows+5; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"time":1,"request":{"method":"GET","url":"/x"},"response":{"status":200}}`)
	}
	sb.WriteString(`]}}`)
	rows, total := summarizeHAR([]byte(sb.String()))
	if total != maxHARRows+5 {
		t.Errorf("total = %d, want %d", total, maxHARRows+5)
	}
	if len(rows) != maxHARRows {
		t.Errorf("rows = %d, want capped at %d", len(rows), maxHARRows)
	}
}

func TestSummarizeHAR_BadInput(t *testing.T) {
	for _, in := range [][]byte{nil, []byte(""), []byte("garbage"), []byte("{}")} {
		if rows, _ := summarizeHAR(in); len(rows) != 0 {
			t.Errorf("expected no rows for %q, got %d", in, len(rows))
		}
	}
}

// stubRenderer records its inputs and writes a sentinel HTML so Produce's
// post-conditions can be checked without node/slidey.
type stubRenderer struct {
	specPath, outPath string
	body              string
}

func (s *stubRenderer) Bundle(_ context.Context, specPath, outPath string) error {
	s.specPath, s.outPath = specPath, outPath
	body := s.body
	if body == "" {
		body = "<html>stub deck</html>"
	}
	return os.WriteFile(outPath, []byte(body), 0o644)
}

func TestProduce_StagesAndRenders(t *testing.T) {
	dir := t.TempDir()
	st := &stubRenderer{}
	html, err := Produce(context.Background(), Evidence{
		RRWeb: []byte(`[{"type":2}]`),
		HAR:   []byte(`{"log":{"entries":[]}}`),
		Title: "t",
	}, st, dir)
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	// Spec + clip staged for the renderer.
	if _, err := os.Stat(filepath.Join(dir, SpecFilename)); err != nil {
		t.Errorf("spec not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ClipFilename)); err != nil {
		t.Errorf("clip not staged: %v", err)
	}
	if st.specPath != filepath.Join(dir, SpecFilename) {
		t.Errorf("renderer got spec %q", st.specPath)
	}
	// Deck produced at the returned path.
	got, err := os.ReadFile(html)
	if err != nil {
		t.Fatalf("read deck: %v", err)
	}
	if !strings.Contains(string(got), "stub deck") {
		t.Errorf("unexpected deck body: %s", got)
	}
}

func TestProduce_NilRenderer(t *testing.T) {
	if _, err := Produce(context.Background(), Evidence{Title: "t"}, nil, t.TempDir()); err == nil {
		t.Fatal("expected error for nil renderer")
	}
}

func TestSlideyRenderer_Command(t *testing.T) {
	var gotName string
	var gotArgs []string
	run := func(_ context.Context, name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}
	r := SlideyRenderer{Dir: "/opt/slidey", Run: run}
	if err := r.Bundle(context.Background(), "/w/deck.slidey.json", "/w/deck.html"); err != nil {
		t.Fatal(err)
	}
	if gotName != "node" {
		t.Errorf("name = %q, want node", gotName)
	}
	want := []string{"/opt/slidey/src/index.js", "bundle", "/w/deck.slidey.json", "/w/deck.html"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}

	// A `slidey` binary on PATH is invoked directly (no node).
	r2 := SlideyRenderer{Bin: "slidey", Run: run}
	_ = r2.Bundle(context.Background(), "/w/s.json", "/w/o.html")
	if gotName != "slidey" {
		t.Errorf("bin path: name = %q, want slidey", gotName)
	}
}
