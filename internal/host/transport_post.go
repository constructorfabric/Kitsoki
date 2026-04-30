// Package host — host.transport.post bridges an `invoke: host.transport.post`
// effect into the active transport.Registry, so phase templates can dispatch
// output to any registered transport (TUI, Jira, Bitbucket).
//
// The registry is injected per-call through context with
// transport.WithRegistry. The orchestrator wires this in before dispatching
// host calls so app authors don't need to think about plumbing.
package host

import (
	"context"
	"encoding/json"
	"fmt"

	"hally/internal/transport"
)

// TransportPostHandler implements host.transport.post.
//
// Required args:
//   - transport (string): registered transport ID, e.g. "jira" / "tui".
//   - thread    (string): the surface-specific thread identifier.
//   - body      (string): the message body to deliver.
//
// Optional args:
//   - title      (string): short heading; some transports surface this.
//   - phase_id   (string): originating phase ID (for orchestrator-side dedup).
//   - bot_marker (string): prefix for inbound-polling filters; default "[hally]".
//
// Returns Result.Data with:
//   - message_id (string): transport-assigned message identifier.
//
// On expected errors (no registry in context, transport not registered,
// missing args, transport.Post failure) the handler returns Result{Error: ...}
// rather than a Go error so the state machine surfaces the failure via
// on_error: routing.
func TransportPostHandler(ctx context.Context, args map[string]any) (Result, error) {
	reg := transport.FromContext(ctx)
	if reg == nil {
		return Result{Error: "host.transport.post: no transport registry installed in context"}, nil
	}

	transportID, _ := args["transport"].(string)
	thread, _ := args["thread"].(string)
	if transportID == "" || thread == "" {
		return Result{Error: "host.transport.post: transport and thread are required"}, nil
	}

	msg := transport.Message{
		PhaseID:   stringArg(args, "phase_id"),
		Title:     stringArg(args, "title"),
		Body:      bodyArg(args, "body"),
		BotMarker: stringArg(args, "bot_marker"),
	}

	id, err := reg.Post(ctx, transport.SessionKey{
		Transport: transportID,
		Thread:    thread,
	}, msg)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	return Result{Data: map[string]any{"message_id": id}}, nil
}

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// bodyArg coerces the body argument into a string suitable for posting to a
// transport.  Strings pass through verbatim.  Maps and slices are pretty-
// printed as JSON so a structured artifact (e.g. the validator-captured
// dict bound from `submitted`) renders meaningfully in a Jira/Bitbucket
// comment instead of silently disappearing through a failed type assertion.
// Nil renders as the empty string.  Anything else is rendered via fmt.Sprint
// so the human reader at least sees the type's stringification rather than
// dropping the post.
func bodyArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case map[string]any, []any:
		// Pretty-print structured payloads so the comment is readable.
		// json.MarshalIndent is deterministic for the same input shape.
		if data, err := json.MarshalIndent(x, "", "  "); err == nil {
			return string(data)
		}
		return fmt.Sprint(x)
	default:
		return fmt.Sprint(x)
	}
}
