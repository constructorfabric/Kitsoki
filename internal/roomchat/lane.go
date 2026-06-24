package roomchat

import (
	"context"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
)

// LaneKind discriminates the three contextual-routing lane surfaces.
type LaneKind string

const (
	// LaneHelp is the read-only help lane (host.agent.ask always).
	LaneHelp LaneKind = "help"
	// LaneWork is the room-work lane; posture follows State.WriteMode.
	LaneWork LaneKind = "work"
	// LaneMeta shadows the existing meta-mode surface at the substrate level.
	LaneMeta LaneKind = "meta"
)

// laneRoom returns the room discriminant for a lane kind:
// "room:help", "room:work", or "room:meta".
func laneRoom(kind LaneKind) string { return "room:" + string(kind) }

// Resolver provides get-or-create, append, list, and start-new/resume
// operations over room chat lanes backed by a host.ChatStore.
// It is the room-lane generalisation of the meta-mode keying model:
// the key tuple is (app_id, "room:<kind>", state_path).
type Resolver struct {
	Store host.ChatStore
}

// Active returns the active lane chat for (appID, kind, statePath), creating
// it if none exists. The bool reports whether the chat was newly created.
// "Active" maps to Store.Resolve, which returns the newest non-archived row
// for the (app, room, scopeKey) tuple — the one-active-lane invariant.
func (r *Resolver) Active(ctx context.Context, appID string, kind LaneKind, statePath, title string) (*host.ChatRecord, bool, error) {
	return r.Store.Resolve(ctx, appID, laneRoom(kind), statePath, title)
}

// Append records a message (role/content) on the lane chat identified by chatID.
func (r *Resolver) Append(ctx context.Context, chatID, role, content string) error {
	_, err := r.Store.AppendMessage(ctx, chatID, role, content, nil)
	return err
}

// StartNew archives the current active lane chat for (appID, kind, statePath)
// and mints a fresh one. This mirrors /meta new behaviour.
func (r *Resolver) StartNew(ctx context.Context, appID string, kind LaneKind, statePath, title string) (*host.ChatRecord, error) {
	// Find the current active lane (newest non-archived).
	active, _, err := r.Active(ctx, appID, kind, statePath, title)
	if err != nil {
		return nil, fmt.Errorf("roomchat: StartNew: resolve active: %w", err)
	}
	// Archive it so the next Resolve mints a fresh row.
	if err := r.Store.Archive(ctx, active.ID); err != nil {
		return nil, fmt.Errorf("roomchat: StartNew: archive: %w", err)
	}
	// Resolve mints a fresh row because the previous active is archived.
	fresh, _, err := r.Store.Resolve(ctx, appID, laneRoom(kind), statePath, title)
	if err != nil {
		return nil, fmt.Errorf("roomchat: StartNew: resolve fresh: %w", err)
	}
	return fresh, nil
}

// Resume returns the lane chat identified by chatID regardless of its status.
// Use this to return to a prior (archived) lane chat by id.
func (r *Resolver) Resume(ctx context.Context, chatID string) (*host.ChatRecord, error) {
	return r.Store.Get(ctx, chatID)
}

// VerbForLane selects the agent verb appropriate for (kind, writeMode).
//   - LaneHelp → always "ask" (read-only by definition).
//   - LaneWork with writeMode==app.WriteModeReadOnly → "ask".
//   - LaneWork with any other writeMode (open/empty) → "task".
//   - LaneMeta → "ask" (full driving delegated to the metamode controller).
func VerbForLane(kind LaneKind, writeMode string) string {
	switch kind {
	case LaneWork:
		if writeMode == app.WriteModeReadOnly {
			return "ask"
		}
		return "task"
	default: // LaneHelp, LaneMeta
		return "ask"
	}
}
