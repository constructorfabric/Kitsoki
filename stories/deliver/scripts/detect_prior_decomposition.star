# detect_prior_decomposition.star — deterministic prior-decomposition check
# (NO LLM). Returns {route: "fresh"|"redecompose"}.
#
# "fresh": no file exists yet at decomposition_path — decompose invokes the
# decomposer agent to write a brand-new manifest, exactly as before B2c.
# "redecompose": a decomposition.yaml ALREADY exists at decomposition_path —
# decompose must NOT blindly overwrite it; route to the redecompose room,
# which authors a delta and applies it via the decompose-update transaction
# (tools/decomposition-update/apply_delta.py) instead
# (proposal: deliver-canonical-decomposition B2c, task 3.1).


def main(ctx):
    path = ctx.inputs["decomposition_path"]
    if path == "":
        return {"route": "fresh"}
    if ctx.fs.exists(path):
        return {"route": "redecompose"}
    return {"route": "fresh"}
