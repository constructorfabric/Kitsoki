package starlark

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.starlark.net/starlark"
)

// HostCaller is the sandbox's ONLY boundary onto the engine's host.Registry
// (S3d, .context/kits-implementation-plan.md Decision 3: "a narrow
// allow-listed ctx.host in the starlark sandbox"). It mirrors HTTPClient
// (http.go) and Inspector (inspect.go): package host injects a concrete
// implementation closing over its own *Registry via WithHost, so this
// package never depends on package host or its types — only on this narrow,
// Go-primitive-only shape.
type HostCaller interface {
	// Invoke calls the named host verb with args and returns its result data
	// (or an error — this package makes no infra/domain distinction the way
	// host.Result does; either becomes a Starlark error, which Run already
	// surfaces as a DomainError, exactly like an ctx.http/ctx.fs failure).
	Invoke(ctx context.Context, name string, args map[string]any) (map[string]any, error)
}

// hostBinding pairs the injected caller with the fixed set of verb names a
// script running under this context is permitted to call.
type hostBinding struct {
	caller HostCaller
	allow  map[string]struct{}
}

type hostCallerKey struct{}

// WithHost injects caller plus the allow-list of host verb names a script may
// call via ctx.host.call during this run. A name not in allow is rejected
// BEFORE Invoke is ever called — deny-by-default, matching the sandbox's
// posture elsewhere (an absent HTTPClient/Inspector fails safe rather than
// silently no-op'ing; see HTTPFromContext / InspectorFromContext).
func WithHost(ctx context.Context, caller HostCaller, allow []string) context.Context {
	set := make(map[string]struct{}, len(allow))
	for _, name := range allow {
		set[name] = struct{}{}
	}
	return context.WithValue(ctx, hostCallerKey{}, hostBinding{caller: caller, allow: set})
}

// RestrictHost narrows an injected HostCaller to the verbs granted by this run's
// capability metadata. It never broadens an existing allow-list.
func RestrictHost(ctx context.Context, allow []string) context.Context {
	b, ok := ctx.Value(hostCallerKey{}).(hostBinding)
	if !ok {
		return ctx
	}
	granted := make(map[string]struct{}, len(allow))
	for _, name := range allow {
		granted[name] = struct{}{}
	}
	narrowed := make(map[string]struct{}, len(granted))
	for name := range granted {
		if _, ok := b.allow[name]; ok {
			narrowed[name] = struct{}{}
		}
	}
	return context.WithValue(ctx, hostCallerKey{}, hostBinding{caller: b.caller, allow: narrowed})
}

// HasHost reports whether a HostCaller has already been injected into ctx —
// the same "don't clobber an existing injection" check StarlarkRunHandler
// uses for HTTP/Inspector, so a testrunner-installed fake host caller isn't
// silently overwritten by the production default.
func HasHost(ctx context.Context) bool {
	_, ok := ctx.Value(hostCallerKey{}).(hostBinding)
	return ok
}

func hostFromContext(ctx context.Context) (HostCaller, map[string]struct{}) {
	b, ok := ctx.Value(hostCallerKey{}).(hostBinding)
	if !ok {
		return nil, nil
	}
	return b.caller, b.allow
}

// ─── ctx.host ───────────────────────────────────────────────────────────────

// hostProxy is the ctx.host value: a single method, call(name, args={}),
// that dispatches through the injected HostCaller after checking name
// against the run's allow-list. There is deliberately no other surface (no
// listing allowed names, no wildcard) — a script either knows the verb name
// it wants or it doesn't get to call anything.
type hostProxy struct {
	ictx context.Context
}

func newHostProxy(ictx context.Context) *hostProxy { return &hostProxy{ictx: ictx} }

func (h *hostProxy) String() string        { return "ctx.host" }
func (h *hostProxy) Type() string          { return "ctx.host" }
func (h *hostProxy) Freeze()               {}
func (h *hostProxy) Truth() starlark.Bool  { return starlark.True }
func (h *hostProxy) Hash() (uint32, error) { return 0, fmt.Errorf("ctx.host is unhashable") }

func (h *hostProxy) AttrNames() []string { return []string{"call"} }

func (h *hostProxy) Attr(name string) (starlark.Value, error) {
	if name == "call" {
		return starlark.NewBuiltin("ctx.host.call", h.call), nil
	}
	return nil, nil // "no such attribute" — the narrow-ctx safety net.
}

// call implements ctx.host.call(name, args={}) -> dict.
func (h *hostProxy) call(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	var argsVal starlark.Value
	if err := starlark.UnpackArgs("ctx.host.call", args, kwargs, "name", &name, "args?", &argsVal); err != nil {
		return nil, err
	}

	caller, allow := hostFromContext(h.ictx)
	if caller == nil {
		return nil, fmt.Errorf("ctx.host.call: no host caller configured for this run")
	}
	if _, ok := allow[name]; !ok {
		return nil, fmt.Errorf("ctx.host.call: verb %q is not allow-listed for this script (allowed: %s)", name, allowListString(allow))
	}

	var goArgs map[string]any
	if argsVal != nil {
		converted, err := starlarkToGo(argsVal)
		if err != nil {
			return nil, fmt.Errorf("ctx.host.call: args: %w", err)
		}
		m, ok := converted.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ctx.host.call: args must be a dict, got %s", argsVal.Type())
		}
		goArgs = m
	}

	result, err := caller.Invoke(h.ictx, name, goArgs)
	if err != nil {
		return nil, fmt.Errorf("ctx.host.call(%q): %w", name, err)
	}
	return goToStarlark(result)
}

// allowListString renders an allow-list as a sorted comma-list for error
// messages.
func allowListString(allow map[string]struct{}) string {
	if len(allow) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(allow))
	for name := range allow {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
