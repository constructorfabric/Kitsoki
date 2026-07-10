# greet.star — the Handler synthesized for a starlark-form host_bindings
# entry (S3a / D2.1). Proves two things end to end:
#   1. the interface's `with:` args (here, `name`) reach ctx.inputs exactly as
#      a normal host.starlark.run effect would receive them;
#   2. the dispatched interface op is injected into ctx.inputs.op (the
#      registry's prefix-fallback fills in "op" — see
#      internal/host/starlark_binding.go's StarlarkBindingHandler).

def main(ctx):
    name = ctx.inputs["name"]
    op = ctx.inputs.get("op", "(no-op)")
    return {"message": "Hello, " + name + "! (op=" + op + ")"}
