package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// intakeServer builds the minimal Server the /api/feedback/local handler
// needs (mirroring graph_rpc_provenance_test's bare &Server{} pattern):
// materializeRoot is the served consumer repo root.
func intakeServer(root string, routing map[string]FeedbackRoute) *Server {
	return &Server{materializeRoot: root, feedbackRouting: routing}
}

func postFeedback(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/feedback/local", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleFeedbackLocal(rec, req)
	return rec
}

func decodeIntakeResp(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return out
}

func feedbackJSONLLines(t *testing.T, root string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".artifacts", "feedback", "feedback.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read feedback.jsonl: %v", err)
	}
	var recs []map[string]any
	for i, ln := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatalf("line %d is not one complete JSON object (atomicity): %v\n%q", i, err, ln)
		}
		recs = append(recs, rec)
	}
	return recs
}

func TestFeedbackIntake_HappyPath(t *testing.T) {
	root := t.TempDir()
	s := intakeServer(root, nil)

	bundle := `{"idempotencyKey":"fb-abc123","kind":"bug","reviewed":true,"userText":"stage pill lies","anchor":{"producer":"pog-portal","artifactId":"portal"}}`
	rec := postFeedback(t, s, bundle)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	resp := decodeIntakeResp(t, rec)
	// httpSink's receipt contract: a string ref.
	if ref, _ := resp["ref"].(string); ref != "fb-abc123" {
		t.Errorf("ref = %v, want fb-abc123 (the bundle's idempotencyKey)", resp["ref"])
	}
	if deduped, _ := resp["deduped"].(bool); deduped {
		t.Errorf("first submit must not be deduped")
	}

	lines := feedbackJSONLLines(t, root)
	if len(lines) != 1 {
		t.Fatalf("feedback.jsonl has %d records, want 1", len(lines))
	}
	got := lines[0]
	receivedAt, _ := got["receivedAt"].(string)
	if _, err := time.Parse(time.RFC3339, receivedAt); err != nil {
		t.Errorf("receivedAt %q is not RFC3339: %v", receivedAt, err)
	}
	if got["kind"] != "bug" || got["userText"] != "stage pill lies" || got["idempotencyKey"] != "fb-abc123" {
		t.Errorf("bundle fields not preserved: %v", got)
	}
}

func TestFeedbackIntake_MalformedJSON(t *testing.T) {
	root := t.TempDir()
	s := intakeServer(root, nil)

	for _, body := range []string{"{not json", "42", `"just a string"`, "[]"} {
		rec := postFeedback(t, s, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %q: status = %d, want 400", body, rec.Code)
		}
	}
	if lines := feedbackJSONLLines(t, root); len(lines) != 0 {
		t.Errorf("malformed submits must append nothing; got %d records", len(lines))
	}
}

func TestFeedbackIntake_MethodNotAllowed(t *testing.T) {
	s := intakeServer(t.TempDir(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/feedback/local", nil)
	rec := httptest.NewRecorder()
	s.handleFeedbackLocal(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

// TestFeedbackIntake_Idempotent mirrors sassfully's router contract: a
// retry with the same idempotencyKey never creates a second line and gets
// the SAME receipt back, marked deduped.
func TestFeedbackIntake_Idempotent(t *testing.T) {
	root := t.TempDir()
	s := intakeServer(root, nil)

	bundle := `{"idempotencyKey":"fb-dup","kind":"idea","reviewed":true,"userText":"same thing twice"}`
	first := decodeIntakeResp(t, postFeedback(t, s, bundle))
	second := decodeIntakeResp(t, postFeedback(t, s, bundle))

	if first["ref"] != second["ref"] {
		t.Errorf("refs differ across retry: %v vs %v", first["ref"], second["ref"])
	}
	if deduped, _ := second["deduped"].(bool); !deduped {
		t.Errorf("second submit should report deduped=true: %v", second)
	}
	if lines := feedbackJSONLLines(t, root); len(lines) != 1 {
		t.Errorf("feedback.jsonl has %d records, want 1 (retry must not double-append)", len(lines))
	}
}

// TestFeedbackIntake_ConcurrentAppendsStayWholeLines exercises the
// append-atomicity guarantee: concurrent submits never tear or interleave
// a JSONL line (each record is one complete object on its own line).
func TestFeedbackIntake_ConcurrentAppendsStayWholeLines(t *testing.T) {
	root := t.TempDir()
	s := intakeServer(root, nil)

	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"idempotencyKey":"fb-conc-%d","kind":"bug","reviewed":true,"userText":"submit %d","pad":%q}`,
				i, i, strings.Repeat("x", 512))
			rec := postFeedback(t, s, body)
			if rec.Code != http.StatusOK {
				t.Errorf("submit %d: status %d", i, rec.Code)
			}
		}(i)
	}
	wg.Wait()

	lines := feedbackJSONLLines(t, root) // fails the test on any torn line
	if len(lines) != n {
		t.Fatalf("feedback.jsonl has %d records, want %d", len(lines), n)
	}
	seen := map[string]bool{}
	for _, rec := range lines {
		key, _ := rec["idempotencyKey"].(string)
		seen[key] = true
	}
	if len(seen) != n {
		t.Errorf("distinct keys = %d, want %d", len(seen), n)
	}
}

// ─── catalog-sink routing (producer-keyed, POG ruling D-F2) ───

const intakeFixtureCatalog = `schema: project-object-graph/seed-catalog/v0
catalog:
  id: feedback-intake-fixture
type_registry:
  - id: core-node
    schema: graph-type/v0
    extends: null
    required_fields: [id, schema, title, status, visibility]
  - id: changeset
    schema: graph-type/v0
    extends: core-node
  - id: portal-feedback
    schema: graph-type/v0
    extends: core-node
nodes:
  - schema: graph/portal-feedback/v0
    id: seed-feedback
    title: Seed feedback node
    status: active
    visibility: internal
`

func writeIntakeCatalog(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "catalog.yaml")
	if err := os.WriteFile(path, []byte(intakeFixtureCatalog), 0o644); err != nil {
		t.Fatalf("write fixture catalog: %v", err)
	}
	return path
}

func TestFeedbackIntake_CatalogSinkFiresForRoutedProducer(t *testing.T) {
	root := t.TempDir()
	writeIntakeCatalog(t, root)
	s := intakeServer(root, map[string]FeedbackRoute{
		"pog-portal": {Sink: "catalog", Catalog: "catalog.yaml", Type: "portal-feedback", Fields: []string{"summary", "report"}},
	})

	bundle := `{"idempotencyKey":"fb-routed-1","kind":"bug","reviewed":true,"userText":"catalog pill drifts","anchor":{"producer":"pog-portal"}}`
	rec := postFeedback(t, s, bundle)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeIntakeResp(t, rec)
	routed, _ := resp["routed"].([]any)
	var catalogRef string
	for _, r := range routed {
		m, _ := r.(map[string]any)
		if m["sink"] == "catalog" {
			catalogRef, _ = m["ref"].(string)
		}
	}
	if catalogRef == "" {
		t.Fatalf("no catalog sink in routed: %v (routing_errors: %v)", resp["routed"], resp["routing_errors"])
	}
	if !strings.HasPrefix(catalogRef, "cs-") {
		t.Errorf("catalog ref = %q, want a cs-<n> changeset id", catalogRef)
	}

	// The proposed node rides the changeset's operations (propose never
	// applies), so assert on the catalog text: the changeset landed and
	// carries the routed node's type, title text, and report ref.
	raw, err := os.ReadFile(filepath.Join(root, "catalog.yaml"))
	if err != nil {
		t.Fatalf("re-read catalog: %v", err)
	}
	text := string(raw)
	for _, want := range []string{catalogRef, "portal-feedback", "catalog pill drifts", "fb-routed-1"} {
		if !strings.Contains(text, want) {
			t.Errorf("catalog missing %q after routed propose", want)
		}
	}
	if strings.Contains(text, "status: authorized") {
		t.Errorf("routed changeset must be PROPOSED, never auto-authorized")
	}
}

func TestFeedbackIntake_CatalogSinkPopulatesKindAndFiledAgainstFromContext(t *testing.T) {
	root := t.TempDir()
	writeIntakeCatalog(t, root)
	s := intakeServer(root, map[string]FeedbackRoute{
		"pog-portal": {Sink: "catalog", Catalog: "catalog.yaml", Type: "portal-feedback", Fields: []string{"summary", "report"}},
	})

	bundle := `{"idempotencyKey":"fb-routed-2","kind":"bug","reviewed":true,"userText":"stage pill drifts on this node",` +
		`"anchor":{"producer":"pog-portal"},"context":{"nodeIds":["feature-portal-feedback"]}}`
	rec := postFeedback(t, s, bundle)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(filepath.Join(root, "catalog.yaml"))
	if err != nil {
		t.Fatalf("re-read catalog: %v", err)
	}
	text := string(raw)
	for _, want := range []string{"kind: bug", "filed_against", "feature-portal-feedback"} {
		if !strings.Contains(text, want) {
			t.Errorf("catalog missing %q after routed propose:\n%s", want, text)
		}
	}
}

func TestFeedbackIntake_CatalogSinkGatedByProducerKey(t *testing.T) {
	root := t.TempDir()
	writeIntakeCatalog(t, root)
	before, err := os.ReadFile(filepath.Join(root, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := intakeServer(root, map[string]FeedbackRoute{
		"pog-portal": {Sink: "catalog", Catalog: "catalog.yaml", Type: "portal-feedback"},
	})

	// Same rule table, different producer: local sink only, catalog untouched.
	bundle := `{"idempotencyKey":"fb-other-1","kind":"bug","reviewed":true,"userText":"not routed","anchor":{"producer":"slidey-studio"}}`
	resp := decodeIntakeResp(t, postFeedback(t, s, bundle))
	routed, _ := resp["routed"].([]any)
	if len(routed) != 1 {
		t.Fatalf("routed = %v, want local only", resp["routed"])
	}
	m, _ := routed[0].(map[string]any)
	if m["sink"] != "local" {
		t.Errorf("routed[0].sink = %v, want local", m["sink"])
	}
	after, err := os.ReadFile(filepath.Join(root, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("catalog changed for an unrouted producer")
	}

	// A "local" rule likewise never proposes.
	s2 := intakeServer(root, map[string]FeedbackRoute{"pog-portal": {Sink: "local"}})
	resp2 := decodeIntakeResp(t, postFeedback(t, s2, `{"idempotencyKey":"fb-local-rule","reviewed":true,"userText":"local rule","anchor":{"producer":"pog-portal"}}`))
	routed2, _ := resp2["routed"].([]any)
	if len(routed2) != 1 {
		t.Errorf("sink \"local\" rule proposed anyway: %v", resp2["routed"])
	}
}

// A propose failure (here: the routed catalog does not exist) degrades to a
// routing_errors entry on a 200 — the JSONL record already landed and the
// receipt contract must hold.
func TestFeedbackIntake_CatalogSinkFailureIsNonBlocking(t *testing.T) {
	root := t.TempDir()
	s := intakeServer(root, map[string]FeedbackRoute{
		"pog-portal": {Sink: "catalog", Catalog: "does-not-exist.yaml", Type: "portal-feedback"},
	})
	rec := postFeedback(t, s, `{"idempotencyKey":"fb-degrade","reviewed":true,"userText":"degrade please","anchor":{"producer":"pog-portal"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (catalog sink is non-blocking)", rec.Code)
	}
	resp := decodeIntakeResp(t, rec)
	if resp["ref"] != "fb-degrade" {
		t.Errorf("ref = %v, want fb-degrade", resp["ref"])
	}
	errs, _ := resp["routing_errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("routing_errors = %v, want one catalog entry", resp["routing_errors"])
	}
	if lines := feedbackJSONLLines(t, root); len(lines) != 1 {
		t.Errorf("local record must land despite the routing failure")
	}
}
