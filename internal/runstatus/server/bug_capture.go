// bug_capture.go — the review-before-file capture store backing
// runstatus.bug.preview.
//
// A web operator who opens the bug-report modal first triggers a *preview*: the
// server parses + scrubs the browser HAR, then HOLDS that exact scrubbed
// snapshot under a generated capture id, returning it for the modal to render.
// When the operator confirms, runstatus.bug.report replays that same held
// capture id so the filed HAR is byte-identical to what was reviewed. Direct
// callers without a browser HAR still fall back to the server RPC recorder.
//
// The store is bounded (a small entry cap AND a short TTL) and concurrency-safe.
// It is best-effort scratch space: a capture that ages out simply forces the
// report path to fall back to a fresh server-recorder snapshot.
package server

import (
	"fmt"
	"time"

	"kitsoki/internal/runstatus/harscrub"
)

// captureCap is the maximum number of held preview captures. Oldest entries are
// evicted past this bound on each put.
const captureCap = 16

// captureTTL is how long a held capture survives. A preview the operator never
// confirms ages out so the store cannot grow stale.
const captureTTL = 5 * time.Minute

// capSnap is one held preview: the scrubbed HAR and when it was captured.
type capSnap struct {
	har     *harscrub.Har
	source  string
	created time.Time
}

// putCapture stores a scrubbed HAR under a fresh id and returns the id. It runs
// an eviction sweep (TTL + cap) first so the store stays bounded.
func (s *Server) putCapture(har *harscrub.Har, source string) string {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if s.captureStore == nil {
		s.captureStore = make(map[string]*capSnap)
	}
	now := time.Now()
	s.sweepCapturesLocked(now)

	s.captureSeq++
	id := fmt.Sprintf("cap-%d-%d", now.UnixNano(), s.captureSeq)
	s.captureStore[id] = &capSnap{har: har, source: source, created: now}
	return id
}

// takeCapture removes and returns the held HAR for id. The second result is
// false when id is unknown or has aged out.
func (s *Server) takeCapture(id string) (*harscrub.Har, string, bool) {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if s.captureStore == nil {
		return nil, "", false
	}
	s.sweepCapturesLocked(time.Now())
	snap, ok := s.captureStore[id]
	if !ok {
		return nil, "", false
	}
	delete(s.captureStore, id)
	return snap.har, snap.source, true
}

// sweepCapturesLocked evicts captures older than the TTL, then evicts the oldest
// remaining entries until at most captureCap-1 remain (leaving room for the
// caller's incoming put). Caller must hold captureMu.
func (s *Server) sweepCapturesLocked(now time.Time) {
	for id, snap := range s.captureStore {
		if now.Sub(snap.created) > captureTTL {
			delete(s.captureStore, id)
		}
	}
	for len(s.captureStore) >= captureCap {
		var oldestID string
		var oldest time.Time
		for id, snap := range s.captureStore {
			if oldestID == "" || snap.created.Before(oldest) {
				oldestID, oldest = id, snap.created
			}
		}
		if oldestID == "" {
			break
		}
		delete(s.captureStore, oldestID)
	}
}
