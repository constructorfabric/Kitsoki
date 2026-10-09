package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestJourneyPackRejectsUnsafeOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".kitsoki", "stories", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki", "stories", "demo", "app.yaml"), []byte("app: {id: demo}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "journey.yaml")
	body := "schema: kitsoki/journey-pack/v1\nid: demo/onboarding\ntitle: Demo\nstory: {app: .kitsoki/stories/demo/app.yaml}\nmatrix: {personas: [author], scenarios: [onboarding], transports: [web]}\noutputs: {tutorial: {publish: /tmp/not-portable}}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, root, _, err := loadJourney(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateJourney(p, root); err == nil {
		t.Fatal("expected absolute output path to fail")
	}
}

func TestJourneyPackFindsRepositoryRootAndAcceptsLocalOverlay(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".kitsoki", "stories", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".kitsoki", "qa", "personas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".kitsoki", "qa", "scenarios"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki", "qa", "personas", "author.yaml"), []byte("id: author\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki", "qa", "scenarios", "onboarding.yaml"), []byte("id: onboarding\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki", "stories", "demo", "app.yaml"), []byte("app: {id: demo}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	journeyDir := filepath.Join(dir, ".kitsoki", "qa", "journeys", "onboarding")
	if err := os.MkdirAll(journeyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(journeyDir, "journey.yaml")
	body := "schema: kitsoki/journey-pack/v1\nid: demo/onboarding\ntitle: Demo\nstory: {app: .kitsoki/stories/demo/app.yaml}\ncatalogs: {personas: [../../personas], scenarios: [../../scenarios]}\nmatrix: {personas: [author], scenarios: [onboarding], transports: [web]}\ngate: {degraded_evidence: block_publish}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, root, _, err := loadJourney(path)
	if err != nil {
		t.Fatal(err)
	}
	if root != dir {
		t.Fatalf("root = %q, want %q", root, dir)
	}
	if err := validateJourney(p, root); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceGeneratedRegionPreservesHumanText(t *testing.T) {
	got := replaceGeneratedRegion("# Human\n\n<!-- kitsoki:generated:start -->\nold\n<!-- kitsoki:generated:end -->\n", "<!-- kitsoki:generated:start -->\nnew\n<!-- kitsoki:generated:end -->")
	if got != "# Human\n\n<!-- kitsoki:generated:start -->\nnew\n<!-- kitsoki:generated:end -->\n" {
		t.Fatalf("unexpected generated-region replacement: %q", got)
	}
}

func TestVerifyJourneyReplayRejectsTransitionDrift(t *testing.T) {
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.jsonl")
	replay := filepath.Join(dir, "replay.jsonl")
	if err := os.WriteFile(origin, []byte("{\"kind\":\"machine.transition\",\"payload\":{\"to\":\"ready\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replay, []byte("{\"kind\":\"machine.transition\",\"payload\":{\"to\":\"idle\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyJourneyReplay(origin, replay); err == nil {
		t.Fatal("expected replay drift to fail")
	}
}

func TestTraceTargetsAcceptsLargeEmbeddedStoryRecord(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "origin.jsonl")
	large := strings.Repeat("x", 5*1024*1024)
	body := `{"kind":"session.story","payload":{"source":"` + large + `"}}` + "\n" +
		`{"kind":"machine.transition","payload":{"to":"core.landing"}}` + "\n"
	if err := os.WriteFile(trace, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	targets, err := traceTargets(trace)
	if err != nil {
		t.Fatalf("traceTargets: %v", err)
	}
	if got, want := targets, []string{"core.landing"}; !slices.Equal(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestValidateJourneyTutorialRejectsMachinePath(t *testing.T) {
	text := "<!-- kitsoki:generated:start -->\nopen /Users/someone/private\n<!-- kitsoki:generated:end -->"
	if err := validateJourneyTutorial(text); err == nil {
		t.Fatal("expected machine-specific tutorial path to fail")
	}
}

func TestJourneyDeckKeepsDemoOriginPending(t *testing.T) {
	p := &journeyPack{ID: "demo/onboarding", Title: "Demo"}
	var freeze journeyFreezeReceipt
	freeze.Origin.Kind = "demo"
	freeze.Origin.Trace = ".artifacts/origin.jsonl"
	freeze.Replay.Flow = ".kitsoki/qa/flows/accepted.yaml"
	freeze.Replay.Status = "passed"
	deck := journeyDeck(p, freeze, "digest")
	scenes, ok := deck["scenes"].([]any)
	if !ok || len(scenes) != 8 {
		t.Fatal("expected proof deck scenes")
	}
	evidence := scenes[2].(map[string]any)
	items := evidence["items"].([]any)
	if got := items[1].(map[string]any)["status"]; got != "pending" {
		t.Fatalf("demo origin status = %v, want pending", got)
	}
}

func TestJourneyDeckCarriesSourceControlledProof(t *testing.T) {
	p := &journeyPack{ID: "demo/onboarding", Title: "Demo"}
	p.Story.App = ".kitsoki/stories/demo/app.yaml"
	p.Story.Entry = "core.landing"
	p.Proof.OriginPrompt = "Show me the safe next step."
	p.Proof.OriginRun = "Claude native · 12s · $0.01"
	p.Proof.Response = []string{"Run make test."}
	p.Proof.Delivered = []string{"Published tutorial."}
	p.Proof.Boundaries = []string{"No files changed."}
	var freeze journeyFreezeReceipt
	freeze.Origin.Kind = "real"
	freeze.Origin.Trace = ".artifacts/origin.jsonl"
	freeze.Replay.Flow = ".kitsoki/qa/flows/accepted.yaml"
	freeze.Replay.Status = "passed"
	deck := journeyDeck(p, freeze, "digest")
	scenes := deck["scenes"].([]any)
	if got := scenes[1].(map[string]any)["lede"]; got != p.Proof.OriginPrompt {
		t.Fatalf("origin prompt = %v", got)
	}
	if got := scenes[2].(map[string]any)["items"].([]any)[0].(map[string]any)["detail"]; got != p.Proof.OriginRun {
		t.Fatalf("origin run = %v", got)
	}
	if got := scenes[3].(map[string]any)["items"].([]any)[0].(map[string]any)["detail"]; got != p.Proof.Response[0] {
		t.Fatalf("response proof = %v", got)
	}
}

func TestJourneyDeckReceiptPreservesDistinctJSONFields(t *testing.T) {
	receipt := struct {
		Status        string            `json:"status"`
		Digest        string            `json:"digest"`
		SourceDigests map[string]string `json:"source_digests"`
	}{Status: "passed", Digest: "deck-digest", SourceDigests: map[string]string{"journey": "journey-digest"}}
	b, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "passed" || got["digest"] != "deck-digest" {
		t.Fatalf("receipt fields missing: %s", b)
	}
}
