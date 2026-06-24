// Package host — read-tool snapshot cap for host.agent.ask / decide / extract.
//
// Every read-tool call's output is captured in the journal so the LLM span is
// replayable from recording. Outputs over ReadSnapshotCap bytes are stored as a
// sha256 hash plus the first ReadSnapshotPrefix bytes; replay detects "divergent
// input" by comparing the hash but cannot reconstruct the full bytes from the
// journal alone. (Decision D9: replay compares hashes, not full bytes.)
//
// The cap is configurable per-app via a constant redeclared in the app package
// (not per-call). This file lives in the host package so decide and extract
// handlers can import it when those phases land.
package host

import (
	"crypto/sha256"
	"fmt"
)

// ReadSnapshotCap is the default maximum byte length for a captured read-tool
// output stored verbatim in the journal. Outputs larger than this are stored as
// a hash + prefix pair. Configurable per-app by overriding this constant in the
// app-level bootstrap (not per-call per D9).
const ReadSnapshotCap = 256 * 1024 // 256 KiB

// ReadSnapshotPrefix is the number of bytes kept verbatim when a read-tool
// output exceeds ReadSnapshotCap.
const ReadSnapshotPrefix = 4 * 1024 // 4 KiB

// ReadSnapshot is the captured output of one read-tool call. When the output
// fits within ReadSnapshotCap it is stored verbatim in Full. When it exceeds the
// cap, Full is empty and Hash + Prefix carry the summary.
//
// Callers journal one ReadSnapshot per tool call. The decide/extract handlers
// will reuse this type when those phases land.
type ReadSnapshot struct {
	// Full holds the complete tool output when len(output) <= ReadSnapshotCap.
	// Empty when the cap was exceeded.
	Full string
	// Hash is the hex-encoded SHA-256 digest of the full output. Set when the
	// cap was exceeded; empty when Full is used.
	Hash string
	// Prefix holds the first ReadSnapshotPrefix bytes of the output. Set when
	// the cap was exceeded; empty when Full is used.
	Prefix string
	// Truncated is true when the output exceeded ReadSnapshotCap.
	Truncated bool
	// Size is the total byte length of the original output.
	Size int
}

// CaptureReadSnapshot captures a read-tool output subject to the cap. cap is
// the per-app threshold (pass ReadSnapshotCap for the default). If cap <= 0, no
// cap is applied and the full output is always stored.
//
// Signature reused by agent_decide.go and agent_extract.go (Phase 2 / 5).
//
//	snap := CaptureReadSnapshot(toolOutput, ReadSnapshotCap)
//	journal.Record(snap)
func CaptureReadSnapshot(output string, cap int) ReadSnapshot {
	size := len(output)
	snap := ReadSnapshot{Size: size}
	if cap <= 0 || size <= cap {
		snap.Full = output
		return snap
	}
	snap.Truncated = true
	h := sha256.Sum256([]byte(output))
	snap.Hash = fmt.Sprintf("%x", h)
	prefixLen := ReadSnapshotPrefix
	if prefixLen > size {
		prefixLen = size
	}
	snap.Prefix = output[:prefixLen]
	return snap
}

// DigestMatches reports whether snap is consistent with output. When the
// snapshot was not truncated, DigestMatches compares the full text directly.
// When the snapshot was truncated, it recomputes the SHA-256 and compares
// hashes. Returns false ("divergent input") when there is a mismatch. Used by
// replay tooling to detect whether a read-tool call would produce the same
// input today as it did at record time.
func DigestMatches(snap ReadSnapshot, output string) bool {
	if !snap.Truncated {
		return snap.Full == output
	}
	h := sha256.Sum256([]byte(output))
	return snap.Hash == fmt.Sprintf("%x", h)
}
