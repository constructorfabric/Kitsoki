// Package host — deterministic agent call-id derivation (wave 3-agent).
//
// DeriveCallID produces a 16-hex-char call identifier that is stable across
// runs.  It is the canonical derivation for all agent calls — both live and
// cassette-backed — so the trace's call_id is deterministic and the runstatus
// SPA can pair AgentCalled with AgentReturned by value alone.
//
// Derivation:
//
//	sha256("agent-call:" + appID + ":" + key)[:16]
//
// where key is:
//   - live call:     "turn:state_path:seq"   (e.g. "3:planning.refine:2")
//   - cassette call: "episodeID:matchIdx"     (e.g. "ep-ask-01:0")
//
// The function is exported so wave 4 tests and downstream packages can call it
// directly without re-implementing the hash.
package host

import (
	"crypto/sha256"
	"fmt"
)

// DeriveCallID returns the 16-hex-char agent call identifier for the given
// appID and key.  It is deterministic: same inputs always produce the same
// output.
//
// For live calls, construct key as:
//
//	fmt.Sprintf("%d:%s:%d", turn, statePath, seq)
//
// For cassette-backed calls, construct key as:
//
//	fmt.Sprintf("%s:%d", episodeID, matchIdx)
func DeriveCallID(appID, key string) string {
	h := sha256.Sum256([]byte("agent-call:" + appID + ":" + key))
	return fmt.Sprintf("%x", h[:8]) // 8 bytes → 16 hex chars
}
