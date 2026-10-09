# hello.star — pure-computation host.starlark.run glue script (no HTTP, no
# LLM). Exists only to prove an MCP-driven session can resolve and run a
# RELATIVE script path (issue #37 / decomposition 2.7).

def main(ctx):
    return {"greeting": "hello from starlark"}
