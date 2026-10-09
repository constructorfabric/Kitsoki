package starlark

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.starlark.net/lib/math"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkjson"
	"go.starlark.net/syntax"
)

// ExchangesOutputKey is the reserved key under which Run reports the body-free
// HTTP exchange summaries in Result.Outputs. The host.starlark.run adapter
// copies it into host.Result.Data so it rides the HostReturned trace event,
// while the orchestrator's bind: only ever binds the keys an author names — so
// this summary is visible in the trace without polluting world unless asked
// for. Authors MUST NOT declare an output with this name.
const ExchangesOutputKey = "__http_exchanges"

// InspectionsOutputKey is the reserved key under which Run reports the body-free
// fs/probe inspection summaries in Result.Outputs, the inspection-side analogue
// of ExchangesOutputKey. The host.starlark.run adapter copies it into
// host.Result.Data so it rides the HostReturned trace event without polluting
// world unless an author binds it. Authors MUST NOT declare an output with this
// name.
const InspectionsOutputKey = "__inspections"

// mainFuncName is the entry point Run calls in every script.
const mainFuncName = "main"

// maxExecutionSteps bounds a script's instruction count. Starlark has no I/O or
// recursion-into-the-host beyond the narrow ctx, but an author can still write
// an accidental hot loop; this turns that into a clean error instead of a hung
// turn. Generous enough for real glue scripts, far below anything pathological.
const maxExecutionSteps = 10_000_000

// Params is the input to Run. The adapter in package host fills it from the
// effect's with: block and whatever world snapshot it can access.
type Params struct {
	// Script is the absolute path to the .star file. Used as the Starlark
	// filename (so tracebacks are meaningful) and to read the source.
	Script string
	// Source is the script bytes. When non-nil it is used directly and Script
	// is treated as a label only — this lets callers (and the Example test)
	// run a script without touching the filesystem.
	Source []byte
	// Sidecar is the parsed interface declaration. When nil, no input/output
	// validation is performed (the loader normally guarantees it is present).
	Sidecar *Sidecar
	// Inputs are the resolved effect inputs (ctx.inputs). Validated against the
	// sidecar before evaluation.
	Inputs map[string]any
	// World is the read-only world snapshot exposed via ctx.world.get.
	World map[string]any
	// Capabilities is the normalized authority for this run. The zero value is
	// interpreted as DefaultCapabilities: pure helpers plus ctx.inputs/world and
	// no external I/O.
	Capabilities CapabilitySpec
}

// Result is the output of Run. Outputs is the validated return dict of main();
// Exchanges is the body-free HTTP summary for the trace and Inspections is the
// body-free fs/probe summary (both also mirrored into Outputs under their
// reserved keys by the adapter — Run leaves Outputs clean so the validation
// against the sidecar is exact).
type Result struct {
	Outputs     map[string]any
	Exchanges   []HTTPExchange
	Inspections []InspectExchange
}

// DomainError is an EXPECTED, author-facing failure: a bad input type, a
// missing output, a malformed script, a Starlark runtime error. The adapter
// maps it to host.Result{Error: ...} (which fires the effect's on_error: arc)
// rather than a Go error (which is reserved for true infra failure). Keeping the
// distinction here means the sandbox itself decides what is "the script's fault"
// versus "the engine's fault".
type DomainError struct {
	msg string
}

func (e *DomainError) Error() string { return e.msg }

// AsDomainError reports whether err is a DomainError and returns its message.
// The adapter uses this to decide between Result.Error and a Go error.
func AsDomainError(err error) (string, bool) {
	var de *DomainError
	if errors.As(err, &de) {
		return de.msg, true
	}
	return "", false
}

// Run evaluates a Starlark script deterministically and returns its outputs.
//
// Sequence: validate inputs against the sidecar → exec the file in a sandboxed
// thread with deterministic json/math/yaml helpers predeclared → resolve and call main(ctx) →
// convert and validate the returned dict against the sidecar. Any script-level
// problem (parse error, runtime error, missing main, wrong shapes) is a
// *DomainError; only a Go-internal failure (which should not normally happen)
// is returned as a plain error.
//
// The HTTPClient is resolved from ictx (WithHTTP). When none is injected, any
// ctx.http call fails — the sandbox never reaches the network by default.
func Run(ictx context.Context, p Params) (*Result, error) {
	return runFunction(ictx, p, mainFuncName)
}

// RunFunction evaluates a Starlark script and calls the named function with the
// same ctx object Run passes to main(ctx). It is for provider-style modules
// whose public interface is a set of pure functions (for example search(ctx),
// get(ctx), comment(ctx)) rather than one main(ctx) glue entry point.
func RunFunction(ictx context.Context, p Params, function string) (*Result, error) {
	function = strings.TrimSpace(function)
	if function == "" {
		return nil, &DomainError{msg: "starlark: function name is required"}
	}
	return runFunction(ictx, p, function)
}

func runFunction(ictx context.Context, p Params, function string) (*Result, error) {
	cap := normalizeRunCapabilities(p.Capabilities)
	ictx = ApplyCapabilityPolicy(ictx, cap)

	// 0. Normalize exotic numeric kinds to the canonical JSON-ish shapes
	//    (int64/float64) before validation and conversion. Effect templating
	//    and expression evaluation can hand over uint64/int32/... values (e.g.
	//    a world counter incremented by an expr), which would otherwise fail
	//    the sidecar's int check here and error goToStarlark's strict type
	//    switch when read via ctx.world.get.
	if p.Inputs != nil {
		p.Inputs = normalizeYAML(p.Inputs).(map[string]any)
	}
	if p.World != nil {
		p.World = normalizeYAML(p.World).(map[string]any)
	}

	// 1. Input validation at the boundary, before any evaluation.
	if p.Sidecar != nil {
		if err := p.Sidecar.validateInputs(p.Inputs); err != nil {
			return nil, err
		}
	}

	// 2. Build the sandboxed interpreter. FileOptions are left at their strict
	//    defaults: no set built-in, no global reassignment, no recursion — all
	//    of which keep behaviour predictable and deterministic.
	thread := &starlark.Thread{Name: "host.starlark.run"}
	thread.SetMaxExecutionSteps(maxExecutionSteps)
	coverage := coverageFromContext(ictx)
	if coverage.flowFile != "" {
		thread.SetLocal("kitsoki_flow_file", coverage.flowFile)
	}

	predeclared := starlark.StringDict{}
	if cap.Stdlib["json"] {
		predeclared["json"] = starlarkjson.Module
	}
	if cap.Stdlib["math"] {
		predeclared["math"] = math.Module
	}
	if cap.Stdlib["yaml"] {
		predeclared["yaml"] = yamlModule
	}

	src := p.Source
	if src == nil {
		return nil, &DomainError{msg: "no script source provided"}
	}
	if coverage.recorder != nil {
		var covBuiltins starlark.StringDict
		src, covBuiltins = coverage.recorder.instrument(p.Script, src)
		for k, v := range covBuiltins {
			predeclared[k] = v
		}
	}

	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, p.Script, src, predeclared)
	if err != nil {
		return nil, &DomainError{msg: fmt.Sprintf("starlark: load %s: %v", scriptLabel(p), err)}
	}

	// 3. Resolve the requested function and verify it is callable.
	mainVal, ok := globals[function]
	if !ok {
		return nil, &DomainError{msg: fmt.Sprintf("starlark: %s does not define %s(ctx)", scriptLabel(p), function)}
	}
	mainFn, ok := mainVal.(starlark.Callable)
	if !ok {
		return nil, &DomainError{msg: fmt.Sprintf("starlark: %s defines %s but it is not callable (it is a %s)", scriptLabel(p), function, mainVal.Type())}
	}

	// 4. Build ctx and call main(ctx).
	ctxVal, err := buildCtx(ictx, p.Inputs, p.World, cap)
	if err != nil {
		// A ctx-construction failure is an input-conversion problem — domain.
		return nil, &DomainError{msg: fmt.Sprintf("starlark: build ctx: %v", err)}
	}

	retVal, err := starlark.Call(thread, mainFn, starlark.Tuple{ctxVal}, nil)
	if err != nil {
		return nil, &DomainError{msg: fmt.Sprintf("starlark: %s(ctx): %v", function, err)}
	}

	// 5. main must return a dict of named outputs.
	retDict, ok := retVal.(*starlark.Dict)
	if !ok {
		return nil, &DomainError{msg: fmt.Sprintf("starlark: %s must return a dict, got %s", function, retVal.Type())}
	}
	outAny, err := starlarkToGo(retDict)
	if err != nil {
		return nil, &DomainError{msg: fmt.Sprintf("starlark: convert outputs: %v", err)}
	}
	outputs, ok := outAny.(map[string]any)
	if !ok {
		return nil, &DomainError{msg: "starlark: outputs did not convert to a string-keyed map"}
	}

	// 6. Output validation against the sidecar.
	if p.Sidecar != nil {
		if err := p.Sidecar.validateOutputs(outputs); err != nil {
			return nil, err
		}
	}

	return &Result{
		Outputs:     outputs,
		Exchanges:   exchangesFromContext(ictx),
		Inspections: inspectionsFromContext(ictx),
	}, nil
}

func normalizeRunCapabilities(cap CapabilitySpec) CapabilitySpec {
	if cap.Stdlib != nil || cap.World || cap.NeedsHTTP() || cap.NeedsInspector() || cap.AllowsHost() {
		if cap.Stdlib == nil {
			cap.Stdlib = DefaultCapabilities().Stdlib
		}
		sortSpec(&cap)
		return cap
	}
	return DefaultCapabilities()
}

// scriptLabel returns a human label for the script in error messages.
func scriptLabel(p Params) string {
	if p.Script != "" {
		return p.Script
	}
	return "<script>"
}

// exchangesFromContext pulls the recorded HTTP summaries from whichever client
// was injected. Both RecordingClient and ReplayClient expose their summaries;
// this reads them back after the run so the adapter can surface them.
func exchangesFromContext(ictx context.Context) []HTTPExchange {
	switch c := HTTPFromContext(ictx).(type) {
	case *RecordingClient:
		return c.Exchanges
	case *ReplayClient:
		return c.Exchanges()
	case *RecordReplayClient:
		return c.Exchanges()
	case capabilityHTTPClient:
		return exchangesFromClient(c.base)
	default:
		return nil
	}
}

func exchangesFromClient(c HTTPClient) []HTTPExchange {
	switch x := c.(type) {
	case *RecordingClient:
		return x.Exchanges
	case *ReplayClient:
		return x.Exchanges()
	case *RecordReplayClient:
		return x.Exchanges()
	case capabilityHTTPClient:
		return exchangesFromClient(x.base)
	case interface{ Exchanges() []HTTPExchange }:
		return x.Exchanges()
	default:
		return nil
	}
}

// inspectionsFromContext pulls the recorded fs/probe summaries from whichever
// inspector was injected, mirroring exchangesFromContext. Both the production
// inspector and the ReplayInspector expose their summaries; this reads them back
// after the run so the adapter can surface them on the trace.
func inspectionsFromContext(ictx context.Context) []InspectExchange {
	switch in := InspectorFromContext(ictx).(type) {
	case *productionInspector:
		return in.Inspections()
	case *ReplayInspector:
		return in.Inspections()
	case capabilityInspector:
		return inspectionsFromInspector(in.base)
	default:
		return nil
	}
}

func inspectionsFromInspector(in Inspector) []InspectExchange {
	switch x := in.(type) {
	case *productionInspector:
		return x.Inspections()
	case *ReplayInspector:
		return x.Inspections()
	case capabilityInspector:
		return inspectionsFromInspector(x.base)
	default:
		return nil
	}
}
