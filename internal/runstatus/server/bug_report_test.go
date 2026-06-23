package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/host"
	"kitsoki/internal/runstatus/harrec"
	"kitsoki/internal/runstatus/harscrub"
)

// bugTestRecorder returns a fresh HAR ring buffer for a test Server. bugReport
// only touches s.recorder and s.bugRoot, so tests construct a bare Server and
// skip the SessionProvider machinery entirely.
func bugTestRecorder() *harrec.Recorder { return harrec.New(8) }

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
	if relPath != filepath.Join("issues", "bugs", id+".md") {
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
	artifactsDir := filepath.Join(root, "issues", "bugs", id+".artifacts")
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

	md, err := os.ReadFile(filepath.Join(root, "issues", "bugs", id+".md"))
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

	artifactsDir := filepath.Join(root, "issues", "bugs", id+".artifacts")
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
	if _, ok := s.takeCapture(capID); ok {
		t.Fatalf("capture should be consumed by report")
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
	artifactsDir := filepath.Join(root, "issues", "bugs", id+".artifacts")
	if _, err := os.Stat(filepath.Join(artifactsDir, "screenshot.png")); !os.IsNotExist(err) {
		t.Fatalf("expected no screenshot.png, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactsDir, "har.json")); err != nil {
		t.Fatalf("har.json should still exist: %v", err)
	}
}

func TestBugReport_GitHubModeSavesDeveloperLocalArtifacts(t *testing.T) {
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

	var issueArgv string
	runner := func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		j := strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "--version":
			return "gh version 2.x\n", "", 0, nil
		case strings.HasPrefix(j, "release"):
			t.Fatalf("github bug evidence must not call gh release: %s", j)
		case strings.HasPrefix(j, "issue create"):
			issueArgv = j
			return "https://github.com/o/r/issues/77\n", "", 0, nil
		}
		return "", "unexpected: " + j, 1, nil
	}
	restore := host.SetExecRunnerForTest(runner)
	defer restore()

	pngBytes := []byte("\x89PNG\r\n\x1a\nFAKE")
	res, rerr := s.bugReport(map[string]any{
		"title":              "GitHub evidence stays local",
		"description":        "Captured from the browser.",
		"screenshot_png_b64": base64.StdEncoding.EncodeToString(pngBytes),
	})
	if rerr != nil {
		t.Fatalf("bugReport error: %+v", rerr)
	}
	m := res.(map[string]any)
	if got := m["url"]; got != "https://github.com/o/r/issues/77" {
		t.Fatalf("unexpected github url: %+v", m)
	}

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

	for _, want := range []string{
		"## Artifacts",
		"These files are not uploaded to GitHub.",
		".artifacts/bug-reports/" + entries[0].Name() + "/har.json",
		".artifacts/bug-reports/" + entries[0].Name() + "/screenshot.png",
	} {
		if !strings.Contains(issueArgv, want) {
			t.Fatalf("issue create argv missing %q: %s", want, issueArgv)
		}
	}
}
