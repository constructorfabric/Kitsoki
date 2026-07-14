def main(ctx):
    return {"result": ctx.host.call("host.dev.onboarding", {
        "op": ctx.inputs["op"],
        "request": ctx.inputs.get("request", ""),
        "workdir": ctx.inputs.get("workdir", ""),
        "repo_root": ctx.inputs.get("repo_root", ""),
        "data": ctx.inputs.get("data", {}),
        "profile_json": ctx.inputs.get("profile_json", ""),
    })}
