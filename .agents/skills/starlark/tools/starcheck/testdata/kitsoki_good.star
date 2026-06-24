# A well-formed kitsoki host.starlark.run script: defines main(ctx), reaches
# only the narrow ctx surface plus the json/math stdlib, returns a dict.
# Passes `starcheck -kitsoki testdata/kitsoki_good.star`.

def main(ctx):
    wid = ctx.inputs["widget_id"]
    resp = ctx.http.get("https://api.example.com/v1/widgets/" + wid)
    if not resp:                     # truthy iff 2xx
        fail("widget lookup failed: %d" % resp.status)
    body = resp.json()
    rounded = math.floor(body.get("price", 0) + 0.5)
    return {"name": body["name"], "price": rounded}
