# resolve_paths.star — derive absolute verdict_path / frames_dir from repo_root.
#
# Reads repo_root and verdict_path from world. If verdict_path is still the
# default relative value and repo_root is known, returns absolute paths.
# Otherwise passes through the existing values unchanged.
#
# Runs AFTER the host.run that binds repo_root, so ctx.world reflects that bind.
#
# Interface (authoritative in resolve_paths.star.yaml):
#   inputs:  (none — reads from world)
#   outputs: verdict_path (string), frames_dir (string)

RELATIVE_DEFAULT = ".artifacts/ui-review/verdict.json"

def main(ctx):
    repo_root    = ctx.world.get("repo_root") or ""
    verdict_path = ctx.world.get("verdict_path") or RELATIVE_DEFAULT
    frames_dir   = ctx.world.get("frames_dir") or ".artifacts/ui-review/frames"

    if repo_root and verdict_path == RELATIVE_DEFAULT:
        return {
            "verdict_path": repo_root + "/.artifacts/ui-review/verdict.json",
            "frames_dir":   repo_root + "/.artifacts/ui-review/frames",
        }

    return {
        "verdict_path": verdict_path,
        "frames_dir":   frames_dir,
    }
