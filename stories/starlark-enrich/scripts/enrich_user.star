# enrich_user.star — deterministic glue behind host.starlark.run.
#
# The story knows a bare numeric user id. This script turns it into a tidy,
# display-ready profile by calling a plain JSON HTTP API and reshaping the
# response — the kind of fiddly-but-deterministic work that is awkward to
# express in YAML effects and too small to justify a bespoke Go handler.
#
# Everything here is deterministic and replayable: there is no clock, no
# randomness, no filesystem, no environment — only the three attributes the
# sandbox exposes on ctx (ctx.inputs, ctx.world, ctx.http) plus the json
# stdlib. A recorded run replays byte-for-byte from a cassette.
#
# ── Interface (authoritative copy lives in enrich_user.star.yaml) ────────────
# These INPUTS/OUTPUTS dicts are a CONVENTION for the human reader; the engine
# ignores them and validates against the sidecar. Keeping them here documents
# the contract right next to the code.
INPUTS = {
    "user_id": "int — the id to look up (1..10 for the demo API)",
    "api_base": "string — base URL of the JSON API",
}
OUTPUTS = {
    "display_name": "string — '<Name> (@<username>)'",
    "contact": "string — 'email · city'",
    "domain": "string — the company internet domain, lower-cased",
    "is_complete": "bool — true when name, email and company were all present",
}


def main(ctx):
    # 1. Read typed inputs. ctx.inputs is a dict (NOT attribute access): the
    #    adapter has already validated these against the sidecar's inputs:.
    user_id = ctx.inputs["user_id"]
    api_base = ctx.inputs["api_base"]

    # 2. Call the API. ctx.http.get returns a response whose truthiness is
    #    "status in 200..299", so the `if not resp` guard catches every non-2xx.
    #    A transport / replay-miss failure raises and surfaces as an on_error:
    #    arc — we don't have to handle that here.
    url = "%s/users/%d" % (api_base, user_id)
    resp = ctx.http.get(url)
    if not resp:
        # Returning a dict with a non-2xx status is one option; instead we fail
        # loudly so the room's on_error: arc fires and world.last_error is set.
        fail("user lookup failed: HTTP %d for %s" % (resp.status, url))

    user = resp.json()

    # 3. Reshape. Pure data wrangling — string formatting, nested-field access,
    #    a derived boolean. This is the part that would be ugly in YAML.
    name = user.get("name", "")
    username = user.get("username", "")
    email = user.get("email", "")
    city = user.get("address", {}).get("city", "")
    company = user.get("company", {})
    company_name = company.get("name", "")

    # The demo API exposes the corporate domain on the website field; normalise
    # it to a bare lower-case host with the json/string stdlib only.
    website = user.get("website", "")
    domain = website.lower().removeprefix("http://").removeprefix("https://").strip("/")

    # 4. Return the named outputs. Every key here MUST be declared in the
    #    sidecar's outputs:; a missing or undeclared key is a load/run error.
    return {
        "display_name": "%s (@%s)" % (name, username),
        "contact": "%s · %s" % (email, city),
        "domain": domain,
        "is_complete": bool(name) and bool(email) and bool(company_name),
    }
