package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/reportcontract"
	"kitsoki/internal/runstatus"
)

func TestMetaImproveReport_FilesLocalEvidenceBundle(t *testing.T) {
	root := t.TempDir()
	s := NewWithSource(bugTraceSource{events: []runstatus.TraceEvent{{
		Msg:       "turn.input",
		SessionID: "sess-1",
		Turn:      1,
		StatePath: "main.done",
		Attrs:     map[string]any{"input": "unexpected output, please improve"},
	}}}, WithBugRoot(root))

	prev, rerr := s.bugPreview(map[string]any{"har_json": browserImproveHAR})
	if rerr != nil {
		t.Fatalf("bugPreview: %+v", rerr)
	}
	res, rerr := s.metaImproveReportContext(context.Background(), map[string]any{
		"session_id":   "sess-1",
		"mode":         "story.improve",
		"chat_id":      "chat-1",
		"capture_id":   prev.(map[string]any)["capture_id"].(string),
		"destination":  "local",
		"report":       "Introspection report\n\nTool and permission notes:\nRemove a wasted shell permission.",
		"guidance":     "Operator guidance: avoid the false start next run.",
		"rrweb_events": `[{"type":4,"data":{"href":"http://127.0.0.1/"}},{"type":2,"data":{}}]`,
		"console_logs": `[{"level":"warn","ts":1,"text":"slow improve capture"}]`,
	})
	if rerr != nil {
		t.Fatalf("metaImproveReportContext: %+v", rerr)
	}
	out := res.(map[string]any)
	if out["sink"] != "local-artifact" {
		t.Fatalf("sink = %#v, want local-artifact; result=%#v", out["sink"], out)
	}
	if out["report_kind"] != reportcontract.KindMetaImprove.String() {
		t.Fatalf("report_kind = %#v", out["report_kind"])
	}

	mdPath := filepath.Join(root, filepath.FromSlash(out["path"].(string)))
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read report md: %v", err)
	}
	md := string(mdData)
	for _, want := range []string{
		"## Introspection Report",
		"Operator guidance: avoid the false start",
		"## Evidence Bundle",
		"scrubbed HAR",
		"session replay",
		"## Posting",
		"## Artifacts",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}

	artifactsDir := filepath.Join(root, filepath.FromSlash(out["artifacts_path"].(string)))
	for _, name := range []string{
		reportcontract.ArtifactHAR,
		reportcontract.ArtifactRRWeb,
		reportcontract.ArtifactConsole,
		reportcontract.ArtifactTrace,
	} {
		if _, err := os.Stat(filepath.Join(artifactsDir, name)); err != nil {
			t.Fatalf("expected artifact %s: %v", name, err)
		}
	}
}

func TestMetaImproveReport_PostsThroughTicketProvider(t *testing.T) {
	root := t.TempDir()
	provider := filepath.Join(root, "private_provider.star")
	if err := os.WriteFile(provider, []byte(`
def create(ctx):
    return {"data": {
        "id": "PRIVATE-1",
        "url": "https://tickets.example.invalid/PRIVATE-1",
        "title": ctx.inputs["title"],
        "local_report": ctx.inputs["local_report_path"],
    }}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider+".yaml", []byte("kind: ticket_provider/v1\nhttp:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewWithSource(bugTraceSource{events: []runstatus.TraceEvent{{
		Msg:       "state.entered",
		SessionID: "sess-2",
		Turn:      1,
		StatePath: "main.done",
	}}}, WithBugRoot(root), WithImproveTicketProvider(provider))

	prev, rerr := s.bugPreview(map[string]any{"har_json": browserImproveHAR})
	if rerr != nil {
		t.Fatalf("bugPreview: %+v", rerr)
	}
	res, rerr := s.metaImproveReportContext(context.Background(), map[string]any{
		"session_id":  "sess-2",
		"capture_id":  prev.(map[string]any)["capture_id"].(string),
		"destination": "configured",
		"report":      "Introspection report\n\nRecommended change: tune the story prompt.",
	})
	if rerr != nil {
		t.Fatalf("metaImproveReportContext: %+v", rerr)
	}
	out := res.(map[string]any)
	if out["sink"] != "ticket-provider" {
		t.Fatalf("sink = %#v, want ticket-provider; result=%#v", out["sink"], out)
	}
	if out["provider_ok"] != true {
		t.Fatalf("provider_ok = %#v; result=%#v", out["provider_ok"], out)
	}
	if out["url"] != "https://tickets.example.invalid/PRIVATE-1" {
		t.Fatalf("url = %#v", out["url"])
	}
	localPath, _ := out["local_path"].(string)
	if localPath == "" {
		t.Fatalf("missing local_path in result: %#v", out)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(localPath))); err != nil {
		t.Fatalf("local report missing after provider post: %v", err)
	}
}

const browserImproveHAR = `{
  "log": {
    "version": "1.2",
    "creator": {"name": "kitsoki-browser", "version": "1"},
    "entries": [{
      "startedDateTime": "2026-07-08T00:00:00Z",
      "time": 12,
      "comment": "json-rpc runstatus.meta.send",
      "request": {
        "method": "POST",
        "url": "http://127.0.0.1:7777/rpc?token=super-secret-token",
        "headers": [{"name":"Authorization","value":"Bearer super-secret-token"}],
        "queryString": [{"name":"token","value":"super-secret-token"}],
        "postData": {"mimeType":"application/json","text":"{\"method\":\"runstatus.meta.send\"}"}
      },
      "response": {
        "status": 200,
        "headers": [{"name":"Content-Type","value":"application/json"}],
        "content": {"size": 15, "mimeType": "application/json", "text": "{\"ok\":true}"}
      }
    }]
  }
}`
