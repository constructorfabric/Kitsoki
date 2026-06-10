package runstatus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TranscriptData is the runstatus.session.transcript response and the inlined
// static-export shape: one oracle call's verbatim backend-native event stream
// plus the parallel capture-time offsets that power the waterfall. It mirrors
// the SPA's TranscriptData (tools/runstatus/src/data/source.ts) field-for-field.
//
// Events holds each sidecar line parsed back into JSON (so the wire carries
// structured objects, not escaped strings) — but the parse is lossless: the
// sidecar is written byte-verbatim by host.fileTranscriptWriter, and round-trips
// through json.RawMessage here without re-ordering keys. Timings[i] is the
// capture-time ms-offset of Events[i]; it is shorter than Events only if the
// .timings sidecar is missing or truncated (then the waterfall degrades to an
// ordered list, never errors).
type TranscriptData struct {
	Format        string            `json:"format"`
	Events        []json.RawMessage `json:"events"`
	Timings       []int64           `json:"timings"`
	SchemaVersion int               `json:"schema_version"`
}

// transcriptSchemaVersion is stamped into a freshly-read TranscriptData. It
// tracks host.TranscriptSchemaVersion (kept a local literal to avoid a
// runstatus→host import edge: the runstatus reader only consumes sidecars, it
// never produces TranscriptRefs). Bump in lockstep when the sidecar layout
// changes incompatibly.
const transcriptSchemaVersion = 1

// EmptyTranscript is the not-found / no-sidecar result: a well-formed empty
// payload rather than an RPC error, so an absent transcript (a verb that emitted
// none, or a source with no transcripts dir) renders as "no Agent actions"
// instead of a surfaced 500. The format is "claude-stream-json" so the consumer
// normalizer has a default dialect, but with zero events it is inert.
func EmptyTranscript() *TranscriptData {
	return &TranscriptData{
		Format:        "claude-stream-json",
		Events:        []json.RawMessage{},
		Timings:       []int64{},
		SchemaVersion: transcriptSchemaVersion,
	}
}

// ReadTranscriptSidecar reads <dir>/<callID>.jsonl (one JSON event per line,
// byte-verbatim) and the parallel <dir>/<callID>.timings ("<event-index>
// <ms-offset>" per line) into a TranscriptData. The read is LAZY by design —
// callers invoke it per-call on demand, never folding the (potentially
// megabyte) stream into a snapshot.
//
// A missing .jsonl is NOT an error: it returns EmptyTranscript so a call whose
// transcript_ref is absent (or whose source exposes a transcripts dir that was
// never populated) degrades cleanly. A missing or short .timings likewise
// degrades — the waterfall falls back to event order. Only a genuine read error
// on an existing file (permissions, IO) is returned.
//
// callID is used solely as a path leaf (filepath.Base) so a crafted id cannot
// escape dir via "../"; an id that resolves outside dir yields EmptyTranscript.
func ReadTranscriptSidecar(dir, callID string) (*TranscriptData, error) {
	leaf := filepath.Base(callID)
	if leaf == "." || leaf == string(filepath.Separator) || strings.Contains(callID, "..") {
		return EmptyTranscript(), nil
	}

	jsonlPath := filepath.Join(dir, leaf+".jsonl")
	raw, err := os.ReadFile(jsonlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return EmptyTranscript(), nil
		}
		return nil, err
	}

	events := parseTranscriptLines(raw)

	// The format is recorded only in the trace's transcript_ref, not in the
	// sidecar itself; default to the claude dialect (the in-host producer) — the
	// consumer's per-event type-sniffing normalizer does not depend on it.
	out := &TranscriptData{
		Format:        "claude-stream-json",
		Events:        events,
		Timings:       readTimings(filepath.Join(dir, leaf+".timings"), len(events)),
		SchemaVersion: transcriptSchemaVersion,
	}
	return out, nil
}

// parseTranscriptLines splits the verbatim .jsonl into one json.RawMessage per
// non-blank line. A line that does not parse as JSON is skipped rather than
// failing the whole read (defensive against a truncated final line on a still-
// writing sidecar).
func parseTranscriptLines(raw []byte) []json.RawMessage {
	events := make([]json.RawMessage, 0, 16)
	sc := bufio.NewScanner(bytes.NewReader(raw))
	// Allow long lines (a tool_result can carry a large file body).
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		events = append(events, json.RawMessage(cp))
	}
	return events
}

// readTimings parses the "<event-index> <ms-offset>" .timings sidecar into a
// dense slice indexed by event position. n is the event count; the result is
// sized to n with any unstamped index left 0. A missing/unreadable file yields
// a zero-filled slice (the waterfall degrades to event order).
func readTimings(path string, n int) []int64 {
	out := make([]int64, n)
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		idx, err1 := strconv.Atoi(fields[0])
		ms, err2 := strconv.ParseInt(fields[1], 10, 64)
		if err1 != nil || err2 != nil || idx < 0 || idx >= n {
			continue
		}
		out[idx] = ms
	}
	return out
}
