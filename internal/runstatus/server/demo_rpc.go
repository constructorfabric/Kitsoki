// demo_rpc.go — the bare "demo.create"/"demo.record"/"demo.doctor"
// JSON-RPC methods (A2, ~/code/POG/.context/use-case-loop-plan.md §3.5):
// thin wire adapters over host.DemoHandler, mirroring graph_rpc.go's
// bare-graph.* pattern for the same reason — a project catalog like POG's
// isn't installed as a kit's instance data, so the kit dispatch surface
// (which requires host_interfaces bound to a fixed catalog) doesn't fit a
// caller-supplied scenario/manifest path either.
package server

import (
	"context"

	"kitsoki/internal/host"
)

func demoRPC(ctx context.Context, op string, params map[string]any) (any, *rpcError) {
	args := make(map[string]any, len(params)+1)
	for k, v := range params {
		args[k] = v
	}
	args["op"] = op
	res, err := host.DemoHandler(ctx, args)
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: "demo." + op + ": " + err.Error()}
	}
	if res.Error != "" {
		return nil, &rpcError{Code: codeServerError, Message: "demo." + op + ": " + res.Error}
	}
	if res.Data == nil {
		return map[string]any{}, nil
	}
	return res.Data, nil
}
