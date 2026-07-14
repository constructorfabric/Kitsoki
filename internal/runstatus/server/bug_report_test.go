package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/ghagent/bugdeck"
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/harrec"
	"kitsoki/internal/runstatus/harscrub"
)

// bugTestRecorder returns a fresh HAR ring buffer for a test Server. bugReport
// only touches s.recorder and s.bugRoot, so tests construct a bare Server and
// skip the SessionProvider machinery entirely.
func bugTestRecorder() *harrec.Recorder { return harrec.New(8) }

type bugTraceSource struct {
	events []runstatus.TraceEvent
}

func (s bugTraceSource) Snapshot() (runstatus.Snapshot, error) {
	return runstatus.Snapshot{
		Session: runstatus.SessionHeader{SessionID: "sess-1", Turn: 1, CurrentState: "main.idle"},
		Events:  s.events,
	}, nil
}

func (s bugTraceSource) Events() ([]runstatus.TraceEvent, error) { return s.events, nil }
func (s bugTraceSource) AppDef() *app.AppDef                     { return nil }

// harMustMarshal marshals a held HAR to a string for substring assertions.
func harMustMarshal(t *testing.T, h *harscrub.Har) string {
	t.Helper()
	b, err := harscrub.Marshal(h)
	if err != nil {
		t.Fatalf("marshal har: %v", err)
	}
	return string(b)
}

func TestBugReport_WritesBugAndScrubbedHAR(t *testing.T) {
	root := t.TempDir()

	s := &Server{
		recorder: bugTestRecorder(),
		bugRoot:  root,
	}

	// Record a couple of fake /rpc exchanges, one carrying an Authorization
	// header that scrubbing must redact.
	s.recorder.Record(
		"POST", "/rpc",
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer super-secret-token"},
		[]byte(`{"jsonrpc":"2.0","method":"runstatus.sessions.list","params":{}}`),
		200, map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","result":[]}`),
		time.Now().UTC(), 1.5,
	)
	s.recorder.Record(
		"POST", "/rpc",
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","method":"runstatus.session.get","params":{"session_id":"abc"}}`),
		200, map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","result":{"id":"abc"}}`),
		time.Now().UTC(), 2.0,
	)

	pngBytes := []byte("\x89PNG\r\n\x1a\nFAKE")
	params := map[string]any{
		"title":              "Foyer button does nothing",
		"body":               "Clicked the foyer button and nothing happened.",
		"severity":           "med",
		"repro_steps":        []any{"open foyer", "click button"},
		"screenshot_png_b64": base64.StdEncoding.EncodeToString(pngBytes),
	}

	res, rerr := s.bugReport(params)
	if rerr != nil {
		t.Fatalf("bugReport returned error: %+v", rerr)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %T", res)
	}
	id, _ := m["id"].(string)
	relPath, _ := m["path"].(string)
	if id == "" || relPath == "" {
		t.Fatalf("missing id/path in result: %+v", m)
	}
	if relPath != filepath.Join(".artifacts", "issues", "bugs", id+".md") {
		t.Fatalf("unexpected rel path %q for id %q", relPath, id)
	}

	// <id>.md exists.
	mdPath := filepath.Join(root, relPath)
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read bug md: %v", err)
	}
	md := string(mdData)
	if !strings.Contains(md, "Foyer button does nothing") {
		t.Fatalf("bug md missing title: %s", md)
	}
	if !strings.Contains(md, "## Artifacts") {
		t.Fatalf("bug md missing Artifacts section: %s", md)
	}
	if !strings.Contains(md, "./"+id+".artifacts/har.json") {
		t.Fatalf("bug md missing har link: %s", md)
	}
	if !strings.Contains(md, "./"+id+".artifacts/screenshot.png") {
		t.Fatalf("bug md missing screenshot link: %s", md)
	}

	// <id>.artifacts/har.json exists and is scrubbed.
	artifactsDir := filepath.Join(root, ".artifacts", "issues", "bugs", id+".artifacts")
	harData, err := os.ReadFile(filepath.Join(artifactsDir, "har.json"))
	if err != nil {
		t.Fatalf("read har.json: %v", err)
	}
	har := string(harData)
	if strings.Contains(har, "super-secret-token") {
		t.Fatalf("har.json leaks Authorization token: %s", har)
	}
	if !strings.Contains(har, "[REDACTED]") {
		t.Fatalf("har.json not scrubbed (no [REDACTED]): %s", har)
	}
	// Sanity: it parses as JSON and has two entries.
	var parsed map[string]any
	if err := json.Unmarshal(harData, &parsed); err != nil {
		t.Fatalf("har.json invalid: %v", err)
	}

	// screenshot.png exists with our bytes.
	pngOut, err := os.ReadFile(filepath.Join(artifactsDir, "screenshot.png"))
	if err != nil {
		t.Fatalf("read screenshot.png: %v", err)
	}
	if string(pngOut) != string(pngBytes) {
		t.Fatalf("screenshot bytes mismatch")
	}
}

func TestBugReport_CaptureIDPath_WithEvidence(t *testing.T) {
	root := t.TempDir()
	s := &Server{recorder: bugTestRecorder(), bugRoot: root, captureStore: nil}

	s.recorder.Record(
		"POST", "/rpc",
		map[string]string{"Authorization": "Bearer super-secret-token"},
		[]byte(`{"jsonrpc":"2.0","method":"runstatus.sessions.list","params":{}}`),
		200, map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","result":[]}`),
		time.Now().UTC(), 1.0,
	)

	// Preview holds the scrubbed snapshot under a capture id.
	prev, rerr := s.bugPreview(nil)
	if rerr != nil {
		t.Fatalf("bugPreview error: %+v", rerr)
	}
	capID := prev.(map[string]any)["capture_id"].(string)

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp/home"
		t.Setenv("HOME", home)
	}
	consoleLogs := `[{"level":"error","ts":1,"text":"failed reading ` + home + `/secret.txt"},{"level":"warn","ts":2,"text":"slow"}]`
	errorInfo := `{"errors":["boom","bang"],"last_rpc":{"method":"runstatus.session.get","code":-32000,"message":"nope"}}`
	rrweb := `[{"type":2,"data":{"href":"` + home + `/app"}}]`

	params := map[string]any{
		"title":        "Review-before-file flow",
		"capture_id":   capID,
		"description":  "Operator saw a stuck spinner after clicking Drive.",
		"console_logs": consoleLogs,
		"error_info":   errorInfo,
		"rrweb_events": rrweb,
	}
	res, rerr := s.bugReport(params)
	if rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}
	id := res.(map[string]any)["id"].(string)

	md, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", id+".md"))
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	mds := string(md)
	if !strings.Contains(mds, "Operator saw a stuck spinner") {
		t.Fatalf("md missing description: %s", mds)
	}
	if !strings.Contains(mds, "## Error state") {
		t.Fatalf("md missing Error state section: %s", mds)
	}
	if !strings.Contains(mds, "Captured errors: 2") {
		t.Fatalf("md missing error count: %s", mds)
	}
	if !strings.Contains(mds, "## Console (recent)") {
		t.Fatalf("md missing Console section: %s", mds)
	}
	for _, want := range []string{
		"## Evidence-derived triage",
		"Generated deterministically from captured evidence",
		"rrweb: 1 event(s)",
		"Console/error state: 2 console entries",
		"last RPC: `runstatus.session.get` code=-32000 message=\"nope\"",
		"Not deterministically captured in the evidence",
	} {
		if !strings.Contains(mds, want) {
			t.Fatalf("md missing evidence triage %q: %s", want, mds)
		}
	}

	artifactsDir := filepath.Join(root, ".artifacts", "issues", "bugs", id+".artifacts")
	for _, f := range []string{"har.json", "rrweb.json", "console.json"} {
		if _, err := os.Stat(filepath.Join(artifactsDir, f)); err != nil {
			t.Fatalf("expected artifact %s: %v", f, err)
		}
	}

	consoleData, _ := os.ReadFile(filepath.Join(artifactsDir, "console.json"))
	if strings.Contains(string(consoleData), home+"/secret.txt") {
		t.Fatalf("console.json leaks $HOME path: %s", consoleData)
	}
	if !strings.Contains(string(consoleData), "$HOME/secret.txt") {
		t.Fatalf("console.json $HOME not substituted: %s", consoleData)
	}

	// capture_id consumed: a second report with it falls back to fresh snapshot.
	if _, _, ok := s.takeCapture(capID); ok {
		t.Fatalf("capture should be consumed by report")
	}
}

func TestBugReport_CaptureIDPath_UsesBrowserHAR(t *testing.T) {
	root := t.TempDir()
	s := &Server{recorder: bugTestRecorder(), bugRoot: root}
	s.recorder.Record(
		"POST", "/rpc",
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","method":"runstatus.sessions.list","params":{}}`),
		200, map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","result":[]}`),
		time.Now().UTC(), 1.0,
	)

	browserHAR := `{
	  "log": {
	    "version": "1.2",
	    "creator": {"name": "kitsoki-browser", "version": "1"},
	    "entries": [{
	      "startedDateTime": "2026-07-08T00:00:00Z",
	      "time": 12,
	      "comment": "json-rpc runstatus.session.turn",
	      "request": {
	        "method": "GET",
	        "url": "https://example.test/api/items?token=super-secret-token",
	        "headers": [{"name":"Authorization","value":"Bearer super-secret-token"}],
	        "queryString": [{"name":"token","value":"super-secret-token"}]
	      },
	      "response": {
	        "status": 503,
	        "headers": [{"name":"Content-Type","value":"application/json"}],
	        "content": {"size": 15, "mimeType": "application/json", "text": "{\"error\":\"bad\"}"}
	      }
	    }]
	  }
	}`
	prev, rerr := s.bugPreview(map[string]any{"har_json": browserHAR})
	if rerr != nil {
		t.Fatalf("bugPreview error: %+v", rerr)
	}

	res, rerr := s.bugReport(map[string]any{
		"title":       "Browser HAR flow",
		"description": "Operator saw a browser request fail.",
		"capture_id":  prev.(map[string]any)["capture_id"].(string),
	})
	if rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}
	id := res.(map[string]any)["id"].(string)

	md, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", id+".md"))
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	if !strings.Contains(string(md), "browser-observed network capture") {
		t.Fatalf("md does not describe browser HAR source: %s", md)
	}

	harData, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", id+".artifacts", "har.json"))
	if err != nil {
		t.Fatalf("read har.json: %v", err)
	}
	har := string(harData)
	if !strings.Contains(har, "https://example.test/api/items") {
		t.Fatalf("har.json missing browser URL: %s", har)
	}
	if strings.Contains(har, "\"url\": \"/rpc\"") {
		t.Fatalf("har.json fell back to server /rpc recorder: %s", har)
	}
	if strings.Contains(har, "super-secret-token") {
		t.Fatalf("har.json leaks token: %s", har)
	}
	if !strings.Contains(har, "[REDACTED]") {
		t.Fatalf("har.json missing redaction: %s", har)
	}
}

func TestBugReport_WritesRedactedTraceArtifact(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	diagnosticText := "email alice@example.com and read " + home + "/secret.txt"

	s := NewWithSource(bugTraceSource{events: []runstatus.TraceEvent{
		{
			Msg:       "turn.input",
			SessionID: "sess-1",
			Turn:      1,
			StatePath: "main.idle",
			Attrs: map[string]any{
				"input":  diagnosticText,
				"intent": "report_bug",
				"slots": map[string]any{
					"description": diagnosticText,
					"count":       float64(3),
				},
			},
		},
		{
			Msg:       "machine.transition",
			SessionID: "sess-1",
			Turn:      1,
			StatePath: "main.idle",
			Attrs: map[string]any{
				"from": "main.idle",
				"to":   "main.done",
			},
		},
	}}, WithBugRoot(root))

	res, rerr := s.bugReport(map[string]any{
		"title":     "trace privacy",
		"body":      "attach the trace",
		"trace_ref": "sess-1",
	})
	if rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}
	id := res.(map[string]any)["id"].(string)

	traceData, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", id+".artifacts", "trace.redacted.jsonl"))
	if err != nil {
		t.Fatalf("read trace.redacted.jsonl: %v", err)
	}
	trace := string(traceData)
	for _, leaked := range []string{"alice@example.com", home + "/secret.txt"} {
		if strings.Contains(trace, leaked) {
			t.Fatalf("redacted trace leaks %q: %s", leaked, trace)
		}
	}
	for _, want := range []string{`"msg":"turn.input"`, `"intent":"report_bug"`, `"count":3`, `"to":"main.done"`} {
		if !strings.Contains(trace, want) {
			t.Fatalf("redacted trace missing %q: %s", want, trace)
		}
	}

	var first map[string]any
	firstLine := strings.Split(strings.TrimSpace(trace), "\n")[0]
	if err := json.Unmarshal([]byte(firstLine), &first); err != nil {
		t.Fatalf("parse first trace event: %v", err)
	}
	inputAlias, _ := first["input"].(string)
	slots, _ := first["slots"].(map[string]any)
	descAlias, _ := slots["description"].(string)
	if inputAlias == "" || !strings.HasPrefix(inputAlias, "[TEXT#") {
		t.Fatalf("input was not aliased: %#v in %s", first["input"], trace)
	}
	if inputAlias != descAlias {
		t.Fatalf("same sensitive text should reuse alias, input=%q description=%q trace=%s", inputAlias, descAlias, trace)
	}

	md, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", id+".md"))
	if err != nil {
		t.Fatalf("read bug md: %v", err)
	}
	if !strings.Contains(string(md), "./"+id+".artifacts/trace.redacted.jsonl") {
		t.Fatalf("bug md missing redacted trace artifact link: %s", md)
	}
	for _, want := range []string{
		"## Evidence-derived triage",
		"Trace: 2 event(s) across 1 turn(s); states: `main.idle`; intents: `report_bug`",
		"In state `main.idle`, submit intent `report_bug`; observed transition `main.idle` -> `main.done`.",
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("bug md missing trace-derived triage %q: %s", want, md)
		}
	}
}

func TestBugReport_NoScreenshot_SkipsFile(t *testing.T) {
	root := t.TempDir()
	s := &Server{recorder: bugTestRecorder(), bugRoot: root}

	res, rerr := s.bugReport(map[string]any{"title": "x", "body": "y"})
	if rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}
	id := res.(map[string]any)["id"].(string)
	artifactsDir := filepath.Join(root, ".artifacts", "issues", "bugs", id+".artifacts")
	if _, err := os.Stat(filepath.Join(artifactsDir, "screenshot.png")); !os.IsNotExist(err) {
		t.Fatalf("expected no screenshot.png, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactsDir, "har.json")); err != nil {
		t.Fatalf("har.json should still exist: %v", err)
	}
}

func TestBugReport_PrivacySubstitutesHighEntropyAndFilesFollowUp(t *testing.T) {
	root := t.TempDir()
	s := &Server{recorder: bugTestRecorder(), bugRoot: root}
	secret := "mF9xQ2rT8vLp0AqZ7nByC4dEuGhJkM3sW6yI"

	res, rerr := s.bugReport(map[string]any{
		"title": "opaque token leak",
		"body":  "response contained " + secret,
	})
	if rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}
	id := res.(map[string]any)["id"].(string)
	md, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", id+".md"))
	if err != nil {
		t.Fatalf("read filed bug: %v", err)
	}
	if strings.Contains(string(md), secret) {
		t.Fatalf("filed bug leaked high-entropy value: %s", md)
	}
	if !strings.Contains(string(md), "[REDACTED]") {
		t.Fatalf("filed bug should contain deterministic replacement: %s", md)
	}
	privacy := res.(map[string]any)["privacy"].(bugprivacy.Result)
	if privacy.FollowUpPath == "" {
		t.Fatalf("expected follow-up path in privacy result: %#v", privacy)
	}
	followUp, err := os.ReadFile(filepath.Join(root, privacy.FollowUpPath))
	if err != nil {
		t.Fatalf("read follow-up: %v", err)
	}
	if strings.Contains(string(followUp), secret) || !strings.Contains(string(followUp), "high_entropy") {
		t.Fatalf("follow-up should be depersonalized and categorized: %s", followUp)
	}
}

func TestBugReport_PrivacyAgentFailureBlocksOriginalAndFilesFollowUp(t *testing.T) {
	root := t.TempDir()
	s := &Server{
		recorder: bugTestRecorder(),
		bugRoot:  root,
		bugPrivacyChecker: bugprivacy.CheckFunc(func(context.Context, bugprivacy.Report, []bugprivacy.Finding) (bugprivacy.Decision, error) {
			return bugprivacy.Decision{Pass: false, Categories: []string{"customer_data"}, Reason: "contains customer data"}, nil
		}),
	}

	_, rerr := s.bugReport(map[string]any{
		"title": "customer report",
		"body":  "body intentionally bland for test",
	})
	if rerr == nil {
		t.Fatal("expected privacy failure")
	}
	if !strings.Contains(rerr.Message, "privacy check failed") || !strings.Contains(rerr.Message, "depersonalized follow-up") {
		t.Fatalf("unexpected error: %#v", rerr)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".artifacts", "issues", "bugs"))
	if err != nil {
		t.Fatalf("read bugs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the follow-up bug, got %d entries", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", entries[0].Name()))
	if err != nil {
		t.Fatalf("read follow-up: %v", err)
	}
	if strings.Contains(string(data), "customer report") || !strings.Contains(string(data), "customer_data") {
		t.Fatalf("follow-up should omit original title and include category: %s", data)
	}
}

func TestBugReport_PrivacyResolverReceivesHarnessSelectionFromTraceRef(t *testing.T) {
	root := t.TempDir()
	want := orchestrator.ProfileSelection{Profile: "codex-native", Model: "gpt-5.5", Effort: "low"}
	drv := &harnessDriver{sel: want}
	s := newHarnessServer(drv)
	s.bugRoot = root

	var got orchestrator.ProfileSelection
	s.bugPrivacyCheckerResolver = func(ctx BugPrivacyContext) bugprivacy.Checker {
		got = ctx.Selection
		return bugprivacy.CheckFunc(func(context.Context, bugprivacy.Report, []bugprivacy.Finding) (bugprivacy.Decision, error) {
			return bugprivacy.Decision{Pass: true, Reason: "safe"}, nil
		})
	}

	if _, rerr := s.bugReport(map[string]any{
		"title":     "selection-aware privacy",
		"body":      "plain report",
		"trace_ref": "sess-1",
	}); rerr != nil {
		t.Fatalf("bugReport returned error: %+v", rerr)
	}
	if got != want {
		t.Fatalf("resolver selection = %+v, want %+v", got, want)
	}
}

func TestBugStatusLocalModeCanFile(t *testing.T) {
	s := &Server{}

	res, rerr := s.bugStatus(context.Background())
	if rerr != nil {
		t.Fatalf("bugStatus error: %+v", rerr)
	}
	m := res.(map[string]any)
	if m["mode"] != "local-artifact" || m["can_file"] != true || m["path"] != filepath.Join(".artifacts", "issues", "bugs") {
		t.Fatalf("unexpected local status: %#v", m)
	}
}

func TestBugStatusGitHubMissingAuthWarns(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	restoreGHCLI := host.SetGHCLITokenForTest(func(context.Context) string { return "" })
	defer restoreGHCLI()

	s := &Server{ticketRepo: "o/r"}
	res, rerr := s.bugStatus(context.Background())
	if rerr != nil {
		t.Fatalf("bugStatus error: %+v", rerr)
	}
	m := res.(map[string]any)
	if m["mode"] != "github" || m["repo"] != "o/r" || m["can_file"] != false {
		t.Fatalf("unexpected github status: %#v", m)
	}
	for _, want := range []string{"Report bug cannot file", "Filing bugs is critical", "kitsoki gh-agent login"} {
		if !strings.Contains(m["warning"].(string), want) {
			t.Fatalf("warning missing %q: %#v", want, m)
		}
	}
	if !strings.Contains(m["setup_hint"].(string), "GitHub auth is not configured") {
		t.Fatalf("setup hint missing auth guidance: %#v", m)
	}
}

// TestBugReport_GitHubModeUploadsArtifacts proves the web GitHub-filing path
// uploads the captured evidence as release assets and links the public URLs in
// the issue (so anyone can open them), while still keeping a local .artifacts/
// copy for developer review.
func TestBugReport_GitHubModeUploadsArtifacts(t *testing.T) {
	root := t.TempDir()
	s := &Server{recorder: bugTestRecorder(), bugRoot: root, ticketRepo: "o/r"}

	s.recorder.Record(
		"POST", "/rpc",
		map[string]string{"Authorization": "Bearer super-secret-token"},
		[]byte(`{"jsonrpc":"2.0","method":"runstatus.session.get","params":{"session_id":"abc"}}`),
		200, map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","result":{"id":"abc"}}`),
		time.Now().UTC(), 1.0,
	)

	issueBody, uploads, restoreAPI := stubBugReportGitHubAPI(t, "77")
	defer restoreAPI()

	pngBytes := []byte("\x89PNG\r\n\x1a\nFAKE")
	res, rerr := s.bugReport(map[string]any{
		"title":              "GitHub evidence is uploaded",
		"description":        "Captured from the browser.",
		"screenshot_png_b64": base64.StdEncoding.EncodeToString(pngBytes),
		"story_path":         "../../../testdata/apps/cloak/app.yaml",
	})
	if rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}
	m := res.(map[string]any)
	if got := m["url"]; got != "https://github.com/o/r/issues/77" {
		t.Fatalf("unexpected github url: %+v", m)
	}

	// Both the HAR and the screenshot are uploaded as release assets.
	if *uploads != 2 {
		t.Fatalf("expected 2 release uploads, got %d", *uploads)
	}

	// A local .artifacts/ copy is still kept for developer review.
	artifactsRoot := filepath.Join(root, ".artifacts", "bug-reports")
	entries, err := os.ReadDir(artifactsRoot)
	if err != nil {
		t.Fatalf("read artifacts root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one artifact dir, got %d", len(entries))
	}
	artifactDir := filepath.Join(artifactsRoot, entries[0].Name())
	for _, f := range []string{"har.json", "screenshot.png"} {
		if _, err := os.Stat(filepath.Join(artifactDir, f)); err != nil {
			t.Fatalf("expected artifact %s: %v", f, err)
		}
	}
	harData, err := os.ReadFile(filepath.Join(artifactDir, "har.json"))
	if err != nil {
		t.Fatalf("read har.json: %v", err)
	}
	if strings.Contains(string(harData), "super-secret-token") {
		t.Fatalf("har.json leaks Authorization token: %s", harData)
	}

	// The issue body links the public release-asset URLs, not local paths.
	for _, want := range []string{
		"## Artifacts",
		"## Evidence-derived triage",
		"uploaded as GitHub release assets",
		"![Screenshot](https://github.com/o/r/releases/download/kitsoki-artifacts/",
		"(https://github.com/o/r/releases/download/kitsoki-artifacts/",
		"engine_version: 0.0.1-scaffold",
		"engine_checksum_sha256: sha256:",
		"story_app_id: cloak-of-darkness",
		"story_checksum_sha256: sha256:",
	} {
		if !strings.Contains(*issueBody, want) {
			t.Fatalf("issue body missing %q: %s", want, *issueBody)
		}
	}
	if strings.Contains(*issueBody, "not uploaded to GitHub") {
		t.Fatalf("upload path must drop the not-uploaded disclaimer: %s", *issueBody)
	}
}

// TestBugReport_GitHubModeDepositsAgentEvidence proves the web filer deposits the
// scrubbed rrweb + HAR onto the agent's evidence store, keyed by the same DeckID
// the agent's webhook will look up — so the agent never re-downloads assets.
func TestBugReport_GitHubModeDepositsAgentEvidence(t *testing.T) {
	root := t.TempDir()
	evidenceDir := t.TempDir()
	s := &Server{recorder: bugTestRecorder(), bugRoot: root, ticketRepo: "o/r", agentEvidenceDir: evidenceDir}
	s.recorder.Record(
		"POST", "/rpc",
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","method":"runstatus.session.get"}`),
		200, map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","result":{}}`),
		time.Now().UTC(), 1.0,
	)

	_, _, restoreAPI := stubBugReportGitHubAPI(t, "77")
	defer restoreAPI()

	if _, rerr := s.bugReport(map[string]any{
		"title":        "deposit me",
		"description":  "x",
		"rrweb_events": `[{"type":2,"data":{}}]`,
	}); rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}

	// Evidence landed under <evidenceDir>/<DeckID("o/r","77")>/.
	depDir := filepath.Join(evidenceDir, bugdeck.DeckID("o/r", "77"))
	for _, f := range []string{"rrweb.json", "har.json"} {
		if _, err := os.Stat(filepath.Join(depDir, f)); err != nil {
			t.Fatalf("expected deposited %s at %s: %v", f, depDir, err)
		}
	}

	// And the agent can load it straight back as deckable evidence.
	store, err := bugdeck.NewEvidenceStore(evidenceDir)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := store.Load(bugdeck.DeckID("o/r", "77"))
	if err != nil {
		t.Fatalf("agent could not load deposited evidence: %v", err)
	}
	if len(ev.RRWeb) == 0 || len(ev.HAR) == 0 {
		t.Fatalf("deposited evidence incomplete: rrweb=%d har=%d", len(ev.RRWeb), len(ev.HAR))
	}
}

func stubBugReportGitHubAPI(t *testing.T, issueNumber string) (*string, *int, func()) {
	t.Helper()
	t.Setenv("GH_TOKEN", "test-token")
	n, err := strconv.Atoi(issueNumber)
	if err != nil {
		t.Fatalf("bad issue number fixture: %v", err)
	}
	var issueBody string
	var uploads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/releases/tags/kitsoki-artifacts":
			writeBugReportJSON(t, w, map[string]any{"id": 1, "upload_url": "http://" + r.Host + "/uploads/assets{?name,label}"})
		case r.Method == http.MethodPost && r.URL.Path == "/uploads/assets":
			uploads++
			writeBugReportJSON(t, w, map[string]any{"id": uploads})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode issue payload: %v", err)
			}
			issueBody, _ = payload["body"].(string)
			writeBugReportJSON(t, w, map[string]any{
				"number":   n,
				"html_url": "https://github.com/o/r/issues/" + issueNumber,
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	return &issueBody, &uploads, func() {
		restore()
		srv.Close()
	}
}

func writeBugReportJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
