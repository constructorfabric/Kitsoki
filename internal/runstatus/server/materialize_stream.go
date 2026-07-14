package server

// materialize_stream.go — the GET /rpc/materialize-stream SSE endpoint
// (node-artifact-materialization plan slice 4). Cloned from turn_stream.go's
// pattern: subscribe to a job's events and translate them into
// text/event-stream frames in real time, so the portal sees stage pills
// advance live instead of polling graph.materialize.status.
//
// Protocol:
//
//	GET /rpc/materialize-stream?job=<job_id>
//	  response: text/event-stream
//
//	Events:
//	  data: {"type":"gate","gate_id":"…","passed":true}
//	  data: {"type":"stage","stage_id":"…","status":"in-progress"|"complete"|"failed"}
//	  data: {"type":"artifact","kind":"…","title":"…","path":"…"}
//	  data: {"type":"status","status":"in-progress"|"awaiting-input"|"complete"|"failed"|"cancelled"}
//	  data: {"type":"error","message":"…"}
//	  data: {"type":"done"}
//
// gate frames fire once, immediately on connect: every gate named by the
// type's materialize.gates already passed validation before
// graph.materialize.start allowed the job to be submitted (see
// materialize.Start's gate-check-before-submit contract), so there is
// nothing to wait on — a late-connecting subscriber still gets the full
// picture up front instead of missing a frame that only ever fires once, at
// a moment before it connected.
//
// A subscriber that connects late (fast/deterministic pilot stories
// routinely finish, or get several rooms in, before a browser's EventSource
// opens — see internal/materialize's package doc on the Subscribe-after-
// Start race) is healed by an initial snapshot replay: the handler
// subscribes to the job FIRST, then replays the server's own accumulated
// per-stage/artifact/status bookkeeping (materializeJobState, fed by
// trackMaterializeJob's start-synchronous subscription) as ordinary frames
// before entering the live loop. Subscribe-first means nothing falls in the
// gap; the price is that a queued live event may re-report state older than
// the snapshot, so emits are monotonic per stage (a stage never regresses
// from complete back to in-progress) and artifact frames dedup by path.
// This closes the stuck-pill gap the first cut deferred to the
// graph.materialize.status poll fallback: a client whose EventSource
// connected mid-run used to keep whatever pills it had missed at "waiting"
// forever, because the fallback only engaged when the stream ERRORED, not
// when it connected late.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"kitsoki/internal/jobs"
	"kitsoki/internal/materialize"
)

// materializeStreamFrame is one SSE data payload for /rpc/materialize-stream.
type materializeStreamFrame struct {
	Type string `json:"type"` // "gate" | "stage" | "artifact" | "status" | "error" | "done" | "ping"

	// gate
	GateID string `json:"gate_id,omitempty"`
	Passed bool   `json:"passed,omitempty"`

	// stage
	StageID string `json:"stage_id,omitempty"`

	// artifact
	Kind  string `json:"kind,omitempty"`
	Title string `json:"title,omitempty"`
	Path  string `json:"path,omitempty"`

	// status (also reused as the stage frame's status field)
	Status string `json:"status,omitempty"`

	// error
	Message string `json:"message,omitempty"`
}

// jobStatusToWire maps a jobs.JobStatus to the plan's node-pill vocabulary:
// running -> in-progress, awaiting_input -> awaiting-input, done -> complete,
// failed/cancelled pass through unchanged.
func jobStatusToWire(status jobs.JobStatus) string {
	switch status {
	case jobs.JobRunning:
		return "in-progress"
	case jobs.JobAwaitingInput:
		return "awaiting-input"
	case jobs.JobDone:
		return "complete"
	default:
		return string(status) // "failed" | "cancelled"
	}
}

func (s *Server) handleMaterializeStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jobID := r.URL.Query().Get("job")
	if jobID == "" {
		http.Error(w, `materialize-stream: missing "job" query parameter`, http.StatusBadRequest)
		return
	}

	s.materializeMu.Lock()
	state, ok := s.materializeJobs[jobs.JobID(jobID)]
	s.materializeMu.Unlock()
	if !ok {
		http.Error(w, "materialize-stream: unknown job: "+jobID, http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	emit := func(f materializeStreamFrame) {
		b, err := json.Marshal(f)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	// Monotonic per-stage emit: the snapshot replay below and the live
	// subscription can both report the same stage, and a queued live event
	// can lag the snapshot — never let a pill regress (waiting <
	// in-progress < complete/failed) or re-emit a status it already has.
	stageRank := func(status string) int {
		switch status {
		case "in-progress":
			return 1
		case "complete", "failed":
			return 2
		default: // "waiting"
			return 0
		}
	}
	emittedStage := map[string]int{}
	emitStage := func(stageID, status string) {
		rank := stageRank(status)
		if rank == 0 || rank <= emittedStage[stageID] {
			return // still waiting, or would repeat/regress what was already sent
		}
		emittedStage[stageID] = rank
		emit(materializeStreamFrame{Type: "stage", StageID: stageID, Status: status})
	}
	emittedArtifacts := map[string]bool{}
	emitArtifact := func(a materializeArtifact) {
		if a.Path == "" || emittedArtifacts[a.Path] {
			return
		}
		emittedArtifacts[a.Path] = true
		emit(materializeStreamFrame{Type: "artifact", Kind: a.Kind, Title: a.Title, Path: a.Path})
	}

	for _, gateID := range state.gatesSnapshot() {
		emit(materializeStreamFrame{Type: "gate", GateID: gateID, Passed: true})
	}

	// Subscribe BEFORE snapshotting so no event falls between the two; then
	// replay the snapshot as frames (see the package doc's late-subscriber
	// paragraph). A snapshot that is already terminal ends the stream right
	// here with the same status/done tail a live run produces.
	ch, unsub := s.materializeSched.Subscribe(jobs.JobID(jobID))
	defer unsub()

	snapStages, snapStatus, snapArtifacts := state.snapshot()
	for _, st := range snapStages {
		emitStage(st.ID, st.Status)
	}
	for _, a := range snapArtifacts {
		emitArtifact(a)
	}
	switch jobs.JobStatus(snapStatus) {
	case jobs.JobDone, jobs.JobFailed, jobs.JobCancelled:
		if jobs.JobStatus(snapStatus) == jobs.JobFailed {
			if job, ok := s.materializeSched.Get(jobs.JobID(jobID)); ok && job.Error != "" {
				emit(materializeStreamFrame{Type: "error", Message: job.Error})
			}
		}
		emit(materializeStreamFrame{Type: "status", Status: jobStatusToWire(jobs.JobStatus(snapStatus))})
		emit(materializeStreamFrame{Type: "done"})
		return
	case jobs.JobAwaitingInput:
		emit(materializeStreamFrame{Type: "status", Status: jobStatusToWire(jobs.JobAwaitingInput)})
	}

	// Idle-connection heartbeat, matching handleTurnStream: a deterministic
	// story can run to completion between two heartbeat frames with nothing
	// else on the wire, and a dev proxy or browser can drop a silent SSE
	// connection before the terminal "done" frame arrives.
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			emit(materializeStreamFrame{Type: "ping"})
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if se, ok := ev.Progress.(materialize.StageEvent); ok {
				emitStage(se.Stage, se.Status)
			}
			switch ev.Status {
			case jobs.JobRunning, jobs.JobAwaitingInput:
				emit(materializeStreamFrame{Type: "status", Status: jobStatusToWire(ev.Status)})
			case jobs.JobDone:
				if a, ok := materializeArtifactFromResult(state, ev); ok {
					emitArtifact(a)
				}
				emit(materializeStreamFrame{Type: "status", Status: jobStatusToWire(ev.Status)})
				emit(materializeStreamFrame{Type: "done"})
				return
			case jobs.JobFailed:
				emit(materializeStreamFrame{Type: "error", Message: ev.Error})
				emit(materializeStreamFrame{Type: "status", Status: jobStatusToWire(ev.Status)})
				emit(materializeStreamFrame{Type: "done"})
				return
			case jobs.JobCancelled:
				emit(materializeStreamFrame{Type: "status", Status: jobStatusToWire(ev.Status)})
				emit(materializeStreamFrame{Type: "done"})
				return
			}
		}
	}
}
