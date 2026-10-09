package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/host"
	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/ticketprovider"
)

// registerTicketProviderTools wires reusable Starlark ticket providers onto
// the studio MCP surface. It is omitted in read-only mode because the generic
// op can mutate remote ticket systems.
func (srv *Server) registerTicketProviderTools() {
	if srv.readOnly {
		return
	}
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "ticket.call",
		Description: "Call a reusable ticket_provider/v1 Starlark module. {script (required .star path), op (search|get|comment|transition|list_mine|create|comment_edit|comment_reactions), args? object, dir? base directory for a relative script}. Returns {ok,data?,error?,exchanges?}. Auth is symbolic inside Starlark: ctx.http.get/post(auth=...) names sidecar auth policies, while this Go transport reads env/secrets and applies request headers without exposing secret values to the Starlark runtime.",
	}, srv.handleTicketCall)
}

type TicketCallArgs struct {
	Script string         `json:"script"`
	Op     string         `json:"op"`
	Args   map[string]any `json:"args,omitempty"`
	Dir    string         `json:"dir,omitempty"`
}

type TicketCallResult struct {
	OK        bool                          `json:"ok"`
	Data      map[string]any                `json:"data,omitempty"`
	Error     *ticketprovider.ProviderError `json:"error,omitempty"`
	Exchanges []starlarkhost.HTTPExchange   `json:"exchanges,omitempty"`
}

func (srv *Server) handleTicketCall(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args TicketCallArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.Script) == "" {
		return buildToolError(ErrBadRequest, "ticket.call: script is required"), nil, nil
	}
	if strings.TrimSpace(args.Op) == "" {
		return buildToolError(ErrBadRequest, "ticket.call: op is required"), nil, nil
	}
	script, err := resolveTicketProviderScript(args.Dir, args.Script)
	if err != nil {
		return buildToolError(ErrBadRequest, "ticket.call: "+err.Error()), nil, nil
	}
	res, err := (&ticketprovider.StarlarkProvider{
		Script: script,
		Env:    host.TicketProviderEnvLookup,
	}).Invoke(ctx, args.Op, args.Args)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("ticket.call: %v", err)), nil, nil
	}
	return nil, TicketCallResult{
		OK:        res.Error == nil,
		Data:      res.Data,
		Error:     res.Error,
		Exchanges: res.Exchanges,
	}, nil
}

func resolveTicketProviderScript(dir, script string) (string, error) {
	script = strings.TrimSpace(script)
	if filepath.IsAbs(script) {
		return filepath.Clean(script), nil
	}
	base := strings.TrimSpace(dir)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		base = cwd
	}
	return filepath.Clean(filepath.Join(base, script)), nil
}
