package server

// video.go implements the three read/capture RPCs the `/review` feedback panel
// drives (docs/tui/video-review.md, slice 2 of the
// mockup-video-studio epic):
//
//	runstatus.video.chapters {video}                                   → {chapters:[…]}
//	runstatus.video.frame    {video, t_ms}                             → {handle, mime, kind}
//	runstatus.feedback.add   {video, source_ref, time_range, frame_handle, instruction} → {ok}
//
// All three are gated to RECORDED video handles: the video param is the opaque
// artifact handle the run journalled, resolved to an on-disk path through the
// session's [ArtifactResolver] — exactly the gate the `/artifact/{id}` route
// uses. An unknown or escaping handle is a not-found, never an arbitrary-path
// read or a server-side ffmpeg shell on attacker input.
//
// video.frame is the one new side effect with teeth: it shells ffmpeg via the
// SINGLE extractor (internal/video.Frame; epic shared decision 2 — one ffmpeg
// site) and records the PNG through the run's substrate (the [FrameRecorder]
// seam on the Entry). ffmpeg-not-found / extraction failure rides back as an
// rpcError, never a crash.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/video"
)

// resolveVideoPath gates a `video` handle param to a recorded artifact: it
// resolves the handle through the entry's [ArtifactResolver] to an absolute
// on-disk path, returning codeNotFound for a missing param, an entry with no
// resolver, or an unknown/escaping handle. This is the root-guard: only a
// handle the run actually journalled resolves, so no arbitrary path is grabbable
// or extractable.
func resolveVideoPath(entry Entry, params map[string]any) (videoPath string, rerr *rpcError) {
	handle, _ := params["video"].(string)
	if handle == "" {
		return "", &rpcError{Code: codeServerError, Message: "video: missing 'video' handle"}
	}
	if entry.Artifacts == nil {
		return "", &rpcError{Code: codeNotFound, Message: "video: no artifact resolver for this session"}
	}
	path, _, ok := entry.Artifacts.LookupArtifact(handle)
	if !ok {
		return "", &rpcError{Code: codeNotFound, Message: "video: unknown handle: " + handle}
	}
	// Belt-and-suspenders path guard (mirrors handleArtifact): the resolved path
	// must be absolute and clean — no traversal smuggled through the journal.
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean != path {
		return "", &rpcError{Code: codeNotFound, Message: "video: resolved path is not a clean absolute path"}
	}
	return path, nil
}

// videoChapters implements runstatus.video.chapters: read the slice-1 chapter
// sidecar for the resolved video. A missing sidecar is NOT an error — it returns
// an empty list so the panel degrades to "no chapters" rather than failing.
func videoChapters(entry Entry, params map[string]any) (any, *rpcError) {
	videoPath, rerr := resolveVideoPath(entry, params)
	if rerr != nil {
		return nil, rerr
	}
	chapters, err := video.ReadChapters(videoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"chapters": []video.Chapter{}}, nil
		}
		return nil, serverErr(err)
	}
	return map[string]any{"chapters": chapters}, nil
}

// videoFrame implements runstatus.video.frame: grab a still at t_ms via the one
// extractor and record it through the substrate, returning the still's handle.
// Requires a [FrameRecorder] on the entry (a live session with a journal);
// read-only surfaces report codeReadOnly.
func videoFrame(ctx context.Context, entry Entry, params map[string]any) (any, *rpcError) {
	videoPath, rerr := resolveVideoPath(entry, params)
	if rerr != nil {
		return nil, rerr
	}
	if entry.Frames == nil {
		return nil, readOnlyErr("video.frame")
	}
	tMs, _ := intParam(params, "t_ms")
	if tMs < 0 {
		return nil, &rpcError{Code: codeServerError, Message: "video.frame: t_ms must be >= 0"}
	}

	pngPath, err := video.Frame(ctx, entry.FrameRunner, videoPath, tMs)
	if err != nil {
		// ffmpeg-not-found / extraction failure → structured error, never a crash.
		return nil, serverErr(fmt.Errorf("video.frame: %w", err))
	}
	defer func() { _ = os.Remove(pngPath) }() // recorder copies it under the root

	label := fmt.Sprintf("frame @ %s", formatMs(tMs))
	handle, err := entry.Frames.RecordFrame(pngPath, label)
	if err != nil {
		return nil, serverErr(fmt.Errorf("video.frame: record: %w", err))
	}
	return map[string]any{"handle": handle, "mime": "image/png", "kind": "image"}, nil
}

// feedbackAdd implements runstatus.feedback.add: persist (and, in future,
// dispatch) one structured feedback note. The video handle is gated like the
// other two RPCs so a note always names a recorded video. Requires a
// [FeedbackSink]; surfaces without one report codeReadOnly.
func feedbackAdd(entry Entry, params map[string]any) (any, *rpcError) {
	videoHandle, _ := params["video"].(string)
	if videoHandle == "" {
		return nil, &rpcError{Code: codeServerError, Message: "feedback.add: missing 'video' handle"}
	}
	if _, rerr := resolveVideoPath(entry, params); rerr != nil {
		return nil, rerr
	}
	if entry.Feedback == nil {
		return nil, readOnlyErr("feedback.add")
	}
	instruction, _ := params["instruction"].(string)
	if instruction == "" {
		return nil, &rpcError{Code: codeServerError, Message: "feedback.add: missing 'instruction'"}
	}
	note := FeedbackNote{
		VideoHandle: videoHandle,
		Instruction: instruction,
		FrameHandle: stringParam(params, "frame_handle"),
		Ts:          time.Now().UTC(),
	}
	if sr, ok := params["source_ref"].(map[string]any); ok {
		note.SourceRef = sr
	}
	if tr, ok := params["time_range"].(map[string]any); ok {
		note.TimeRange = tr
	}
	if err := entry.Feedback.AddFeedback(note); err != nil {
		return nil, serverErr(fmt.Errorf("feedback.add: %w", err))
	}
	return map[string]any{"ok": true}, nil
}

func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// formatMs renders milliseconds as M:SS for a human caption.
func formatMs(ms int) string {
	totalSec := ms / 1000
	return fmt.Sprintf("%d:%02d", totalSec/60, totalSec%60)
}

// ── Default file-backed FeedbackSink ──────────────────────────────────────────

// JSONLFeedbackSink is the default [FeedbackSink]: it appends one JSON note per
// line to an append-only feedback.jsonl. Concurrent appends are serialised by a
// mutex (the file is opened O_APPEND per call so a crash leaves a valid prefix).
type JSONLFeedbackSink struct {
	Path string
	mu   sync.Mutex
}

// AddFeedback appends note as one JSON line.
func (s *JSONLFeedbackSink) AddFeedback(note FeedbackNote) error {
	b, err := json.Marshal(note)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(append(b, '\n'))
	return err
}

// ── Default journal-backed FrameRecorder ──────────────────────────────────────

// JournalFrameRecorder is the default [FrameRecorder]: it copies the still PNG
// under FramesDir, computes the same <stem>#<sha-prefix> handle shape the
// artifacts_dir transport uses, and appends a [journal.KindArtifactEmitted]
// entry so the existing [JournalArtifactResolver] (and thus /artifact/{id})
// resolves it with no special case. It is the production wiring; the integrate
// phase stamps it onto each live Entry.
type JournalFrameRecorder struct {
	Writer    journal.Writer
	SID       app.SessionID
	FramesDir string
	mu        sync.Mutex
	seq       int
}

// RecordFrame copies pngPath into FramesDir under a content-addressed name,
// journals an artifact.emitted entry, and returns the handle.
func (r *JournalFrameRecorder) RecordFrame(pngPath, label string) (string, error) {
	data, err := os.ReadFile(pngPath)
	if err != nil {
		return "", fmt.Errorf("read frame: %w", err)
	}
	if r.FramesDir != "" {
		if err := os.MkdirAll(r.FramesDir, 0o755); err != nil {
			return "", fmt.Errorf("frames dir: %w", err)
		}
	}
	sum := sha256.Sum256(data)
	handle := fmt.Sprintf("frame#%x", sum[:4])
	dest := filepath.Join(r.FramesDir, handle+".png")
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("write frame: %w", err)
	}

	r.mu.Lock()
	r.seq++
	r.mu.Unlock()

	evt := journal.ArtifactEvent{
		ID:        handle,
		Kind:      "image",
		Mime:      "image/png",
		Label:     label,
		Path:      dest,
		Producer:  "runstatus.video.frame",
		SizeBytes: int64(len(data)),
		CreatedAt: time.Now(),
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return "", err
	}
	// Turn=0, Seq=0 → the SQLite writer auto-assigns the next seq for the
	// (session, turn=0) out-of-turn row group (AppendJournalTx), so the still's
	// artifact.emitted entry never collides with the video's own turn=0 entry
	// (the UNIQUE (session,turn,seq) constraint). A hardcoded Seq collides.
	if err := r.Writer.Append(journal.Entry{
		Ts:      time.Now(),
		Session: r.SID,
		Kind:    journal.KindArtifactEmitted,
		Body:    json.RawMessage(body),
	}); err != nil {
		return "", fmt.Errorf("journal frame: %w", err)
	}
	return handle, nil
}
