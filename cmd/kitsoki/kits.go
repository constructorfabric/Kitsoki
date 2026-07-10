// kits.go wires the installed-kit extension surface (S3b,
// .context/kits-implementation-plan.md) into the CLI-built servers: `kitsoki
// serve` (MCP) and `kitsoki web` (runstatus JSON-RPC). Both carriers share
// the same internal/kitendpoint.Dispatcher; this file only adapts it to each
// carrier's own seam (internal/mcp.KitCaller for MCP, the dispatcher's own
// type for runstatus, which imports it directly with no cycle risk).
package main

import (
	"context"

	"kitsoki/internal/host"
	"kitsoki/internal/kit"
	"kitsoki/internal/kitendpoint"
	kitsokimcp "kitsoki/internal/mcp"
)

// buildKitDispatcher discovers kit.yaml manifests under kitsDir (empty =
// disabled — a nil dispatcher, meaning "no kits installed", the common case
// today since S2's `kitsoki kit add` doesn't exist yet) and returns a
// kitendpoint.Dispatcher over them plus a fresh host.Registry seeded with
// RegisterBuiltins. A malformed kit.yaml under kitsDir is a fail-fast error —
// same posture as any other config problem at startup.
func buildKitDispatcher(kitsDir string) (*kitendpoint.Dispatcher, error) {
	if kitsDir == "" {
		return nil, nil
	}
	kits, err := kit.DiscoverDir(kitsDir)
	if err != nil {
		return nil, err
	}
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	return kitendpoint.NewDispatcher(kits, reg), nil
}

// mcpKitCaller adapts a *kitendpoint.Dispatcher to internal/mcp.KitCaller —
// the import-cycle-avoiding seam documented on that interface (internal/host
// already imports internal/mcp, and kitendpoint imports internal/host, so
// internal/mcp cannot import kitendpoint directly; cmd/kitsoki is the layer
// above both and bridges them here).
type mcpKitCaller struct {
	d *kitendpoint.Dispatcher
}

func (c mcpKitCaller) Call(ctx context.Context, kitName, iface, op string, args map[string]any) (ok bool, data map[string]any, errMsg string, err error) {
	result, callErr := c.d.Call(ctx, kitName, iface, op, args)
	if callErr != nil {
		return false, nil, "", callErr
	}
	if result.Error != "" {
		return false, nil, result.Error, nil
	}
	return true, result.Data, "", nil
}

// mcpKitOption returns the kitsokimcp.Option that wires d into the MCP
// server's kit_call tool, or nil (no-op) when d is nil.
func mcpKitOption(d *kitendpoint.Dispatcher) kitsokimcp.Option {
	if d == nil {
		return func(*kitsokimcp.Server) {}
	}
	return kitsokimcp.WithKits(mcpKitCaller{d: d})
}
