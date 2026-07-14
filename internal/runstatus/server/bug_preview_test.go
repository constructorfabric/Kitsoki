package server

import (
	"strings"
	"testing"
	"time"
)

func TestBugPreview_ReturnsCaptureAndScrubbedHAR(t *testing.T) {
	s := &Server{recorder: bugTestRecorder()}

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

	res, rerr := s.bugPreview(nil)
	if rerr != nil {
		t.Fatalf("bugPreview error: %+v", rerr)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", res)
	}

	id, _ := m["capture_id"].(string)
	if id == "" {
		t.Fatalf("missing capture_id: %+v", m)
	}
	if m["har"] == nil {
		t.Fatalf("missing har in preview result")
	}
	if _, ok := m["depth"].(int); !ok {
		t.Fatalf("depth not an int: %T", m["depth"])
	}

	// The held capture must be scrubbed: no Authorization token leaks.
	har, source, found := s.takeCapture(id)
	if !found {
		t.Fatalf("capture %q not held", id)
	}
	if source != "server-rpc-recorder" {
		t.Fatalf("source=%q, want server-rpc-recorder", source)
	}
	blob := harMustMarshal(t, har)
	if strings.Contains(blob, "super-secret-token") {
		t.Fatalf("held HAR leaks Authorization token: %s", blob)
	}
	if !strings.Contains(blob, "[REDACTED]") {
		t.Fatalf("held HAR not scrubbed: %s", blob)
	}

	// takeCapture removes it: a second take fails.
	if _, _, again := s.takeCapture(id); again {
		t.Fatalf("capture should be removed after take")
	}
}

func TestBugPreview_UsesBrowserHARWhenProvided(t *testing.T) {
	s := &Server{recorder: bugTestRecorder()}
	s.recorder.Record(
		"POST", "/rpc",
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","method":"runstatus.sessions.list","params":{}}`),
		200, map[string]string{"Content-Type": "application/json"},
		[]byte(`{"jsonrpc":"2.0","result":[]}`),
		time.Now().UTC(), 1.5,
	)

	browserHAR := `{
	  "log": {
	    "version": "1.2",
	    "creator": {"name": "kitsoki-browser", "version": "1"},
	    "entries": [{
	      "startedDateTime": "2026-07-08T00:00:00Z",
	      "time": 12,
	      "comment": "json-rpc runstatus.session.turn token=super-secret-token",
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

	res, rerr := s.bugPreview(map[string]any{"har_json": browserHAR})
	if rerr != nil {
		t.Fatalf("bugPreview error: %+v", rerr)
	}
	m := res.(map[string]any)
	if m["har_source"] != "browser-fetch" {
		t.Fatalf("har_source=%v, want browser-fetch", m["har_source"])
	}
	if m["depth"] != 1 {
		t.Fatalf("depth=%v, want 1", m["depth"])
	}
	id := m["capture_id"].(string)
	har, source, found := s.takeCapture(id)
	if !found {
		t.Fatalf("capture %q not held", id)
	}
	if source != "browser-fetch" {
		t.Fatalf("source=%q, want browser-fetch", source)
	}
	if got := har.Log.Entries[0].Request.URL; strings.Contains(got, "super-secret-token") {
		t.Fatalf("browser HAR URL was not scrubbed: %s", got)
	}
	if got := har.Log.Entries[0].Request.URL; !strings.Contains(got, "token=%5BREDACTED%5D") {
		t.Fatalf("browser HAR URL missing redacted query: %s", got)
	}
	if got := har.Log.Entries[0].Request.URL; strings.Contains(got, "/rpc") {
		t.Fatalf("browser HAR was replaced by server recorder: %s", got)
	}
	if got := har.Log.Entries[0].Comment; strings.Contains(got, "super-secret-token") {
		t.Fatalf("browser HAR comment was not scrubbed: %s", got)
	}
}
