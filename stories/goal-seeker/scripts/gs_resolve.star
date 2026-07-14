# gs_resolve.star — derive the goal's execution context from its decomposition,
# so opening the goal-seeker needs ZERO operator seed (elegance item 1).
#
# THE PROBLEM. world.workdir defaults to "." → host.starlark.run's inspector roots
# at the process cwd. In the common launcher shape that cwd is the primary checkout,
# while a goal's decomposition.yaml lives in the goal's OWN git worktree
# (docs/goals/<slug>/ under .worktrees/<name>/). So gs_lint's
# `no decomposition.yaml at docs/goals/<slug>/decomposition.yaml` fails unless an
# operator hand-seeds an absolute workdir pointing at the worktree — fragile, and
# forced on every open. If the process is already running from the goal worktree,
# the direct path remains valid and workdir should stay ".".
#
# THE FIX. The goal DECLARES its execution context once, in a top-level `runtime:`
# block of its decomposition.yaml (base_branch + worktree_path). This script, run
# FIRST in bootstrap's on_enter (before gs_init), locates the decomposition and
# reads that block, then binds world.workdir / world.main_worktree_path /
# world.base_branch so every downstream room (gs_init, gs_lint, dispatch's redcheck,
# ship-it's integrate) roots at the goal's worktree with no seed.
#
# LOCATING (chicken-and-egg: the runtime block can't be the LOCATOR because we can't
# read the file until we've found it). Two on-disk cases, both resolved with ctx.fs
# alone (no git exec available in the sandbox), rooted at the current workdir ("."):
#   (a) DIRECT — <goal_dir>/decomposition.yaml exists under the root (the goal lives
#       on the checkout the process runs from, e.g. the flow fixtures' testdata, or
#       the cwd already IS the goal worktree). workdir stays "." unless the file's
#       runtime.worktree_path also points at a visible checkout carrying the same
#       goal, in which case the declared worktree wins.
#   (b) WORKTREE — the goal lives in a sibling git worktree: glob
#       `.worktrees/*/<goal_dir>/decomposition.yaml`. Exactly one match derives
#       worktree_path by stripping the `/<goal_dir>/decomposition.yaml` suffix, so
#       <worktree_path>/<goal_dir>/decomposition.yaml still resolves. This is the
#       real-goal path.
# Neither case is reached when an operator already seeded workdir (!= "." / "") —
# an explicit override always wins, so this script is a default, never a clobber.
#
# WHY RELATIVE. worktree_path is relative to the checkout root visible to ctx.fs;
# NewProductionInspector Abs's it against the process cwd. A CWD-independent absolute
# root would need a git-worktree-root fallback in internal/host/starlark_run.go; kept
# out deliberately (the declared block is the fix).


def _text(v):
    if v == None:
        return ""
    return str(v).strip()


def main(ctx):
    goal_dir = _text(ctx.inputs.get("goal_dir")).rstrip("/")
    cur_workdir = _text(ctx.inputs.get("workdir"))
    cur_main = _text(ctx.inputs.get("main_worktree_path"))
    cur_base = _text(ctx.inputs.get("base_branch"))

    if goal_dir == "":
        fail("gs_resolve: goal_dir is required")

    # Explicit operator seed wins: a non-default workdir means the caller pointed us
    # somewhere on purpose — echo everything back unchanged.
    if cur_workdir != "" and cur_workdir != ".":
        return {
            "workdir": cur_workdir,
            "main_worktree_path": cur_main,
            "base_branch": cur_base,
            "resolved": "seeded",
        }

    suffix = "/" + goal_dir + "/decomposition.yaml"
    direct = goal_dir + "/decomposition.yaml"

    worktree_path = "."
    decomp_rel = ""
    resolved = ""
    if ctx.fs.exists(direct):
        # (a) DIRECT — keep workdir at "." so goal_dir keeps resolving.
        worktree_path = "."
        decomp_rel = direct
        resolved = "direct"
    else:
        # (b) WORKTREE — find the sibling worktree that carries this goal.
        matches = ctx.fs.glob(".worktrees/*/" + goal_dir + "/decomposition.yaml")
        if len(matches) == 1:
            decomp_rel = matches[0]
            worktree_path = decomp_rel[:len(decomp_rel) - len(suffix)]
            resolved = "worktree"
        elif len(matches) == 0:
            # Cannot locate it — leave the defaults untouched so gs_lint surfaces the
            # clean "no decomposition.yaml at <goal_dir>" error rather than this
            # script masking it.
            return {
                "workdir": cur_workdir,
                "main_worktree_path": cur_main,
                "base_branch": cur_base,
                "resolved": "not_found",
            }
        else:
            fail("gs_resolve: " + str(len(matches)) +
                 " worktrees carry " + goal_dir +
                 "/decomposition.yaml (ambiguous); seed workdir explicitly")

    # Read the declared runtime block. base_branch is authoritative when present.
    # In the DIRECT case, a visible runtime.worktree_path with the same goal file is
    # also authoritative. That keeps primary-checkout launches honest when main later
    # grows a copy of the decomposition, while preserving "." when already inside the
    # goal worktree or a hermetic fixture.
    base_branch = cur_base
    doc = yaml.decode(ctx.fs.read(decomp_rel))
    if type(doc) == "dict":
        runtime = doc.get("runtime") or {}
        if type(runtime) == "dict":
            rb = _text(runtime.get("base_branch"))
            if rb != "":
                base_branch = rb
            # A declared worktree_path only overrides in the WORKTREE case. In the
            # DIRECT case, honour it only when the declared checkout is visible from
            # this process root and carries the same goal.
            rw = _text(runtime.get("worktree_path"))
            if rw != "":
                rw = rw.rstrip("/")
                if resolved == "worktree":
                    worktree_path = rw
                elif resolved == "direct" and ctx.fs.exists(rw + "/" + goal_dir + "/decomposition.yaml"):
                    worktree_path = rw
                    resolved = "runtime_worktree"

    return {
        "workdir": worktree_path,
        # The goal's base worktree — where the RED-first gate (dispatch's redcheck)
        # and ship-it's integrate run. Same location as workdir for a goal driven in
        # its own worktree.
        "main_worktree_path": worktree_path,
        "base_branch": base_branch,
        "resolved": resolved,
    }
