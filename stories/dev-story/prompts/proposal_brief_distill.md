# Keep the proposal brief in sync

You maintain a single file: the proposal **brief** at `{{ args.brief_path }}`
in your working directory. The operator is in a live discovery conversation;
your job this turn is to fold the newest exchange into the brief so it always
reflects the current best understanding.

## ⛔ Hard constraints

- You write **exactly one file**: `{{ args.brief_path }}`. Never create, edit,
  or delete anything else — no source, tests, config, or story YAML.
- You are **not implementing the idea**. You are keeping a design brief
  current. Writing anything other than the brief is a hard failure.

## What just happened

The operator said:

> {{ args.message }}

The interviewer replied:

> {{ args.reply }}

(Discovery turn {{ args.turns }}.)

## Your task

1. `Read` `{{ args.brief_path }}` as it stands — the brief on disk IS the
   accumulated memory; build on it, don't restart it.
2. Edit it in place so the spine — **Kind**, **Why**, **What changes**,
   **Impact**, **Why kitsoki**, **How it's used** — incorporates everything
   discussed so far, including this exchange. Fill the sections you now have
   signal for; leave a clear `<…>` placeholder where you still don't.
   Reconcile contradictions in favour of the operator's latest direction.
3. Keep it skim-in-two-minutes; link to code (`file:line`) and existing docs
   rather than restating them. See `docs/proposals/templates/README.md`.
4. Do not invent decisions the operator hasn't made — a brief with honest
   open holes beats a confident wrong one.

## Output

Submit `{ idea, note }` (see `schemas/brief-distill.json`): `idea` is a crisp
1–2 sentence problem/idea statement synthesising the discovery so far; `note`
is one line on what you changed in the brief this pass.
