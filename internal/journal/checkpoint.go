package journal

import "strings"

// CheckpointWorldCadence is how many turns elapse between automatic
// world/state checkpoints under DefaultPolicy. It trades replay cost against
// log volume: a smaller value shortens the patch tail a reader must fold after
// the latest checkpoint, a larger one writes fewer full snapshots. 20 is the
// chosen balance for typical session lengths; it is not a hard limit, only the
// default policy's cadence.
const CheckpointWorldCadence = 20

// CheckpointChatCadence is how many appended messages elapse between automatic
// "chats/<id>" checkpoints under DefaultPolicy. Chats grow by message rather
// than by turn, so they checkpoint on message count; 10 keeps a chat's replay
// tail short without snapshotting after every reply.
const CheckpointChatCadence = 10

// CheckpointContext carries the information a Policy uses to decide whether
// to emit a checkpoint for a given document.
type CheckpointContext struct {
	// Turn is the current session turn number.
	Turn int64

	// ChatMessageCount is the number of messages appended to this chat since
	// the last checkpoint. Only relevant for "chats/<id>" documents.
	ChatMessageCount int

	// JobStatusChanged reports whether the job's status changed this turn.
	// Only relevant for "jobs/<id>" documents.
	JobStatusChanged bool
}

// Policy decides when to emit a checkpoint for a document.
type Policy interface {
	// ShouldCheckpoint returns true if a checkpoint should be emitted for doc
	// given ctx.
	ShouldCheckpoint(doc DocID, ctx CheckpointContext) bool
}

// defaultPolicy implements Policy with the standard cadences. It carries no
// state; the zero value is ready to use and is what DefaultPolicy returns.
type defaultPolicy struct{}

// DefaultPolicy returns the standard checkpoint policy:
//
//   - world / state: every CheckpointWorldCadence turns.
//   - chats/<id>: every CheckpointChatCadence appended messages.
//   - jobs/<id>: on every status transition (jobs are low-volume; a snapshot
//     per status change keeps their replay tail trivial).
func DefaultPolicy() Policy {
	return defaultPolicy{}
}

func (defaultPolicy) ShouldCheckpoint(doc DocID, ctx CheckpointContext) bool {
	switch {
	case doc == "world" || doc == "state":
		return ctx.Turn > 0 && ctx.Turn%CheckpointWorldCadence == 0

	case strings.HasPrefix(string(doc), "chats/"):
		return ctx.ChatMessageCount > 0 && ctx.ChatMessageCount%CheckpointChatCadence == 0

	case strings.HasPrefix(string(doc), "jobs/"):
		return ctx.JobStatusChanged

	default:
		// Unknown doc kind falls back to the world/state turn cadence.
		return ctx.Turn > 0 && ctx.Turn%CheckpointWorldCadence == 0
	}
}
