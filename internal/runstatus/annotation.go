package runstatus

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Annotation is one operator score or label attached to a trace event or turn.
// It lives in a sidecar JSONL file alongside the session trace so the primary
// trace is never mutated by the UI. Each append adds one JSON line.
//
// Either TargetCallID or TargetTurn (or both) identifies what is being
// annotated. TargetCallID references a specific agent call (matched by
// TraceEvent.attrs.call_id); TargetTurn references all events in a turn.
// Score is a 0–1 float; Label is a short tag; Comment is free-form prose.
// Annotator is the operator identity (display name or email).
type Annotation struct {
	// Ts is the wall-clock time the annotation was written.
	Ts time.Time `json:"ts"`
	// SessionID links this annotation back to its session.
	SessionID string `json:"session_id"`
	// TargetCallID is the call_id of the agent call being annotated (if set).
	TargetCallID string `json:"target_call_id,omitempty"`
	// TargetTurn is the turn number being annotated (if set; 0 means unset).
	TargetTurn int `json:"target_turn,omitempty"`
	// Score is an operator quality score for the target (0.0–1.0).
	Score *float64 `json:"score,omitempty"`
	// Label is a short tag (e.g. "good", "bad", "off-topic").
	Label string `json:"label,omitempty"`
	// Comment is free-form operator commentary.
	Comment string `json:"comment,omitempty"`
	// Annotator is the operator identity who wrote this annotation.
	Annotator string `json:"annotator,omitempty"`
	// SchemaVersion is incremented when the Annotation shape changes
	// incompatibly. Current version is 1.
	SchemaVersion int `json:"schema_version"`
}

// AnnotationPath returns the path to the sidecar JSONL file that holds
// annotations for the session at sessionID inside sessionDir.
//
// Path: <sessionDir>/<sessionID>.annotations.jsonl
//
// sessionDir is typically store.SessionsDir() / <appID>; the caller is
// responsible for ensuring the directory exists before calling AppendAnnotation.
func AnnotationPath(sessionDir, sessionID string) string {
	return filepath.Join(sessionDir, sessionID+".annotations.jsonl")
}

// LoadAnnotations reads and decodes all Annotation records from the JSONL file
// at path. It returns an empty (non-nil) slice without error when the file does
// not exist — no annotations have been written yet. A corrupt line is silently
// skipped (lenient by design: a partial write at the end of file must not
// prevent reading earlier annotations). An error is returned only for OS-level
// failures other than file-not-found.
func LoadAnnotations(path string) ([]Annotation, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Annotation{}, nil
		}
		return nil, fmt.Errorf("load annotations %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []Annotation
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var a Annotation
		if err := json.Unmarshal(line, &a); err != nil {
			// Skip corrupt lines; a partial final write must not break the reader.
			continue
		}
		out = append(out, a)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("load annotations %s: %w", path, err)
	}
	if out == nil {
		out = []Annotation{}
	}
	return out, nil
}

// AppendAnnotation opens (or creates) the JSONL sidecar file at path in
// append-only mode, writes one JSON line for a, then closes the file. The
// parent directory must exist; AppendAnnotation does not create it.
//
// The written line is valid JSON followed by a newline, so the sidecar is a
// valid JSONL file after any number of AppendAnnotation calls.
func AppendAnnotation(path string, a Annotation) error {
	if a.SchemaVersion == 0 {
		a.SchemaVersion = 1
	}
	line, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("append annotation: marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("append annotation %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("append annotation %s: write: %w", path, err)
	}
	return nil
}
