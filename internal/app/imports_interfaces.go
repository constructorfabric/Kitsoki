// Package app — host interfaces.
//
// See docs/stories/imports.md "host_interfaces" for the authoring model.
//
// A host_interface is a named capability the app depends on. The child
// declares the operation surface and a default binding; the importer
// can rebind it onto a different handler. At load time we rewrite
// every iface.<name>.<op> invocation target into the concrete
// host.<handler>.<op> form so the runtime dispatch path is unchanged.
//
// The runtime convention is that interface operations are addressed as
// `iface.<interface-name>.<op-name>` and concrete handlers as
// `host.<carrier>.<op-name>`. Resolving an interface op consists of
// replacing the `iface.<name>` prefix with `host.<bound-handler>`.
package app

import (
	"fmt"
	"strings"
)

// ifaceTerminalName returns the terminal capability segment of a
// (possibly alias-prefixed) host_interface name — the part after the
// final `__`. For an unprefixed name (no `__`) it returns the name
// unchanged. Used by import folding to let an importer's base-name
// host_binding (e.g. `ticket`) cascade to every transitively-lifted
// `<alias>__ticket` iface that has no explicit prefixed override.
func ifaceTerminalName(name string) string {
	if idx := strings.LastIndex(name, "__"); idx >= 0 {
		return name[idx+2:]
	}
	return name
}

// ifaceEntry captures a resolved (binding, allowed-op-set) pair for one
// host_interface, used by the deferred resolver.
type ifaceEntry struct {
	binding string
	ops     map[string]struct{}
}

// resolveAllInterfaces is the FINAL pass that rewrites every remaining
// `iface.<name>.<op>` invocation in the merged AppDef to a concrete
// `<binding>.<op>` host name. It runs at the top of Load() AFTER
// resolveImports has folded every child and lifted every iface
// declaration into def.HostInterfaces (top-level entries plus
// alias-prefixed entries for imported ifaces).
//
// Why this is deferred ("bindings compose"): if we
// rewrote during the child's own load, the immediate parent could
// override but no grandparent could ever rebind — by the time the
// grandparent's fold sees it the iface ref is already a concrete
// handler. Deferring resolution lets every layer's host_bindings have
// a chance to override the iface's Default before final rewrite.
//
// Op-suffix semantics: the binding handler is always extended with
// `.<op>` to produce the dispatched name (`host.foo` + op `bar` →
// `host.foo.bar`). This is what makes multi-op interfaces (e.g.
// `pr_engine.open_pr` + `pr_engine.close_pr`) work — both
// resolve to distinct concrete handlers a host registry can keep
// separate. Single-op interfaces are a special case where the author
// either (a) registers the host with the op-suffixed name or
// (b) registers a no-arg alias from `<binding>.<op>` to `<binding>`.
//
// Side effect: every concrete handler the resolver introduces is
// unioned into def.Hosts so validateDef's allow-list check passes.
//
// Returns one ValidationError per problem: undeclared iface, undeclared
// op on a declared iface, iface with no Default and no binding.
func resolveAllInterfaces(def *AppDef, file string) []error {
	if def == nil {
		return nil
	}

	var errs []error

	// Build the iface table: name → (binding, allowed ops).
	table := make(map[string]ifaceEntry, len(def.HostInterfaces))
	for name := range def.HostInterfaces {
		iface := def.HostInterfaces[name]
		if iface == nil {
			continue
		}
		if iface.Default == "" {
			errs = append(errs, &ValidationError{File: file, Message: fmt.Sprintf("host_interface %q: no default and no host_binding override", name)})
			continue
		}
		ops := make(map[string]struct{}, len(iface.Operations))
		for op := range iface.Operations {
			ops[op] = struct{}{}
		}
		table[name] = ifaceEntry{binding: iface.Default, ops: ops}
	}

	hostSet := make(map[string]struct{}, len(def.Hosts))
	for _, h := range def.Hosts {
		hostSet[h] = struct{}{}
	}

	rewriteIfaceInvokesRecursive(def.States, table, file, &errs, hostSet, &def.Hosts)
	return errs
}

// rewriteIfaceInvokesRecursive walks states and rewrites every
// Effect.Invoke that begins with `iface.` to its concrete form
// `<binding>.<op>`. Errors are appended; on error the original Invoke
// is kept so validateDef surfaces it as a downstream issue.
func rewriteIfaceInvokesRecursive(states map[string]*State, table map[string]ifaceEntry, file string, errs *[]error, hostSet map[string]struct{}, hosts *[]string) {
	for _, s := range states {
		if s == nil {
			continue
		}
		rewriteIfaceInvokesInEffects(s.OnEnter, table, file, errs, hostSet, hosts)
		for _, list := range s.On {
			for i := range list {
				rewriteIfaceInvokesInEffects(list[i].Effects, table, file, errs, hostSet, hosts)
			}
		}
		if len(s.States) > 0 {
			rewriteIfaceInvokesRecursive(s.States, table, file, errs, hostSet, hosts)
		}
	}
}

// rewriteIfaceInvokesInEffects rewrites Effect.Invoke (and recurses into
// nested OnComplete chains) for every iface reference. New concrete
// handler names are unioned into the parent allow-list via hostSet.
func rewriteIfaceInvokesInEffects(effs []Effect, table map[string]ifaceEntry, file string, errs *[]error, hostSet map[string]struct{}, hosts *[]string) {
	for i := range effs {
		if effs[i].Invoke != "" && strings.HasPrefix(effs[i].Invoke, "iface.") {
			rest := strings.TrimPrefix(effs[i].Invoke, "iface.")
			dot := strings.LastIndexByte(rest, '.')
			if dot < 1 {
				*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("invoke %q: expected iface.<name>.<op>", effs[i].Invoke)})
				continue
			}
			name, op := rest[:dot], rest[dot+1:]
			entry, ok := table[name]
			if !ok {
				*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("invoke %q: iface %q not declared (declared: %v)", effs[i].Invoke, name, ifaceNames(table))})
				continue
			}
			if _, opOK := entry.ops[op]; !opOK {
				*errs = append(*errs, &ValidationError{File: file, Message: fmt.Sprintf("invoke %q: op %q not declared on iface %q", effs[i].Invoke, op, name)})
				continue
			}
			invoke := entry.binding + "." + op
			effs[i].Invoke = invoke
			if _, seen := hostSet[invoke]; !seen {
				hostSet[invoke] = struct{}{}
				*hosts = append(*hosts, invoke)
			}
		}
		if len(effs[i].OnComplete) > 0 {
			rewriteIfaceInvokesInEffects(effs[i].OnComplete, table, file, errs, hostSet, hosts)
		}
	}
}

// ifaceNames returns the sorted list of declared iface names from the
// resolution table, for use in error messages.
func ifaceNames(table map[string]ifaceEntry) []string {
	out := make([]string, 0, len(table))
	for k := range table {
		out = append(out, k)
	}
	// Sort is deferred to fmt — order doesn't strictly need to be
	// alphabetical here, but keeping the message stable matters for tests.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
