// Package store — jsonl.go: append-only JSONL session trace.
//
// JSONLSink is the write side of the trace-as-state design.  One line per
// event, O_APPEND + fsync, no line ever rewritten.  Every session entry point
// (kitsoki turn, session continue, TUI) will eventually use this path; for
// wave 1 it is added but not wired into the orchestrator.
//
// File layout:
//
//	line 1:  {"kind":"session.header","schema_version":1, ...}
//	line 2+: one store.Event JSON object, terminated with \n
//
// Invariants enforced at write time:
//   - NUL bytes anywhere in the marshalled line are rejected.
//   - ts fields are RFC3339Nano in UTC with explicit Z suffix.
//   - encoding/json rejects NaN/Inf by default; that default is preserved.
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/unicode/norm"
	"kitsoki/internal/app"
)

// maxSchemaVersion is the highest SessionHeader schema_version this build
// can open.  Files written by a newer version are refused at open time.
const maxSchemaVersion = 1

// sessionHeaderKind is the discriminant written on line 1 of every trace.
// Uses dotted form consistent with all other EventKind values (e.g. "turn.start").
// Finding (additional): renamed from the undotted "SessionHeader" to the dotted
// "session.header" for vocabulary consistency.
const sessionHeaderKind = "session.header"

// sessionHeader is the first line of every trace file.
type sessionHeader struct {
	Kind          string    `json:"kind"`
	SchemaVersion int       `json:"schema_version"`
	WrittenAt     time.Time `json:"written_at"`
}

// EventSink is the write-side abstraction for a session event log.
// The orchestrator takes an EventSink, not a *store.Store.
type EventSink interface {
	// Append marshals one event and appends it to the log.
	Append(ev Event) error
	// History returns the in-memory history accumulated since OpenJSONL.
	History() History
}

// JSONLSink implements EventSink against an on-disk JSONL file.
// Append is O(1) per event; OpenJSONL is O(N) once per session.
type JSONLSink struct {
	// Path is the file path passed to OpenJSONL; exposed for diagnostics.
	Path string
	hist History
	f    *os.File

	// rawLines retains a copy of the exact bytes written (or loaded) for
	// each event, without the trailing newline.  Lines()[i] corresponds to
	// hist[i].  This allows Layer 7 consumers (runstatus.FromSink) to
	// populate Snapshot.RawLines with the actual bytes the writer wrote,
	// rather than re-marshalling through a separate code path.
	//
	// Memory: O(N) bytes per session where N is the number of events.  The
	// per-event bytes already exist on disk; keeping them here is a 2× factor
	// in memory for in-process sessions.  Acceptable for phase A scale.
	rawLines [][]byte

	// openIno is the inode number of the trace file at open time.
	// Before each Append we stat the path and compare to detect replacement.
	openIno uint64

	// seqByTurn tracks the next seq to assign for each turn number.
	// Populated from history on open; incremented on each Append.
	seqByTurn map[app.TurnNumber]int

	// hookFsync, if non-nil, replaces the real fsync call in Append.
	// Used by tests to inject fsync failures. Must not be set concurrently
	// with Append calls.
	hookFsync func(*os.File) error

	// hookWrite, if non-nil, replaces the real write call in Append.
	// Used by tests to inject write failures (e.g. ENOSPC).
	hookWrite func(*os.File, []byte) (int, error)
}

// OpenJSONL opens path for appending.
//
//   - Non-existent path: creates the file and writes the SessionHeader line.
//   - Existing path: validates the header on line 1, then reopens for append.
//     Returns an error if the header is missing, a duplicate exists, or the
//     schema_version exceeds maxSchemaVersion.
func OpenJSONL(path string) (*JSONLSink, error) {
	// Attempt to read the existing file.
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("store/jsonl: read %q: %w", path, err)
	}

	var hist History

	if os.IsNotExist(err) || len(existing) == 0 {
		// New file: create with the header.
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, fmt.Errorf("store/jsonl: open %q: %w", path, err)
		}
		// Acquire exclusive advisory lock (non-blocking: fail if already held).
		if flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("store/jsonl: open %q: trace file is locked by another writer: %w", path, flockErr)
		}
		hdr := sessionHeader{
			Kind:          sessionHeaderKind,
			SchemaVersion: maxSchemaVersion,
			WrittenAt:     time.Now().UTC(),
		}
		line, err := marshalLine(hdr)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("store/jsonl: marshal header: %w", err)
		}
		if _, err := f.Write(line); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("store/jsonl: write header: %w", err)
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("store/jsonl: fsync after header: %w", err)
		}
		ino, inoErr := fileInode(f)
		if inoErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("store/jsonl: stat %q for inode: %w", path, inoErr)
		}
		return &JSONLSink{Path: path, f: f, openIno: ino, seqByTurn: make(map[app.TurnNumber]int)}, nil
	}

	// Existing file: validate and load.
	var rawLines [][]byte
	hist, rawLines, err = loadAndValidate(existing, path)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store/jsonl: reopen %q for append: %w", path, err)
	}
	// Acquire exclusive advisory lock (non-blocking).
	if flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("store/jsonl: open %q: trace file is locked by another writer: %w", path, flockErr)
	}
	ino, inoErr := fileInode(f)
	if inoErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("store/jsonl: stat %q for inode: %w", path, inoErr)
	}
	// Initialise per-turn seq counters from existing history.
	seqByTurn := make(map[app.TurnNumber]int)
	for _, ev := range hist {
		seqByTurn[ev.Turn] = ev.Seq + 1 // next seq for this turn
	}
	return &JSONLSink{Path: path, hist: hist, rawLines: rawLines, f: f, openIno: ino, seqByTurn: seqByTurn}, nil
}

// fileInode returns the inode number of the open file f.
func fileInode(f *os.File) (uint64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot read inode: unexpected Sys() type")
	}
	return st.Ino, nil
}

// pathInode returns the inode number of the file at path (via Lstat — does not
// follow symlinks for the path itself, since we want the inode of what the path
// points to at this moment).
func pathInode(path string) (uint64, error) {
	fi, err := os.Stat(path) // Stat follows symlinks to get the target's inode
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot read inode: unexpected Sys() type")
	}
	return st.Ino, nil
}

// loadAndValidate reads existing JSONL bytes, validates the header, and
// returns the event history (all lines after line 1) plus the corresponding
// raw line bytes (one entry per event, without trailing newline).
func loadAndValidate(data []byte, path string) (History, [][]byte, error) {
	lines, splitErr := splitLines(data)
	if splitErr != nil {
		return nil, nil, fmt.Errorf("store/jsonl: %q: %w", path, splitErr)
	}
	if len(lines) == 0 {
		return nil, nil, fmt.Errorf("store/jsonl: %q: trace missing session.header on line 1", path)
	}

	// Line 1 must be the header.
	var hdr struct {
		Kind          string `json:"kind"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := json.Unmarshal(lines[0], &hdr); err != nil {
		return nil, nil, fmt.Errorf("store/jsonl: %q: line 1 is not valid JSON: %w", path, err)
	}
	if hdr.Kind != sessionHeaderKind {
		return nil, nil, fmt.Errorf("store/jsonl: %q: trace missing session.header on line 1 (got kind=%q)", path, hdr.Kind)
	}
	if hdr.SchemaVersion > maxSchemaVersion {
		return nil, nil, fmt.Errorf("store/jsonl: %q: schema_version %d on disk exceeds highest supported %d", path, hdr.SchemaVersion, maxSchemaVersion)
	}

	// Remaining lines are events; check for duplicate header and (turn,seq) monotonicity.
	var hist History
	var rawLines [][]byte

	// seqByTurn tracks the highest seq seen per turn (for monotonicity and gap detection).
	// We use -1 to mean "no events seen yet for this turn".
	seqByTurn := make(map[app.TurnNumber]int)
	// maxTurn tracks the highest turn number seen so far (for cross-turn monotonicity).
	var maxTurn app.TurnNumber

	for i, raw := range lines[1:] {
		lineNum := i + 2 // 1-based, header is line 1
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, nil, fmt.Errorf("store/jsonl: %q: line %d is not valid JSON: %w", path, lineNum, err)
		}
		if probe.Kind == sessionHeaderKind {
			return nil, nil, fmt.Errorf("store/jsonl: %q: duplicate session.header at line %d", path, lineNum)
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, nil, fmt.Errorf("store/jsonl: %q: line %d: unmarshal event: %w", path, lineNum, err)
		}

		// Enforce (turn, seq) monotonicity.
		// Off-path events may have parent_turn set; their turn number still must
		// be monotonic relative to the max turn seen so far (the max+1 rule).
		//
		// Cross-turn out-of-order (finding 2.9): once a higher turn has appeared,
		// events for a lower turn number are rejected as out-of-order. This catches
		// the case where turn 1 seq 1 arrives after turn 2 has started — the gap
		// check within turn 1 would pass (seq 0 → seq 1 is valid), but the cross-
		// turn ordering is broken.
		if ev.Turn < maxTurn {
			return nil, nil, fmt.Errorf("store/jsonl: %q: out-of-order turn at line %d: turn=%d arrives after turn=%d (cross-turn ordering violation)",
				path, lineNum, ev.Turn, maxTurn)
		}
		if ev.Turn > maxTurn {
			maxTurn = ev.Turn
		}
		if prev, seen := seqByTurn[ev.Turn]; seen {
			if ev.Seq == prev {
				return nil, nil, fmt.Errorf("store/jsonl: %q: duplicate (turn,seq) at line %d: turn=%d seq=%d",
					path, lineNum, ev.Turn, ev.Seq)
			}
			if ev.Seq < prev {
				return nil, nil, fmt.Errorf("store/jsonl: %q: out-of-order (turn,seq) at line %d: turn=%d seq=%d (previous seq=%d)",
					path, lineNum, ev.Turn, ev.Seq, prev)
			}
			if ev.Seq != prev+1 {
				return nil, nil, fmt.Errorf("store/jsonl: %q: gap in seq within turn %d at line %d: expected seq %d, got %d",
					path, lineNum, ev.Turn, prev+1, ev.Seq)
			}
		} else {
			// First event for this turn; must start at seq 0.
			if ev.Seq != 0 {
				return nil, nil, fmt.Errorf("store/jsonl: %q: gap in seq within turn %d at line %d: expected seq 0, got %d",
					path, lineNum, ev.Turn, ev.Seq)
			}
		}
		seqByTurn[ev.Turn] = ev.Seq

		hist = append(hist, ev)
		// Retain the raw bytes for this event (raw already a copy from splitLines).
		rawLines = append(rawLines, []byte(raw))
	}
	return hist, rawLines, nil
}

// Note: we no longer enforce a maximum line size at read time.
// The PIPE_BUF limit was removed to allow arbitrary-sized events in traces.

// splitLines splits JSONL bytes into lines with strict validation:
//   - Every line MUST end with exactly \n (no CRLF, no missing trailing newline).
//   - A file that does not end with \n is "trace corrupted: missing trailing newline at EOF".
//   - CRLF (\r\n) is a hard error — the \r is not stripped.
//   - Any line containing a NUL byte is an error.
//
// Returns (lines, nil) on success. The returned slices are independent copies.
// Returns (nil, error) on any structural violation.
func splitLines(data []byte) ([]json.RawMessage, error) {
	if len(data) == 0 {
		return nil, nil
	}

	// The file must end with \n.
	if data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("trace corrupted: missing trailing newline at EOF")
	}

	var out []json.RawMessage
	lineNum := 0
	remaining := data
	for len(remaining) > 0 {
		lineNum++
		nlIdx := bytes.IndexByte(remaining, '\n')
		// We already checked the file ends with \n, so nlIdx must be found.
		if nlIdx < 0 {
			// Should not happen: we verified the last byte is \n above.
			return nil, fmt.Errorf("trace corrupted: missing trailing newline at EOF")
		}

		// The line content (without the trailing \n).
		line := remaining[:nlIdx]
		remaining = remaining[nlIdx+1:]

		// Skip empty lines (blank lines between events are allowed).
		if len(line) == 0 {
			continue
		}

		// Reject CRLF: \r immediately before the \n.
		if line[len(line)-1] == '\r' {
			return nil, fmt.Errorf("trace corrupted: CRLF line ending at line %d (JSONL requires LF-only)", lineNum)
		}

		// Reject NUL bytes.
		if bytes.IndexByte(line, 0) >= 0 {
			return nil, fmt.Errorf("trace corrupted: NUL byte in line %d", lineNum)
		}

		cp := make([]byte, len(line))
		copy(cp, line)
		out = append(out, json.RawMessage(cp))
	}
	return out, nil
}

// traceEvent is the on-disk shape of a store.Event in the JSONL trace.
// state_path and parent_turn are top-level fields written at event time by
// the orchestrator; no exporter-side back-fill is needed or performed.
// episode_id and match_idx are present only on cassette-backed OracleCalled
// events; they enable post-resume reconstruction of per-episode match counters
// (§3.1 / §3.3.2) so replay:any episodes produce collision-free call_ids
// after a reload.
type traceEvent struct {
	Turn       app.TurnNumber  `json:"turn"`
	Seq        int             `json:"seq"`
	Ts         time.Time       `json:"ts"`
	Kind       EventKind       `json:"kind"`
	StatePath  app.StatePath   `json:"state_path,omitempty"`
	ParentTurn app.TurnNumber  `json:"parent_turn,omitempty"`
	CallID     string          `json:"call_id,omitempty"`
	EpisodeID  string          `json:"episode_id,omitempty"`
	MatchIdx   int             `json:"match_idx,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// Append marshals ev and appends it to the trace file.
//
// Rejection rules (all return an error; the file is not modified):
//   - payload contains a NUL byte
//   - payload contains NFD-normalised unicode (must be NFC)
//   - marshalled line > pipeBuf bytes
//   - ts is normalised to UTC with explicit Z suffix
//   - trace file has been replaced at path (inode changed since open)
func (s *JSONLSink) Append(ev Event) error {
	// Inode check: detect file replacement between open and this append.
	if s.openIno != 0 {
		curIno, inoErr := pathInode(s.Path)
		if inoErr == nil && curIno != s.openIno {
			return fmt.Errorf("store/jsonl: append: trace file replaced under us; original inode %d, current %d",
				s.openIno, curIno)
		}
	}

	// Stamp ts: if the caller did not set Ts, use wall-clock now (UTC).
	// This ensures every event on disk has a non-zero, RFC3339Nano timestamp
	// even when callers (e.g. newOrchestratorEvent) don't set Ts explicitly.
	ts := ev.Ts
	if ts.IsZero() {
		ts = time.Now()
	}
	ts = ts.UTC()

	payload := ev.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}

	// Reject NUL bytes in the raw payload bytes before marshaling.
	if bytes.IndexByte(payload, 0) >= 0 {
		return fmt.Errorf("store/jsonl: append: payload contains NUL byte")
	}

	// Reject NFD unicode in the payload string.
	if err := rejectNFD(payload); err != nil {
		return fmt.Errorf("store/jsonl: append: %w", err)
	}

	// Assign seq: the sink is the sole authority for dense-from-0 seq numbering
	// within each turn on the JSONL path.
	//
	// Policy (finding 2.11 — pinned): the caller's ev.Seq is always overwritten
	// by the sink's own counter. Callers MUST NOT rely on ev.Seq being preserved;
	// the seq value on the returned in-memory history (and on disk) is the one
	// the sink assigned. Rationale: the machine sets ev.Seq via a package-level
	// monotonic counter that is not per-turn; that value is meaningless on the
	// JSONL path and overwriting it silently is the correct behaviour for
	// correctness. Any caller that needs to read back the assigned seq should
	// call History() after Append rather than inspecting the input ev.Seq.
	//
	// The alternative (assert-on-mismatch) was considered but rejected because
	// all existing callers either pass Seq=0 (new event, let the sink assign) or
	// pass the exact expected next seq from a slice they wrote themselves — in
	// both cases the assertion would pass but add no safety over the existing
	// tests. Silent-overwrite with clear documentation is the chosen policy.
	var assignedSeq int
	if next, ok := s.seqByTurn[ev.Turn]; ok {
		assignedSeq = next
	} else {
		assignedSeq = 0
	}

	te := traceEvent{
		Turn:       ev.Turn,
		Seq:        assignedSeq,
		Ts:         ts,
		Kind:       ev.Kind,
		StatePath:  ev.StatePath,
		ParentTurn: ev.ParentTurn,
		CallID:     ev.CallID,
		EpisodeID:  ev.EpisodeID,
		MatchIdx:   ev.MatchIdx,
		Payload:    payload,
	}

	line, err := marshalLine(te)
	if err != nil {
		return fmt.Errorf("store/jsonl: append: marshal: %w", err)
	}

	if s.hookWrite != nil {
		if _, err := s.hookWrite(s.f, line); err != nil {
			return fmt.Errorf("store/jsonl: append: write: %w", err)
		}
	} else {
		if _, err := s.f.Write(line); err != nil {
			return fmt.Errorf("store/jsonl: append: write: %w", err)
		}
	}
	if s.hookFsync != nil {
		if err := s.hookFsync(s.f); err != nil {
			return fmt.Errorf("store/jsonl: append: fsync: %w", err)
		}
	} else {
		if err := s.f.Sync(); err != nil {
			return fmt.Errorf("store/jsonl: append: fsync: %w", err)
		}
	}
	// Update in-memory history with the stamped seq and ts.
	evWithSeq := ev
	evWithSeq.Seq = assignedSeq
	evWithSeq.Ts = ts
	s.hist = append(s.hist, evWithSeq)
	// Retain a copy of the raw bytes written (without the trailing \n) so
	// Lines() can return the exact bytes the writer wrote.  line includes \n;
	// strip it before storing.
	rawCopy := make([]byte, len(line)-1) // line ends with \n
	copy(rawCopy, line[:len(line)-1])
	s.rawLines = append(s.rawLines, rawCopy)
	// Advance the per-turn seq counter.
	s.seqByTurn[ev.Turn] = assignedSeq + 1
	return nil
}

// rejectNFD walks the JSON bytes looking for string values that contain
// NFD-normalised unicode.  We scan the raw JSON bytes for decoded string
// content.  If any string in the payload is not NFC-normalised, we return
// an error.
func rejectNFD(raw json.RawMessage) error {
	// We use json.Decoder to iterate over string tokens.
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		s, ok := tok.(string)
		if !ok {
			continue
		}
		if !norm.NFC.IsNormalString(s) {
			return fmt.Errorf("payload string contains NFD-normalised unicode (must be NFC)")
		}
	}
	return nil
}

// History returns the in-memory event history.
func (s *JSONLSink) History() History {
	return s.hist
}

// Lines returns a defensive copy (slice header only) of the raw JSONL bytes
// for each event, one entry per event in the same order as History().
// Each element is the marshalled line without the trailing newline.
//
// This allows Layer 7 consumers to populate Snapshot.RawLines with the exact
// bytes the writer wrote rather than re-marshalling through a separate code
// path.  The underlying byte slices are shared with the sink's internal
// buffer; callers must not mutate them.
//
// Memory: O(N) per session.  Acceptable for phase A scale; phase B can
// revisit if memory pressure surfaces.
func (s *JSONLSink) Lines() [][]byte {
	out := make([][]byte, len(s.rawLines))
	copy(out, s.rawLines)
	return out
}

// Close releases the advisory flock and closes the underlying file.
// The kernel also releases the lock on process exit (including abnormal exit).
func (s *JSONLSink) Close() error {
	// Release the flock before closing (best-effort; Close also releases it).
	_ = syscall.Flock(int(s.f.Fd()), syscall.LOCK_UN)
	return s.f.Close()
}

// MarshalEventLine marshals ev to the canonical on-disk JSONL representation
// (same format as JSONLSink.Append writes). Returns the JSON bytes WITHOUT the
// trailing newline. Useful for Layer 7 byte-equality assertions: callers can
// compare MarshalEventLine(ev) against the raw JSONL line for the same event.
//
// Note: Seq is taken from ev.Seq directly (not re-assigned by a sink). Pass
// events read from a JSONL file (via OpenJSONL) to get the on-disk Seq value.
func MarshalEventLine(ev Event) ([]byte, error) {
	payload := ev.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	te := traceEvent{
		Turn:       ev.Turn,
		Seq:        ev.Seq,
		Ts:         ev.Ts.UTC(),
		Kind:       ev.Kind,
		StatePath:  ev.StatePath,
		ParentTurn: ev.ParentTurn,
		CallID:     ev.CallID,
		EpisodeID:  ev.EpisodeID,
		MatchIdx:   ev.MatchIdx,
		Payload:    payload,
	}
	b, err := json.Marshal(te)
	if err != nil {
		return nil, fmt.Errorf("store: MarshalEventLine: %w", err)
	}
	return b, nil
}

// marshalLine marshals v to JSON, appends a trailing \n, and rejects NUL bytes.
func marshalLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	// Append trailing newline.
	line := append(b, '\n')

	// Reject NUL bytes.
	if bytes.IndexByte(line, 0) >= 0 {
		return nil, fmt.Errorf("marshalled line contains NUL byte")
	}

	return line, nil
}

// validateUTF8NoNUL checks that s contains no NUL bytes before we attempt to
// marshal it.  We surface this early so the error is clear.
func validateUTF8NoNUL(s string) error {
	if strings.IndexByte(s, 0) >= 0 {
		return fmt.Errorf("string contains NUL byte")
	}
	return nil
}
