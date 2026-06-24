package studio

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/host"
)

// mcpProgress bridges Kitsoki's live turn stream onto MCP progress
// notifications. MCP progress is opt-in per call: clients attach a
// progressToken in CallToolParams.Meta, and servers must echo that token on
// notifications/progress. Clients that do not opt in keep the existing
// synchronous tool-call behavior.
type mcpProgress struct {
	session *mcpsdk.ServerSession
	token   any
	tool    string
	step    atomic.Uint64
}

func newMCPProgress(req *mcpsdk.CallToolRequest, tool string) *mcpProgress {
	if req == nil || req.Params == nil || req.Session == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	return &mcpProgress{session: req.Session, token: token, tool: tool}
}

func (p *mcpProgress) Start(ctx context.Context, handle string) {
	if p == nil {
		return
	}
	msg := p.tool + ": started"
	if handle != "" {
		msg = fmt.Sprintf("%s for %s", msg, handle)
	}
	p.notify(ctx, msg)
}

func (p *mcpProgress) Done(ctx context.Context, handle, state string) {
	if p == nil {
		return
	}
	msg := p.tool + ": completed"
	if handle != "" && state != "" {
		msg = fmt.Sprintf("%s for %s at %s", msg, handle, state)
	} else if handle != "" {
		msg = fmt.Sprintf("%s for %s", msg, handle)
	}
	p.notify(ctx, msg)
}

func (p *mcpProgress) AwaitingOperator(ctx context.Context, handle string) {
	if p == nil {
		return
	}
	msg := p.tool + ": awaiting operator"
	if handle != "" {
		msg = fmt.Sprintf("%s for %s", msg, handle)
	}
	p.notify(ctx, msg)
}

func (p *mcpProgress) Error(ctx context.Context, handle string, err error) {
	if p == nil || err == nil {
		return
	}
	msg := p.tool + ": error"
	if handle != "" {
		msg = fmt.Sprintf("%s for %s", msg, handle)
	}
	p.notify(ctx, fmt.Sprintf("%s: %v", msg, err))
}

func (p *mcpProgress) notify(ctx context.Context, message string) {
	if p == nil {
		return
	}
	progress := float64(p.step.Add(1))
	_ = p.session.NotifyProgress(ctx, &mcpsdk.ProgressNotificationParams{
		ProgressToken: p.token,
		Message:       message,
		Progress:      progress,
	})
}

// OnStreamEvent implements host.StreamSink. It forwards live routing,
// assistant narration, extended thinking, and tool breadcrumbs to the MCP
// client as progress notifications before the final CallToolResult exists.
func (p *mcpProgress) OnStreamEvent(ctx context.Context, ev host.StreamEvent) {
	if p == nil || ev.IsResult {
		return
	}
	switch ev.Type {
	case "routing":
		p.notify(ctx, streamRoutingMessage(ev))
	case "assistant":
		if text := strings.TrimSpace(ev.Thinking); text != "" {
			p.notify(ctx, "thinking: "+compactProgressText(text))
		}
		if text := strings.TrimSpace(ev.Text); text != "" {
			p.notify(ctx, "assistant: "+compactProgressText(text))
		}
		for _, tool := range streamToolUses(ev) {
			if tool.Preview != "" {
				p.notify(ctx, fmt.Sprintf("tool: %s %s", tool.Name, compactProgressText(tool.Preview)))
			} else {
				p.notify(ctx, "tool: "+tool.Name)
			}
		}
	}
}

func streamRoutingMessage(ev host.StreamEvent) string {
	parts := []string{"routing"}
	if ev.Intent != "" {
		parts = append(parts, "intent="+ev.Intent)
	}
	if ev.RoutedBy != "" {
		parts = append(parts, "via="+ev.RoutedBy)
	}
	if ev.MatchType != "" {
		parts = append(parts, "match="+ev.MatchType)
	}
	if ev.Confidence > 0 {
		parts = append(parts, fmt.Sprintf("confidence=%.2f", ev.Confidence))
	}
	return strings.Join(parts, " ")
}

func streamToolUses(ev host.StreamEvent) []host.StreamToolUse {
	if len(ev.Tools) > 0 {
		return ev.Tools
	}
	if ev.Tool != "" {
		return []host.StreamToolUse{{Name: ev.Tool, Preview: ev.Preview}}
	}
	return nil
}

func compactProgressText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const limit = 240
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
