// Package roomchat provides the room-chat lane substrate for
// contextual-room-routing (CRR slice 2). It keys chat threads by
// (app, "room:<lane>", state_path) in the same host.ChatStore that
// meta-mode uses, generalising the meta-mode key model to room lanes.
//
// Lane kinds:
//
//   - LaneHelp  — read-only Q&A / guidance lane (host.agent.ask).
//   - LaneWork  — room policy-scoped work lane (ask when read_only, task/converse when open).
//   - LaneMeta  — existing meta-mode surface (substrate only; driving delegated to metamode).
//
// No LLM calls are made from this package.
package roomchat
