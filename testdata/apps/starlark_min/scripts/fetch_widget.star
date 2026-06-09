# fetch_widget.star — minimal host.starlark.run glue script.
#
# Reads a widget id from ctx.inputs, fetches it over ctx.http (served from a
# cassette in tests), and returns the widget's name. A non-2xx response is an
# explicit fail() so the effect's on_error: arc fires and world.last_error is
# set — this is what the HTTP-error flow fixture exercises.

def main(ctx):
    widget_id = ctx.inputs["widget_id"]
    url = "https://api.example.com/widgets/" + widget_id
    resp = ctx.http.get(url)
    if resp.status != 200:
        fail("widget fetch failed: status %d" % resp.status)
    body = resp.json()
    return {"widget_name": body["name"]}
