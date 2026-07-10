# prepare_judge.star — JSON-encode the judge prompt's object context (the
# current leg and the driver's own report). Agent prompt templating renders a
# raw object value as a Go map string, and the template mini-language has no
# json filter, so the encoding runs here (same reason pick_leg.star emits
# current_leg_json).
#
# Interface (authoritative in prepare_judge.star.yaml):
#   inputs:  drive_result (object)
#   outputs: drive_result_json (string)

def main(ctx):
    return {"drive_result_json": json.encode(ctx.inputs.get("drive_result", {}))}
