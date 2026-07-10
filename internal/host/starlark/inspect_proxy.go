package starlark

import (
	"context"
	"fmt"

	"go.starlark.net/starlark"
)

// inspectorProxy is the shared backing for the ctx.fs value and the ctx.probe
// builtin. Both route through the injected Inspector (the sandbox's only
// filesystem + process boundary) and capture the Go context so the underlying
// inspector sees cancellation/deadlines. It is the inspection-side analogue of
// httpProxy.
type inspectorProxy struct {
	ictx context.Context
	in   Inspector
}

func newInspectorProxy(ictx context.Context) *inspectorProxy {
	return &inspectorProxy{ictx: ictx, in: InspectorFromContext(ictx)}
}

// ─── ctx.fs ─────────────────────────────────────────────────────────────────

// fsValue is the ctx.fs value. It exposes read, exists, glob, and write — the
// small filesystem surface a script needs without any way to delete, chmod,
// run a shell, or escape the rooted working directory. Mirrors httpProxy's shape
// (a small attr-bearing value backed by a Go boundary).
type fsValue struct {
	p *inspectorProxy
}

func (f *fsValue) String() string        { return "ctx.fs" }
func (f *fsValue) Type() string          { return "ctx.fs" }
func (f *fsValue) Freeze()               {}
func (f *fsValue) Truth() starlark.Bool  { return starlark.True }
func (f *fsValue) Hash() (uint32, error) { return 0, fmt.Errorf("ctx.fs is unhashable") }

func (f *fsValue) AttrNames() []string { return []string{"read", "exists", "glob", "write"} }

func (f *fsValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "read":
		return starlark.NewBuiltin("ctx.fs.read", f.read), nil
	case "exists":
		return starlark.NewBuiltin("ctx.fs.exists", f.exists), nil
	case "glob":
		return starlark.NewBuiltin("ctx.fs.glob", f.glob), nil
	case "write":
		return starlark.NewBuiltin("ctx.fs.write", f.write), nil
	}
	return nil, nil // nil,nil → "no such attribute" with a clear traceback
}

// read implements ctx.fs.read(path) -> string. A read of an out-of-bounds or
// oversize path is a Starlark error so the script's traceback (and ultimately
// the effect's on_error: arc) reflects it.
func (f *fsValue) read(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path string
	if err := starlark.UnpackArgs("ctx.fs.read", args, kwargs, "path", &path); err != nil {
		return nil, err
	}
	data, err := f.p.in.Read(f.p.ictx, path)
	if err != nil {
		return nil, err
	}
	return starlark.String(string(data)), nil
}

// exists implements ctx.fs.exists(path) -> bool.
func (f *fsValue) exists(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path string
	if err := starlark.UnpackArgs("ctx.fs.exists", args, kwargs, "path", &path); err != nil {
		return nil, err
	}
	ok, err := f.p.in.Exists(f.p.ictx, path)
	if err != nil {
		return nil, err
	}
	return starlark.Bool(ok), nil
}

// glob implements ctx.fs.glob(pattern) -> [path] (sorted, repo-relative).
func (f *fsValue) glob(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var pattern string
	if err := starlark.UnpackArgs("ctx.fs.glob", args, kwargs, "pattern", &pattern); err != nil {
		return nil, err
	}
	matches, err := f.p.in.Glob(f.p.ictx, pattern)
	if err != nil {
		return nil, err
	}
	elems := make([]starlark.Value, len(matches))
	for i, m := range matches {
		elems[i] = starlark.String(m)
	}
	return starlark.NewList(elems), nil
}

// write implements ctx.fs.write(path, content) -> path. It replaces one rooted
// file and returns the normalized repo-relative path.
func (f *fsValue) write(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path, content string
	if err := starlark.UnpackArgs("ctx.fs.write", args, kwargs, "path", &path, "content", &content); err != nil {
		return nil, err
	}
	written, err := f.p.in.Write(f.p.ictx, path, []byte(content))
	if err != nil {
		return nil, err
	}
	return starlark.String(written), nil
}

// ─── ctx.probe ──────────────────────────────────────────────────────────────

// probe implements ctx.probe(name, args=[]) -> {exit: int, out: string}. name
// must be on the inspector's allow-list; a non-zero exit is returned in the
// result dict (not as an error) so a script can branch on a clean failure, while
// an unknown name or a transport failure is a Starlark error.
func (p *inspectorProxy) probe(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		name    string
		argList = &starlark.List{}
	)
	if err := starlark.UnpackArgs("ctx.probe", args, kwargs, "name", &name, "args?", &argList); err != nil {
		return nil, err
	}
	goArgs, err := listToStringSlice(argList)
	if err != nil {
		return nil, err
	}
	res, err := p.in.Probe(p.ictx, name, goArgs)
	if err != nil {
		return nil, err
	}
	d := starlark.NewDict(2)
	if err := d.SetKey(starlark.String("exit"), starlark.MakeInt(res.Exit)); err != nil {
		return nil, err
	}
	if err := d.SetKey(starlark.String("out"), starlark.String(res.Out)); err != nil {
		return nil, err
	}
	return d, nil
}

// listToStringSlice converts a Starlark list of probe arguments to a Go string
// slice. Every element must be a string. A nil/empty list yields nil.
func listToStringSlice(l *starlark.List) ([]string, error) {
	if l == nil || l.Len() == 0 {
		return nil, nil
	}
	out := make([]string, 0, l.Len())
	for i := 0; i < l.Len(); i++ {
		s, ok := starlark.AsString(l.Index(i))
		if !ok {
			return nil, fmt.Errorf("ctx.probe: arg %d is not a string (got %s)", i, l.Index(i).Type())
		}
		out = append(out, s)
	}
	return out, nil
}
