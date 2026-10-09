def main(ctx):
    candidates = ctx.inputs["candidates"]
    if len(candidates) == 0:
        fail("cannot prove an empty corpus")
    proofs = []
    ids = []
    for candidate in candidates:
        proof_result = ctx.host.call("host.corpus.prove", {"candidate": candidate})
        proof = proof_result.get("proof", None)
        if proof == None:
            fail("host.corpus.prove returned no proof for " + candidate.get("id", "(unknown)"))
        proofs.append(proof)
        ids.append(candidate["id"])
    return {
        "proofs": proofs,
        "validation": {"ok": True, "task_count": len(proofs), "task_ids": ids, "failures": []},
    }
