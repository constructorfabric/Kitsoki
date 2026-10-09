def main(ctx):
    candidates = ctx.inputs["candidates"]
    proofs = ctx.inputs["proofs"]
    if len(candidates) != len(proofs):
        fail("receipt requires one proof for every selected candidate")
    receipt = {
            "kind": "corpus-receipt.v1",
            "selection_id": ctx.inputs["selection_id"],
            "corpus_role": ctx.inputs["corpus_role"],
            "source_manifest_ref": ctx.inputs["source_manifest_ref"],
            "candidate_count": len(candidates),
            "candidates": candidates,
            "proofs": proofs,
    }
    frozen = ctx.host.call("host.corpus.freeze_receipt", {"receipt": receipt})
    return {"receipt": frozen.get("receipt", receipt)}
