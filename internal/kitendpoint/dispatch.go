// Package kitendpoint implements the generic kit extension-surface
// dispatcher (S3b, .context/kits-implementation-plan.md design decision D2.2/
// D2.3): resolving `kit.<kit>.<iface>.<op>` against an installed kit's
// declared host_interfaces and invoking the concrete handler — transparently
// a Go host verb or a starlark-bound one (S3a) — through the shared
// host.Registry.
//
// This is the ONE mechanism both carriers share: the JSON-RPC fallback in
// internal/runstatus/server (kit.<kit>.<iface>.<op>) and the generic studio
// MCP tool (kit_call) both call Dispatcher.Call. Neither carrier does its own
// resolution — this package is the single source of truth for "what does
// kit X's interface Y, operation Z, actually invoke."
//
// # Journaling
//
// Dispatcher.Call does not write its own trace/journal entries. It invokes
// through the same *host.Registry every other host.* call in this codebase
// goes through, so a call dispatched here is exactly as journaled/cassette-
// testable as any other host invocation — see internal/testrunner's cassette
// dispatcher, which wraps a Registry entry by handler name and is agnostic to
// who called Invoke. Building a second, bespoke logging path here would only
// diverge from that existing, already-tested discipline.
package kitendpoint

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/kit"
	"kitsoki/internal/ticketprovider"
)

// MethodPrefix is the JSON-RPC method / dot-namespace prefix every kit
// endpoint call carries: "kit.<kit>.<iface>.<op>".
const MethodPrefix = "kit."

// ParseMethod splits a "kit.<kit>.<iface>.<op>" dot-name into its four
// segments. ok is false when method does not start with MethodPrefix or does
// not have exactly four dot-separated segments — callers use this to decide
// whether a JSON-RPC method belongs to the kit dispatch fallback at all.
func ParseMethod(method string) (kitName, iface, op string, ok bool) {
	if !strings.HasPrefix(method, MethodPrefix) {
		return "", "", "", false
	}
	parts := strings.Split(method, ".")
	if len(parts) != 4 {
		return "", "", "", false
	}
	if parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

// Dispatcher resolves and invokes kit endpoint calls against an installed-kit
// registry and a shared host.Registry. It is safe for concurrent use.
type Dispatcher struct {
	kits *kit.Registry
	reg  *host.Registry

	mu        sync.Mutex
	storyDefs map[string]*app.AppDef // cache key: kit identity + "/" + story name
	scripts   map[string]struct{}    // handler names already registered from StarlarkHostBindings
}

// NewDispatcher builds a Dispatcher over an installed-kit registry and the
// host.Registry calls should be invoked through. reg is typically one
// RegisterBuiltins has already populated; kit-declared starlark bindings are
// registered into it lazily, on first use of the story that declares them.
func NewDispatcher(kits *kit.Registry, reg *host.Registry) *Dispatcher {
	return &Dispatcher{
		kits:      kits,
		reg:       reg,
		storyDefs: make(map[string]*app.AppDef),
		scripts:   make(map[string]struct{}),
	}
}

// Call resolves kitName's declared interface iface, validates op against its
// declared operations (when any are declared — an interface with no declared
// operations is treated as open), and invokes the resolved handler via the
// shared host.Registry. args are passed through unchanged as the handler's
// invocation args (mirroring a host_interface op's `with:` block at a normal
// invoke site).
func (d *Dispatcher) Call(ctx context.Context, kitName, iface, op string, args map[string]any) (host.Result, error) {
	if d == nil {
		return host.Result{}, fmt.Errorf("kitendpoint: nil dispatcher")
	}
	manifest, ok := d.kits.Get(kitName)
	if !ok {
		return host.Result{}, fmt.Errorf("kitendpoint: unknown installed kit %q", kitName)
	}

	entry, storyName, err := d.resolveInterface(manifest, iface)
	if err != nil {
		return host.Result{}, err
	}
	if len(entry.Operations) > 0 {
		if _, ok := entry.Operations[op]; !ok {
			return host.Result{}, fmt.Errorf("kitendpoint: %s: interface %q (story %q) declares no operation %q", manifest.Identity(), iface, storyName, op)
		}
	}
	if entry.Default == "" {
		return host.Result{}, fmt.Errorf("kitendpoint: %s: interface %q (story %q) has no default binding", manifest.Identity(), iface, storyName)
	}

	handlerName := entry.Default + "." + op
	return d.reg.Invoke(ctx, handlerName, withKitContext(args, manifest))
}

// withKitContext returns a shallow copy of args with "_kit_dir" and
// "_kit_name" set to the resolved kit's on-disk root and short name — a
// small, deliberately generic convention (not special-cased to any one
// handler) so a handler bound through the kit dispatch surface can resolve
// kit-relative paths (e.g. S5's host.graph.presentation locating the calling
// kit's scripts/presentation.star) without the engine hardcoding which kit
// it's talking to. Handlers that don't need kit context simply ignore the
// extra keys. Any caller-supplied "_kit_dir"/"_kit_name" in args is
// overwritten — these are dispatcher-owned, not client-settable.
func withKitContext(args map[string]any, manifest *kit.Def) map[string]any {
	out := make(map[string]any, len(args)+2)
	for k, v := range args {
		out[k] = v
	}
	out["_kit_dir"] = manifest.Dir()
	out["_kit_name"] = manifest.Kit
	return out
}

// resolveInterface finds the manifest story that declares iface as a
// host_interface and returns its resolved HostInterfaceDef, loading (and
// caching) each candidate story's standalone AppDef as needed. Any starlark
// host_bindings the story's own imports resolved (AppDef.StarlarkHostBindings)
// are registered into the shared host.Registry on first use, so a story that
// binds one of ITS OWN sub-imports to a starlark script (S3a) works exactly
// as if it were invoked from inside a running session.
func (d *Dispatcher) resolveInterface(manifest *kit.Def, iface string) (*app.HostInterfaceDef, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, storyName := range manifest.Provides.Stories {
		def, err := d.loadStoryLocked(manifest, storyName)
		if err != nil {
			return nil, "", err
		}
		entry, ok := def.HostInterfaces[iface]
		if !ok || entry == nil {
			continue
		}
		d.registerScriptsLocked(def)
		return entry, storyName, nil
	}
	return nil, "", fmt.Errorf("kitendpoint: %s: no provided story declares host_interface %q", manifest.Identity(), iface)
}

// loadStoryLocked returns the cached standalone-loaded AppDef for
// manifest/storyName, loading and caching it on first use. Callers must hold
// d.mu.
func (d *Dispatcher) loadStoryLocked(manifest *kit.Def, storyName string) (*app.AppDef, error) {
	key := manifest.Identity() + "/" + storyName
	if def, ok := d.storyDefs[key]; ok {
		return def, nil
	}
	storyPath := filepath.Join(manifest.StoryDir(storyName), "app.yaml")
	def, err := app.Load(storyPath)
	if err != nil {
		return nil, fmt.Errorf("kitendpoint: %s: load story %q: %w", manifest.Identity(), storyName, err)
	}
	d.storyDefs[key] = def
	return def, nil
}

// registerScriptsLocked registers every starlark host_bindings handler
// def.StarlarkHostBindings names into the shared registry, skipping ones
// already registered (idempotent across repeated calls / overlapping stories
// that happen to share a script). Callers must hold d.mu.
func (d *Dispatcher) registerScriptsLocked(def *app.AppDef) {
	for name, scriptPath := range def.StarlarkHostBindings {
		if _, seen := d.scripts[name]; seen {
			continue
		}
		d.scripts[name] = struct{}{}
		handler := host.StarlarkBindingHandler(scriptPath)
		if ticketprovider.IsProviderScript(scriptPath) {
			d.reg.ReplaceTicketProvider(name, handler)
			continue
		}
		d.reg.Replace(name, handler)
	}
}

// Kits returns the installed-kit registry this dispatcher resolves against —
// used by carriers that also want to list installed kits (e.g. a future
// `runstatus.kits.list` handler, S3c).
func (d *Dispatcher) Kits() *kit.Registry {
	if d == nil {
		return nil
	}
	return d.kits
}
