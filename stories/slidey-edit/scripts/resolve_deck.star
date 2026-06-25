# resolve_deck.star — resolve an operator-supplied deck reference to a concrete
# spec_path for edit_existing.
#
# The project keeps all deck specs in one folder (decks_dir). An operator may
# give a bare deck name ("kitsoki-pitch"), a name with an extension, or a full
# path. Extension preference for a bare name: PREFER <name>.slidey.json, and
# accept <name>.json only when the .slidey.json variant does not exist. Existence
# is checked read-only via ctx.fs.exists (repo-rooted).
#
# If a reference already carries a .json extension it is used verbatim. If no
# candidate exists on disk we still return the FIRST (preferred) candidate so the
# downstream render surfaces a sane "not found" error rather than a blank path.
#
# Interface (authoritative in resolve_deck.star.yaml):
#   inputs:  spec_path (string), summary (string?), decks_dir (string?)
#   world:   decks_dir (object) — fallback when the input is empty
#   outputs: source_deck (object) -> {spec_path, summary}
#            deck        (object) -> {spec_path, summary}  (promoted draft = the
#                        existing spec, so the existing-deck path renders it IN
#                        PLACE without an authoring agent pass)
#            workspace   (string) -> the deck's own directory, so render + refine
#                        resolve the deck's sibling assets (it works in the web
#                        viewer, so it must work when loaded — no copy to a
#                        bare workspace that strips the assets).

def main(ctx):
    raw = (ctx.inputs.get("spec_path") or "").strip()

    # The web compose box funnels typed text into an intent slot with the verb
    # still attached (e.g. "edit kitsoki-pitch"). Strip a leading edit verb so a
    # bare deck name survives. The trailing space requirement keeps real
    # filenames like "editorial-notes" intact.
    for verb in ["edit_existing ", "edit existing ", "edit ", "revise ", "open "]:
        if raw.lower().startswith(verb):
            raw = raw[len(verb):].strip()
            break

    decks_dir = (ctx.inputs.get("decks_dir") or ctx.world.get("decks_dir") or "docs/decks").rstrip("/")
    summary = ctx.inputs.get("summary") or "Existing slidey deck"

    if raw.endswith(".json"):
        # Already a concrete filename / path — honor it verbatim.
        candidates = [raw]
    else:
        # Bare name (or name with a subdir): prefer .slidey.json, then .json.
        # Try under decks_dir first, then as a repo-relative path as given.
        candidates = [
            decks_dir + "/" + raw + ".slidey.json",
            decks_dir + "/" + raw + ".json",
            raw + ".slidey.json",
            raw + ".json",
        ]

    resolved = ""
    for c in candidates:
        if ctx.fs.exists(c):
            resolved = c
            break
    if resolved == "":
        # Nothing on disk — keep the preferred candidate so the error is legible.
        resolved = candidates[0]

    # The deck's own directory is the workspace, so render + refine resolve its
    # sibling assets (e.g. assets/cat-wrangling.png) exactly as the web viewer
    # does. Fall back to "." when the resolved path is bare.
    slash = resolved.rfind("/")
    workspace = resolved[:slash] if slash > 0 else "."

    return {
        "source_deck": {"spec_path": resolved, "summary": summary},
        # Promote the existing spec straight to the draft slot so the
        # existing-deck path renders it IN PLACE with no authoring agent.
        "deck": {"spec_path": resolved, "summary": summary},
        "workspace": workspace,
    }
