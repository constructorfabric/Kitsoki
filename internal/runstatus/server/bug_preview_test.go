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
	har, found := s.takeCapture(id)
	if !found {
		t.Fatalf("capture %q not held", id)
	}
	blob := harMustMarshal(t, har)
	if strings.Contains(blob, "super-secret-token") {
		t.Fatalf("held HAR leaks Authorization token: %s", blob)
	}
	if !strings.Contains(blob, "[REDACTED]") {
		t.Fatalf("held HAR not scrubbed: %s", blob)
	}

	// takeCapture removes it: a second take fails.
	if _, again := s.takeCapture(id); again {
		t.Fatalf("capture should be removed after take")
	}
}
