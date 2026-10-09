def main(ctx):
    candidates = ctx.inputs["candidates"]
    if len(candidates) == 0:
        fail("no corpus-case.v1 candidates were supplied")
    for candidate in candidates:
        if type(candidate) != "dict":
            fail("candidate must be an object")
        if candidate.get("kind", "") != "corpus-case.v1":
            fail("candidate kind must be corpus-case.v1")
        if candidate.get("id", "") == "":
            fail("candidate id is required")
    # This is intentionally just canonical intake shaping. Verification is
    # never read here; only host.corpus.prove may admit a selected case.
    return {"candidates": candidates}
