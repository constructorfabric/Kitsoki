package journal

import (
	"encoding/json"
	"time"

	"kitsoki/internal/app"
)

// DocID names a physical document tracked by the journal.
// Predefined values are "world", "state", and dynamic forms "chats/<id>",
// "jobs/<id>". Empty for typed-only entries.
type DocID string

// Version is a monotonic per-session-per-document counter.
// It starts at 1 and increments on every patch or checkpoint write.
// Zero is the sentinel "before any write".
type Version int64

// Entry is a single journal record. The Kind field determines the shape of Body.
type Entry struct {
	Ts         time.Time       `json:"ts"`
	Session    app.SessionID   `json:"session_id"`
	Turn       app.TurnNumber  `json:"turn"`
	Seq        int             `json:"seq"`
	Kind       string          `json:"ev"`
	Doc        DocID           `json:"doc,omitempty"`
	DocVersion Version         `json:"doc_version,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// PatchOp is a single RFC 6902 JSON-Patch operation.
type PatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

// ---- Patch entry kinds ------------------------------------------------------

// KindWorldPatch is a JSON-Patch batch against the "world" document.
const KindWorldPatch = "world.patch"

// KindStateTransition is a JSON-Patch op against the "state" document.
const KindStateTransition = "state.transition"

// KindChatsAppend is a JSON-Patch op against a "chats/<id>" document.
const KindChatsAppend = "chats.append"

// KindJobsUpdate is a JSON-Patch op against a "jobs/<id>" document.
const KindJobsUpdate = "jobs.update"

// ---- Checkpoint kinds -------------------------------------------------------

// KindWorldCheckpoint carries a full "world" document snapshot.
const KindWorldCheckpoint = "world.checkpoint"

// KindStateCheckpoint carries a full "state" document snapshot.
const KindStateCheckpoint = "state.checkpoint"

// KindChatsCheckpoint carries a full "chats/<id>" document snapshot.
const KindChatsCheckpoint = "chats.checkpoint"

// KindJobsCheckpoint carries a full "jobs/<id>" document snapshot.
const KindJobsCheckpoint = "jobs.checkpoint"

// ---- Typed entry kinds ------------------------------------------------------

// KindHostInvoked records a host handler invocation with its arguments.
const KindHostInvoked = "host.invoked"

// KindHostDispatched records a background host dispatch (job submitted).
const KindHostDispatched = "host.dispatched"

// KindHostReturned records the return value of a host handler call.
const KindHostReturned = "host.returned"

// KindClarifyRequested records a foreground clarification request with its schema.
const KindClarifyRequested = "clarify.requested"

// KindClarifyAnswered records the user's response to a clarification request.
const KindClarifyAnswered = "clarify.answered"

// KindOffPathQuestion records an off-path question submitted to the oracle.
const KindOffPathQuestion = "offpath.question"

// KindOffPathAnswer records the oracle's answer to an off-path question.
const KindOffPathAnswer = "offpath.answer"

// KindOffPathChatResolved records which chat was resolved for an off-path session.
const KindOffPathChatResolved = "offpath.chat.resolved"

// KindTimeoutArmed records that a state timeout was armed.
const KindTimeoutArmed = "timeout.armed"

// KindTimeoutCancelled records that a state timeout was cancelled.
const KindTimeoutCancelled = "timeout.cancelled"

// KindTimeoutFired records that a state timeout fired and a transition was triggered.
const KindTimeoutFired = "timeout.fired"

// KindInboxItemOpened records that the user opened an inbox item.
const KindInboxItemOpened = "inbox.item.opened"

// KindInboxItemDismissed records that the user dismissed an inbox item.
const KindInboxItemDismissed = "inbox.item.dismissed"

// KindValidationRejected records that an intent was rejected by the validator.
const KindValidationRejected = "validation.rejected"

// KindGuardRejected records that a transition guard rejected an intent.
const KindGuardRejected = "guard.rejected"

// KindOffPathEntered records the user entering off-path mode.
const KindOffPathEntered = "offpath.entered"

// KindOffPathExited records the user leaving off-path mode.
const KindOffPathExited = "offpath.exited"

// KindViewRendered carries the literal rendered view text the TUI displayed at
// the end of a turn. Resume reads these entries verbatim to
// rehydrate the transcript pane without re-evaluating any view template.
const KindViewRendered = "view.rendered"

// KindDisambigPresented records that the TUI displayed a disambiguation menu.
const KindDisambigPresented = "disambig.presented"

// KindDisambigChosen records the user's selection from a disambiguation menu.
const KindDisambigChosen = "disambig.chosen"

// KindChatDriveSubmitted records a chat-input-queue Enqueue (a drive
// submitted against a chat). Body: {drive_id, chat_id, transport, actor,
// payload_snippet}. Resume reads these to render historical drives in the
// transcript and to surface in-flight ones (when paired with the live
// chat_input_queue row).
const KindChatDriveSubmitted = "chat.drive.submitted"

// KindChatDriveCompleted records a chat drive reaching terminal "done"
// (the dispatch produced an assistant message). Body: {drive_id, chat_id,
// result_seq}.
const KindChatDriveCompleted = "chat.drive.completed"

// KindChatDriveFailed records a chat drive reaching terminal "failed"
// (dispatch errored). Body: {drive_id, chat_id, error_message}.
const KindChatDriveFailed = "chat.drive.failed"

// KindChatDriveDismissed records a chat drive that was operator-suppressed
// without dispatch. Body: {drive_id, chat_id, reason}.
const KindChatDriveDismissed = "chat.drive.dismissed"

// ---- oracle call tracing (Phase N: full prompt/response capture) -------------

// KindOracleCall records a completed oracle verb call with full prompt,
// system prompt, and response payload. One entry per oracle.* call.
//
// Body shape:
//
//	{verb, agent, model, duration_ms, prompt_tokens, response_tokens, cost_usd?,
//	 system_prompt, prompt, input, response, error?,
//	 call_id}
//
// tool_calls and files_changed are NOT stored here — they are aggregated at
// export time from KindTaskTool / KindTaskEnd entries in the same window.
// call_id is a per-call UUID that correlates this entry with the lean slog
// oracle.<verb>.complete record emitted in the same call.
const KindOracleCall = "oracle.call"

// ---- oracle-split Phase 4 event kinds (task trace) ---------------------------

// KindTaskTool records a single tool call by a task agent. Body shape:
//
//	{tool, input, output_preview, trace_id, parent_trace_id, seq}
//
// Stream-only variants (task.tool.start / task.tool.end) are emitted
// to the StreamSink but NOT written to the journal (D17). Only this
// rolled-up event is journalled — one entry per tool call.
const KindTaskTool = "task.tool"

// KindTaskAcceptanceAttempt records one pass of the acceptance loop
// (post_cmd run or schema-only check). Body:
//
//	{attempt, exit_code, stdout_preview, rejected_reason}
const KindTaskAcceptanceAttempt = "task.acceptance.attempt"

// KindTaskEnd records the terminal event of a task call. Body:
//
//	{outcome, submitted, files_changed, final_diff, replay_mode,
//	 initial_state_hash, trace_id}
//
// replay_mode is one of "file_diff", "sandboxed_write", or
// "external_side_effect".
const KindTaskEnd = "task.end"

// ---- artifact tracing -------------------------------------------------------

// KindArtifactEmitted records that a host call produced a named media artifact
// (e.g. a rendered PNG, MP4, PDF, HTML page, or slideshow). Emitted by the
// host.artifacts_dir handler when the caller supplies src_path + kind instead
// of body text.
//
// Body shape (ArtifactEvent):
//
//	{id, kind, mime, label, path, producer, size_bytes, created_at}
//
// id         — stable handle (<basename>#<counter>, same shape as message_id)
// kind       — one of: video / image / pdf / html / slideshow
// mime       — MIME type (e.g. "video/mp4", "image/png")
// label      — human-readable display name (from args["label"])
// path       — absolute path of the file under the artifacts root
// producer   — host call name that produced the artifact (always "host.artifacts_dir")
// size_bytes — file size in bytes after copy
// created_at — timestamp of the copy operation
const KindArtifactEmitted = "artifact.emitted"

// ArtifactEvent is the body shape for KindArtifactEmitted entries.
// Writers marshal this to json.RawMessage and assign it to Entry.Body.
// Readers switch on Entry.Kind == KindArtifactEmitted and unmarshal here.
type ArtifactEvent struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Mime      string    `json:"mime,omitempty"`
	Label     string    `json:"label,omitempty"`
	Path      string    `json:"path"`
	Producer  string    `json:"producer"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// ---- IDE link tracing -------------------------------------------------------

// KindIDEContextCaptured records one host.ide.get_* pull whose result feeds a
// decision (lands toward an oracle prompt or a world slot). It is the labeled
// datapoint that makes "the room decided X because the editor showed Y"
// reconstructable from the trace alone. The raw host.ide.* request/response is
// already covered by host.invoked/host.returned; this entry pins the IDE
// provenance (which workspace/port served it) and a digest of the response so
// the decision's editor inputs are auditable without re-opening the socket.
//
// Body shape:
//
//	{verb, request, response_digest, port, workspace}
//
// verb            — the host.ide.* verb ("get_diagnostics" | "get_selection" | "get_open_editors")
// request         — the args map sent to the tool
// response_digest — a short stable digest of the response (sha256 hex prefix of the
//
//	canonical JSON; NOT the raw selection text — keeps secrets/PII
//	out of the trace per the selection-privacy lean)
//
// port            — link.Port()
// workspace       — link.Workspace()
const KindIDEContextCaptured = "ide.context_captured"

// ---- Predicate helpers ------------------------------------------------------

// patchKinds is the set of patch-entry kind values.
var patchKinds = map[string]struct{}{
	KindWorldPatch:      {},
	KindStateTransition: {},
	KindChatsAppend:     {},
	KindJobsUpdate:      {},
}

// checkpointKinds is the set of checkpoint-entry kind values.
var checkpointKinds = map[string]struct{}{
	KindWorldCheckpoint: {},
	KindStateCheckpoint: {},
	KindChatsCheckpoint: {},
	KindJobsCheckpoint:  {},
}

// IsPatchKind reports whether kind is a patch-entry kind.
func IsPatchKind(kind string) bool {
	_, ok := patchKinds[kind]
	return ok
}

// IsCheckpointKind reports whether kind is a checkpoint-entry kind.
func IsCheckpointKind(kind string) bool {
	_, ok := checkpointKinds[kind]
	return ok
}

// IsTypedKind reports whether kind is a typed (semantic) entry kind — i.e.
// neither a patch nor a checkpoint.
func IsTypedKind(kind string) bool {
	return !IsPatchKind(kind) && !IsCheckpointKind(kind)
}
