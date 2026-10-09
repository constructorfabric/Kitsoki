package graphsrv

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const graphClaimInputSchema = `{"type":"object","properties":{"catalog":{"type":"string"},"id":{"type":"string"},"branch":{"type":"string"},"liveness_handle":{"type":"string"}},"required":["id","branch","liveness_handle"],"additionalProperties":false}`
const graphReleaseInputSchema = `{"type":"object","properties":{"catalog":{"type":"string"},"id":{"type":"string"},"liveness_handle":{"type":"string"}},"required":["id","liveness_handle"],"additionalProperties":false}`

type graphClaimArgs struct {
	Catalog string `json:"catalog,omitempty"`
	ID      string `json:"id"`
	Branch  string `json:"branch"`
	Handle  string `json:"liveness_handle"`
}
type graphReleaseArgs struct {
	Catalog string `json:"catalog,omitempty"`
	ID      string `json:"id"`
	Handle  string `json:"liveness_handle"`
}
type graphClaimOK struct {
	OK              bool             `json:"ok"`
	Catalog         string           `json:"catalog"`
	ID              string           `json:"id"`
	Claim           ClaimProvenance  `json:"claim"`
	TransferredFrom *ClaimProvenance `json:"transferred_from,omitempty"`
}
type graphReleaseOK struct {
	OK       bool            `json:"ok"`
	Catalog  string          `json:"catalog"`
	ID       string          `json:"id"`
	Released ClaimProvenance `json:"released"`
}

func registerGraphClaimTools(srv *mcpsdk.Server, deps *Deps) {
	if deps.Mode == ModeRead {
		return
	}
	srv.AddTool(&mcpsdk.Tool{Name: "graph.claim", Description: "Claim a graph node for the configured actor and branch. A live claim is refused with holder provenance; a holder observed dead transfers atomically.", InputSchema: json.RawMessage(graphClaimInputSchema)}, recorded(deps, "graph.claim", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphClaim(ctx, deps, req)
	}))
	srv.AddTool(&mcpsdk.Tool{Name: "graph.release", Description: "Release the caller's graph-node claim. Actor and liveness handle must match the holder.", InputSchema: json.RawMessage(graphReleaseInputSchema)}, recorded(deps, "graph.release", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphRelease(ctx, deps, req)
	}))
}

func claimTarget(ctx context.Context, deps *Deps, catalog, id string) (string, string, string, *ErrorPayload) {
	if deps.Actor == "" {
		return "", "", "", NewError(CodeValidation, "graph claim tools require server --actor", "start graph MCP through the sanctioned launcher so actor provenance is stamped")
	}
	path, _, alias, ep := deps.resolveWrite(ctx, catalog)
	if ep != nil {
		return "", "", "", ep
	}
	res, err := deps.Registry.Invoke(ctx, "host.graph.get", map[string]any{"catalog_path": path, "ids": []string{id}})
	if err != nil || res.Error != "" {
		return "", "", "", NewError(CodeUnknownNode, fmt.Sprintf("graph claim target %q was not found", id), "call graph.find or graph.get to choose a bound node")
	}
	nodes, _ := res.Data["nodes"].([]any)
	if len(nodes) != 1 {
		return "", "", "", NewError(CodeUnknownNode, fmt.Sprintf("graph claim target %q was not found", id), "")
	}
	return path, alias, id, nil
}

func handleGraphClaim(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphClaimArgs
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return errorResult(NewError(CodeValidation, "graph.claim: arguments are not valid JSON: "+err.Error(), "")), nil
	}
	if args.ID == "" || args.Branch == "" || args.Handle == "" {
		return errorResult(NewError(CodeValidation, "graph.claim requires id, branch, and liveness_handle", "")), nil
	}
	path, alias, id, ep := claimTarget(ctx, deps, args.Catalog, args.ID)
	if ep != nil {
		return errorResult(ep), nil
	}
	claim, holder, transferred := deps.Claims.Claim(ctx, path, id, deps.Actor, args.Branch, args.Handle)
	if !transferred && holder != nil {
		return journal(deps, "graph.claim", path, alias, req.Params.Arguments, errorResult(NewError(CodeClaimHeld, fmt.Sprintf("graph node %q is claimed by actor %q on branch %q", id, holder.Actor, holder.Branch), "wait for release or use the queue after its liveness detector observes the holder dead")), ""), nil
	}
	return journal(deps, "graph.claim", path, alias, req.Params.Arguments, okResult(graphClaimOK{OK: true, Catalog: alias, ID: id, Claim: claim, TransferredFrom: func() *ClaimProvenance {
		if transferred {
			return holder
		}
		return nil
	}()}), ""), nil
}

func handleGraphRelease(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphReleaseArgs
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return errorResult(NewError(CodeValidation, "graph.release: arguments are not valid JSON: "+err.Error(), "")), nil
	}
	if args.ID == "" || args.Handle == "" {
		return errorResult(NewError(CodeValidation, "graph.release requires id and liveness_handle", "")), nil
	}
	path, alias, id, ep := claimTarget(ctx, deps, args.Catalog, args.ID)
	if ep != nil {
		return errorResult(ep), nil
	}
	holder, released := deps.Claims.Release(path, id, deps.Actor, args.Handle)
	if !released {
		return journal(deps, "graph.release", path, alias, req.Params.Arguments, errorResult(NewError(CodeNotClaimHolder, fmt.Sprintf("graph node %q is not claimed by actor %q with this liveness handle", id, deps.Actor), "current holder provenance is intentionally not released to an untrusted releaser")), ""), nil
	}
	return journal(deps, "graph.release", path, alias, req.Params.Arguments, okResult(graphReleaseOK{OK: true, Catalog: alias, ID: id, Released: holder}), ""), nil
}
