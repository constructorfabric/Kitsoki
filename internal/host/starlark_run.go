// Package host — host.starlark.run — deterministic Starlark glue capability.
//
// This file is the thin adapter between the host.Handler contract and the
// sandbox in internal/host/starlark. The sandbox lives in its own package (with
// its own types and no dependency on package host) so that host can import it
// without an import cycle; this adapter is the only bridge.
//
// See docs/architecture/hosts.md (host.starlark.run) for the author-facing
// reference and internal/host/starlark/doc.go for the sandbox design.
package host

import (
	"context"
	"fmt"
	"os"

	starlarkhost "kitsoki/internal/host/starlark"
)

// StarlarkRunHandler implements host.starlark.run.
//
// Args (from the effect's with: block):
//   - script (string, required): path to the .star file. Resolved to an absolute
//     path by the loader at load time, so by dispatch it is absolute.
//   - inputs (object, optional): the named inputs exposed to the script as
//     ctx.inputs.<name>. Validated against the sidecar's inputs: block.
//
// World access: the script reads world via ctx.world.get("key"). The
// orchestrator injects a read-only snapshot of the current world into ctx
// (WithWorldSnapshot, applied after earlier on_enter binds), so ctx.world
// reflects live world state with no author plumbing. An explicit with.world
// object, when supplied, is overlaid on top of that snapshot (the testrunner
// uses this for direct fixture control). Outputs flow ONLY through main()'s
// return dict — ctx.world is never writable.
//
// Returns Result.Data set to the script's declared outputs, plus the reserved
// key starlarkhost.ExchangesOutputKey carrying the body-free HTTP exchange
// summaries (so they ride the HostReturned trace event). bind: only binds the
// keys an author names, so the summaries never reach world unasked-for.
//
// Error mapping: a *starlark.DomainError (bad input/output, malformed or
// failing script) becomes Result.Error so the effect's on_error: arc fires and
// world.last_error is set; only a true infrastructure failure (e.g. the script
// file cannot be read) is returned as a Go error.
func StarlarkRunHandler(ctx context.Context, args map[string]any) (Result, error) {
	scriptPath, _ := args["script"].(string)
	if scriptPath == "" {
		return Result{Error: "host.starlark.run: script argument is required"}, nil
	}

	// Resolve a relative script path against KITSOKI_APP_DIR (the directory of
	// app.yaml, published by loaders), mirroring the agent prompt/schema path
	// convention (resolvePromptPath). The loader's validateStarlarkEffects
	// resolves against def.BaseDir at load time but does NOT rewrite the
	// effect's with.script to absolute, so by dispatch the arg is still the
	// author-relative path — resolve it here so the read succeeds regardless of
	// the process working directory.
	scriptPath = resolvePromptPath(scriptPath)

	src, err := os.ReadFile(scriptPath)
	if err != nil {
		// The file not existing is an infra failure: the loader is supposed to
		// have verified it at load time, so this means the on-disk state changed
		// out from under us.
		return Result{}, fmt.Errorf("host.starlark.run: read script %q: %w", scriptPath, err)
	}

	// Load the sidecar (script.star + ".yaml"). It is authoritative over the
	// script's interface; the loader verifies it exists, so a read failure here
	// is infra, but a parse failure is the author's fault (domain).
	sidecarPath := scriptPath + ".yaml"
	sidecar, scErr := starlarkhost.LoadSidecar(sidecarPath)
	if scErr != nil {
		if _, statErr := os.Stat(sidecarPath); statErr == nil {
			// File exists but is malformed → domain error.
			return Result{Error: fmt.Sprintf("host.starlark.run: %v", scErr)}, nil
		}
		return Result{}, fmt.Errorf("host.starlark.run: %w", scErr)
	}

	inputs := toStringMap(args["inputs"])

	// ctx.world is the live world snapshot the orchestrator injected, with any
	// explicit with.world object overlaid on top (override wins per key). A
	// direct unit-test caller that injects neither simply gets an empty world.
	worldSnapshot := WorldSnapshotFromContext(ctx)
	if override := toStringMap(args["world"]); override != nil {
		merged := make(map[string]any, len(worldSnapshot)+len(override))
		for k, v := range worldSnapshot {
			merged[k] = v
		}
		for k, v := range override {
			merged[k] = v
		}
		worldSnapshot = merged
	}

	// Inject the production recording HTTP client unless a caller already
	// installed one (the testrunner installs a replay client for flow fixtures).
	runCtx := ctx
	if !starlarkhost.HasHTTPClient(ctx) {
		runCtx = starlarkhost.WithHTTP(ctx, starlarkhost.NewRecordingClient())
	}

	// Inject a production inspector rooted at the run's working dir unless a
	// caller already installed one (the testrunner installs a replay inspector
	// for flow fixtures). Mirrors the HTTP default-injection block above; the
	// safe deny-all default is applied by InspectorFromContext when nothing is
	// injected. Root at world.workdir when present, else the process cwd.
	if !starlarkhost.HasInspector(runCtx) {
		root, _ := worldSnapshot["workdir"].(string)
		if root == "" {
			if cwd, cwdErr := os.Getwd(); cwdErr == nil {
				root = cwd
			}
		}
		runCtx = starlarkhost.WithInspector(runCtx, starlarkhost.NewProductionInspector(root))
	}

	res, runErr := starlarkhost.Run(runCtx, starlarkhost.Params{
		Script:  scriptPath,
		Source:  src,
		Sidecar: sidecar,
		Inputs:  inputs,
		World:   worldSnapshot,
	})
	if runErr != nil {
		if msg, isDomain := starlarkhost.AsDomainError(runErr); isDomain {
			return Result{Error: fmt.Sprintf("host.starlark.run: %s", msg)}, nil
		}
		return Result{}, fmt.Errorf("host.starlark.run: %w", runErr)
	}

	// Surface the script's declared outputs as Result.Data, plus the reserved
	// HTTP-summary and inspection-summary keys so the trace can show what the
	// script called.
	data := make(map[string]any, len(res.Outputs)+2)
	for k, v := range res.Outputs {
		data[k] = v
	}
	if len(res.Exchanges) > 0 {
		summaries := make([]any, len(res.Exchanges))
		for i, ex := range res.Exchanges {
			summaries[i] = map[string]any{
				"method": ex.Method,
				"url":    ex.URL,
				"status": ex.Status,
			}
		}
		data[starlarkhost.ExchangesOutputKey] = summaries
	}
	if len(res.Inspections) > 0 {
		summaries := make([]any, len(res.Inspections))
		for i, ix := range res.Inspections {
			summaries[i] = map[string]any{
				"op":     ix.Op,
				"target": ix.Target,
				"status": ix.Status,
			}
		}
		data[starlarkhost.InspectionsOutputKey] = summaries
	}

	return Result{Data: data}, nil
}

// toStringMap coerces a YAML/JSON-decoded value into a map[string]any. Accepts
// the map[string]any shape goccy/go-yaml and the effect renderer produce, and
// the map[any]any shape some YAML decoders emit; anything else yields nil so a
// missing/ill-typed inputs block is simply an empty input set rather than a
// crash.
func toStringMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			if ks, ok := k.(string); ok {
				out[ks] = val
			}
		}
		return out
	default:
		return nil
	}
}
