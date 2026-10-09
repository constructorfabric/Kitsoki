def main(ctx):
    result = ctx.host.call("host.dev.profile_setup", {
        "op": ctx.inputs["op"],
        "target_path": ctx.inputs["target_path"],
        "workdir": ctx.inputs["workdir"],
        "repo_root": ctx.inputs["repo_root"],
        "candidate": ctx.inputs.get("candidate", {}),
    })
    defaults = {
        "schema": "",
        "status": "",
        "target_path": "",
        "config_path": "",
        "local_config_path": "",
        "baseline_exists": False,
        "local_exists": False,
        "baseline_default_profile": "",
        "local_default_profile": "",
        "default_profile": "",
        "profiles": [],
        "backend_sources": [],
        "env_sources": [],
        "candidate_profile": {},
        "candidate_action": "",
        "patch_preview": "",
        "warnings": [],
        "writes": [],
        "error": "",
    }
    for key, value in defaults.items():
        if key not in result:
            result[key] = value
    return {"result": result}
