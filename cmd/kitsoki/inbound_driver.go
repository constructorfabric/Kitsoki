// inbound_driver.go — adapts a persisted orchestrator session to the
// inbound.Driver seam so the poll→intent bridge (internal/transport/inbound)
// can advance the same session a browser drives, serialised by the per-session
// writer lock and recording the external author as slots.author.
package main

import (
	"context"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/transport/inbound"
)

// orchestratorBridgeDriver implements [inbound.Driver]: it submits the
// classified intent against the bound session under the store writer lock and
// injects the external author as slots.author — the same identity contract the
// web Driver honours, so a bridge-driven checkpoint records a real principal
// instead of the anonymous fallback.
type orchestratorBridgeDriver struct {
	orch  *orchestrator.Orchestrator
	store store.Store
	sid   app.SessionID
}

// newBridgeDriver binds an orchestrator + persisted store + session id into an
// [inbound.Driver].
func newBridgeDriver(orch *orchestrator.Orchestrator, st store.Store, sid app.SessionID) inbound.Driver {
	return &orchestratorBridgeDriver{orch: orch, store: st, sid: sid}
}

func (d *orchestratorBridgeDriver) SubmitIntent(ctx context.Context, intent string, slots map[string]any, author string) error {
	if slots == nil {
		slots = map[string]any{}
	}
	// Inject the resolved external author unless the classifier already set one.
	if author != "" {
		if existing, present := slots["author"]; !present || existing == nil || existing == "" {
			slots["author"] = author
		}
	}
	return d.store.WithWriterLock(ctx, d.sid, func() error {
		_, err := d.orch.SubmitDirect(ctx, d.sid, intent, slots)
		return err
	})
}
