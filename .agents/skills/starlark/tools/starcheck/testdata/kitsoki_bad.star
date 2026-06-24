# Two kitsoki-profile failures that the general resolver alone would miss:
#   - no top-level def main(ctx)  → loads, but fails at host.starlark.run dispatch
#   - reference to `time`, which is NOT predeclared in the sandbox (only json+math)
# `starcheck -kitsoki testdata/kitsoki_bad.star` flags both.

def derive(ctx):                      # wrong entry-point name (engine calls main)
    now = time.now()                  # error: undefined: time (not in {json, math})
    return {"stamp": now}
