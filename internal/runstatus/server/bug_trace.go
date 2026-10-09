package server

import (
	"strings"

	"kitsoki/internal/bugreport"
	"kitsoki/internal/runstatus/harscrub"
)

// decodeTraceEvidence resolves the reported session and exports a depersonalized
// JSONL trace attachment. It never reads a client-supplied path; trace_ref is
// treated only as a server-side session id. Unknown ids and snapshot failures
// simply omit the optional attachment so bug filing still works.
func (s *Server) decodeTraceEvidence(params map[string]any, opts harscrub.ScrubOptions) []byte {
	sessionID := strings.TrimSpace(stringParam(params, "trace_ref"))
	if sessionID == "" || s.provider == nil {
		return nil
	}
	entry, rerr := s.resolve(map[string]any{"session_id": sessionID})
	if rerr != nil {
		return nil
	}
	snap, err := entry.Source.Snapshot()
	if err != nil || len(snap.Events) == 0 {
		return nil
	}
	return bugreport.DepersonalizedTraceJSONL(snap.Events, opts)
}
