// Package host — starlark-bindable host_bindings (S3a, design decision D2.1
// in .context/kits-implementation-plan.md).
//
// A `host_bindings` entry in an app.yaml's `imports.<alias>:` block may name a
// starlark script instead of a concrete handler (see
// internal/app.HostBindingSpec for the three author forms). The loader
// (internal/app/imports.go: resolveHostBindingScripts) resolves every such
// entry into a synthetic handler name and records the (name -> absolute
// script path) pair on AppDef.StarlarkHostBindings. This file is the runtime
// half: RegisterStarlarkBindings turns that data into real Registry entries,
// each one a thin Handler closing over the script and delegating to the
// existing host.starlark.run adapter (StarlarkRunHandler) — no per-kit Go
// ever needed, matching D2's "three small, independently useful engine
// changes; no per-kit Go ever" framing. This also implements the
// gh-ticket-adapter proposal as a byproduct: bind `ticket:` (or any interface)
// straight to a starlark script instead of writing a Go handler.
package host

import (
	"context"
	"os"
	"strings"

	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/ticketprovider"
)

// StarlarkBindingHandler returns a Handler that delegates every call to
// host.starlark.run for the fixed script at scriptPath, translating the
// interface-op call into the shape StarlarkRunHandler expects.
//
// args is whatever the dispatched effect's `with:` block supplied for the
// interface op, PLUS an "op" key when Registry.Invoke's prefix-fallback filled
// it in (this handler is always registered under the bare binding name, so an
// invoke of `<binding>.<op>` always falls back and injects op — see
// Registry.getWithName/Invoke). That op is exactly what D2.1 means by
// "injecting the interface op into ctx.inputs": the script reads
// ctx.inputs.op to know which operation it was asked to perform, exactly as
// it would read any other named input.
//
// A top-level "world" key (present only when a caller explicitly wired one,
// e.g. a direct test seam) is forwarded to StarlarkRunHandler's own world-
// override arg rather than folded into inputs, mirroring how a hand-authored
// `invoke: host.starlark.run` effect keeps `with.world` and `with.inputs` as
// siblings.
func StarlarkBindingHandler(scriptPath string) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		inputs := make(map[string]any, len(args))
		for k, v := range args {
			if k == "world" {
				continue
			}
			inputs[k] = v
		}

		if ticketprovider.IsProviderScript(scriptPath) {
			op, _ := args["op"].(string)
			res, err := (&ticketprovider.StarlarkProvider{
				Script: scriptPath,
				Env:    TicketProviderEnvLookup,
				World:  toStringMap(args["world"]),
			}).Invoke(ctx, op, inputs)
			if err != nil {
				return Result{}, err
			}
			return ticketProviderHostResult(res), nil
		}

		innerArgs := map[string]any{
			"script": scriptPath,
			"inputs": inputs,
		}
		if world, ok := args["world"]; ok {
			innerArgs["world"] = world
		}
		return StarlarkRunHandler(ctx, innerArgs)
	}
}

func ticketProviderHostResult(res ticketprovider.Result) Result {
	data := make(map[string]any, len(res.Data)+2)
	for k, v := range res.Data {
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
	if res.Error == nil {
		return Result{Data: data}
	}
	data["error_code"] = res.Error.Code
	data["error_message"] = res.Error.Message
	if res.Error.Hint != "" {
		data["error_hint"] = res.Error.Hint
	}
	if len(res.Error.Details) > 0 {
		data["error_details"] = res.Error.Details
	}
	return Result{Data: data, Error: res.Error.Error()}
}

// TicketProviderEnvLookup resolves credentials for reusable ticket providers.
// It checks per-call command env overrides, registry-injected secrets, process
// env, then ~/.kitsoki/secrets.yaml. It returns only the requested value to the
// Go HTTP transport; Starlark code receives symbolic auth names, never secrets.
func TicketProviderEnvLookup(ctx context.Context, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if env := CLIExecEnvFromCtx(ctx); len(env) > 0 {
		if v := strings.TrimSpace(env[name]); v != "" {
			return v
		}
	}
	if secrets := SecretsFromContext(ctx); len(secrets) > 0 {
		if v := strings.TrimSpace(secrets[name]); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	if secrets := LoadSecrets(); len(secrets) > 0 {
		return strings.TrimSpace(secrets[name])
	}
	return ""
}

// RegisterStarlarkBindings registers one StarlarkBindingHandler per (handler
// name -> absolute script path) pair — typically def.StarlarkHostBindings
// after a successful app.Load(). Callers should invoke this alongside
// RegisterBuiltins, before ValidateAllowList runs: resolveAllInterfaces
// already unions every synthesized handler name into def.Hosts, so the
// allow-list check expects these registrations to exist.
//
// Like RegisterBuiltins, this panics (via Registry.Register) on a duplicate
// name — an init-time contract. Handler names are content-hashed from the
// script's absolute path (see internal/app's starlarkBindingHandlerName), so
// a collision here would mean the same AppDef supplied two different script
// paths that happened to hash identically, or a caller registering the same
// bindings map twice; both indicate a caller bug, not something to silently
// paper over.
func RegisterStarlarkBindings(reg *Registry, bindings map[string]string) {
	for name, scriptPath := range bindings {
		reg.Register(name, StarlarkBindingHandler(scriptPath))
	}
}
