package artifactjob

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"kitsoki/internal/app"
)

// RunURL returns the stable run URL for a job under a local or hosted base URL.
// Empty baseURL returns the canonical path-only form used by local routers.
func RunURL(baseURL string, jobID JobID) string {
	p := path.Join("/run", string(jobID))
	if baseURL == "" {
		return p
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return p
	}
	u.Path = path.Join(strings.TrimRight(u.Path, "/"), p)
	return u.String()
}

// ReindexTrace rebuilds one job's run/artifact index rows from immutable trace
// JSONL. It is deliberately lenient about trace shape so it can consume both
// slog traces ({msg: artifact.emitted, ...}) and journal JSONL
// ({ev: artifact.emitted, body: {...}}).
func ReindexTrace(ctx context.Context, index RunIndex, job Job, tracePath string) (Run, []Artifact, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		return Run{}, nil, fmt.Errorf("artifactjob.ReindexTrace: open trace: %w", err)
	}
	defer f.Close()
	return ReindexTraceReader(ctx, index, job, tracePath, f)
}

// ReindexTraceReader is the testable form of ReindexTrace.
func ReindexTraceReader(ctx context.Context, index RunIndex, job Job, tracePath string, r io.Reader) (Run, []Artifact, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	run := Run{
		JobID:     job.ID,
		SessionID: job.SessionID,
		Story:     job.Story,
		Status:    job.Status,
		TracePath: tracePath,
	}
	var artifacts []Artifact
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			return Run{}, nil, fmt.Errorf("artifactjob.ReindexTrace: line %d: %w", lineNo, err)
		}
		applyRunFacts(&run, raw)
		if artifact, ok := artifactFromTrace(job.ID, raw); ok {
			artifacts = append(artifacts, artifact)
		}
	}
	if err := scanner.Err(); err != nil {
		return Run{}, nil, fmt.Errorf("artifactjob.ReindexTrace: scan: %w", err)
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = job.CreatedAt
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	if run.SessionID == "" {
		run.SessionID = app.SessionID(firstString(job.SessionID, app.SessionID("")))
	}
	if err := index.UpsertRun(ctx, run); err != nil {
		return Run{}, nil, err
	}
	for _, artifact := range artifacts {
		if err := index.UpsertArtifact(ctx, artifact); err != nil {
			return Run{}, nil, err
		}
	}
	return run, artifacts, nil
}

func applyRunFacts(run *Run, raw map[string]any) {
	if run.SessionID == "" {
		if sid, ok := raw["session_id"].(string); ok {
			run.SessionID = app.SessionID(sid)
		}
	}
	if t := traceTime(raw); !t.IsZero() {
		if run.StartedAt.IsZero() || t.Before(run.StartedAt) {
			run.StartedAt = t
		}
		if isTerminalTraceEvent(raw) {
			tt := t
			run.EndedAt = &tt
		}
	}
	if turn := intFromAny(raw["turn"]); turn > run.LastTurn {
		run.LastTurn = turn
	}
}

func artifactFromTrace(jobID JobID, raw map[string]any) (Artifact, bool) {
	kind := eventKind(raw)
	if kind != "artifact.emitted" {
		return Artifact{}, false
	}
	body := raw
	if b, ok := raw["body"].(map[string]any); ok {
		body = b
	} else if attrs, ok := raw["attrs"].(map[string]any); ok {
		if b, ok := attrs["body"].(map[string]any); ok {
			body = b
		} else {
			body = attrs
		}
	}
	a := Artifact{JobID: jobID}
	a.Handle, _ = body["id"].(string)
	if a.Handle == "" {
		a.Handle, _ = body["handle"].(string)
	}
	a.Kind, _ = body["kind"].(string)
	a.MIME, _ = body["mime"].(string)
	a.Label, _ = body["label"].(string)
	a.Path, _ = body["path"].(string)
	a.SizeBytes = int64(intFromAny(body["size_bytes"]))
	a.CreatedAt = traceTime(body)
	if a.CreatedAt.IsZero() {
		a.CreatedAt = traceTime(raw)
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	return a, a.Handle != "" && a.Path != ""
}

func eventKind(raw map[string]any) string {
	if ev, ok := raw["ev"].(string); ok {
		return ev
	}
	if msg, ok := raw["msg"].(string); ok {
		return msg
	}
	return ""
}

func isTerminalTraceEvent(raw map[string]any) bool {
	switch eventKind(raw) {
	case "turn.done", "job.terminal":
		return true
	}
	if status, ok := raw["status"].(string); ok {
		return status == string(StatusDone) || status == string(StatusFailed) || status == string(StatusCancelled)
	}
	return false
}

func traceTime(raw map[string]any) time.Time {
	for _, key := range []string{"time", "ts", "created_at"} {
		v, ok := raw[key]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
				return parsed.UTC()
			}
		case float64:
			if t > 1_000_000_000_000 {
				return time.UnixMilli(int64(t)).UTC()
			}
			if t > 0 {
				return time.Unix(int64(t), 0).UTC()
			}
		case json.Number:
			if i, err := t.Int64(); err == nil {
				if i > 1_000_000_000_000 {
					return time.UnixMilli(i).UTC()
				}
				return time.Unix(i, 0).UTC()
			}
		}
	}
	return time.Time{}
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

func firstString(v app.SessionID, fallback app.SessionID) string {
	if v != "" {
		return string(v)
	}
	return string(fallback)
}
