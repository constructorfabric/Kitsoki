package starlark

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// buildCtx assembles the `ctx` value passed to main(ctx). It is a Starlark
// struct containing inputs plus exactly the attributes granted by the run's
// CapabilitySpec. This keeps the sandbox honest: a script cannot even discover
// a ctx capability that the orchestration metadata did not grant. http is the
// network boundary; fs and probe are the filesystem + allow-listed-process
// boundary (see Inspector); host is the narrow allow-listed engine-verb
// boundary (see HostCaller/WithHost in host_proxy.go).
//
// ictx is the Go context carrying the injected HTTPClient, Inspector, and
// HostCaller (resolved lazily per call). inputs are the (already
// type-validated) effect inputs. worldSnapshot is the read-only world map.
func buildCtx(ictx context.Context, inputs, worldSnapshot map[string]any, cap CapabilitySpec) (starlark.Value, error) {
	inputsVal, err := goToStarlark(inputs)
	if err != nil {
		return nil, fmt.Errorf("convert inputs: %w", err)
	}

	attrs := starlark.StringDict{"inputs": inputsVal}
	if cap.World {
		attrs["world"] = newWorldProxy(worldSnapshot)
	}
	if cap.NeedsHTTP() {
		attrs["http"] = newHTTPProxy(ictx)
	}
	if cap.AllowsFS() {
		inspect := newInspectorProxy(ictx)
		attrs["fs"] = &fsValue{p: inspect}
	}
	if cap.AllowsProbe() {
		attrs["probe"] = starlark.NewBuiltin("ctx.probe", newInspectorProxy(ictx).probe)
	}
	if cap.AllowsHost() {
		attrs["host"] = newHostProxy(ictx)
	}

	return starlarkstruct.FromStringDict(starlarkstruct.Default, attrs), nil
}

// ─── ctx.world ──────────────────────────────────────────────────────────────

// worldProxy is the read-only view of world handed to a script. The only
// method is get(key) -> value|None: there is deliberately no set, so a script
// cannot mutate world out-of-band — outputs flow ONLY through main()'s return.
type worldProxy struct {
	snapshot map[string]any
}

func newWorldProxy(snapshot map[string]any) *worldProxy {
	if snapshot == nil {
		snapshot = map[string]any{}
	}
	return &worldProxy{snapshot: snapshot}
}

func (w *worldProxy) String() string        { return "ctx.world" }
func (w *worldProxy) Type() string          { return "ctx.world" }
func (w *worldProxy) Freeze()               {}
func (w *worldProxy) Truth() starlark.Bool  { return starlark.True }
func (w *worldProxy) Hash() (uint32, error) { return 0, fmt.Errorf("ctx.world is unhashable") }

// AttrNames lists the proxy's methods so dir(ctx.world) is honest.
func (w *worldProxy) AttrNames() []string { return []string{"get"} }

// Attr binds the get method. Any other attribute access raises a Starlark
// error with the standard "has no .x field" traceback, which is the narrow-ctx
// safety net: unknown surface fails at eval rather than silently doing nothing.
func (w *worldProxy) Attr(name string) (starlark.Value, error) {
	if name == "get" {
		return starlark.NewBuiltin("ctx.world.get", w.get), nil
	}
	return nil, nil // nil,nil → "no such attribute" with a clear traceback
}

// get implements ctx.world.get(key) -> value | None.
func (w *worldProxy) get(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	if err := starlark.UnpackArgs("ctx.world.get", args, kwargs, "key", &key); err != nil {
		return nil, err
	}
	v, ok := w.snapshot[key]
	if !ok {
		return starlark.None, nil
	}
	return goToStarlark(v)
}

// ─── value conversion ───────────────────────────────────────────────────────

// goToStarlark converts a JSON-ish Go value (the shape produced by goccy/go-yaml
// and encoding/json) into a Starlark value. Unsupported types are an error
// rather than a silent nil so a bug surfaces loudly.
func goToStarlark(v any) (starlark.Value, error) {
	switch x := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(x), nil
	case string:
		return starlark.String(x), nil
	case int:
		return starlark.MakeInt(x), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case float64:
		return starlark.Float(x), nil
	case []any:
		elems := make([]starlark.Value, len(x))
		for i, e := range x {
			ev, err := goToStarlark(e)
			if err != nil {
				return nil, err
			}
			elems[i] = ev
		}
		return starlark.NewList(elems), nil
	case map[string]any:
		d := starlark.NewDict(len(x))
		// Sort keys so dict insertion order (and thus any iteration order a
		// script observes) is deterministic across runs.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ev, err := goToStarlark(x[k])
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), ev); err != nil {
				return nil, err
			}
		}
		return d, nil
	default:
		// Go-native handlers (host stubs, wrapped CLI output split into
		// lines, structured records, …) commonly produce concretely-typed
		// slices/maps ([]string, []map[string]any, …) instead of routing
		// through a JSON decode (which would yield the []any/map[string]any
		// shapes handled above). Convert any slice/map reflectively rather
		// than requiring every caller to re-wrap its values into JSON-ish
		// shapes by hand.
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			elems := make([]starlark.Value, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				ev, err := goToStarlark(rv.Index(i).Interface())
				if err != nil {
					return nil, err
				}
				elems[i] = ev
			}
			return starlark.NewList(elems), nil
		case reflect.Map:
			if rv.Type().Key().Kind() != reflect.String {
				return nil, fmt.Errorf("cannot convert Go value of type %T to Starlark", v)
			}
			d := starlark.NewDict(rv.Len())
			keys := make([]string, 0, rv.Len())
			for _, k := range rv.MapKeys() {
				keys = append(keys, k.String())
			}
			sort.Strings(keys)
			for _, k := range keys {
				ev, err := goToStarlark(rv.MapIndex(reflect.ValueOf(k).Convert(rv.Type().Key())).Interface())
				if err != nil {
					return nil, err
				}
				if err := d.SetKey(starlark.String(k), ev); err != nil {
					return nil, err
				}
			}
			return d, nil
		default:
			return nil, fmt.Errorf("cannot convert Go value of type %T to Starlark", v)
		}
	}
}

// starlarkToGo converts a Starlark value back into a JSON-ish Go value for the
// returned outputs. Numbers become int64 when they fit, else float64, matching
// the int/number distinction the sidecar validates against.
func starlarkToGo(v starlark.Value) (any, error) {
	switch x := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(x), nil
	case starlark.String:
		return string(x), nil
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			return i, nil
		}
		// Out of int64 range — fall back to float64 (matches JSON behaviour).
		return float64(x.Float()), nil
	case starlark.Float:
		return float64(x), nil
	case *starlark.List:
		out := make([]any, x.Len())
		for i := 0; i < x.Len(); i++ {
			ev, err := starlarkToGo(x.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, x.Len())
		for i := 0; i < x.Len(); i++ {
			ev, err := starlarkToGo(x.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]any, x.Len())
		for _, item := range x.Items() {
			ks, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("dict key %v is not a string", item[0])
			}
			ev, err := starlarkToGo(item[1])
			if err != nil {
				return nil, err
			}
			out[ks] = ev
		}
		return out, nil
	default:
		return nil, fmt.Errorf("cannot convert Starlark value of type %s to a Go output", v.Type())
	}
}
