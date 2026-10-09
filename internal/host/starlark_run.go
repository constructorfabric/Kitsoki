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
	"path/filepath"

	starlarkhost "kitsoki/internal/host/starlark"
)

// StarlarkRunHandler implements host.starlark.run.
//
// Args (from the effect's with: block):
//   - script (string, required): path to the .star file. Resolved to an absolute
//     path by the loader at load time, so by dispatch it is absolute.
//   - function (string, optional): exported function to call. Defaults to
//     main; direct CLI and JSON-RPC callers may use this for provider-style
//     scripts without wrapping every operation in a main function.
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

	// Resolve a relative script path against the per-session story search path,
	// falling back to KITSOKI_APP_DIR (the directory of app.yaml, published by
	// loaders) when no renderer is in ctx. The loader's validateStarlarkEffects
	// resolves against def.BaseDir at load time but does NOT rewrite the
	// effect's with.script to absolute, so by dispatch the arg is still the
	// author-relative path — resolve it here so the read succeeds regardless of
	// the process working directory.
	rawScript := scriptPath
	scriptPath = resolvePromptPathCtx(ctx, scriptPath)

	src, err := os.ReadFile(scriptPath)
	if err != nil && !filepath.IsAbs(rawScript) {
		// Robustness for an ad-hoc plan whose `verify.script` was authored
		// repo-root-relative (e.g. "stories/dev-story/verify/x.star") rather
		// than app-relative ("verify/x.star"): story-root resolution prepends
		// the app dir, which doubles the prefix and misses. If the raw path
		// exists as-is (relative to the process cwd / repo root), use it.
		// Principle of least surprise: a script that exists on disk should be
		// found whether the plan named it app- or repo-relative. (The plan's
		// script value is interpretive — proposed by an LLM — so the runtime
		// must tolerate both.)
		if _, statErr := os.Stat(rawScript); statErr == nil {
			scriptPath = rawScript
			src, err = os.ReadFile(scriptPath)
		}
	}
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
	capabilities, capErr := starlarkhost.ParseCapabilities(args["capabilities"])
	if capErr != nil {
		return Result{Error: fmt.Sprintf("host.starlark.run: capabilities: %v", capErr)}, nil
	}

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

	// Inject the production recording HTTP client only when metadata grants
	// ctx.http, unless a caller already installed one (the testrunner installs a
	// replay client for flow fixtures).
	runCtx := ctx
	if capabilities.RequiresInjectedHTTP() && !starlarkhost.HasHTTPClient(runCtx) {
		return Result{Error: "host.starlark.run: capabilities.http.cassette_required requires an injected Starlark HTTP client"}, nil
	}
	if capabilities.NeedsHTTP() && !starlarkhost.HasHTTPClient(ctx) {
		runCtx = starlarkhost.WithHTTP(ctx, starlarkhost.NewRecordingClient())
	}

	// Inject a production inspector rooted at the run's working dir only when
	// metadata grants ctx.fs or ctx.probe, unless a caller already installed one
	// (the testrunner installs a replay inspector for flow fixtures). Mirrors
	// the HTTP default-injection block above; the
	// safe deny-all default is applied by InspectorFromContext when nothing is
	// injected. Root at world.workdir when present, else the per-session
	// prompt renderer's story root (WithPromptRenderer, orchestrator-injected
	// per dispatch — see below), else the repo root enclosing KITSOKI_APP_DIR
	// (the target app's directory, published by loaders — see AppDirEnv in
	// agent_ask.go), else the repo containing the resolved story script, else
	// the process cwd. Without the AppDirEnv fallback, a driver whose own
	// process cwd differs from the app being driven can resolve ctx.fs.*
	// paths against the wrong directory. The app dir is widened to its
	// enclosing repo root because ctx.fs paths are repo-relative by contract
	// while an app.yaml may live in a subdirectory of its repo (kitsoki's own
	// stories/<name>/ chief among them): host.run effects in the same room
	// execute at the repo root, so rooting ctx.fs at the bare app dir makes
	// the two host surfaces disagree about the same relative path.
	//
	// The prompt-renderer step takes priority over KITSOKI_APP_DIR because the
	// latter is a single process-global env var (session_runtime.go's
	// publishAppDir docstring calls this out explicitly): two concurrent
	// studio sessions on different stories race to set it, and whichever
	// session.new call ran last wins for BOTH sessions' subsequent dispatches,
	// with no revert on session close. A studio-driven dispatch always has a
	// prompt renderer injected per session (orchestrator.go's o.promptRenderer,
	// built once from def.BaseDir and threaded through host_dispatch.go's
	// WithPromptRenderer per turn), so preferring its RootDir() here makes the
	// inspector root session-scoped instead of shared mutable global state.
	// CLI one-shots, direct unit-test callers, and any other caller with no
	// renderer in ctx keep exactly the previous AppDirEnv-based behavior.
	if capabilities.NeedsInspector() && !starlarkhost.HasInspector(runCtx) {
		root, _ := worldSnapshot["workdir"].(string)
		// "." is dev-story's own bare-relative default for an unbound workdir
		// (stories/dev-story/app.yaml's `workdir: "{{ world.workdir == '' ?
		// '.' : world.workdir }}"` projection) — it is not a resolved root, it's
		// the same "nothing was bound" signal as "", just spelled differently.
		// A per-session prompt renderer or KITSOKI_APP_DIR, when available, is
		// always a better root than the literal "." (which resolves against
		// whatever the CURRENT PROCESS's cwd happens to be — the wrong repo
		// entirely for a nested/driven session, e.g. prd_publish.star's
		// docs/prd/ write silently landing nowhere and failing closed). But
		// when neither is available, "." must still pass through unchanged
		// rather than fall into the inferRepoRootForScript/cwd chain below:
		// goal-seeker's own resolution scripts intentionally run with
		// workdir="." and no renderer/AppDir to mean "resolve against this
		// process's cwd" (see TestGoalSeekerResolveWorkdir_FromRuntimeBlock),
		// and inferRepoRootForScript would instead resolve the SCRIPT's own
		// repo, which can differ from the caller's intended cwd-rooted target.
		dotSentinel := root == "."
		if root == "" || dotSentinel {
			resolved := ""
			if pr := PromptRendererFromCtx(ctx); pr != nil {
				if appDir := pr.RootDir(); appDir != "" {
					resolved = widenToRepoRoot(appDir)
				}
			}
			if resolved == "" {
				if appDir := os.Getenv(AppDirEnv); appDir != "" {
					resolved = widenToRepoRoot(appDir)
				}
			}
			switch {
			case resolved != "":
				root = resolved
			case dotSentinel:
				root = "."
			default:
				root = ""
			}
		}
		if root == "" {
			if inferred := inferRepoRootForScript(scriptPath); inferred != "" {
				root = inferred
			} else if cwd, cwdErr := os.Getwd(); cwdErr == nil {
				root = cwd
			}
		}
		runCtx = starlarkhost.WithInspector(runCtx, starlarkhost.NewProductionInspector(root))
	}

	function, _ := args["function"].(string)
	if function == "" {
		function = "main"
	}
	params := starlarkhost.Params{
		Script:       scriptPath,
		Source:       src,
		Sidecar:      sidecar,
		Inputs:       inputs,
		World:        worldSnapshot,
		Capabilities: capabilities,
	}
	var res *starlarkhost.Result
	var runErr error
	if function == "main" {
		res, runErr = starlarkhost.Run(runCtx, params)
	} else {
		res, runErr = starlarkhost.RunFunction(runCtx, params, function)
	}
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

// AllowedStarlarkHostVerbs is the fixed, narrow set of host verbs a
// starlark-run script may call via ctx.host.call (S3d — narrow allow-listed
// ctx.host in the sandbox, .context/kits-implementation-plan.md Decision 3).
// Kept intentionally tiny and read/introspection-oriented: this is a
// trust-boundary list, not a convenience one — a kit script runs sandboxed
// but can already reach fs/http/probe, so ctx.host additionally reaching
// arbitrary host verbs (shell exec, ticket mutation, ...) would widen that a
// lot for little benefit. host.graph.* isn't registered until S5 (the
// object-graph kit's engine verbs); listing those names now means a kit
// script written against ctx.host.call for them keeps working unmodified
// once S5 lands, without S3d depending on that slice.
var AllowedStarlarkHostVerbs = starlarkhost.BuiltinHostVerbVocabulary

// NewStarlarkRunHandler returns a host.starlark.run Handler bound to reg,
// enabling ctx.host.call inside the running script to invoke back into reg
// itself, narrowed to AllowedStarlarkHostVerbs (S3d). RegisterBuiltins uses
// this instead of the bare StarlarkRunHandler function value so ctx.host
// works for any story/kit run driven through the production registry.
//
// The bare StarlarkRunHandler remains directly usable (existing callers —
// cmd/kitsoki/runtime.go, internal/testrunner/flows.go — register it as-is
// for their own registries/flow fixtures); it simply leaves ctx.host
// unconfigured, which fails safe ("no host caller configured for this run")
// rather than silently doing nothing, exactly like an un-injected
// HTTPClient/Inspector.
func NewStarlarkRunHandler(reg *Registry) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		if cap, err := starlarkhost.ParseCapabilities(args["capabilities"]); err == nil && cap.AllowsHost() && !starlarkhost.HasHost(ctx) {
			ctx = starlarkhost.WithHost(ctx, registryHostCaller{reg: reg}, cap.Host.Verbs)
		}
		return StarlarkRunHandler(ctx, args)
	}
}

// registryHostCaller adapts a *Registry to starlarkhost.HostCaller (S3d),
// closing over the SAME registry the story/kit call is otherwise dispatched
// through — so ctx.host.call("host.graph.load", ...) resolves exactly like
// an ordinary invoke: target would, including any test/replay stub installed
// on that registry.
type registryHostCaller struct{ reg *Registry }

func (c registryHostCaller) Invoke(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	result, err := c.reg.Invoke(ctx, name, args)
	if err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}
	return result.Data, nil
}

func inferRepoRootForScript(scriptPath string) string {
	return inferRepoRootForDir(filepath.Dir(scriptPath))
}

// widenToRepoRoot resolves an app directory to its enclosing repo root
// (go.mod / .kitsoki-root marker), falling back to appDir itself when no
// marker is found walking upward. Shared by every appDir-shaped inspector-root
// candidate (the per-session prompt renderer's RootDir() and the KITSOKI_APP_DIR
// env var) so both apply the identical widening policy.
func widenToRepoRoot(appDir string) string {
	if inferred := inferRepoRootForDir(appDir); inferred != "" {
		return inferred
	}
	return appDir
}

// inferRepoRootForDir walks dir upward to the nearest enclosing repo root,
// identified by a go.mod or .kitsoki-root marker. Returns "" when no marker is
// found so callers keep their own fallback.
func inferRepoRootForDir(dir string) string {
	for {
		if fileExists(filepath.Join(dir, "go.mod")) || fileExists(filepath.Join(dir, ".kitsoki-root")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
