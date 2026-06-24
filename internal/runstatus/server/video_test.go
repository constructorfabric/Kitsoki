package server_test

// video_test.go exercises the three /review feedback-mode RPCs:
//
//   - runstatus.video.frame resolves a RECORDED video handle, grabs a still via
//     a stubbed extractor (no ffmpeg in CI), records it through a stub
//     FrameRecorder, and returns the still handle.
//   - runstatus.video.frame 404s on an unknown/escaping video handle — the
//     non-negotiable escape-path guard: no arbitrary path is extractable.
//   - runstatus.video.chapters reads the slice-1 sidecar (and returns [] when
//     absent).
//   - runstatus.feedback.add appends a well-formed note via a stub sink.
//
// No LLM, no subprocess, no real ffmpeg — the extractor's Runner is stubbed to
// copy fixture PNG bytes (kitsoki test rule: tests never shell ffmpeg).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/video"
)

// stubFrameRecorder records the PNG by reading it (proving the extractor wrote a
// file) and returns a fixed handle.
type stubFrameRecorder struct {
	handle    string
	lastLabel string
	lastBytes []byte
}

func (r *stubFrameRecorder) RecordFrame(pngPath, label string) (string, error) {
	b, err := os.ReadFile(pngPath)
	if err != nil {
		return "", err
	}
	r.lastBytes = b
	r.lastLabel = label
	return r.handle, nil
}

// stubFeedbackSink collects notes in memory.
type stubFeedbackSink struct {
	mu    sync.Mutex
	notes []server.FeedbackNote
}

func (s *stubFeedbackSink) AddFeedback(note server.FeedbackNote) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = append(s.notes, note)
	return nil
}

// fixturePNGRunner is a video.Runner that, instead of shelling ffmpeg, writes
// fixture PNG bytes to the output path (the last positional arg). It lets the
// frame RPC exercise the real video.Frame contract with zero subprocess.
func fixturePNGRunner(content []byte) video.RunnerFunc {
	return func(_ context.Context, _ string, _ string, args ...string) (string, string, error) {
		out := args[len(args)-1]
		return "", "", os.WriteFile(out, content, 0o644)
	}
}

// buildVideoServer wires a single-session-style stub provider with a recorded
// video artifact (handle → on-disk mp4 path) plus the frame recorder and
// feedback sink seams. It returns the server, the video handle, and the seams.
func buildVideoServer(t *testing.T, withChapters bool) (*httptest.Server, string, *stubFrameRecorder, *stubFeedbackSink) {
	t.Helper()
	dir := t.TempDir()
	videoHandle := "demo_video#ab12cd34"
	videoPath := filepath.Join(dir, "demo_video.mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("FAKEMP4"), 0o644))

	if withChapters {
		_, err := video.WriteChapters(videoPath, []video.Chapter{
			{Index: 0, ID: "scene-1", Label: "Intro", StartMs: 0, EndMs: 3000,
				Source: video.SourceRef{Kind: "slidey", SpecPath: "deck.json", SceneID: "intro"}},
		})
		require.NoError(t, err)
	}

	resolver := newStubResolver(map[string]artifactEntry{
		videoHandle: {path: videoPath, mime: "video/mp4"},
	})
	rec := &stubFrameRecorder{handle: "frame#deadbeef"}
	sink := &stubFeedbackSink{}

	p := newStubProvider()
	p.mu.Lock()
	p.entries["sess-vid"] = server.Entry{
		Source:    &stubSource{header: runstatus.SessionHeader{SessionID: "sess-vid"}, def: testDef()},
		Artifacts: resolver,
		Frames:    rec,
		Feedback:  sink,
		// Per-entry fixture runner: copies a fixture PNG instead of shelling
		// ffmpeg. Injected here (not a shared global) so the parallel video tests
		// never race each other's runner.
		FrameRunner: fixturePNGRunner(minimalPNG),
	}
	p.mu.Unlock()

	ts := httptest.NewServer(server.NewMulti(p).Handler())
	t.Cleanup(ts.Close)
	return ts, videoHandle, rec, sink
}

// TestVideoFrame_ResolvesRecordedHandle proves video.frame resolves a recorded
// video handle, extracts via the stubbed runner, records the PNG, and returns
// the still handle + mime + kind.
func TestVideoFrame_ResolvesRecordedHandle(t *testing.T) {
	t.Parallel()

	ts, videoHandle, rec, _ := buildVideoServer(t, false)

	var res struct {
		Handle string `json:"handle"`
		Mime   string `json:"mime"`
		Kind   string `json:"kind"`
	}
	rpcCall(t, ts, "runstatus.video.frame",
		map[string]any{"session_id": "sess-vid", "video": videoHandle, "t_ms": 14000}, &res)

	assert.Equal(t, "frame#deadbeef", res.Handle)
	assert.Equal(t, "image/png", res.Mime)
	assert.Equal(t, "image", res.Kind)
	assert.Equal(t, minimalPNG, rec.lastBytes, "recorder must receive the extracted PNG bytes")
	assert.Contains(t, rec.lastLabel, "0:14", "label carries the human timestamp")
}

// TestVideoFrame_404OnUnknownHandle proves video.frame returns codeNotFound for
// a handle the run never recorded — no ffmpeg shell on attacker input.
func TestVideoFrame_404OnUnknownHandle(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := buildVideoServer(t, false)

	code, _ := rpcCallExpectError(t, ts, "runstatus.video.frame",
		map[string]any{"session_id": "sess-vid", "video": "ghost#00000000", "t_ms": 0})
	assert.Equal(t, -32002, code, "unknown video handle must be codeNotFound")
}

// TestVideoFrame_404OnEscapingPath proves a handle whose resolver returns a
// non-clean / relative path (a smuggled traversal) is rejected — the
// non-negotiable escape-path guard.
func TestVideoFrame_404OnEscapingPath(t *testing.T) {
	t.Parallel()

	resolver := newStubResolver(map[string]artifactEntry{
		"evil#deadbeef": {path: "../../etc/passwd", mime: "video/mp4"},
	})
	p := newStubProvider()
	p.mu.Lock()
	p.entries["sess-vid"] = server.Entry{
		Source:      &stubSource{header: runstatus.SessionHeader{SessionID: "sess-vid"}, def: testDef()},
		Artifacts:   resolver,
		Frames:      &stubFrameRecorder{handle: "frame#x"},
		FrameRunner: fixturePNGRunner(minimalPNG),
	}
	p.mu.Unlock()
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	t.Cleanup(ts.Close)

	code, _ := rpcCallExpectError(t, ts, "runstatus.video.frame",
		map[string]any{"session_id": "sess-vid", "video": "evil#deadbeef", "t_ms": 0})
	assert.Equal(t, -32002, code, "an escaping resolved path must be codeNotFound")
}

// TestVideoChapters_ReadsSidecar proves video.chapters reads the slice-1 sidecar
// for a recorded video, and returns [] when none exists.
func TestVideoChapters_ReadsSidecar(t *testing.T) {
	t.Parallel()
	ts, videoHandle, _, _ := buildVideoServer(t, true)

	var res struct {
		Chapters []video.Chapter `json:"chapters"`
	}
	rpcCall(t, ts, "runstatus.video.chapters",
		map[string]any{"session_id": "sess-vid", "video": videoHandle}, &res)
	require.Len(t, res.Chapters, 1)
	assert.Equal(t, "scene-1", res.Chapters[0].ID)
	assert.Equal(t, "slidey", res.Chapters[0].Source.Kind)

	// No sidecar → empty list, not an error.
	ts2, vh2, _, _ := buildVideoServer(t, false)
	var res2 struct {
		Chapters []video.Chapter `json:"chapters"`
	}
	rpcCall(t, ts2, "runstatus.video.chapters",
		map[string]any{"session_id": "sess-vid", "video": vh2}, &res2)
	assert.Empty(t, res2.Chapters)
}

// TestFeedbackAdd_AppendsWellFormedNote proves feedback.add gates the video
// handle and appends a structured note carrying the source_ref, time_range,
// frame handle, and instruction.
func TestFeedbackAdd_AppendsWellFormedNote(t *testing.T) {
	t.Parallel()
	ts, videoHandle, _, sink := buildVideoServer(t, true)

	var ok struct {
		OK bool `json:"ok"`
	}
	rpcCall(t, ts, "runstatus.feedback.add", map[string]any{
		"session_id":   "sess-vid",
		"video":        videoHandle,
		"frame_handle": "frame#deadbeef",
		"instruction":  "heading clips on mobile",
		"source_ref":   map[string]any{"kind": "slidey", "scene_id": "intro"},
		"time_range":   map[string]any{"start_ms": 14000, "end_ms": 16000},
	}, &ok)
	require.True(t, ok.OK)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	require.Len(t, sink.notes, 1)
	note := sink.notes[0]
	assert.Equal(t, videoHandle, note.VideoHandle)
	assert.Equal(t, "frame#deadbeef", note.FrameHandle)
	assert.Equal(t, "heading clips on mobile", note.Instruction)
	assert.Equal(t, "slidey", note.SourceRef["kind"])
	assert.False(t, note.Ts.IsZero(), "server stamps the capture time")
}

// TestFeedbackAdd_MissingInstruction proves a note without an instruction is a
// structured malformed-request error.
func TestFeedbackAdd_MissingInstruction(t *testing.T) {
	t.Parallel()
	ts, videoHandle, _, _ := buildVideoServer(t, true)
	code, _ := rpcCallExpectError(t, ts, "runstatus.feedback.add",
		map[string]any{"session_id": "sess-vid", "video": videoHandle})
	assert.Equal(t, -32000, code)
}

// TestJSONLFeedbackSink_Appends proves the default file-backed sink appends one
// JSON line per note to feedback.jsonl.
func TestJSONLFeedbackSink_Appends(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "feedback.jsonl")
	sink := &server.JSONLFeedbackSink{Path: path}
	require.NoError(t, sink.AddFeedback(server.FeedbackNote{VideoHandle: "v#1", Instruction: "a"}))
	require.NoError(t, sink.AddFeedback(server.FeedbackNote{VideoHandle: "v#1", Instruction: "b"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2, "two appended notes → two JSON lines")
	var n0 server.FeedbackNote
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &n0))
	assert.Equal(t, "a", n0.Instruction)
}
