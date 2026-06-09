// meta_driver.go — the concrete server.MetaDriver for `kitsoki web`'s meta-mode
// overlay chat, built over an internal/metamode.Controller.
//
// Like the SessionRegistry (registry.go), this lives in package main because it
// composes a metamode.Controller from a per-session *chats.Store + agent
// registry + AppDef — the import-direction inversion the server package's
// MetaDriver interface forces.
//
// Two flavours share the type:
//
//   - per-session: bound to a live *entry, scoped to that session's current
//     FSM state. Serves story.* (edit / ask) and kitsoki.* modes.
//   - self (home screen): entry == nil. Serves only the cross-app kitsoki.*
//     modes (which need no running story); see SessionRegistry.MetaSelf.
package main

import (
	"context"
	"fmt"
	"sort"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/metamode"
	"kitsoki/internal/runstatus/server"
)

// metaDriver adapts a metamode.Controller to server.MetaDriver. chats is the
// concrete store (for transcript reads, which the ChatHandle seam doesn't
// expose). entry is nil for the session-less self driver.
type metaDriver struct {
	ctrl  *metamode.Controller
	chats *chats.Store
	entry *entry
}

var _ server.MetaDriver = (*metaDriver)(nil)

// snapshot builds the metamode.Snapshot for the bound scope: the live session's
// id + current state, or the zero value for the self driver (its modes are
// cross-app and ignore state).
func (d *metaDriver) snapshot() metamode.Snapshot {
	if d.entry == nil {
		return metamode.Snapshot{}
	}
	state := ""
	if snap, err := d.entry.source.Snapshot(); err == nil {
		state = snap.Session.CurrentState
	}
	return metamode.Snapshot{SessionID: d.entry.sid, State: app.StatePath(state)}
}

// turnContext builds the per-turn ambient context. AppFile pins the story tree
// (for edit detection + commit) and StatePath feeds the agent preamble. World /
// RenderedView are left empty — optional, and the no-LLM stub doesn't need them.
func (d *metaDriver) turnContext() metamode.TurnContext {
	if d.entry == nil {
		return metamode.TurnContext{}
	}
	state := ""
	if snap, err := d.entry.source.Snapshot(); err == nil {
		state = snap.Session.CurrentState
	}
	return metamode.TurnContext{StatePath: state, AppFile: d.entry.StoryPath}
}

// enterSession resolves (or resumes) the metamode session for mode. A non-empty
// chatID resumes that specific row; otherwise it resolves by (mode, scope).
func (d *metaDriver) enterSession(ctx context.Context, mode, chatID string) (*metamode.Session, error) {
	if d.ctrl == nil {
		return nil, fmt.Errorf("meta: no controller")
	}
	if chatID != "" {
		return d.ctrl.EnterByChatID(ctx, d.snapshot(), mode, chatID)
	}
	return d.ctrl.Enter(ctx, d.snapshot(), mode)
}

// Modes lists the meta modes available in this scope. The self driver filters
// to the cross-app kitsoki.* group (story modes need a running story).
func (d *metaDriver) Modes(ctx context.Context) ([]server.MetaModeInfo, error) {
	if d.ctrl == nil || d.ctrl.AppDef == nil {
		return nil, fmt.Errorf("meta: no app def")
	}
	out := make([]server.MetaModeInfo, 0, len(d.ctrl.AppDef.MetaModes))
	for key, m := range d.ctrl.AppDef.MetaModes {
		if m == nil {
			continue
		}
		if d.entry == nil && m.Group != "kitsoki" {
			continue // self driver: cross-app modes only
		}
		out = append(out, server.MetaModeInfo{
			Key:      key,
			Label:    m.Label,
			Banner:   m.Banner,
			Agent:    m.Agent,
			ReadOnly: modeIsReadOnly(m),
			Group:    m.Group,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Enter resolves the chat and returns the existing transcript for rehydration.
func (d *metaDriver) Enter(ctx context.Context, mode, chatID string) (server.MetaSession, error) {
	sess, err := d.enterSession(ctx, mode, chatID)
	if err != nil {
		return server.MetaSession{}, err
	}
	msgs, err := d.Transcript(ctx, sess.Chat.ID())
	if err != nil {
		return server.MetaSession{}, err
	}
	return server.MetaSession{ChatID: sess.Chat.ID(), ModeKey: mode, Messages: msgs}, nil
}

// Send issues one turn and maps the metamode SendResult onto the wire result.
func (d *metaDriver) Send(ctx context.Context, mode, chatID, input string) (server.MetaSendResult, error) {
	sess, err := d.enterSession(ctx, mode, chatID)
	if err != nil {
		return server.MetaSendResult{}, err
	}
	res, err := d.ctrl.Send(ctx, sess, input, d.turnContext())
	if err != nil {
		return server.MetaSendResult{}, err
	}
	return server.MetaSendResult{
		Assistant:       res.Assistant,
		ChatID:          res.ChatID,
		ReloadRequested: res.ReloadRequested,
		ChangedFiles:    res.ChangedFiles,
		CommitSHA:       res.CommitSHA,
	}, nil
}

// NewChat archives mode's active chat and opens a fresh one in the same scope.
func (d *metaDriver) NewChat(ctx context.Context, mode, chatID string) (server.MetaSession, error) {
	sess, err := d.enterSession(ctx, mode, chatID)
	if err != nil {
		return server.MetaSession{}, err
	}
	fresh, err := d.ctrl.NewChat(ctx, sess)
	if err != nil {
		return server.MetaSession{}, err
	}
	return server.MetaSession{ChatID: fresh.Chat.ID(), ModeKey: mode, Messages: []server.MetaMessage{}}, nil
}

// Transcript reads the chat row's user/assistant turns from the concrete store.
func (d *metaDriver) Transcript(ctx context.Context, chatID string) ([]server.MetaMessage, error) {
	if d.chats == nil {
		return nil, fmt.Errorf("meta: no chat store")
	}
	msgs, err := d.chats.Transcript(ctx, chatID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]server.MetaMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		out = append(out, server.MetaMessage{Role: m.Role, Text: m.Content})
	}
	return out, nil
}

// modeIsReadOnly reports whether a meta mode cannot edit files. Read-only modes
// (story.ask, kitsoki.ask) carry an explicit {Read,Glob,Grep} allowlist;
// edit-capable modes (story.edit, kitsoki.edit) leave Tools unset. A mode with
// any write/exec tool is editing.
func modeIsReadOnly(m *app.MetaModeDef) bool {
	if len(m.Tools) == 0 {
		return false
	}
	for _, t := range m.Tools {
		switch t {
		case "Write", "Edit", "MultiEdit", "NotebookEdit", "Bash":
			return false
		}
	}
	return true
}
