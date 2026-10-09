# judge.star — minimal parameterized gate assertion for the materialize
# checks tests: ok iff the node-supplied input `want` is "yes". The same
# script serves the passing and failing fixture nodes — they differ only in
# check_inputs, which is exactly the reusable-check contract under test.
def main(ctx):
    want = ctx.inputs["want"]
    if want == "yes":
        return {"ok": True, "reasons": []}
    return {"ok": False, "reasons": ["want is %s, not yes" % want]}
