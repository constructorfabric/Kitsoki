package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	starlarkhost "kitsoki/internal/host/starlark"
)

// CodeactEvaluationConfig is the deterministic evaluator seam shared by
// mcp-codeact-compatible callers. It deliberately accepts concrete capability
// and I/O dependencies from the embedding server: evaluating a snippet never
// creates another agent or grants authority from tool arguments.
type CodeactEvaluationConfig struct {
	WorkingDir   string
	Args         CodeactEvalArgs
	Capabilities starlarkhost.CapabilitySpec
	Inspector    starlarkhost.Inspector
	HTTP         starlarkhost.HTTPClient
}

// EvaluateCodeact evaluates exactly one Starlark action using the same result
// shape as the standalone mcp-codeact server. Embeddings can inject a guarded
// Inspector to bind file operations to a higher-level authority plane.
func EvaluateCodeact(ctx context.Context, cfg CodeactEvaluationConfig) (CodeactEvalOK, error) {
	workingDir, err := filepath.Abs(cfg.WorkingDir)
	if err != nil {
		return CodeactEvalOK{}, fmt.Errorf("resolve codeact working directory: %w", err)
	}
	capabilities := normalizeCodeactCapabilities(cfg.Capabilities)
	if cfg.HTTP != nil {
		ctx = starlarkhost.WithHTTP(ctx, cfg.HTTP)
	}
	if cfg.Inspector != nil {
		ctx = starlarkhost.WithInspector(ctx, cfg.Inspector)
	}
	if capabilities.NeedsHTTP() && !starlarkhost.HasHTTPClient(ctx) {
		ctx = starlarkhost.WithHTTP(ctx, starlarkhost.NewRecordingClient())
	}
	if capabilities.NeedsInspector() && !starlarkhost.HasInspector(ctx) {
		ctx = starlarkhost.WithInspector(ctx, starlarkhost.NewProductionInspector(workingDir))
	}

	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "<mcp-codeact-snippet>",
		Source:       []byte(cfg.Args.Snippet),
		Inputs:       cfg.Args.Inputs,
		World:        cfg.Args.World,
		Capabilities: capabilities,
	})
	if err != nil {
		return CodeactEvalOK{}, err
	}
	return CodeactEvalOK{
		OK:           true,
		Outputs:      res.Outputs,
		Exchanges:    res.Exchanges,
		Inspections:  res.Inspections,
		Capabilities: capabilities.CapabilityLabels(),
		WorkingDir:   workingDir,
	}, nil
}

// CodeactCapabilityHash is a stable digest of a capability declaration. It is
// intentionally independent of Go map order so receipts can prove both the
// requested and server-effective authority later.
func CodeactCapabilityHash(cap starlarkhost.CapabilitySpec) string {
	cap = normalizeCodeactCapabilities(cap)
	b, _ := json.Marshal(cap)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func normalizeCodeactCapabilities(cap starlarkhost.CapabilitySpec) starlarkhost.CapabilitySpec {
	if cap.Stdlib == nil && !cap.World && !cap.NeedsHTTP() && !cap.NeedsInspector() && !cap.AllowsHost() {
		cap = starlarkhost.DefaultCapabilities()
	}
	if cap.Stdlib == nil {
		cap.Stdlib = starlarkhost.DefaultCapabilities().Stdlib
	}
	cap.Stdlib = cloneBoolMap(cap.Stdlib)
	cap.HTTP.Methods = sortedCopy(cap.HTTP.Methods)
	cap.HTTP.Hosts = sortedCopy(cap.HTTP.Hosts)
	cap.FS.ReadPatterns = sortedCopy(cap.FS.ReadPatterns)
	cap.FS.WritePatterns = sortedCopy(cap.FS.WritePatterns)
	cap.Probe.Names = sortedCopy(cap.Probe.Names)
	cap.Host.Verbs = sortedCopy(cap.Host.Verbs)
	return cap
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
